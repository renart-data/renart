package service

import (
	"context"
	"os"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"

	"renart/internal/sqlintelligence"
	"renart/internal/sqllsp"
	webexecution "renart/internal/web/execution"
)

func (s *pipelinePlanningSession) SemanticImpact(ctx context.Context) webexecution.SemanticImpactReport {
	if s == nil || s.owner == nil || s.source == nil || s.input.Plan == nil {
		return webexecution.UnavailableSemanticImpact("Semantic impact analysis is unavailable for this deployment.")
	}
	return s.owner.semanticImpactForDeployment(ctx, s.input.Plan.PipelineUUID, s.source.pipelineDir)
}

func (s *PipelinePlanService) semanticImpactForDeployment(
	ctx context.Context,
	pipelineUUID string,
	candidateDir string,
) webexecution.SemanticImpactReport {
	if s == nil || s.deps.Snapshots == nil {
		return webexecution.UnavailableSemanticImpact("Deployment snapshots are unavailable for semantic comparison.")
	}
	latest, err := s.deps.Snapshots.Latest(ctx, pipelineUUID)
	if err != nil {
		return webexecution.UnavailableSemanticImpact("The latest deployment could not be loaded for semantic comparison.")
	}
	if latest == nil {
		return webexecution.NoBaselineSemanticImpact()
	}
	baselineDir, err := os.MkdirTemp("", "renart-semantic-baseline-")
	if err != nil {
		return webexecution.UnavailableSemanticImpact("The deployment baseline could not be prepared for semantic comparison.")
	}
	defer func() { _ = os.RemoveAll(baselineDir) }()
	if err := s.deps.Snapshots.MaterializeForPipelineExecution(
		ctx, latest.VersionID, pipelineUUID, baselineDir,
	); err != nil {
		return webexecution.UnavailableSemanticImpact("The deployment baseline failed integrity validation.")
	}
	baselineGraph, err := sqllsp.LoadGraphFromDir(ctx, baselineDir)
	if err != nil {
		return webexecution.UnavailableSemanticImpact("The deployment baseline could not be analyzed.")
	}
	candidateGraph, err := sqllsp.LoadGraphFromDir(ctx, candidateDir)
	if err != nil {
		return webexecution.UnavailableSemanticImpact("The saved working tree could not be analyzed.")
	}
	return webexecution.CompareSemanticImpact(
		latest.VersionID,
		semanticImpactAssetSnapshots(ctx, baselineGraph),
		semanticImpactAssetSnapshots(ctx, candidateGraph),
	)
}

func semanticImpactAssetSnapshots(ctx context.Context, graph sqllsp.CanonicalGraph) []webexecution.SemanticAssetSnapshot {
	renderingByAsset := make(map[string]sqllsp.RenderedSQL, len(graph.Renderings))
	for _, rendering := range graph.Renderings {
		renderingByAsset[rendering.AssetID] = rendering
	}
	relationByAsset := make(map[string]sqllsp.RelationNode, len(graph.Relations))
	for _, relation := range graph.Relations {
		if relation.AssetID != "" {
			relationByAsset[relation.AssetID] = relation
		}
	}
	schemaByRelation := make(map[string]sqllsp.SchemaLayer, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		schemaByRelation[layer.RelationID] = layer
	}

	result := make([]webexecution.SemanticAssetSnapshot, 0, len(graph.Renderings))
	for _, asset := range graph.Assets {
		rendering, ok := renderingByAsset[asset.ID]
		if !ok || strings.TrimSpace(rendering.RenderedSQL) == "" {
			continue
		}
		identity, identityErr := sqlintelligence.QuerySemanticIdentity(rendering.RenderedSQL, asset.Dialect)
		snapshot := webexecution.SemanticAssetSnapshot{
			Name: asset.Name, Dialect: asset.Dialect,
			SourceFingerprint:    identity.SourceFingerprint,
			CanonicalFingerprint: identity.CanonicalFingerprint,
			Complete:             identityErr == nil,
			Columns:              []webexecution.SemanticColumnContract{},
		}
		relation, relationOK := relationByAsset[asset.ID]
		layer, layerOK := schemaByRelation[relation.ID]
		if layerOK && layer.SourceKind == "declared" {
			// The graph deliberately trusts declared table schemas for downstream
			// resolution. This asset's diff must compare its actual SQL output,
			// even while its own declaration is stale. Reuse shared Go inference
			// without altering the canonical graph or weakening schema checks.
			inferred := sqllsp.InferOutputSchema(ctx, graph, sqllsp.TextDocumentItem{
				URI: asset.URI, LanguageID: "sql", Text: rendering.RenderedSQL,
			}, asset.Dialect)
			for index := range inferred.Columns {
				for _, declared := range layer.Columns {
					if inferred.Columns[index].Name == declared.Name {
						inferred.Columns[index].Nullable = declared.Nullable
						break
					}
				}
			}
			layer.Columns, layer.Completeness = inferred.Columns, inferred.Completeness
		}
		snapshot.Complete = snapshot.Complete && relationOK && layerOK &&
			layer.Completeness == "complete" && len(layer.Columns) > 0
		if layerOK {
			for _, column := range layer.Columns {
				contract := webexecution.SemanticColumnContract{
					Name:        strings.TrimSpace(column.Name),
					Type:        semanticImpactType(column.Type, asset.Dialect),
					Nullability: semanticImpactNullability(column.Nullable),
				}
				if contract.Name == "" || contract.Type == "" {
					snapshot.Complete = false
				}
				snapshot.Columns = append(snapshot.Columns, contract)
			}
		}
		snapshot.Source = semanticFileSourceAnchors(asset.URI, asset.Dialect, snapshot.CanonicalFingerprint, len(snapshot.Columns))
		result = append(result, snapshot)
	}
	return result
}

func semanticImpactType(value, dialect string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	nativeDialect, err := golyglot.ParseDialect(strings.TrimSpace(dialect))
	if err != nil {
		nativeDialect = golyglot.DialectGeneric
	}
	parsed, err := golyglot.ParseDataType(value, nativeDialect)
	if err == nil && parsed.Known() {
		return parsed.SQL()
	}
	return strings.ToUpper(strings.Join(strings.Fields(value), " "))
}

func semanticImpactNullability(value *bool) string {
	if value == nil {
		return "unknown"
	}
	if *value {
		return "nullable"
	}
	return "non_null"
}
