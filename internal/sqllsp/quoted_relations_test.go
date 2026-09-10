package sqllsp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEngineResolvesQuotedQualifiedRelations(t *testing.T) {
	for _, tc := range []struct{ name, reference, dialect string }{
		{"marts.blub", `"marts"."blub"`, "duckdb"},
		{"chess_playground.marts.blub", `"chess_playground"."marts"."blub"`, "duckdb"},
		{"chess_playground.marts.blub", "\"chess_playground\" \n. \"marts\"\t. blub", "duckdb"},
		{"chess playground.marts.blub", `"chess playground"."marts"."blub"`, "duckdb"},
		{"marts.bl\"ub", `"marts"."bl""ub"`, "duckdb"},
		{"warehouse.marts.blub", "`warehouse`.`marts`.`blub`", "mysql"},
	} {
		t.Run(tc.reference, func(t *testing.T) {
			engine := NewEngine(CanonicalGraph{
				Assets:    []AssetNode{{ID: "query", Name: "query", URI: "file:///query.sql", Dialect: tc.dialect}},
				Relations: []RelationNode{{ID: "table", Name: tc.name}},
				Schemas:   []SchemaLayer{{RelationID: "table", Completeness: "complete", Columns: []ColumnInfo{{Name: "id", Type: "integer"}}}},
			})
			doc := TextDocumentItem{URI: "file:///query.sql", Text: "select t.id from " + tc.reference + " as t"}
			for _, diagnostic := range engine.Diagnostics(doc) {
				require.NotEqual(t, "unresolved-relation", diagnostic.Code, "%+v", diagnostic)
				require.NotEqual(t, "unresolved-column", diagnostic.Code, "%+v", diagnostic)
			}
			analysis := analyzeSQLWithResolver(doc.Text, engine)
			require.Len(t, analysis.relations, 1)
			require.Equal(t, tc.name, analysis.relations[0].name)
			require.Equal(t, tc.reference, doc.Text[analysis.relations[0].start:analysis.relations[0].end])

			doc.Text = strings.Replace(doc.Text, "t.id", "t.missing_column", 1)
			var missingColumn bool
			for _, diagnostic := range engine.Diagnostics(doc) {
				missingColumn = missingColumn || diagnostic.Code == "unresolved-column"
			}
			require.True(t, missingColumn, "quoting must not disable column validation")
		})
	}
}

func TestNormalizeRelationPreservesQuotedContents(t *testing.T) {
	for _, tc := range []struct{ reference, name string }{
		{`"warehouse" . "marts" . "blub"`, "warehouse.marts.blub"},
		{`"my schema"."some.table"`, "my schema.some.table"},
		{"`my``schema`.`table`", "my`schema.table"},
		{`'data/my file.parquet'`, "data/my file.parquet"},
		{`'data/it''s here.parquet'`, "data/it's here.parquet"},
	} {
		t.Run(tc.reference, func(t *testing.T) {
			require.Equal(t, tc.name, normalizeRelation(tc.reference))
		})
	}
}
