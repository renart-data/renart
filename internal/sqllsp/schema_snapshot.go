package sqllsp

import (
	"context"
	"slices"
	"sort"
	"strings"

	"renart/internal/sqlintelligence"
)

type InferenceAsset struct {
	ID        string
	Name      string
	URI       URI
	SQL       string
	Dialect   string
	Upstreams []string
}

// OutputSchemaInference is the pure projection result for one SQL document
// against an already-resolved canonical graph. It lets non-asset SQL surfaces
// share the same AST, compact-analysis, and tolerant fallbacks without adding
// a synthetic materialized relation to the graph.
type OutputSchemaInference struct {
	Columns      []ColumnInfo
	SourceKind   string
	Completeness string
	Confidence   string
}

const maxSchemaInferenceRounds = 5

// InferSchemaSnapshot fills unknown SQL relation schemas with one shared,
// topologically ordered fixpoint. Golyglot's typed AST analysis is the fast
// path; cached compact analysis fills incomplete projection names (especially
// stars), and the tolerant LSP projection analyzer is the final fallback.
func InferSchemaSnapshot(ctx context.Context, graph CanonicalGraph, assets []InferenceAsset) CanonicalGraph {
	if len(assets) == 0 {
		assets = inferenceAssetsFromGraph(graph)
	}
	relationByAssetID := map[string]RelationNode{}
	knownRelations := map[string]bool{}
	for _, layer := range graph.Schemas {
		if layer.Completeness == "complete" && len(layer.Columns) > 0 {
			knownRelations[layer.RelationID] = true
		}
	}
	for _, relation := range graph.Relations {
		if relation.AssetID != "" {
			relationByAssetID[relation.AssetID] = relation
		}
	}

	targets := make([]InferenceAsset, 0, len(assets))
	for _, asset := range assets {
		relation, ok := relationByAssetID[asset.ID]
		if !ok || knownRelations[relation.ID] || strings.TrimSpace(asset.SQL) == "" {
			continue
		}
		targets = append(targets, asset)
	}
	if len(targets) == 0 {
		return graph
	}
	targets = topoOrderInferenceAssets(targets)

	baseSchemas := make([]SchemaLayer, 0, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		if layer.SourceKind != "inferred-analysis" && layer.SourceKind != "inferred-ast" && layer.SourceKind != "inferred-tolerant" {
			baseSchemas = append(baseSchemas, layer)
		}
	}
	inferred := map[string]OutputSchemaInference{}

	for round := 0; round < maxSchemaInferenceRounds; round++ {
		if err := ctx.Err(); err != nil {
			return graph
		}
		engine := NewEngine(graph)
		validationSchema, relationConfidence := ValidationSchema(graph)
		changed := false
		for _, asset := range targets {
			layer := inferOutputSchema(
				ctx,
				engine,
				TextDocumentItem{URI: asset.URI, LanguageID: "sql", Text: asset.SQL},
				asset.Dialect,
				validationSchema,
				relationConfidence,
			)
			next := layer.Columns
			previous := inferred[asset.ID]
			if slices.Equal(previous.Columns, layer.Columns) && previous.SourceKind == layer.SourceKind {
				continue
			}
			inferred[asset.ID] = layer
			relation := relationByAssetID[asset.ID]
			engine.SetRelationColumns(relation.ID, next)
			columns := make(map[string]string, len(next))
			for _, column := range next {
				columns[column.Name] = column.Type
			}
			validationSchema[relation.Name] = columns
			relationConfidence[relation.Name] = sqlintelligence.RelationUnknown
			if layer.Completeness == "complete" && len(columns) > 0 {
				relationConfidence[relation.Name] = sqlintelligence.RelationKnown
			}
			changed = true
		}
		if !changed {
			break
		}

		schemas := append(make([]SchemaLayer, 0, len(baseSchemas)+len(targets)), baseSchemas...)
		for _, asset := range targets {
			layer, ok := inferred[asset.ID]
			if !ok || len(layer.Columns) == 0 {
				continue
			}
			relation := relationByAssetID[asset.ID]
			schemas = append(schemas, SchemaLayer{
				RelationID:   relation.ID,
				SourceKind:   layer.SourceKind,
				Completeness: layer.Completeness,
				Confidence:   layer.Confidence,
				Columns:      layer.Columns,
				Provenance:   relation.Provenance,
			})
		}
		graph.Schemas = schemas
	}
	return graph
}

// InferOutputSchema derives one SQL document's output against an already
// resolved graph without mutating that graph or treating the document as a
// materialized asset.
func InferOutputSchema(
	ctx context.Context,
	graph CanonicalGraph,
	doc TextDocumentItem,
	dialect string,
) OutputSchemaInference {
	engine := NewEngine(graph)
	validationSchema, relationConfidence := ValidationSchema(graph)
	return inferOutputSchema(ctx, engine, doc, dialect, validationSchema, relationConfidence)
}

func inferOutputSchema(
	ctx context.Context,
	engine *Engine,
	doc TextDocumentItem,
	dialect string,
	validationSchema sqlintelligence.Schema,
	relationConfidence map[string]sqlintelligence.RelationConfidence,
) OutputSchemaInference {
	projection := engine.renderDocument(doc)
	annotated, annotateErr := sqlintelligence.InferOutputSchema(ctx, projection.doc.Text, dialect, validationSchema)
	next := columnInfosFromSchemaColumns(annotated.Columns)
	result := OutputSchemaInference{
		Columns: next, SourceKind: "inferred-ast", Completeness: "complete", Confidence: "high",
	}
	if annotateErr != nil || !annotated.NamesComplete || len(next) == 0 {
		analysis, analysisErr := sqlintelligence.AnalyzeQuery(ctx, projection.doc.Text, dialect, validationSchema)
		if analysisErr == nil && analysis.OutputNamesComplete && len(analysis.OutputColumns) > 0 {
			next = columnInfosFromSchemaColumns(analysis.OutputColumns)
			result = OutputSchemaInference{
				Columns: next, SourceKind: "inferred-analysis", Completeness: "partial", Confidence: "medium",
			}
			if analysisStarSourcesKnown(analysis, relationConfidence) {
				result.Completeness = "complete"
				result.Confidence = "high"
			}
		} else if len(next) > 0 {
			result.Completeness = "partial"
			result.Confidence = "medium"
		}
	}
	if len(next) == 0 {
		next = engine.InferOutputColumns(projection.doc.Text)
		result = OutputSchemaInference{
			Columns: next, SourceKind: "inferred-tolerant", Completeness: "partial", Confidence: "medium",
		}
	}
	return result
}

func columnInfosFromSchemaColumns(columns []sqlintelligence.SchemaColumn) []ColumnInfo {
	result := make([]ColumnInfo, 0, len(columns))
	for _, column := range columns {
		if name := strings.TrimSpace(column.Name); name != "" {
			// Inference layers describe names and types only. Constraints belong to
			// the declared/observed overlays below; otherwise a query expression's
			// inferred nullability is mistaken for durable table metadata.
			result = append(result, ColumnInfo{Name: name, Type: strings.TrimSpace(column.Type)})
		}
	}
	return result
}

func analysisStarSourcesKnown(analysis sqlintelligence.QueryAnalysis, confidence map[string]sqlintelligence.RelationConfidence) bool {
	if len(analysis.StarProjections) == 0 {
		return true
	}
	for _, relation := range analysis.BaseTables {
		if !strings.EqualFold(relation.Kind, "table") {
			continue
		}
		known := false
		for name, value := range confidence {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(relation.Name)) {
				known = value == sqlintelligence.RelationKnown
				break
			}
		}
		if !known {
			return false
		}
	}
	return true
}

// ValidationSchema projects a canonical schema snapshot into the semantic SQL
// validator's schema and explicit known/unknown relation confidence.
func ValidationSchema(graph CanonicalGraph) (sqlintelligence.Schema, map[string]sqlintelligence.RelationConfidence) {
	layersByRelation := make(map[string]SchemaLayer, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		layersByRelation[layer.RelationID] = layer
	}
	schema := make(sqlintelligence.Schema, len(graph.Relations))
	confidence := make(map[string]sqlintelligence.RelationConfidence, len(graph.Relations))
	for _, relation := range graph.Relations {
		name := strings.TrimSpace(relation.Name)
		if name == "" {
			continue
		}
		columns := map[string]string{}
		layer, hasLayer := layersByRelation[relation.ID]
		if hasLayer {
			for _, column := range layer.Columns {
				if columnName := strings.TrimSpace(column.Name); columnName != "" {
					columns[columnName] = column.Type
				}
			}
		}
		schema[name] = columns
		confidence[name] = sqlintelligence.RelationUnknown
		if hasLayer && layer.Completeness == "complete" && len(columns) > 0 {
			confidence[name] = sqlintelligence.RelationKnown
		}
	}
	return schema, confidence
}

// ValidationSchemaConstraints projects only explicitly declared column
// constraints. Inferred layers intentionally contribute no constraints: a
// primary or foreign key is a data contract and must never be guessed from a
// projection name.
func ValidationSchemaConstraints(graph CanonicalGraph) sqlintelligence.SchemaConstraints {
	validationSchema, _ := ValidationSchema(graph)
	return validationSchemaConstraints(graph, validationSchema)
}

// ValidationSchemaWithConstraints builds the per-request schema payload in one
// pass over the graph-facing projection, avoiding a duplicate projection on
// the editor's per-keystroke diagnostics path.
func ValidationSchemaWithConstraints(graph CanonicalGraph) (sqlintelligence.Schema, sqlintelligence.SchemaConstraints, map[string]sqlintelligence.RelationConfidence) {
	validationSchema, confidence := ValidationSchema(graph)
	return validationSchema, validationSchemaConstraints(graph, validationSchema), confidence
}

func validationSchemaConstraints(graph CanonicalGraph, validationSchema sqlintelligence.Schema) sqlintelligence.SchemaConstraints {
	layersByRelation := make(map[string]SchemaLayer, len(graph.Schemas))
	for _, layer := range graph.Schemas {
		if strings.EqualFold(strings.TrimSpace(layer.SourceKind), "declared") {
			layersByRelation[layer.RelationID] = layer
		}
	}
	constraints := make(sqlintelligence.SchemaConstraints)
	for _, relation := range graph.Relations {
		name := strings.TrimSpace(relation.Name)
		layer, ok := layersByRelation[relation.ID]
		if name == "" || !ok {
			continue
		}
		columns := make(map[string]sqlintelligence.SchemaColumnConstraints)
		for _, column := range layer.Columns {
			columnName := strings.TrimSpace(column.Name)
			if columnName == "" || (column.Nullable == nil && !column.PrimaryKey && column.ForeignKey == nil) {
				continue
			}
			metadata := sqlintelligence.SchemaColumnConstraints{
				Nullable:   cloneBoolPointer(column.Nullable),
				PrimaryKey: column.PrimaryKey,
			}
			if column.ForeignKey != nil {
				table := strings.TrimSpace(column.ForeignKey.Table)
				targetColumn := strings.TrimSpace(column.ForeignKey.Column)
				// Golyglot validates the entire schema before the query. Only pass
				// references whose target is represented in this snapshot; otherwise
				// one unrelated or partially-known asset would emit E220 in every
				// document. Bruin remains authoritative for invalid FK metadata.
				if table != "" && targetColumn != "" && validationSchemaHasColumn(validationSchema, table, targetColumn) {
					metadata.ForeignKey = &sqlintelligence.SchemaColumnReference{Table: table, Column: targetColumn}
				}
			}
			columns[columnName] = metadata
		}
		if len(columns) > 0 {
			constraints[name] = sqlintelligence.SchemaTableConstraints{Columns: columns}
		}
	}
	return constraints
}

func validationSchemaHasColumn(schema sqlintelligence.Schema, tableName, columnName string) bool {
	for candidateTable, columns := range schema {
		if !strings.EqualFold(strings.TrimSpace(candidateTable), strings.TrimSpace(tableName)) {
			continue
		}
		for candidateColumn := range columns {
			if strings.EqualFold(strings.TrimSpace(candidateColumn), strings.TrimSpace(columnName)) {
				return true
			}
		}
		return false
	}
	return false
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func inferenceAssetsFromGraph(graph CanonicalGraph) []InferenceAsset {
	assetsByID := make(map[string]AssetNode, len(graph.Assets))
	for _, asset := range graph.Assets {
		assetsByID[asset.ID] = asset
	}
	result := make([]InferenceAsset, 0, len(graph.Renderings))
	for _, rendering := range graph.Renderings {
		asset, ok := assetsByID[rendering.AssetID]
		if !ok {
			continue
		}
		upstreams := make([]string, 0, len(asset.InputRelations))
		for _, relationID := range asset.InputRelations {
			for _, relation := range graph.Relations {
				if relation.ID == relationID {
					upstreams = append(upstreams, relation.Name)
					break
				}
			}
		}
		result = append(result, InferenceAsset{ID: asset.ID, Name: asset.Name, URI: asset.URI, SQL: rendering.RenderedSQL, Dialect: asset.Dialect, Upstreams: upstreams})
	}
	return result
}

func topoOrderInferenceAssets(targets []InferenceAsset) []InferenceAsset {
	indexByName := map[string]int{}
	for i, asset := range targets {
		for _, name := range []string{asset.ID, asset.Name, string(asset.URI)} {
			if strings.TrimSpace(name) != "" {
				indexByName[strings.ToLower(strings.TrimSpace(name))] = i
			}
		}
	}
	downstream := make([][]int, len(targets))
	indegree := make([]int, len(targets))
	for i, asset := range targets {
		for _, upstream := range asset.Upstreams {
			j, ok := indexByName[strings.ToLower(strings.TrimSpace(upstream))]
			if !ok || j == i {
				continue
			}
			downstream[j] = append(downstream[j], i)
			indegree[i]++
		}
	}
	queue := make([]int, 0, len(targets))
	for i := range targets {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	sort.Ints(queue)
	ordered := make([]InferenceAsset, 0, len(targets))
	visited := make([]bool, len(targets))
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		visited[i] = true
		ordered = append(ordered, targets[i])
		for _, j := range downstream[i] {
			indegree[j]--
			if indegree[j] == 0 {
				queue = append(queue, j)
				sort.Ints(queue)
			}
		}
	}
	for i := range targets {
		if !visited[i] {
			ordered = append(ordered, targets[i])
		}
	}
	return ordered
}
