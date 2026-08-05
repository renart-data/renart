package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	webmodel "renart/internal/web/model"
)

func BuildInferAssetColumnsQuery(parsedPipeline *pipeline.Pipeline, asset *pipeline.Asset, environment string) (QueryConnectionRequest, error) {
	if parsedPipeline == nil || asset == nil {
		return QueryConnectionRequest{}, fmt.Errorf("asset context is required")
	}

	connectionName, err := targetConnectionNameForAsset(asset, parsedPipeline)
	if err != nil {
		return QueryConnectionRequest{}, fmt.Errorf("failed to resolve asset connection: %w", err)
	}

	targetTableName := strings.TrimSpace(asset.Name)
	if targetTableName == "" {
		return QueryConnectionRequest{}, fmt.Errorf("asset name is required to infer columns")
	}

	query := fmt.Sprintf("select * from %s limit 1", QuoteQualifiedIdentifier(targetTableName))
	return QueryConnectionRequest{
		ConnectionName: connectionName,
		Query:          query,
		Environment:    environment,
		Output:         "json",
		LogicalSchema:  true,
	}, nil
}

// FillColumnsFromDB applies the Bruin fill-columns-from-db patch for a SQL
// asset, trying both the ./-prefixed and bare relative path forms the CLI
// accepts. It returns the HTTP status plus the response body.
func (s *AssetService) FillColumnsFromDB(ctx context.Context, assetID string) (int, map[string]any, *APIError) {
	relAssetPath, _, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return 0, nil, badRequestError("asset_resolve_failed", err.Error())
	}

	assetType := strings.ToLower(string(asset.Type))
	if !strings.Contains(assetType, "sql") && !strings.HasSuffix(strings.ToLower(relAssetPath), ".sql") {
		return 0, nil, badRequestError("unsupported_asset_type", "fill-columns-from-db is supported for sql assets only")
	}

	normalizedPath := filepath.ToSlash(relAssetPath)
	withDot := "./" + strings.TrimPrefix(normalizedPath, "./")
	withoutDot := strings.TrimPrefix(normalizedPath, "./")

	type patchResult struct {
		Operation webmodel.OperationMetadata `json:"operation"`
		Output    string                     `json:"output"`
		ExitCode  int                        `json:"exit_code"`
		Error     string                     `json:"error,omitempty"`
	}

	targets := []string{withDot, withoutDot}
	results := make([]patchResult, 0, len(targets))
	allSucceeded := true

	for _, targetPath := range targets {
		out, runErr := s.deps.Executor.ApplyPatch(ctx, PatchRequest{
			Operation:  "fill-columns-from-db",
			TargetPath: targetPath,
		})

		result := patchResult{
			Operation: webmodel.OperationMetadata{Type: "patch", Operation: "fill-columns-from-db", Target: targetPath, TargetPath: targetPath},
			Output:    string(out),
			ExitCode:  0,
		}

		if runErr != nil {
			allSucceeded = false
			result.ExitCode = 1
			result.Error = runErr.Error()
		}

		results = append(results, result)
	}

	s.deps.SuppressWatcher(relAssetPath)
	s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.updated", relAssetPath)

	status := http.StatusOK
	responseStatus := "ok"
	if !allSucceeded {
		status = http.StatusBadRequest
		responseStatus = "error"
	}

	return status, map[string]any{
		"status":  responseStatus,
		"results": results,
	}, nil
}

// InferAssetColumns runs a single-row query against the asset's connection
// and infers column names/types from the result.
func (s *AssetService) InferAssetColumns(ctx context.Context, assetID string) (int, map[string]any, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return 0, nil, badRequestError("asset_resolve_failed", err.Error())
	}
	if isAPIAsset(asset) {
		inferred, apiErr := s.InferAssetColumnsFromDefinition(ctx, assetID)
		if apiErr != nil {
			return apiErr.Status, nil, apiErr
		}
		if len(inferred) == 0 {
			return http.StatusBadRequest, map[string]any{
				"status":    "error",
				"columns":   []WorkspaceColumn{},
				"operation": webmodel.OperationMetadata{Type: "asset_definition", Target: asset.Name, Operation: "infer-api-columns"},
				"error":     "API asset columns could not be inferred from response.fields or OpenAPI metadata",
			}, nil
		}
		return http.StatusOK, map[string]any{
			"status":    "ok",
			"columns":   inferred,
			"operation": webmodel.OperationMetadata{Type: "asset_definition", Target: asset.Name, Operation: "infer-api-columns"},
		}, nil
	}

	inferred, output, apiErr := s.inferMaterializedAssetColumns(ctx, parsedPipeline, asset, "")
	queryReq, buildErr := BuildInferAssetColumnsQuery(parsedPipeline, asset, "")
	if buildErr != nil {
		return 0, nil, badRequestError("infer_columns_command_build_failed", buildErr.Error())
	}
	operation := webmodel.OperationMetadata{Type: "query_connection", ConnectionName: queryReq.ConnectionName, Query: queryReq.Query, Environment: queryReq.Environment}
	if apiErr != nil {
		return http.StatusBadRequest, map[string]any{
			"status":     "error",
			"columns":    []WorkspaceColumn{},
			"raw_output": string(output),
			"operation":  operation,
			"error":      apiErr.Message,
		}, nil
	}
	return http.StatusOK, map[string]any{
		"status":     "ok",
		"columns":    inferred,
		"raw_output": string(output),
		"operation":  operation,
	}, nil
}

func (s *AssetService) inferMaterializedAssetColumns(
	ctx context.Context,
	parsedPipeline *pipeline.Pipeline,
	asset *pipeline.Asset,
	environment string,
) ([]WorkspaceColumn, []byte, *APIError) {
	queryReq, err := BuildInferAssetColumnsQuery(parsedPipeline, asset, environment)
	if err != nil {
		return nil, nil, badRequestError("infer_columns_command_build_failed", err.Error())
	}
	if s.deps.Executor == nil {
		return nil, nil, internalError("infer_columns_unavailable", "query execution is not configured")
	}
	output, err := s.deps.Executor.QueryConnection(ctx, queryReq)
	if err != nil {
		return nil, output, badRequestError("infer_columns_failed", err.Error())
	}

	inferred := make([]WorkspaceColumn, 0)
	for _, column := range InferSQLColumnsFromQueryOutput(output) {
		inferred = append(inferred, WorkspaceColumn{Name: column.Name, Type: column.Type})
	}
	return inferred, output, nil
}

// UpdateAssetColumns replaces the asset's column definitions and persists
// the asset file.
func (s *AssetService) UpdateAssetColumns(ctx context.Context, assetID string, columns []any) (StatusResponse, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return StatusResponse{}, badRequestError("asset_resolve_failed", err.Error())
	}

	columnBytes, err := json.Marshal(columns)
	if err != nil {
		return StatusResponse{}, badRequestError("invalid_request_body", err.Error())
	}

	var parsedColumns []WorkspaceColumn
	if err := json.Unmarshal(columnBytes, &parsedColumns); err != nil {
		return StatusResponse{}, badRequestError("invalid_request_body", err.Error())
	}

	asset.Columns = ModelColumnsToPipelineColumns(parsedColumns)
	if apiErr := loaderMaterializationAPIError(asset); apiErr != nil {
		return StatusResponse{}, apiErr
	}
	if apiErr := s.persistAssetPreservingInferredName(asset, parsedPipeline); apiErr != nil {
		return StatusResponse{}, apiErr
	}

	if relAssetPath, decodeErr := DecodeID(assetID); decodeErr == nil {
		s.deps.SuppressWatcher(relAssetPath)
		s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.columns.updated", relAssetPath)
	}

	return StatusResponse{Status: "ok"}, nil
}
