package service

import (
	"context"
	"reflect"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/sqllsp"
	webmodel "renart/internal/web/model"
)

const maxAuthoringSchemaRounds = 5

// resolveAuthoringSchemaGraph is the common pure-authoring adapter used by
// typecheck and both LSP transports. Declaration providers run without any I/O
// authority, SQL projection inference runs over the canonical graph, and Load
// passthrough schemas feed back into that graph until the result stabilizes.
func resolveAuthoringSchemaGraph(
	ctx context.Context,
	graph sqllsp.CanonicalGraph,
	pp *pipeline.Pipeline,
	inferenceAssets []sqllsp.InferenceAsset,
) sqllsp.CanonicalGraph {
	if pp == nil {
		return sqllsp.InferSchemaSnapshot(ctx, graph, inferenceAssets)
	}

	for round := 0; round < maxAuthoringSchemaRounds; round++ {
		if ctx.Err() != nil {
			return graph
		}
		before := append([]sqllsp.SchemaLayer(nil), graph.Schemas...)
		forecast := completeGraphSchemaForecast(graph)
		resolver := newAssetDefinitionSchemaResolverWithForecast(pp, forecast)
		for _, asset := range pp.Assets {
			if asset == nil {
				continue
			}
			evidence := resolver.AvailableEvidence(ctx, asset)
			columns := pipelineColumnsForSchemaEvidence(evidence)
			if len(columns) == 0 {
				continue
			}
			graph = upsertAuthoringDeclarationLayer(graph, asset.Name, columns, evidence)
		}

		graph = stripInferredSchemaLayers(graph)
		graph = sqllsp.InferSchemaSnapshot(ctx, graph, inferenceAssets)
		if reflect.DeepEqual(before, graph.Schemas) {
			break
		}
	}
	return graph
}

func completeGraphSchemaForecast(graph sqllsp.CanonicalGraph) map[string][]pipeline.Column {
	layers := make(map[string]sqllsp.SchemaLayer, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		if strings.EqualFold(strings.TrimSpace(layer.Completeness), string(SchemaComplete)) && len(layer.Columns) > 0 {
			layers[layer.RelationID] = layer
		}
	}
	result := make(map[string][]pipeline.Column)
	for _, relation := range graph.Relations {
		layer, ok := layers[relation.ID]
		if !ok {
			continue
		}
		columns := make([]pipeline.Column, 0, len(layer.Columns))
		for _, column := range layer.Columns {
			converted := pipeline.Column{
				Name: column.Name, Type: column.Type, Description: column.Description,
				PrimaryKey: column.PrimaryKey,
			}
			if column.Nullable != nil {
				value := *column.Nullable
				converted.Nullable = pipeline.DefaultTrueBool{Value: &value}
			}
			if column.ForeignKey != nil {
				converted.ForeignKey = &pipeline.ColumnReference{Table: column.ForeignKey.Table, Column: column.ForeignKey.Column}
			}
			columns = append(columns, converted)
		}
		result[strings.ToLower(strings.TrimSpace(relation.Name))] = columns
	}
	return result
}

func upsertAuthoringDeclarationLayer(
	graph sqllsp.CanonicalGraph,
	assetName string,
	columns []pipeline.Column,
	evidence SchemaEvidence,
) sqllsp.CanonicalGraph {
	relationID := ""
	var provenance []sqllsp.Provenance
	for _, relation := range graph.Relations {
		if strings.EqualFold(strings.TrimSpace(relation.Name), strings.TrimSpace(assetName)) {
			relationID = relation.ID
			provenance = relation.Provenance
			break
		}
	}
	if relationID == "" {
		return graph
	}
	infos := make([]sqllsp.ColumnInfo, 0, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column.Name) != "" {
			infos = append(infos, columnInfoFromPipelineColumn(column))
		}
	}
	if len(infos) == 0 {
		return graph
	}
	completeness := string(evidence.Completeness)
	if completeness == "" {
		completeness = string(SchemaComplete)
	}
	confidence := string(evidence.Confidence)
	if confidence == "" {
		confidence = string(SchemaConfidenceHigh)
	}
	next := sqllsp.SchemaLayer{
		RelationID: relationID, SourceKind: "declared", Completeness: completeness,
		Confidence: confidence, Columns: infos, Provenance: provenance,
	}
	for index := range graph.Schemas {
		if graph.Schemas[index].RelationID == relationID {
			graph.Schemas[index] = next
			return graph
		}
	}
	graph.Schemas = append(graph.Schemas, next)
	return graph
}

func stripInferredSchemaLayers(graph sqllsp.CanonicalGraph) sqllsp.CanonicalGraph {
	kept := make([]sqllsp.SchemaLayer, 0, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(layer.SourceKind)), "inferred-") {
			continue
		}
		kept = append(kept, layer)
	}
	graph.Schemas = kept
	return graph
}

func pipelineFromSchemaModels(assets []webmodel.Asset) *pipeline.Pipeline {
	pp := &pipeline.Pipeline{Assets: make([]*pipeline.Asset, 0, len(assets))}
	for _, asset := range assets {
		parameters := make(pipeline.ParameterMap, len(asset.Parameters))
		for key, value := range asset.Parameters {
			parameters[key] = value
		}
		upstreams := make([]pipeline.Upstream, 0, len(asset.Upstreams))
		for _, upstream := range asset.Upstreams {
			if strings.TrimSpace(upstream) != "" {
				upstreams = append(upstreams, pipeline.Upstream{Type: "asset", Value: upstream})
			}
		}
		pp.Assets = append(pp.Assets, &pipeline.Asset{
			Name:       asset.Name,
			Type:       pipeline.AssetType(asset.Type),
			Connection: asset.ExplicitConnection,
			Parameters: parameters,
			Columns:    ModelColumnsToPipelineColumns(asset.Columns),
			Upstreams:  upstreams,
			ExecutableFile: pipeline.ExecutableFile{
				Path: asset.Path, Content: asset.Content,
			},
			DefinitionFile: pipeline.TaskDefinitionFile{Path: asset.Path},
			Materialization: pipeline.Materialization{
				Type: pipeline.MaterializationType(asset.MaterializationType),
			},
		})
	}
	return pp
}
