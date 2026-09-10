package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	bruinlint "github.com/bruin-data/bruin/pkg/lint"
	"github.com/bruin-data/bruin/pkg/pipeline"
	bruinpython "github.com/bruin-data/bruin/pkg/python"
)

type assetRenderSemanticOutcome struct {
	handled    bool
	status     AssetRenderStatus
	stages     []AssetRenderStage
	issues     []AssetRenderIssue
	redactions []AssetRenderRedaction
}

func assetRenderConnectionName(info *directPipelineInfo) (string, error) {
	if info == nil || info.Pipeline == nil || info.Asset == nil {
		return "", fmt.Errorf("asset render context is incomplete")
	}
	return targetConnectionNameForAsset(info.Asset, info.Pipeline)
}

func renderSemanticAsset(
	info *directPipelineInfo,
	renderer *jinja.Renderer,
	renderCtx context.Context,
	connectionName string,
	effectiveFullRefresh bool,
	workspaceRoot string,
) assetRenderSemanticOutcome {
	if info == nil || info.Asset == nil || info.Pipeline == nil {
		return assetRenderSemanticOutcome{}
	}
	asset := info.Asset
	switch {
	case asset.Type == pipeline.AssetTypePython:
		return renderPythonSemanticAsset(asset, connectionName, effectiveFullRefresh, workspaceRoot)
	case strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".seed"):
		assetRenderer, err := semanticAssetRenderer(renderer, renderCtx, info.Pipeline, asset)
		if err != nil {
			return semanticRenderError("materialization", "seed_template_context_failed", err.Error())
		}
		return renderSeedSemanticAsset(asset, assetRenderer, connectionName)
	case isLoadAsset(asset):
		return renderLoadSemanticAsset(asset, info.Config, renderCtx, connectionName)
	case isAPIAsset(asset):
		assetRenderer, err := semanticAssetRenderer(renderer, renderCtx, info.Pipeline, asset)
		if err != nil {
			return semanticRenderError("extraction", "api_template_context_failed", err.Error())
		}
		return renderAPISemanticAsset(asset, info.Pipeline, assetRenderer, renderCtx, connectionName)
	case isSensorAssetType(asset.Type) && !isQuerySensorAssetType(asset.Type):
		outcome := renderConditionSensorSemanticAsset(asset, connectionName)
		if asset.Type == pipeline.AssetTypeBigqueryTableSensor && outcome.status != AssetRenderStatusError {
			operatorOutcome := assetRenderSemanticOutcome{handled: true, status: AssetRenderStatusOK}
			appendBigQueryQueryCostGuard(&operatorOutcome, info)
			if len(operatorOutcome.stages) > 0 {
				operatorOutcome.stages = append(operatorOutcome.stages, outcome.stages...)
				operatorOutcome.issues = append(operatorOutcome.issues, outcome.issues...)
				operatorOutcome.redactions = append(operatorOutcome.redactions, outcome.redactions...)
				operatorOutcome.status = mergeAssetRenderStatus(operatorOutcome.status, outcome.status)
				return operatorOutcome
			}
		}
		return outcome
	case asset.Type == pipeline.AssetTypeIngestr:
		return renderIngestrSemanticAsset(renderCtx, info.Pipeline, asset, connectionName, effectiveFullRefresh)
	default:
		return assetRenderSemanticOutcome{}
	}
}

func semanticAssetRenderer(
	renderer *jinja.Renderer,
	renderCtx context.Context,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
) (*jinja.Renderer, error) {
	if renderer == nil {
		return nil, fmt.Errorf("asset renderer is missing")
	}
	scoped, err := renderer.CloneForAsset(renderCtx, pl, asset)
	if err != nil {
		return nil, fmt.Errorf("build asset template context: %w", err)
	}
	concrete, ok := scoped.(*jinja.Renderer)
	if !ok {
		return nil, fmt.Errorf("asset renderer has an unsupported implementation")
	}
	return concrete, nil
}

func renderPythonSemanticAsset(asset *pipeline.Asset, connectionName string, effectiveFullRefresh bool, workspaceRoot string) assetRenderSemanticOutcome {
	entrypoint := asset.ExecutableFile.Path
	if entrypoint == "" {
		entrypoint = asset.DefinitionFile.Path
	}
	operation := map[string]any{
		"operation":       "execute_python",
		"runtime":         "embedded renart Python SDK",
		"entrypoint":      workspaceRelativeRenderPath(workspaceRoot, entrypoint),
		"materialization": semanticMaterialization(asset, effectiveFullRefresh, nil),
	}
	if asset.Materialization.Type != "" && asset.Materialization.Type != pipeline.MaterializationTypeNone {
		operation["target"] = map[string]any{
			"connection": connectionName,
			"object":     asset.Name,
		}
	}
	return assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusPartial,
		stages: []AssetRenderStage{{
			Kind:     "runtime",
			Language: "json",
			Content:  mustRenderOperationJSON(operation),
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelityRuntimeOnly,
			Message:  "the saved entrypoint and declared materialization are known, but user code and SDK calls determine the operations and output at runtime",
		}},
	}
}

func renderSeedSemanticAsset(asset *pipeline.Asset, renderer *jinja.Renderer, connectionName string) assetRenderSemanticOutcome {
	seedPath, ok := asset.Parameters.GetString("path")
	if !ok || strings.TrimSpace(seedPath) == "" {
		return semanticRenderError("materialization", "seed_path_missing", fmt.Sprintf("%s requires a path parameter", asset.Type))
	}
	renderedPath, err := renderer.Render(seedPath)
	if err != nil {
		return semanticRenderError("materialization", "seed_path_render_failed", fmt.Sprintf("failed to render seed path: %v", err))
	}
	assetPath := asset.ExecutableFile.Path
	if strings.TrimSpace(assetPath) == "" {
		assetPath = asset.DefinitionFile.Path
	}
	fileType, _ := asset.Parameters.GetString("file_type")
	_, sourceOptions, err := resolveSlingSeedSource(renderedPath, fileType, filepath.Dir(assetPath))
	if err != nil {
		return semanticRenderError("materialization", "seed_source_invalid", err.Error())
	}
	var sourceConfig struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal([]byte(sourceOptions), &sourceConfig); err != nil {
		return semanticRenderError("materialization", "seed_source_invalid", fmt.Sprintf("decode Sling seed source options: %v", err))
	}
	displayPath, pathRedacted := redactedRenderLocation(renderedPath)

	enforceSchema := true
	if raw, exists := asset.Parameters.GetString("enforce_schema"); exists {
		enforceSchema = strings.EqualFold(strings.TrimSpace(raw), "true")
	}
	columns := make([]map[string]string, 0, len(asset.Columns))
	if enforceSchema {
		for _, column := range asset.Columns {
			name := strings.TrimSpace(column.Name)
			if name == "" {
				continue
			}
			source := strings.TrimSpace(column.SourceColumn)
			if source == "" {
				source = name
			}
			columns = append(columns, map[string]string{
				"name":   name,
				"source": source,
				"cast":   slingSeedColumnType(column.Type),
			})
		}
	}
	operation := map[string]any{
		"operation": "sling_load",
		"source": map[string]any{
			"location": displayPath,
			"format":   sourceConfig.Format,
		},
		"target": map[string]any{
			"connection": connectionName,
			"object":     asset.Name,
		},
		"materialization": map[string]any{"mode": "full-refresh"},
		"schema": map[string]any{
			"enforced": enforceSchema,
			"columns":  columns,
		},
	}
	outcome := assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusOK,
		stages: []AssetRenderStage{{
			Kind:     "materialization",
			Language: "json",
			Content:  mustRenderOperationJSON(operation),
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelitySemantic,
			Redacted: pathRedacted,
			Message:  "semantic Sling operation; connection credentials and the generated loader process are intentionally omitted",
		}},
	}
	if pathRedacted {
		outcome.redactions = append(outcome.redactions, AssetRenderRedaction{Kind: "source_url_credentials", Replacement: "REDACTED"})
	}
	return outcome
}

func renderLoadSemanticAsset(asset *pipeline.Asset, cfg *config.Config, renderCtx context.Context, connectionName string) assetRenderSemanticOutcome {
	parallelism, err := loadParallelism(asset)
	if err != nil {
		return semanticRenderError("materialization", "load_parallelism_invalid", err.Error())
	}
	params := loadParamsFromAsset(asset)
	params.DestinationConnection = connectionName
	if params.SourceConnection == "" {
		return semanticRenderError("materialization", "load_source_connection_missing", "load asset requires a source_connection parameter")
	}
	if params.SourceTable == "" {
		return semanticRenderError("materialization", "load_source_missing", "load asset requires a source_table parameter")
	}

	targetObject := params.AssetName
	targetKind := "table"
	if isLocalLoadConnection(params.DestinationConnection) {
		targetKind = "file"
		targetObject = params.DestinationObject
	} else if cfg != nil && cfg.SelectedEnvironment != nil && cfg.SelectedEnvironment.Connections != nil {
		connectionType := cfg.SelectedEnvironment.Connections.ConnectionsSummaryList()[params.DestinationConnection]
		switch loadConnectionCategory(connectionType) {
		case LoadCategoryStorage, LoadCategoryFile:
			targetKind = "object"
			targetObject = params.DestinationObject
		}
	}
	if strings.TrimSpace(targetObject) == "" {
		return semanticRenderError("materialization", "load_target_missing", "load target requires a destination object")
	}

	modeArgs, err := slingMaterializationArgs(renderCtx, asset)
	if err != nil {
		return semanticRenderError("materialization", "load_materialization_invalid", err.Error())
	}
	displaySource, sourceRedacted := redactedRenderLocation(params.SourceTable)
	displayTarget, targetRedacted := redactedRenderLocation(targetObject)
	operation := map[string]any{
		"operation": "sling_copy",
		"source": map[string]any{
			"connection": params.SourceConnection,
			"object":     displaySource,
		},
		"target": map[string]any{
			"connection": params.DestinationConnection,
			"kind":       targetKind,
			"object":     displayTarget,
		},
		"materialization": semanticMaterialization(asset, contextFullRefresh(renderCtx), modeArgs),
	}
	if parallelism > 0 {
		operation["parallelism"] = parallelism
	}
	outcome := assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusOK,
		stages: []AssetRenderStage{{
			Kind:     "materialization",
			Language: "json",
			Content:  mustRenderOperationJSON(operation),
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelitySemantic,
			Redacted: sourceRedacted || targetRedacted,
			Message:  "semantic Sling copy; named connections are shown without credentials",
		}},
	}
	if sourceRedacted || targetRedacted {
		outcome.redactions = append(outcome.redactions, AssetRenderRedaction{Kind: "object_url_credentials", Replacement: "REDACTED"})
	}
	return outcome
}

func renderAPISemanticAsset(asset *pipeline.Asset, pl *pipeline.Pipeline, renderer *jinja.Renderer, renderCtx context.Context, connectionName string) assetRenderSemanticOutcome {
	spec, _, err := parseNativeAPIAssetSpec(asset.ExecutableFile.Content, asset, pl)
	if err != nil {
		return semanticRenderError("extraction", "api_spec_invalid", err.Error())
	}
	if strings.TrimSpace(spec.Request.URL) == "" {
		return semanticRenderError("extraction", "api_request_url_missing", "api asset request.url is required")
	}

	itemName := strings.TrimSpace(spec.Iterate.As)
	if itemName == "" {
		itemName = "item"
	}
	item := ""
	if len(spec.Iterate.Over) > 0 {
		item = spec.Iterate.Over[0]
	}
	renderer.SetContextValue(itemName, item)
	renderer.SetContextValue("item", item)
	renderedURL, err := renderer.Render(spec.Request.URL)
	if err != nil {
		return semanticRenderError("extraction", "api_request_render_failed", err.Error())
	}
	params, err := renderedRequestParams(renderer, spec.Request.Params)
	if err != nil {
		return semanticRenderError("extraction", "api_request_render_failed", err.Error())
	}
	finalURL, err := urlWithQueryParams(renderedURL, params)
	if err != nil {
		return semanticRenderError("extraction", "api_request_url_invalid", err.Error())
	}
	parsedURL, err := url.Parse(finalURL)
	if err != nil {
		return semanticRenderError("extraction", "api_request_url_invalid", err.Error())
	}
	displayURL := redactedURLString(parsedURL, apiAuthQueryParamName(spec.Auth))
	urlRedacted := displayURL != parsedURL.String()

	method := strings.ToUpper(strings.TrimSpace(spec.Request.Method))
	if method == "" {
		method = http.MethodGet
	}
	headerNames := make([]string, 0, len(spec.Request.Headers))
	for name := range spec.Request.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	extraction := map[string]any{
		"operation": "http_extract",
		"request": map[string]any{
			"method":       method,
			"url":          displayURL,
			"header_names": headerNames,
			"has_body":     spec.Request.Body != nil,
		},
		"authentication": map[string]any{
			"type": spec.Auth.Type,
			"in":   spec.Auth.In,
			"name": spec.Auth.Name,
		},
		"iteration": map[string]any{
			"as":    itemName,
			"items": len(spec.Iterate.Over),
		},
		"pagination": map[string]any{
			"type":      spec.Pagination.Type,
			"max_pages": spec.Pagination.MaxPages,
		},
		"response": map[string]any{
			"records_path": spec.Response.RecordsPath,
			"fields":       spec.Response.Fields,
		},
	}
	modeArgs, err := slingMaterializationArgs(renderCtx, asset)
	if err != nil {
		return semanticRenderError("materialization", "api_materialization_invalid", err.Error())
	}
	materialization := map[string]any{
		"operation": "sling_load_jsonlines",
		"source": map[string]any{
			"format":  "jsonlines",
			"flatten": true,
		},
		"target": map[string]any{
			"connection": connectionName,
			"object":     apiTargetObjectName(asset, spec),
		},
		"materialization": semanticMaterialization(asset, contextFullRefresh(renderCtx), modeArgs),
	}
	authRedacted := apiSpecContainsCredentials(spec)
	outcome := assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusOK,
		stages: []AssetRenderStage{
			{
				Kind:     "extraction",
				Language: "json",
				Content:  mustRenderOperationJSON(extraction),
				Status:   AssetRenderStageStatusOK,
				Fidelity: AssetRenderFidelitySemantic,
				Redacted: urlRedacted || authRedacted,
				Message:  "semantic HTTP request shape; credential values, response-dependent pages, and records are omitted",
			},
			{
				Kind:     "materialization",
				Language: "json",
				Content:  mustRenderOperationJSON(materialization),
				Status:   AssetRenderStageStatusOK,
				Fidelity: AssetRenderFidelitySemantic,
				Message:  "semantic Sling write of runtime-fetched JSONL records",
			},
		},
	}
	if urlRedacted || authRedacted {
		outcome.redactions = append(outcome.redactions, AssetRenderRedaction{Kind: "api_credentials", Replacement: "REDACTED"})
	}
	return outcome
}

func renderConditionSensorSemanticAsset(asset *pipeline.Asset, connectionName string) assetRenderSemanticOutcome {
	operation := map[string]any{
		"connection": connectionName,
		"runtime_controls": map[string]any{
			"poke_interval": parameterString(asset, "poke_interval"),
			"timeout":       parameterString(asset, "timeout"),
		},
	}
	var missingCode, missingMessage string
	if asset.Type == pipeline.AssetTypeS3KeySensor {
		operation["operation"] = "wait_for_s3_key"
		operation["bucket"] = parameterString(asset, "bucket_name")
		operation["key"] = parameterString(asset, "bucket_key")
		if operation["bucket"] == "" || operation["key"] == "" {
			missingCode = "key_sensor_parameters_missing"
			missingMessage = "S3 key sensor requires bucket_name and bucket_key parameters"
		}
	} else {
		operation["operation"] = "wait_for_table"
		operation["table"] = parameterString(asset, "table")
		if operation["table"] == "" {
			missingCode = "table_sensor_table_missing"
			missingMessage = "table sensor requires a table parameter"
		}
	}
	if missingCode != "" {
		return semanticRenderError("condition", missingCode, missingMessage)
	}
	return assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusOK,
		stages: []AssetRenderStage{{
			Kind:     "condition",
			Language: "json",
			Content:  mustRenderOperationJSON(operation),
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelitySemantic,
			Message:  "semantic sensor condition; once/wait/skip mode and each probe result are selected at runtime",
		}},
	}
}

func renderIngestrSemanticAsset(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, connectionName string, effectiveFullRefresh bool) assetRenderSemanticOutcome {
	materialization := semanticMaterialization(asset, effectiveFullRefresh, nil)
	materialization["effective_strategy"] = ingestrEffectiveStrategy(asset)
	operation := map[string]any{
		"operation": "ingestr_copy",
		"source": map[string]any{
			"connection": parameterString(asset, "source_connection"),
			"object":     parameterString(asset, "source_table"),
			"kind":       parameterString(asset, "source"),
		},
		"target": map[string]any{
			"connection": connectionName,
			"object":     asset.Name,
			"kind":       parameterString(asset, "destination"),
		},
		"materialization": materialization,
		"cdc":             parameterString(asset, "cdc"),
		"engine_version":  parameterString(asset, "version"),
	}
	outcome := assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusOK,
		stages: []AssetRenderStage{{
			Kind:     "materialization",
			Language: "json",
			Content:  mustRenderOperationJSON(operation),
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelitySemantic,
			Message:  "semantic ingestr operation; resolved connection URIs and credentials are omitted",
		}},
	}

	validationIssues, err := bruinlint.EnsureIngestrAssetIsValidForASingleAsset(ctx, pl, asset)
	if err != nil {
		outcome.status = AssetRenderStatusError
		outcome.stages[0].Status = AssetRenderStageStatusError
		outcome.stages[0].Message = fmt.Sprintf("ingestr configuration validation failed: %v", err)
		outcome.issues = append(outcome.issues, AssetRenderIssue{
			Code: "ingestr_validation_failed", Severity: "error", Message: outcome.stages[0].Message,
		})
		return outcome
	}
	if len(validationIssues) > 0 {
		messages := make([]string, 0, len(validationIssues))
		for _, issue := range validationIssues {
			if issue == nil || strings.TrimSpace(issue.Description) == "" {
				continue
			}
			message := strings.TrimSpace(issue.Description)
			messages = append(messages, message)
			outcome.issues = append(outcome.issues, AssetRenderIssue{
				Code: "ingestr_configuration_invalid", Severity: "error", Message: message,
			})
		}
		if len(messages) > 0 {
			outcome.status = AssetRenderStatusError
			outcome.stages[0].Status = AssetRenderStageStatusError
			outcome.stages[0].Message = "ingestr configuration is invalid: " + strings.Join(messages, "; ")
		}
	}
	return outcome
}

func ingestrEffectiveStrategy(asset *pipeline.Asset) string {
	if asset == nil {
		return ""
	}
	effective, _ := asset.Parameters.GetString("incremental_strategy")
	effective = strings.TrimSpace(effective)
	if asset.Materialization.Strategy != pipeline.MaterializationStrategyNone {
		if translated, ok := bruinpython.TranslateBruinMaterializationStrategyToIngestr(asset.Materialization.Strategy); ok {
			effective = translated
		}
	}
	if cdc, _ := asset.Parameters.GetString("cdc"); cdc == "true" && effective == "" {
		return "merge"
	}
	return effective
}

func semanticMaterialization(asset *pipeline.Asset, fullRefresh bool, runtimeArgs []string) map[string]any {
	result := map[string]any{
		"type":                asset.Materialization.Type,
		"strategy":            asset.Materialization.Strategy,
		"full_refresh":        fullRefresh,
		"incremental_key":     asset.Materialization.IncrementalKey,
		"primary_key_columns": asset.ColumnNamesWithPrimaryKey(),
	}
	if len(runtimeArgs) > 0 {
		result["runtime_options"] = runtimeArgs
	}
	return result
}

func contextFullRefresh(ctx context.Context) bool {
	fullRefresh, _ := ctx.Value(pipeline.RunConfigFullRefresh).(bool)
	return fullRefresh
}

func parameterString(asset *pipeline.Asset, name string) string {
	if asset == nil {
		return ""
	}
	value, _ := asset.Parameters.GetString(name)
	return strings.TrimSpace(value)
}

func apiSpecContainsCredentials(spec nativeAPISpec) bool {
	if strings.TrimSpace(spec.Auth.Type) != "" && !strings.EqualFold(strings.TrimSpace(spec.Auth.Type), "none") {
		return true
	}
	for name := range spec.Request.Headers {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "authorization" || normalized == "proxy-authorization" || strings.Contains(normalized, "api-key") || strings.Contains(normalized, "token") {
			return true
		}
	}
	return false
}

func redactedRenderLocation(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() {
		return trimmed, false
	}
	redacted := redactedURLString(parsed, "")
	return redacted, redacted != parsed.String()
}

func mustRenderOperationJSON(operation map[string]any) string {
	encoded, err := json.MarshalIndent(operation, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\n  \"operation\": %q\n}", fmt.Sprint(operation["operation"]))
	}
	return string(encoded)
}

func semanticRenderError(kind, code, message string) assetRenderSemanticOutcome {
	return assetRenderSemanticOutcome{
		handled: true,
		status:  AssetRenderStatusError,
		stages: []AssetRenderStage{{
			Kind:     kind,
			Language: "json",
			Status:   AssetRenderStageStatusError,
			Fidelity: AssetRenderFidelitySemantic,
			Message:  message,
		}},
		issues: []AssetRenderIssue{{Code: code, Severity: "error", Message: message}},
	}
}

func mergeAssetRenderStatus(current, next AssetRenderStatus) AssetRenderStatus {
	priority := map[AssetRenderStatus]int{
		AssetRenderStatusOK:          0,
		AssetRenderStatusPartial:     1,
		AssetRenderStatusUnsupported: 2,
		AssetRenderStatusError:       3,
	}
	if priority[next] > priority[current] {
		return next
	}
	return current
}
