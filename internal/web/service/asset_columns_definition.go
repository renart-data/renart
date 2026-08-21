package service

import (
	"context"
	"strings"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/sqllsp"
)

// InferAssetColumnsFromDefinition derives a SQL asset's output columns (name +
// type) from its rendered definition and the declared columns of the pipeline's
// assets — the assets are the source of truth, not the materialized warehouse
// tables. It renders the asset's SQL (Jinja + variables + dates + macros), then
// asks the native Golyglot engine to annotate the projection's types against the
// upstream asset schema.
func (s *AssetService) InferAssetColumnsFromDefinition(ctx context.Context, assetID string) ([]WorkspaceColumn, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return nil, badRequestError("asset_resolve_failed", err.Error())
	}
	policy := schemaPolicyForAsset(asset)
	if policy.Kind == assetSchemaKindSQL || policy.Kind == assetSchemaKindLoad {
		return s.inferGraphColumnsFromDefinition(ctx, parsedPipeline, asset)
	}
	resolver := newAssetDefinitionSchemaResolver(parsedPipeline)
	assetContext := AssetSchemaContext{
		Service: s, AssetID: assetID, Pipeline: parsedPipeline, Asset: asset,
		ResolveDefinition: resolver.Available,
	}
	for _, provider := range assetSchemaSourceProviders() {
		if provider.ID() != columnSourceDefinition || !provider.Matches(assetContext) {
			continue
		}
		evidence, apiErr := provider.Observe(ctx, SchemaEvidenceRequest{
			Context: assetContext,
			Allow:   SchemaEvidenceAccess{Filesystem: true, Network: true},
		})
		if apiErr != nil {
			return nil, apiErr
		}
		return evidence.Columns, nil
	}
	return nil, badRequestError("unsupported_asset_type", "column inference from this asset definition is not supported")
}

func (s *AssetService) inferGraphColumnsFromDefinition(
	ctx context.Context,
	parsedPipeline *pipeline.Pipeline,
	asset *pipeline.Asset,
) ([]WorkspaceColumn, *APIError) {
	columns, _, _, apiErr := s.inferGraphSchemaFromDefinition(ctx, parsedPipeline, asset)
	return columns, apiErr
}

func (s *AssetService) inferGraphSchemaFromDefinition(
	ctx context.Context,
	parsedPipeline *pipeline.Pipeline,
	asset *pipeline.Asset,
) ([]WorkspaceColumn, SchemaCompleteness, SchemaConfidence, *APIError) {
	policy := schemaPolicyForAsset(asset)
	if policy.Kind != assetSchemaKindSQL && policy.Kind != assetSchemaKindLoad {
		return nil, SchemaUnknown, SchemaConfidenceLow, badRequestError("unsupported_asset_type", "canonical graph column inference is supported for SQL and Load assets only")
	}
	inferencePipeline, inferenceTarget := pipelineWithoutTargetColumns(parsedPipeline, asset)

	nodes := make([]sqllsp.AssetNode, 0, len(inferencePipeline.Assets))
	declared := make(map[string][]sqllsp.ColumnInfo, len(inferencePipeline.Assets))
	inferenceAssets := make([]sqllsp.InferenceAsset, 0, len(inferencePipeline.Assets))
	for _, candidate := range inferencePipeline.Assets {
		if candidate == nil || strings.TrimSpace(candidate.Name) == "" {
			continue
		}
		candidateDialect, candidateDialectErr := AssetTypeToDialect(candidate.Type)
		kind := strings.ToLower(strings.TrimSpace(string(candidate.Type)))
		if candidateDialectErr == nil {
			kind = "sql_model"
		}
		uri := typeCheckAssetURI(s.deps.WorkspaceRoot, candidate)
		nodes = append(nodes, sqllsp.AssetNode{
			ID: candidate.Name, Name: candidate.Name, Kind: kind,
			Dialect: candidateDialect, Connection: candidate.Connection, URI: uri,
		})
		for _, column := range candidate.Columns {
			if strings.TrimSpace(column.Name) != "" {
				declared[candidate.Name] = append(declared[candidate.Name], columnInfoFromPipelineColumn(column))
			}
		}
		if candidateDialectErr != nil || strings.TrimSpace(assetSQLSource(candidate)) == "" {
			continue
		}
		rendered, renderErr := s.renderAssetQuery(ctx, inferencePipeline, candidate)
		if renderErr != nil {
			if candidate == inferenceTarget {
				return nil, SchemaUnknown, SchemaConfidenceLow, badRequestError("query_render_failed", renderErr.Error())
			}
			continue
		}
		upstreams := make([]string, 0, len(candidate.Upstreams))
		for _, upstream := range candidate.Upstreams {
			if upstream.Type == "asset" && strings.TrimSpace(upstream.Value) != "" {
				upstreams = append(upstreams, upstream.Value)
			}
		}
		inferenceAssets = append(inferenceAssets, sqllsp.InferenceAsset{
			ID: candidate.Name, Name: candidate.Name, URI: uri, SQL: rendered,
			Dialect: candidateDialect, Upstreams: upstreams,
		})
	}
	graph := sqllsp.GraphFromRenartAssets(sqllsp.FileURI(s.deps.WorkspaceRoot), nodes, declared)
	graph = resolveAuthoringSchemaGraph(ctx, graph, inferencePipeline, inferenceAssets)
	columns, completeness, confidence := authoringGraphRelationSchema(graph, asset.Name)
	if len(columns) == 0 {
		return nil, SchemaUnknown, SchemaConfidenceLow, badRequestError("column_inference_failed", "the canonical authoring graph could not infer an output schema")
	}
	return columns, completeness, confidence, nil
}

func pipelineWithoutTargetColumns(pp *pipeline.Pipeline, target *pipeline.Asset) (*pipeline.Pipeline, *pipeline.Asset) {
	copyPipeline := new(pipeline.Pipeline)
	*copyPipeline = *pp
	copyPipeline.Assets = append([]*pipeline.Asset(nil), pp.Assets...)
	var copyTarget *pipeline.Asset
	for index, candidate := range copyPipeline.Assets {
		if candidate != target {
			continue
		}
		copyTarget = new(pipeline.Asset)
		*copyTarget = *candidate
		copyTarget.Columns = nil
		copyPipeline.Assets[index] = copyTarget
		break
	}
	if copyTarget == nil {
		copyTarget = new(pipeline.Asset)
		*copyTarget = *target
		copyTarget.Columns = nil
	}
	return copyPipeline, copyTarget
}

// RefreshAssetColumnsFromDefinition infers columns from the asset definition and
// reconciles them into the asset, preserving user-authored metadata. This is the
// definition-driven counterpart to the warehouse-driven fill paths.
func (s *AssetService) RefreshAssetColumnsFromDefinition(ctx context.Context, assetID string) (ColumnReconcileResult, *APIError) {
	inferred, apiErr := s.InferAssetColumnsFromDefinition(ctx, assetID)
	if apiErr != nil {
		return ColumnReconcileResult{}, apiErr
	}
	return s.ReconcileAssetColumns(ctx, assetID, inferred)
}

// renderAssetQuery renders an asset's SQL with the same Jinja context the
// dependency reconcile uses (pipeline variables, run dates, macros), so column
// inference sees the real query.
func (s *AssetService) renderAssetQuery(ctx context.Context, parsedPipeline *pipeline.Pipeline, asset *pipeline.Asset) (string, error) {
	renderer := jinja.NewRendererWithYesterday(parsedPipeline.Name, "web-column-infer")
	assetRenderer, err := renderer.CloneForAsset(ctx, parsedPipeline, asset)
	if err != nil {
		return "", err
	}
	return assetRenderer.Render(mergeAssetMacrosWithQuery(asset.ExecutableFile.Content, parsedPipeline.Macros))
}

func authoringGraphRelationSchema(graph sqllsp.CanonicalGraph, relationName string) ([]WorkspaceColumn, SchemaCompleteness, SchemaConfidence) {
	relationID := ""
	for _, relation := range graph.Relations {
		if strings.EqualFold(strings.TrimSpace(relation.Name), strings.TrimSpace(relationName)) {
			relationID = relation.ID
			break
		}
	}
	var columns []sqllsp.ColumnInfo
	completeness := SchemaUnknown
	confidence := SchemaConfidenceLow
	for _, layer := range graph.Schemas {
		if layer.RelationID == relationID && len(layer.Columns) > 0 {
			columns = layer.Columns
			completeness = SchemaCompleteness(layer.Completeness)
			confidence = SchemaConfidence(layer.Confidence)
		}
	}
	result := make([]WorkspaceColumn, 0, len(columns))
	for _, column := range columns {
		result = append(result, WorkspaceColumn{
			Name: column.Name, Type: column.Type, Description: column.Description,
			Nullable: cloneBool(column.Nullable), PrimaryKey: column.PrimaryKey,
		})
	}
	return result, completeness, confidence
}
