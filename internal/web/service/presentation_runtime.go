package service

import (
	"context"
	"fmt"
	"strings"

	"renart/internal/bruincompat"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

func (s *PresentationService) Run(
	ctx context.Context,
	workspaceID string,
	request model.PresentationRunRequest,
) (model.PresentationRunResult, *APIError) {
	return s.runtime.Run(ctx, workspaceID, request)
}

func (s *PresentationService) Preview(
	ctx context.Context,
	workspaceID string,
	request model.PresentationPreviewRequest,
) (model.PresentationPreviewResult, *APIError) {
	return s.runtime.Preview(ctx, workspaceID, request)
}

// resolvePresentationAssetDataset is the facade adapter between the
// presentation runtime and workspace/Bruin asset semantics. Query-backed
// datasets are resolved entirely inside the presentation domain.
func (s *PresentationService) resolvePresentationAssetDataset(
	ctx context.Context,
	connections presentation.ConnectionTypeLookup,
	datasetID string,
	definition presentation.DatasetDefinition,
	environment string,
) (presentation.RuntimeDataset, error) {
	if s.deps.CurrentState == nil || s.deps.ResolveAssetByID == nil {
		return presentation.RuntimeDataset{}, fmt.Errorf("asset-backed presentation datasets are not configured")
	}
	matches := presentationAssetMatches(s.deps.CurrentState(), definition.Asset)
	if len(matches) == 0 {
		return presentation.RuntimeDataset{}, fmt.Errorf("asset %q could not be resolved", definition.Asset)
	}
	if len(matches) > 1 {
		return presentation.RuntimeDataset{}, fmt.Errorf("asset %q is ambiguous; use its unique URI", definition.Asset)
	}
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, matches[0].ID)
	if err != nil || parsedPipeline == nil || asset == nil {
		if err == nil {
			err = fmt.Errorf("asset resolver returned an incomplete asset")
		}
		return presentation.RuntimeDataset{}, fmt.Errorf("resolve asset %q: %w", definition.Asset, err)
	}
	connection, err := targetConnectionNameForAsset(asset, parsedPipeline)
	if err != nil {
		return presentation.RuntimeDataset{}, fmt.Errorf("resolve connection for asset %q: %w", asset.Name, err)
	}
	connectionType := bruincompat.NormalizeConnectionType(connections.GetConnectionType(connection))
	if connectionType == "" {
		return presentation.RuntimeDataset{}, fmt.Errorf("connection %q is not configured in environment %q", connection, environment)
	}
	targetKind, _ := assetTargetIntent(asset, parsedPipeline)
	if !bruincompat.IsSourceAssetType(asset.Type) && targetKind != assetRenderTargetKindRelation {
		return presentation.RuntimeDataset{}, fmt.Errorf("asset %q does not materialize a queryable relation", asset.Name)
	}
	assetCopy := *asset
	selected, configErr := loadSelectedConfigReadOnlyFS(nil, s.deps.ConfigPath, environment)
	if configErr != nil {
		return presentation.RuntimeDataset{}, fmt.Errorf("load environment %q: %w", environment, configErr)
	}
	if selected.SelectedEnvironment != nil && strings.TrimSpace(selected.SelectedEnvironment.SchemaPrefix) != "" {
		assetCopy.PrefixSchema(selected.SelectedEnvironment.SchemaPrefix)
	}
	columnTypes := make(map[string]string, len(definition.Columns))
	for _, column := range definition.Columns {
		columnTypes[strings.ToLower(strings.TrimSpace(column.Name))] = strings.TrimSpace(column.Type)
	}
	if len(matches[0].Columns) > 0 {
		columnTypes = make(map[string]string, len(matches[0].Columns))
		for _, column := range matches[0].Columns {
			columnTypes[strings.ToLower(strings.TrimSpace(column.Name))] = strings.TrimSpace(column.Type)
		}
	}
	return presentation.RuntimeDataset{
		ID: datasetID, Connection: connection, ConnectionType: connectionType,
		Query:       "SELECT * FROM " + quoteRuntimeRelation(assetCopy.Name, connectionType),
		ColumnTypes: columnTypes,
	}, nil
}

// Compatibility wrappers keep focused service tests and any in-package callers
// on the same helpers while implementation ownership lives in presentation.
func firstPresentationError(findings []presentation.Finding) *presentation.Finding {
	return presentation.FirstError(findings)
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
	return presentation.RenderQuery(
		baseQuery, connectionType, definitions, values, literals, bindings, datasetID, limit,
	)
}
