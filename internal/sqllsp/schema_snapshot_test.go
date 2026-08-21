package sqllsp

import (
	"context"
	"testing"

	"renart/internal/sqlintelligence"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferSchemaSnapshotUsesASTAndPropagatesThroughGraph(t *testing.T) {
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{
		{ID: "base", Name: "analytics.base", URI: "file:///workspace/base.sql", Dialect: "duckdb"},
		{ID: "middle", Name: "analytics.middle", URI: "file:///workspace/middle.sql", Dialect: "duckdb"},
		{ID: "final", Name: "analytics.final", URI: "file:///workspace/final.sql", Dialect: "duckdb"},
	}, map[string][]ColumnInfo{
		"base": {{Name: "id", Type: "INTEGER"}},
	})
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{
		{ID: "final", Name: "analytics.final", URI: "file:///workspace/final.sql", SQL: "select * from analytics.middle", Dialect: "duckdb", Upstreams: []string{"analytics.middle"}},
		{ID: "middle", Name: "analytics.middle", URI: "file:///workspace/middle.sql", SQL: "select id, id + 1 as next_id from analytics.base", Dialect: "duckdb", Upstreams: []string{"analytics.base"}},
	})

	schema, confidence := ValidationSchema(graph)
	if confidence["analytics.middle"] != sqlintelligence.RelationKnown || confidence["analytics.final"] != sqlintelligence.RelationKnown {
		t.Fatalf("inferred confidence = %#v", confidence)
	}
	for _, relation := range []string{"analytics.middle", "analytics.final"} {
		if _, ok := schema[relation]["id"]; !ok {
			t.Fatalf("%s did not inherit id: %#v", relation, schema[relation])
		}
		if _, ok := schema[relation]["next_id"]; !ok {
			t.Fatalf("%s did not inherit next_id: %#v", relation, schema[relation])
		}
	}

	inferredLayers := 0
	for _, layer := range graph.Schemas {
		if layer.RelationID != "relation:renart:analytics.middle" && layer.RelationID != "relation:renart:analytics.final" {
			continue
		}
		if layer.SourceKind == "unknown" {
			continue
		}
		inferredLayers++
		if layer.SourceKind != "inferred-ast" || layer.Completeness != "complete" || layer.Confidence != "high" {
			t.Fatalf("inferred layer does not have expected source/completeness: %#v", layer)
		}
	}
	if inferredLayers != 2 {
		t.Fatalf("inferred compact-analysis layers = %d, want 2: %#v", inferredLayers, graph.Schemas)
	}
}

func TestInferOutputSchemaReadsExistingInferredLayersWithoutRebuildingGraph(t *testing.T) {
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{{
		ID: "orders", Name: "analytics.orders", URI: "file:///workspace/orders.sql", Dialect: "duckdb",
	}}, nil)
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{{
		ID: "orders", Name: "analytics.orders", URI: "file:///workspace/orders.sql", Dialect: "duckdb",
		SQL: "select 1 as order_id, 42::bigint as total_amount",
	}})
	before := append([]SchemaLayer(nil), graph.Schemas...)

	inferred := InferOutputSchema(context.Background(), graph, TextDocumentItem{
		URI: "inmemory://presentation/orders.sql", LanguageID: "sql",
		Text: "select * from analytics.orders",
	}, "duckdb")

	require.Len(t, inferred.Columns, 2)
	assert.ElementsMatch(t, []string{"order_id", "total_amount"}, []string{
		inferred.Columns[0].Name, inferred.Columns[1].Name,
	})
	assert.Equal(t, "complete", inferred.Completeness)
	assert.Equal(t, before, graph.Schemas)
}

func TestInferSchemaSnapshotInfersJoinedStar(t *testing.T) {
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{
		{ID: "left", Name: "analytics.left", URI: "file:///workspace/left.sql", Dialect: "duckdb"},
		{ID: "right", Name: "analytics.right", URI: "file:///workspace/right.sql", Dialect: "duckdb"},
		{ID: "joined", Name: "analytics.joined", URI: "file:///workspace/joined.sql", Dialect: "duckdb"},
	}, map[string][]ColumnInfo{
		"left":  {{Name: "id", Type: "INTEGER"}, {Name: "left_value", Type: "VARCHAR"}},
		"right": {{Name: "id", Type: "INTEGER"}, {Name: "right_value", Type: "DOUBLE"}},
	})
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{{
		ID: "joined", Name: "analytics.joined", URI: "file:///workspace/joined.sql", Dialect: "duckdb",
		SQL:       "select * from analytics.left l join analytics.right r on l.id = r.id",
		Upstreams: []string{"analytics.left", "analytics.right"},
	}})

	schema, confidence := ValidationSchema(graph)
	if confidence["analytics.joined"] != sqlintelligence.RelationKnown {
		t.Fatalf("joined confidence = %q, want known", confidence["analytics.joined"])
	}
	if got := schema["analytics.joined"]; got["left_value"] == "" || got["right_value"] == "" {
		t.Fatalf("joined star did not include both relation schemas: %#v", got)
	}
	for _, layer := range graph.Schemas {
		if layer.RelationID == "relation:renart:analytics.joined" && layer.SourceKind == "inferred-ast" {
			return
		}
	}
	t.Fatalf("joined star did not produce a native AST layer: %#v", graph.Schemas)
}

func TestInferSchemaSnapshotFallsBackForDuckDBTableFunctionType(t *testing.T) {
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{{
		ID: "range", Name: "analytics.range", URI: "file:///workspace/range.sql", Dialect: "duckdb",
	}}, nil)
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{{
		ID: "range", Name: "analytics.range", URI: "file:///workspace/range.sql",
		SQL: "select range as value from range(10)", Dialect: "duckdb",
	}})

	schema, confidence := ValidationSchema(graph)
	if confidence["analytics.range"] != sqlintelligence.RelationKnown {
		t.Fatalf("range confidence = %q, want known", confidence["analytics.range"])
	}
	if got := schema["analytics.range"]["value"]; got != "BIGINT" {
		t.Fatalf("range output type = %q, want BIGINT", got)
	}
	foundFallback := false
	for _, layer := range graph.Schemas {
		if layer.RelationID != "relation:renart:analytics.range" || layer.SourceKind == "unknown" {
			continue
		}
		foundFallback = true
		if layer.SourceKind != "inferred-ast" {
			t.Fatalf("range inference should use annotated-AST fallback: %#v", layer)
		}
	}
	if !foundFallback {
		t.Fatalf("range inference did not produce a schema layer: %#v", graph.Schemas)
	}
}

func TestInferSchemaSnapshotKeepsDuckDBRangeArithmeticBigInt(t *testing.T) {
	graph := CanonicalGraph{
		Assets: []AssetNode{{ID: "range-arithmetic", Name: "analytics.range_arithmetic", URI: "file:///range-arithmetic.sql"}},
		Relations: []RelationNode{{
			ID: "analytics.range_arithmetic", Name: "analytics.range_arithmetic", AssetID: "range-arithmetic",
		}},
	}
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{{
		ID: "range-arithmetic", Name: "analytics.range_arithmetic", URI: "file:///range-arithmetic.sql",
		SQL: "select range, range * 2 as double_range from range(1, 2, 1)", Dialect: "duckdb",
	}})

	schema, _ := ValidationSchema(graph)
	if got := schema["analytics.range_arithmetic"]["range"]; got != "BIGINT" {
		t.Fatalf("range output type = %q, want BIGINT", got)
	}
	if got := schema["analytics.range_arithmetic"]["double_range"]; got != "BIGINT" {
		t.Fatalf("double_range output type = %q, want BIGINT", got)
	}
}

func TestValidationSchemaKeepsUnknownRelationExplicit(t *testing.T) {
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{{ID: "python", Name: "analytics.python"}}, nil)
	schema, confidence := ValidationSchema(graph)
	if _, exists := schema["analytics.python"]; !exists {
		t.Fatal("unknown relation is absent from validation schema")
	}
	if confidence["analytics.python"] != sqlintelligence.RelationUnknown {
		t.Fatalf("confidence = %q, want unknown", confidence["analytics.python"])
	}
}

func TestValidationSchemaDoesNotTreatPartialLayerAsKnown(t *testing.T) {
	graph := CanonicalGraph{
		Relations: []RelationNode{{ID: "relation", Name: "analytics.partial"}},
		Schemas: []SchemaLayer{{
			RelationID: "relation", SourceKind: "inferred-tolerant", Completeness: "partial", Confidence: "medium",
			Columns: []ColumnInfo{{Name: "known_column", Type: "INTEGER"}},
		}},
	}
	schema, confidence := ValidationSchema(graph)
	if schema["analytics.partial"]["known_column"] != "INTEGER" {
		t.Fatalf("partial schema columns were lost: %#v", schema)
	}
	if confidence["analytics.partial"] != sqlintelligence.RelationUnknown {
		t.Fatalf("partial schema confidence = %q, want unknown", confidence["analytics.partial"])
	}
}

func TestValidationSchemaConstraintsKeepsDeclaredMetadataOnly(t *testing.T) {
	nullable := false
	graph := GraphFromRenartAssets("file:///workspace", []AssetNode{
		{ID: "users", Name: "analytics.users"},
		{ID: "orders", Name: "analytics.orders"},
		{ID: "derived", Name: "analytics.derived"},
	}, map[string][]ColumnInfo{
		"users": {{Name: "id", Type: "INTEGER", Nullable: &nullable, PrimaryKey: true}},
		"orders": {
			{Name: "id", Type: "INTEGER"},
			{Name: "user_id", Type: "INTEGER", ForeignKey: &ColumnReference{Table: "analytics.users", Column: "id"}},
			{Name: "invalid_id", Type: "INTEGER", ForeignKey: &ColumnReference{Table: "analytics.missing", Column: "id"}},
		},
	})
	graph = InferSchemaSnapshot(context.Background(), graph, []InferenceAsset{{
		ID: "derived", Name: "analytics.derived", SQL: "select id from analytics.users", Dialect: "duckdb",
	}})

	constraints := ValidationSchemaConstraints(graph)
	users := constraints["analytics.users"].Columns["id"]
	require.NotNil(t, users.Nullable)
	assert.False(t, *users.Nullable)
	assert.True(t, users.PrimaryKey)

	orders := constraints["analytics.orders"].Columns
	require.NotNil(t, orders["user_id"].ForeignKey)
	assert.Equal(t, sqlintelligence.SchemaColumnReference{Table: "analytics.users", Column: "id"}, *orders["user_id"].ForeignKey)
	assert.Nil(t, orders["invalid_id"].ForeignKey, "unrepresented FK targets must not poison every validation request")
	assert.NotContains(t, constraints, "analytics.derived", "inferred columns must not acquire guessed constraints")
}
