package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

const presentationRuntimeRowLimit = 1000

type presentationRuntimeDataset struct {
	id             string
	connection     string
	connectionType string
	query          string
	columnTypes    map[string]string
}

// Run executes the bounded, read-only data needed to render a dashboard or
// report. Each visualization is evaluated independently so bindings on one
// visualization never change another visualization that happens to reuse the
// same dataset.
func (s *PresentationService) Run(
	ctx context.Context,
	workspaceID string,
	request model.PresentationRunRequest,
) (model.PresentationRunResult, *APIError) {
	artifact, apiErr := s.loadRuntimeArtifact(ctx, workspaceID)
	if apiErr != nil {
		return model.PresentationRunResult{}, apiErr
	}
	return s.runPresentationArtifact(ctx, artifact, request)
}

// Preview validates and executes an unsaved typed snapshot without writing it
// to the workspace. The saved artifact supplies the immutable identity and CAS
// boundary; only the authored draft fields are converted into the runtime
// representation.
func (s *PresentationService) Preview(
	ctx context.Context,
	workspaceID string,
	request model.PresentationPreviewRequest,
) (model.PresentationPreviewResult, *APIError) {
	artifact, apiErr := s.loadPreviewArtifact(ctx, workspaceID, request)
	if apiErr != nil {
		return model.PresentationPreviewResult{}, apiErr
	}
	findings := presentationFindingsToModel(artifact.Problems)
	if firstPresentationError(artifact.Problems) != nil {
		return model.PresentationPreviewResult{
			Status:           "invalid",
			ArtifactRevision: artifact.Revision,
			Findings:         findings,
			FilterValues:     map[string]any{},
			Visualizations:   map[string]model.PresentationDatasetResult{},
		}, nil
	}
	run, apiErr := s.runPresentationArtifact(ctx, artifact, model.PresentationRunRequest{
		Environment:      request.Environment,
		FilterValues:     request.FilterValues,
		VisualizationIDs: request.VisualizationIDs,
		IncludeOptions:   request.IncludeOptions,
	})
	if apiErr != nil {
		return model.PresentationPreviewResult{}, apiErr
	}
	return model.PresentationPreviewResult{
		Status:           run.Status,
		ArtifactRevision: run.ArtifactRevision,
		Findings:         findings,
		FilterValues:     run.FilterValues,
		Visualizations:   run.Visualizations,
		Options:          run.Options,
	}, nil
}

func (s *PresentationService) runPresentationArtifact(
	ctx context.Context,
	artifact *presentation.Artifact,
	request model.PresentationRunRequest,
) (model.PresentationRunResult, *APIError) {
	if problem := firstPresentationError(artifact.Problems); problem != nil {
		return model.PresentationRunResult{}, &APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "presentation_not_runnable",
			Message: fmt.Sprintf("%s: %s", problem.Path, problem.Message),
		}
	}
	if s.deps.NewConnectionManager == nil || s.deps.RunConnectionQuery == nil {
		return model.PresentationRunResult{}, &APIError{
			Status: http.StatusNotImplemented, Code: "presentation_runtime_unavailable",
			Message: "presentation query execution is not configured",
		}
	}

	filterValues, filterFindings := presentation.ResolveParameterValues(artifact.Filters, request.FilterValues)
	if problem := firstPresentationError(filterFindings); problem != nil {
		return model.PresentationRunResult{}, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_filter_invalid",
			Message: fmt.Sprintf("%s: %s", problem.Path, problem.Message),
		}
	}
	literals, err := presentation.ParameterSQLLiterals(artifact.Filters, filterValues)
	if err != nil {
		return model.PresentationRunResult{}, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_filter_invalid", Message: err.Error(),
		}
	}

	manager, err := s.deps.NewConnectionManager(ctx, strings.TrimSpace(request.Environment))
	if err != nil {
		return model.PresentationRunResult{}, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_environment_invalid", Message: err.Error(),
		}
	}
	if manager == nil {
		return model.PresentationRunResult{}, &APIError{
			Status: http.StatusInternalServerError, Code: "presentation_connection_manager_failed",
			Message: "connection manager is unavailable",
		}
	}

	requested, apiErr := requestedPresentationVisualizations(artifact, request.VisualizationIDs)
	if apiErr != nil {
		return model.PresentationRunResult{}, apiErr
	}
	result := model.PresentationRunResult{
		Status:           "ok",
		ArtifactRevision: artifact.Revision,
		FilterValues:     filterValues,
		Visualizations:   make(map[string]model.PresentationDatasetResult, len(requested)),
	}
	cache := make(map[string]model.PresentationDatasetResult)
	for _, visualization := range requested {
		dataset, resolveErr := s.resolveRuntimeDataset(ctx, manager, artifact, visualization.Dataset, request.Environment)
		if resolveErr != nil {
			result.Visualizations[visualization.ID] = failedPresentationDatasetResult(visualization.Dataset, resolveErr)
			result.Status = "error"
			continue
		}
		query, renderErr := renderPresentationQuery(
			dataset.query,
			dataset.connectionType,
			artifact.Filters,
			filterValues,
			literals,
			visualization.FilterBindings,
			visualization.Dataset,
			presentationRuntimeRowLimit+1,
		)
		if renderErr != nil {
			result.Visualizations[visualization.ID] = failedPresentationDatasetResult(visualization.Dataset, renderErr)
			result.Status = "error"
			continue
		}
		cacheKey := strings.Join([]string{dataset.connection, strings.TrimSpace(request.Environment), query}, "\x00")
		datasetResult, ok := cache[cacheKey]
		if !ok {
			datasetResult = s.executePresentationDataset(ctx, dataset, request.Environment, query)
			cache[cacheKey] = datasetResult
		}
		datasetResult.Dataset = visualization.Dataset
		result.Visualizations[visualization.ID] = datasetResult
		if datasetResult.Status != "ok" {
			result.Status = "error"
		}
	}

	if request.IncludeOptions {
		result.Options = make(map[string]model.PresentationDatasetResult)
		for _, filter := range artifact.Filters {
			if filter.Options == nil || strings.TrimSpace(filter.Options.Dataset) == "" {
				continue
			}
			datasetID := strings.TrimSpace(filter.Options.Dataset)
			dataset, resolveErr := s.resolveRuntimeDataset(ctx, manager, artifact, datasetID, request.Environment)
			if resolveErr != nil {
				result.Options[filter.ID] = failedPresentationDatasetResult(datasetID, resolveErr)
				result.Status = "error"
				continue
			}
			query := wrapPresentationQuery(dataset.query, dataset.connectionType, "", presentationRuntimeRowLimit+1)
			cacheKey := strings.Join([]string{dataset.connection, strings.TrimSpace(request.Environment), query}, "\x00")
			datasetResult, ok := cache[cacheKey]
			if !ok {
				datasetResult = s.executePresentationDataset(ctx, dataset, request.Environment, query)
				cache[cacheKey] = datasetResult
			}
			datasetResult.Dataset = datasetID
			result.Options[filter.ID] = datasetResult
			if datasetResult.Status != "ok" {
				result.Status = "error"
			}
		}
	}
	return result, nil
}

func (s *PresentationService) loadPreviewArtifact(
	ctx context.Context,
	workspaceID string,
	request model.PresentationPreviewRequest,
) (*presentation.Artifact, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return nil, apiErr
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &APIError{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return nil, &APIError{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	current, err := presentation.DecodeArtifact(path, content)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	if strings.TrimSpace(request.ExpectedRevision) == "" || request.ExpectedRevision != current.Revision {
		return nil, &APIError{
			Status: http.StatusConflict, Code: "presentation_preview_conflict",
			Message: "This presentation changed after preview began. Reload the latest file before running the draft.",
		}
	}
	draft, err := presentationFromModel(path, request.Artifact)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if draft.ID != current.ID || draft.Kind != current.Kind {
		return nil, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_identity_immutable",
			Message: "Presentation identity and kind cannot be changed while previewing a draft.",
		}
	}
	normalized, err := presentation.MarshalArtifact(*draft)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	draft, err = presentation.DecodeArtifact(path, normalized)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	s.enrichProblems(ctx, draft)
	return draft, nil
}

func (s *PresentationService) loadRuntimeArtifact(ctx context.Context, workspaceID string) (*presentation.Artifact, *APIError) {
	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return nil, apiErr
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &APIError{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return nil, &APIError{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	artifact, err := presentation.DecodeArtifact(path, content)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	s.enrichProblems(ctx, artifact)
	return artifact, nil
}

func firstPresentationError(findings []presentation.Finding) *presentation.Finding {
	for index := range findings {
		if strings.EqualFold(strings.TrimSpace(findings[index].Severity), "error") {
			return &findings[index]
		}
	}
	return nil
}

func requestedPresentationVisualizations(
	artifact *presentation.Artifact,
	requestedIDs []string,
) ([]presentation.ArtifactVisualization, *APIError) {
	byID := make(map[string]presentation.ArtifactVisualization, len(artifact.Visualizations))
	for _, visualization := range artifact.Visualizations {
		byID[visualization.ID] = visualization
	}
	if len(requestedIDs) == 0 {
		result := append([]presentation.ArtifactVisualization(nil), artifact.Visualizations...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return result, nil
	}
	seen := map[string]bool{}
	result := make([]presentation.ArtifactVisualization, 0, len(requestedIDs))
	for _, rawID := range requestedIDs {
		id := strings.TrimSpace(rawID)
		visualization, ok := byID[id]
		if !ok {
			return nil, &APIError{
				Status: http.StatusBadRequest, Code: "presentation_visualization_unknown",
				Message: fmt.Sprintf("visualization %q is not declared", id),
			}
		}
		if !seen[id] {
			result = append(result, visualization)
			seen[id] = true
		}
	}
	return result, nil
}

func (s *PresentationService) resolveRuntimeDataset(
	ctx context.Context,
	manager config.ConnectionAndDetailsGetter,
	artifact *presentation.Artifact,
	datasetID string,
	environment string,
) (presentationRuntimeDataset, error) {
	datasetID = strings.TrimSpace(datasetID)
	definition, ok := artifact.Datasets[datasetID]
	if !ok {
		return presentationRuntimeDataset{}, fmt.Errorf("dataset %q is not declared", datasetID)
	}
	columnTypes := make(map[string]string, len(definition.Columns))
	for _, column := range definition.Columns {
		columnTypes[strings.ToLower(strings.TrimSpace(column.Name))] = strings.TrimSpace(column.Type)
	}
	if strings.TrimSpace(definition.Query) != "" {
		connection := strings.TrimSpace(definition.Connection)
		connectionType := normalizeConnectionType(manager.GetConnectionType(connection))
		if connectionType == "" {
			return presentationRuntimeDataset{}, fmt.Errorf("connection %q is not configured in environment %q", connection, environment)
		}
		assetType, ok := queryAssetTypeForConnectionType(connectionType)
		if !ok {
			return presentationRuntimeDataset{}, fmt.Errorf("connection %q does not support presentation queries", connection)
		}
		readOnly, err := isPresentationReadOnlyQuery(ctx, definition.Query, assetType)
		if err != nil {
			return presentationRuntimeDataset{}, fmt.Errorf("validate dataset %q query: %w", datasetID, err)
		}
		if !readOnly {
			return presentationRuntimeDataset{}, fmt.Errorf("dataset %q query must be one read-only SELECT", datasetID)
		}
		return presentationRuntimeDataset{
			id: datasetID, connection: connection, connectionType: connectionType,
			query: strings.TrimSpace(definition.Query), columnTypes: columnTypes,
		}, nil
	}

	if s.deps.CurrentState == nil || s.deps.ResolveAssetByID == nil {
		return presentationRuntimeDataset{}, fmt.Errorf("asset-backed presentation datasets are not configured")
	}
	matches := presentationAssetMatches(s.deps.CurrentState(), definition.Asset)
	if len(matches) == 0 {
		return presentationRuntimeDataset{}, fmt.Errorf("asset %q could not be resolved", definition.Asset)
	}
	if len(matches) > 1 {
		return presentationRuntimeDataset{}, fmt.Errorf("asset %q is ambiguous; use its unique URI", definition.Asset)
	}
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, matches[0].ID)
	if err != nil || parsedPipeline == nil || asset == nil {
		if err == nil {
			err = fmt.Errorf("asset resolver returned an incomplete asset")
		}
		return presentationRuntimeDataset{}, fmt.Errorf("resolve asset %q: %w", definition.Asset, err)
	}
	connection, err := targetConnectionNameForAsset(asset, parsedPipeline)
	if err != nil {
		return presentationRuntimeDataset{}, fmt.Errorf("resolve connection for asset %q: %w", asset.Name, err)
	}
	connectionType := normalizeConnectionType(manager.GetConnectionType(connection))
	if connectionType == "" {
		return presentationRuntimeDataset{}, fmt.Errorf("connection %q is not configured in environment %q", connection, environment)
	}
	targetKind, _ := assetTargetIntent(asset, parsedPipeline)
	if !isSourceAssetType(asset.Type) && targetKind != assetRenderTargetKindRelation {
		return presentationRuntimeDataset{}, fmt.Errorf("asset %q does not materialize a queryable relation", asset.Name)
	}
	assetCopy := *asset
	selected, configErr := loadSelectedConfigReadOnlyFS(nil, s.deps.ConfigPath, environment)
	if configErr != nil {
		return presentationRuntimeDataset{}, fmt.Errorf("load environment %q: %w", environment, configErr)
	}
	if selected.SelectedEnvironment != nil && strings.TrimSpace(selected.SelectedEnvironment.SchemaPrefix) != "" {
		assetCopy.PrefixSchema(selected.SelectedEnvironment.SchemaPrefix)
	}
	if len(matches[0].Columns) > 0 {
		columnTypes = make(map[string]string, len(matches[0].Columns))
		for _, column := range matches[0].Columns {
			columnTypes[strings.ToLower(strings.TrimSpace(column.Name))] = strings.TrimSpace(column.Type)
		}
	}
	return presentationRuntimeDataset{
		id: datasetID, connection: connection, connectionType: connectionType,
		query:       "SELECT * FROM " + quoteRuntimeRelation(assetCopy.Name, connectionType),
		columnTypes: columnTypes,
	}, nil
}

func isPresentationReadOnlyQuery(ctx context.Context, query string, assetType pipeline.AssetType) (bool, error) {
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return false, err
	}
	parsed, err := sqlintelligence.ParseContextWithSchemaContext(ctx, query, dialect, nil)
	if err != nil {
		return false, err
	}
	if len(parsed.Errors) > 0 {
		return false, fmt.Errorf("query could not be parsed: %s", strings.Join(parsed.Errors, "; "))
	}
	return parsed.IsReadOnlyResult, nil
}

func (s *PresentationService) executePresentationDataset(
	ctx context.Context,
	dataset presentationRuntimeDataset,
	environment string,
	query string,
) model.PresentationDatasetResult {
	started := time.Now()
	columns, rows, err := s.deps.RunConnectionQuery(ctx, dataset.connection, strings.TrimSpace(environment), query)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		return model.PresentationDatasetResult{
			Dataset: dataset.id, Status: "error", Columns: []string{}, Rows: [][]any{},
			DurationMS: duration, Error: err.Error(),
		}
	}
	if columns == nil {
		columns = []string{}
	}
	truncated := len(rows) > presentationRuntimeRowLimit
	totalRows := len(rows)
	if truncated {
		rows = rows[:presentationRuntimeRowLimit]
	}
	resultRows := make([][]any, 0, len(rows))
	for _, row := range rows {
		values := make([]any, len(columns))
		for index, column := range columns {
			values[index] = presentationRowValue(row, column)
		}
		resultRows = append(resultRows, values)
	}
	columnTypes := make([]string, len(columns))
	for index, column := range columns {
		columnTypes[index] = dataset.columnTypes[strings.ToLower(strings.TrimSpace(column))]
	}
	return model.PresentationDatasetResult{
		Dataset: dataset.id, Status: "ok", Columns: columns, ColumnTypes: columnTypes,
		Rows: resultRows, TotalRows: totalRows, Truncated: truncated, DurationMS: duration,
	}
}

func presentationRowValue(row map[string]any, column string) any {
	if value, ok := row[column]; ok {
		return value
	}
	for key, value := range row {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(column)) {
			return value
		}
	}
	return nil
}

func failedPresentationDatasetResult(dataset string, err error) model.PresentationDatasetResult {
	return model.PresentationDatasetResult{
		Dataset: dataset, Status: "error", Columns: []string{}, Rows: [][]any{}, Error: err.Error(),
	}
}

func renderPresentationQuery(
	baseQuery string,
	connectionType string,
	definitions []presentation.FilterDefinition,
	values map[string]any,
	literals map[string]string,
	bindings []presentation.FilterBinding,
	datasetID string,
	limit int,
) (string, error) {
	definitionsByID := make(map[string]presentation.FilterDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByID[strings.TrimSpace(definition.ID)] = definition
	}
	predicates := make([]string, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		bindingDataset := strings.TrimSpace(binding.Dataset)
		if bindingDataset == "" {
			bindingDataset = datasetID
		}
		if bindingDataset != datasetID {
			continue
		}
		definition, ok := definitionsByID[strings.TrimSpace(binding.Filter)]
		if !ok {
			return "", fmt.Errorf("filter %q is not declared", binding.Filter)
		}
		predicate, err := renderPresentationFilterPredicate(
			connectionType,
			binding.Column,
			strings.ToLower(strings.TrimSpace(binding.Operator)),
			definition,
			values[definition.ID],
			literals[definition.ID],
		)
		if err != nil {
			return "", fmt.Errorf("filter %q: %w", binding.Filter, err)
		}
		if !seen[predicate] {
			predicates = append(predicates, predicate)
			seen[predicate] = true
		}
	}
	return wrapPresentationQuery(baseQuery, connectionType, strings.Join(predicates, " AND "), limit), nil
}

func renderPresentationFilterPredicate(
	connectionType string,
	column string,
	operator string,
	definition presentation.FilterDefinition,
	value any,
	literal string,
) (string, error) {
	identifier := quoteRuntimeIdentifier(column, connectionType)
	switch operator {
	case "equals":
		return identifier + " = " + literal, nil
	case "not_equals":
		return identifier + " <> " + literal, nil
	case "in", "not_in":
		values, ok := value.([]any)
		if !ok {
			if stringsValue, stringsOK := value.([]string); stringsOK {
				values = make([]any, len(stringsValue))
				for index := range stringsValue {
					values[index] = stringsValue[index]
				}
			} else {
				return "", fmt.Errorf("%s requires a list value", operator)
			}
		}
		if len(values) == 0 {
			if operator == "not_in" {
				return "1 = 1", nil
			}
			return "1 = 0", nil
		}
		keyword := " IN ("
		if operator == "not_in" {
			keyword = " NOT IN ("
		}
		return identifier + keyword + literal + ")", nil
	case "between":
		return identifier + " BETWEEN " + literal, nil
	case "before", "lt":
		return identifier + " < " + literal, nil
	case "after", "gt":
		return identifier + " > " + literal, nil
	case "on_or_before", "lte":
		return identifier + " <= " + literal, nil
	case "on_or_after", "gte":
		return identifier + " >= " + literal, nil
	case "contains", "starts_with":
		text, ok := value.(string)
		if !ok || definition.Type != presentation.ParameterTypeText {
			return "", fmt.Errorf("%s requires a text value", operator)
		}
		pattern := escapePresentationLikePattern(text)
		if operator == "contains" {
			pattern = "%" + pattern + "%"
		} else {
			pattern += "%"
		}
		return identifier + " LIKE '" + EscapeSQLLiteral(pattern) + "' ESCAPE '!'", nil
	default:
		return "", fmt.Errorf("operator %q is not supported", operator)
	}
}

func quoteRuntimeIdentifier(identifier, connectionType string) string {
	identifier = strings.TrimSpace(strings.Trim(identifier, "`\"[]"))
	switch normalizeConnectionType(connectionType) {
	case "google_cloud_platform":
		return "`" + strings.ReplaceAll(identifier, "`", "\\`") + "`"
	case "databricks", "mysql", "doris", "vitess", "planetscale_mysql", "clickhouse", "starrocks":
		return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
	case "mssql", "synapse", "fabric":
		return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
}

func escapePresentationLikePattern(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func wrapPresentationQuery(baseQuery, connectionType, predicate string, limit int) string {
	baseQuery = strings.TrimRight(strings.TrimSpace(baseQuery), "; \n\r\t")
	where := ""
	if strings.TrimSpace(predicate) != "" {
		where = "\nWHERE " + predicate
	}
	switch normalizeConnectionType(connectionType) {
	case "mssql", "synapse", "fabric":
		return fmt.Sprintf("SELECT TOP (%d) * FROM (\n%s\n) AS renart_presentation_dataset%s", limit, baseQuery, where)
	case "oracle":
		return fmt.Sprintf("SELECT * FROM (\n%s\n) renart_presentation_dataset%s\nFETCH FIRST %d ROWS ONLY", baseQuery, where, limit)
	default:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS renart_presentation_dataset%s\nLIMIT %d", baseQuery, where, limit)
	}
}
