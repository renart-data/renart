package sqllsp

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"renart/internal/authoringdiag"
)

func TestCatalogObservationAge(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		observed time.Time
		want     string
	}{
		{name: "recent", observed: now.Add(-30 * time.Second), want: "just now"},
		{name: "minutes", observed: now.Add(-12 * time.Minute), want: "12m ago"},
		{name: "hours", observed: now.Add(-3 * time.Hour), want: "3h ago"},
		{name: "days", observed: now.Add(-49 * time.Hour), want: "2d ago"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := catalogObservationAge(test.observed, now); got != test.want {
				t.Fatalf("catalogObservationAge() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEngineCompletesColumnsFromCTE(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id", Type: "integer"},
				{Name: "customer_id", Type: "integer"},
			},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `with recent_orders as (
  select order_id, customer_id from orders
)
select r.
from recent_orders r`}

	items := engine.Complete(doc, Position{Line: 3, Character: len("select r.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "customer_id") {
		t.Fatalf("expected CTE columns in completions, got %#v", labels)
	}
}

func TestEngineResolvesAndCompletesCTEAfterVizComment(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:playful-maple", Name: "playful_maple"}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:playful-maple",
			Columns: []ColumnInfo{
				{Name: "trino_seconds", Type: "double"},
				{Name: "starrocks_seconds", Type: "double"},
				{Name: "result", Type: "varchar"},
				{Name: "trino_rows", Type: "bigint"},
			},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `/* @viz(line, x: count, y: count_star()) */
with preagg as (
  select
    round(trino_seconds / starrocks_seconds) as ratio,
    count(*) over (order by trino_seconds / starrocks_seconds rows unbounded preceding) as count
  from playful_maple
  where result = '"MATCH"' and trino_rows > 0
)
select
from preagg`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == authoringdiag.CodeUnresolvedRelation && strings.Contains(diagnostic.Message, "preagg") {
			t.Fatalf("CTE after viz comment was unresolved: %#v", engine.Diagnostics(doc))
		}
	}
	items := engine.Complete(doc, PositionAt(doc.Text, strings.Index(doc.Text, "\nfrom preagg")))
	labels := completionLabels(items)
	for _, column := range []string{"ratio", "count"} {
		if !slices.Contains(labels, column) {
			t.Fatalf("expected CTE column %s after viz comment, got %#v", column, labels)
		}
	}
}

func TestEngineDoesNotTreatCopyOptionsAsColumns(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:create-partitions", Name: "create_partitions.create_partitions"}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:create-partitions",
			Columns:    []ColumnInfo{{Name: "statement", Type: "varchar"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `COPY create_partitions.create_partitions
TO .data/create_all_partitions.sql
format csv
header FALSE
delimiter '\t'`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == authoringdiag.CodeUnresolvedColumn {
			t.Fatalf("COPY format option was treated as a column: %#v", engine.Diagnostics(doc))
		}
	}
}

func TestEngineReportsGolyglotExpressionTypeMismatch(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:values", Name: "values_table"}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:values",
			SourceKind: "declared",
			Columns:    []ColumnInfo{{Name: "id", Type: "INTEGER"}},
		}},
	})

	diagnostics := engine.Diagnostics(TextDocumentItem{
		URI:  "file:///query.sql",
		Text: "select id + 'not a number' from values_table",
	})
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "sql-type-mismatch" {
			if diagnostic.Severity != diagnosticSeverityError {
				t.Fatalf("type mismatch severity = %d, want error", diagnostic.Severity)
			}
			return
		}
	}
	t.Fatalf("expected sql-type-mismatch diagnostic, got %#v", diagnostics)
}

func TestEngineAcceptsDuckDBValuesSeedCTE(t *testing.T) {
	const uri = URI("file:///notebooks/release-demo/seed.sql")
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:      "release-demo-seed",
			URI:     uri,
			Dialect: "duckdb",
		}},
	})

	valid := TextDocumentItem{URI: uri, Text: `WITH seed(day, channel, revenue, orders) AS (
  VALUES
    (DATE '2026-08-18', 'Organic', 12400, 182),
    (DATE '2026-08-18', 'Paid', 8150, 104),
    (DATE '2026-08-19', 'Organic', 13150, 191),
    (DATE '2026-08-19', 'Paid', 9025, 119)
)
SELECT day, channel, revenue, orders
FROM seed
ORDER BY day, channel`}
	for _, diagnostic := range engine.Diagnostics(valid) {
		if diagnostic.Severity == diagnosticSeverityError {
			t.Fatalf("valid VALUES-backed seed CTE produced an error: %#v", diagnostic)
		}
	}

	invalid := engine.Diagnostics(TextDocumentItem{
		URI:  uri,
		Text: "SELECT FROM seed ORDER BY",
	})
	for _, diagnostic := range invalid {
		if diagnostic.Severity == diagnosticSeverityError {
			return
		}
	}
	t.Fatalf("invalid SELECT produced no error diagnostics: %#v", invalid)
}

func TestEngineCompletesColumnsInsideCTEs(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{
				ID:   "relation:game-results",
				Name: "chess.game_results",
			},
			{
				ID:   "relation:players",
				Name: "chess.players",
			},
		},
		Schemas: []SchemaLayer{
			{
				RelationID: "relation:game-results",
				Columns: []ColumnInfo{
					{Name: "username", Type: "varchar"},
					{Name: "color", Type: "varchar"},
					{Name: "time_class", Type: "varchar"},
				},
			},
			{
				RelationID: "relation:players",
				Columns: []ColumnInfo{
					{Name: "rating", Type: "integer"},
				},
			},
		},
	})

	t.Run("qualified upstream in first CTE", func(t *testing.T) {
		doc := TextDocumentItem{URI: "file:///query.sql", Text: `with opening_rollup as (
  select games.
  from chess.game_results games
)
select * from opening_rollup`}

		items := engine.Complete(doc, Position{Line: 1, Character: len("  select games.")})
		labels := completionLabels(items)
		for _, column := range []string{"username", "color", "time_class"} {
			if !slices.Contains(labels, column) {
				t.Fatalf("expected %s inside the first CTE, got %#v", column, labels)
			}
		}
	})

	t.Run("unqualified columns from earlier CTE", func(t *testing.T) {
		doc := TextDocumentItem{URI: "file:///query.sql", Text: `with opening_rollup as (
  select username, color, time_class
  from chess.game_results
),
ranked_openings as (
  select *,

  from opening_rollup
)
select * from ranked_openings`}

		items := engine.Complete(doc, Position{Line: 6, Character: 0})
		labels := completionLabels(items)
		for _, column := range []string{"username", "color", "time_class"} {
			if !slices.Contains(labels, column) {
				t.Fatalf("expected earlier CTE column %s, got %#v", column, labels)
			}
		}
	})

	t.Run("does not inherit aliases from consuming query", func(t *testing.T) {
		doc := TextDocumentItem{URI: "file:///query.sql", Text: `with opening_rollup as (
  select outside.
  from chess.game_results games
)
select *
from opening_rollup
join chess.players outside on true`}

		items := engine.Complete(doc, Position{Line: 1, Character: len("  select outside.")})
		labels := completionLabels(items)
		if slices.Contains(labels, "rating") {
			t.Fatalf("did not expect consuming-query alias columns inside the CTE, got %#v", labels)
		}
	})
}

func TestEngineCompletesColumnsFromSubqueryAlias(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "total_amount"},
			},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select sq.
from (
  select order_id, total_amount from orders
) sq`}

	items := engine.Complete(doc, Position{Line: 0, Character: len("select sq.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "total_amount") {
		t.Fatalf("expected subquery columns in completions, got %#v", labels)
	}
}

func TestEngineCompletesExplicitColumnsFromValuesSubquery(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from (values (1, 2), (3, 4)) n(a, b)`}

	items := engine.Complete(doc, PositionAt(doc.Text, strings.Index(doc.Text, "*")))
	labels := completionLabels(items)
	for _, column := range []string{"a", "b"} {
		if !slices.Contains(labels, column) {
			t.Fatalf("expected VALUES alias column %s, got %#v", column, labels)
		}
	}

	qualified := TextDocumentItem{URI: doc.URI, Text: `select n.
from (values (1, 2), (3, 4)) n(a, b)`}
	qualifiedLabels := completionLabels(
		engine.Complete(qualified, Position{Line: 0, Character: len("select n.")}),
	)
	for _, column := range []string{"a", "b"} {
		if !slices.Contains(qualifiedLabels, column) {
			t.Fatalf("expected qualified VALUES alias column %s, got %#v", column, qualifiedLabels)
		}
	}
}

func TestEngineUsesDescribeResultColumnsForSubquery(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:my-asset",
			Name: "some_pipeline.my_asset",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:my-asset",
			Columns: []ColumnInfo{
				{Name: "actual_id", Type: "INTEGER"},
				{Name: "actual_value", Type: "VARCHAR"},
			},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from (describe some_pipeline.my_asset) described`}

	items := engine.Complete(doc, PositionAt(doc.Text, strings.Index(doc.Text, "*")))
	labels := completionLabels(items)
	for _, column := range []string{"column_name", "column_type", "null", "key", "default", "extra"} {
		if !slices.Contains(labels, column) {
			t.Fatalf("expected DESCRIBE result column %s, got %#v", column, labels)
		}
	}
	for _, sourceColumn := range []string{"actual_id", "actual_value"} {
		if slices.Contains(labels, sourceColumn) {
			t.Fatalf("did not expect source column %s for DESCRIBE result, got %#v", sourceColumn, labels)
		}
	}

	output := engine.InferOutputColumns(doc.Text)
	outputNames := make([]string, 0, len(output))
	for _, column := range output {
		outputNames = append(outputNames, column.Name)
	}
	if !slices.Equal(outputNames, []string{"column_name", "column_type", "null", "key", "default", "extra"}) {
		t.Fatalf("unexpected DESCRIBE output columns: %#v", outputNames)
	}
}

func TestEngineCompletesColumnsFromNestedSubqueryAlias(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:city",
			Name: "city",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:city",
			Columns: []ColumnInfo{
				{Name: "ID"},
				{Name: "Name"},
			},
		}},
	})

	completionDoc := TextDocumentItem{URI: "file:///query.sql", Text: `select outer_sq.
from (
  select *
  from (
    select ci.ID, ci.Name from city ci
  ) inner_sq
) outer_sq`}

	items := engine.Complete(completionDoc, Position{Line: 0, Character: len("select outer_sq.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "ID") || !slices.Contains(labels, "Name") {
		t.Fatalf("expected recursively derived subquery columns, got %#v", labels)
	}

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select outer_sq.ID
from (
  select *
  from (
    select ci.ID, ci.Name from city ci
  ) inner_sq
) outer_sq`}
	definition := engine.Definition(doc, Position{Line: 0, Character: len("select outer_sq.ID")})
	idStart := strings.Index(doc.Text, "ci.ID") + len("ci.")
	assertSingleLocationRange(t, doc.Text, definition, idStart, idStart+len("ID"))

	hover := engine.Hover(doc, Position{Line: 0, Character: len("select outer_sq.ID")})
	if hover == nil || !strings.Contains(hover.Contents, "outer_sq.ID") {
		t.Fatalf("expected nested subquery column hover, got %#v", hover)
	}
}

func TestEngineCompletesColumnsInsideCorrelatedSubquery(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "relation:orders", Name: "orders"},
			{ID: "relation:line_items", Name: "line_items"},
		},
		Schemas: []SchemaLayer{
			{RelationID: "relation:orders", Columns: []ColumnInfo{{Name: "order_id"}, {Name: "customer_id"}}},
			{RelationID: "relation:line_items", Columns: []ColumnInfo{{Name: "order_id"}, {Name: "sku"}}},
		},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from orders o
where exists (
  select 1
  from line_items li
  where li.order_id = o.
)`}

	items := engine.Complete(doc, Position{Line: 5, Character: len("  where li.order_id = o.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "customer_id") {
		t.Fatalf("expected outer query alias columns inside correlated subquery, got %#v", labels)
	}
}

func TestEngineDoesNotExpandOuterAliasesForSubqueryStar(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "relation:orders", Name: "orders"},
			{ID: "relation:line_items", Name: "line_items"},
		},
		Schemas: []SchemaLayer{
			{RelationID: "relation:orders", Columns: []ColumnInfo{{Name: "order_id"}, {Name: "customer_id"}}},
			{RelationID: "relation:line_items", Columns: []ColumnInfo{{Name: "sku"}}},
		},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select sq.
from orders o
join (
  select *
  from line_items li
) sq on true`}

	items := engine.Complete(doc, Position{Line: 0, Character: len("select sq.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "sku") {
		t.Fatalf("expected local subquery star column, got %#v", labels)
	}
	if slices.Contains(labels, "customer_id") {
		t.Fatalf("did not expect outer alias columns in subquery star expansion, got %#v", labels)
	}
}

func TestEngineDoesNotTreatQualifiedRelationAsUnresolvedAlias(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:example.range_10",
			Name: "example.range_10",
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from example.range_10`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-alias" {
			t.Fatalf("unexpected unresolved alias diagnostic for qualified relation: %#v", diagnostic)
		}
	}
}

func TestEngineWarnsForCrossConnectionAssetReference(t *testing.T) {
	queryURI := URI("file:///report.sql")
	engine := NewEngine(GraphFromRenartAssets("file:///workspace", []AssetNode{
		{
			ID:         "orders",
			Name:       "analytics.orders",
			Connection: "postgres-default",
			URI:        "file:///orders.sql",
		},
		{
			ID:         "report",
			Name:       "analytics.report",
			Connection: "duckdb-default",
			URI:        queryURI,
		},
	}, nil))

	doc := TextDocumentItem{URI: queryURI, Text: "select * from analytics.orders"}
	diagnostics := engine.Diagnostics(doc)
	var warning *Diagnostic
	for i := range diagnostics {
		if diagnostics[i].Code == "cross-connection-reference" {
			warning = &diagnostics[i]
			break
		}
	}
	if warning == nil {
		t.Fatalf("expected cross-connection warning, got %#v", diagnostics)
	}
	if warning.Severity != diagnosticSeverityWarn {
		t.Fatalf("expected warning severity, got %#v", warning)
	}
	if !strings.Contains(warning.Message, "postgres-default") || !strings.Contains(warning.Message, "duckdb-default") {
		t.Fatalf("expected both connections in warning, got %q", warning.Message)
	}

	sameConnection := GraphFromRenartAssets("file:///workspace", []AssetNode{
		{ID: "orders", Name: "analytics.orders", Connection: "duckdb-default", URI: "file:///orders.sql"},
		{ID: "report", Name: "analytics.report", Connection: "duckdb-default", URI: queryURI},
	}, nil)
	for _, diagnostic := range NewEngine(sameConnection).Diagnostics(doc) {
		if diagnostic.Code == "cross-connection-reference" {
			t.Fatalf("did not expect a warning for the same connection: %#v", diagnostic)
		}
	}
}

func TestEngineDoesNotConsumeJoinAsImplicitAlias(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "relation:example.range_10", Name: "example.range_10"},
			{ID: "relation:example.my_python_asset_1", Name: "example.my_python_asset_1"},
		},
		Schemas: []SchemaLayer{
			{RelationID: "relation:example.range_10", Columns: []ColumnInfo{{Name: "range"}}},
			{RelationID: "relation:example.my_python_asset_1", Columns: []ColumnInfo{{Name: "col1"}}},
		},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `SELECT
  *
FROM example.range_10
JOIN example.my_python_asset_1
  ON col1 = range`}

	if diagnostics := engine.Diagnostics(doc); len(diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics for consecutive qualified relations: %#v", diagnostics)
	}
}

func TestEngineDoesNotTreatQualifiedRelationInScalarSubqueryAsAliasColumn(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "relation:example.my_sql_asset_2", Name: "example.my_sql_asset_2"},
			{ID: "relation:example.my_sql_asset_3", Name: "example.my_sql_asset_3"},
		},
		Schemas: []SchemaLayer{{
			RelationID: "relation:example.my_sql_asset_2",
			Columns:    []ColumnInfo{{Name: "range"}},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `SELECT
  *,
  (select first(range) from example.my_sql_asset_2)
FROM example.my_sql_asset_3`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-alias" || diagnostic.Code == "unresolved-column" {
			t.Fatalf("unexpected diagnostic for scalar subquery qualified relation: %#v", diagnostic)
		}
	}
}

// Regression for a false-positive unresolved-alias: an alias defined inside a
// derived table (`b` here) is local to the subquery and must not be re-checked
// against the parent scope.
func TestEngineDoesNotFlagDerivedTableInnerAlias(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:base", Name: "base"}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:base",
			Columns:    []ColumnInfo{{Name: "x"}},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select sub.x
from (select b.x from base b) sub`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-alias" {
			t.Fatalf("unexpected unresolved-alias for derived-table inner alias: %#v", diagnostic)
		}
	}
}

// Companion to the test above: a genuinely undefined qualifier inside a derived
// table must still be reported, i.e. the fix filters valid child-scope aliases
// without hiding real errors.
func TestEngineFlagsUnresolvedAliasInsideDerivedTable(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:base", Name: "base"}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select sub.x
from (select missing.x from base b) sub`}

	found := false
	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-alias" && strings.Contains(diagnostic.Message, "missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unresolved-alias for `missing` inside derived table, got %#v", engine.Diagnostics(doc))
	}
}

// Regression for go-to-definition on an unqualified column in a statement that
// does not start at offset 0 (here the second branch of a UNION). The column
// definition ranges were computed relative to the statement segment but applied
// against the whole document.
func TestEngineDefinesUnqualifiedColumnAfterUnion(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select 1 as z
union all
select a from (select 5 as a) sub`}

	cursor := strings.Index(doc.Text, "select a") + len("select ")
	definition := engine.Definition(doc, PositionAt(doc.Text, cursor))

	aliasStart := strings.Index(doc.Text, "as a") + len("as ")
	assertSingleLocationRange(t, doc.Text, definition, aliasStart, aliasStart+len("a"))
}

// Regression for semantic-token offsets on a qualified relation written with
// whitespace around the dot. normalizeRelation strips the spaces, so token
// spans derived from the normalized name drifted off the source characters.
func TestEngineSemanticTokensHandlesSpacedQualifiedRelation(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "relation:schema.table", Name: "schema.table"}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select * from schema . table`}

	decoded := decodeSemanticTokenRanges(t, doc.Text, engine.SemanticTokens(doc))
	var schemaText, tableText string
	for _, token := range decoded {
		switch token.tokenType {
		case semanticTokenSchema:
			schemaText = token.text
		case semanticTokenTable:
			tableText = token.text
		}
	}
	if schemaText != "schema" {
		t.Fatalf("expected schema token covering `schema`, got %q (%#v)", schemaText, decoded)
	}
	if tableText != "table" {
		t.Fatalf("expected table token covering `table`, got %q (%#v)", tableText, decoded)
	}
}

func TestEngineSignatureHelpForInsertValues(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:analytics.orders",
			Name: "analytics.orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:analytics.orders",
			Columns: []ColumnInfo{
				{Name: "order_id", Type: "integer"},
				{Name: "customer_id", Type: "integer"},
			},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `insert into analytics.orders values (1, `}
	help := engine.SignatureHelp(doc, Position{Line: 0, Character: len(doc.Text)})
	if help == nil || len(help.Signatures) != 1 {
		t.Fatalf("expected signature help, got %#v", help)
	}
	if help.ActiveParameter != 1 {
		t.Fatalf("expected active parameter 1, got %#v", help)
	}
	if !strings.Contains(help.Signatures[0].Label, "order_id integer") || !strings.Contains(help.Signatures[0].Label, "customer_id integer") {
		t.Fatalf("expected column labels in signature help, got %#v", help.Signatures[0].Label)
	}
}

func TestEngineCompletesColumnsOnlyFromCurrentUnionBranch(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `with cte as (select 1 a),
blub as (select 3 as bli)

select a from cte
UNION
select *,
from blub`}

	items := engine.Complete(doc, Position{Line: 5, Character: len("select *,")})
	labels := completionLabels(items)
	if slices.Contains(labels, "a") {
		t.Fatalf("did not expect previous UNION branch column in completions, got %#v", labels)
	}
	if !slices.Contains(labels, "bli") {
		t.Fatalf("expected current UNION branch column in completions, got %#v", labels)
	}
}

func TestEngineCompletesColumnsAtEmptySameLineProjection(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:analytics.orders",
			Name: "analytics.orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:analytics.orders",
			Columns: []ColumnInfo{
				{Name: "order_id", Type: "integer"},
				{Name: "customer_id", Type: "integer"},
			},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *, from analytics.orders`}

	items := engine.Complete(doc, Position{Line: 0, Character: len("select *, ")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "customer_id") {
		t.Fatalf("expected source columns at an empty projection, got %#v", labels)
	}
}

func TestEngineDefinitionTargetsCTENameAliasAndColumns(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	sql := `with cte as (select 1 as a),
blub as (select 3 as bli)
select c.a
from cte c
union
select bli
from blub`
	doc := TextDocumentItem{URI: "file:///query.sql", Text: sql}

	cteLocations := engine.Definition(doc, Position{Line: 3, Character: len("from ct")})
	assertSingleLocationRange(t, sql, cteLocations, strings.Index(sql, "cte"), strings.Index(sql, "cte")+len("cte"))

	aliasLocations := engine.Definition(doc, Position{Line: 2, Character: len("select c")})
	aliasStart := strings.LastIndex(sql, " c")
	assertSingleLocationRange(t, sql, aliasLocations, aliasStart+1, aliasStart+2)

	qualifiedColumnLocations := engine.Definition(doc, Position{Line: 2, Character: len("select c.a")})
	columnStart := strings.Index(sql, "1 as a") + len("1 as ")
	assertSingleLocationRange(t, sql, qualifiedColumnLocations, columnStart, columnStart+len("a"))

	unqualifiedColumnLocations := engine.Definition(doc, Position{Line: 5, Character: len("select bli")})
	bliStart := strings.Index(sql, "3 as bli") + len("3 as ")
	assertSingleLocationRange(t, sql, unqualifiedColumnLocations, bliStart, bliStart+len("bli"))
}

func TestEngineHoverIncludesAliasAndColumnDetails(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{{
				Name:        "order_id",
				Type:        "integer",
				Description: "Order identifier",
			}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select o.order_id
from orders o`}

	aliasHover := engine.Hover(doc, Position{Line: 1, Character: len("from orders o")})
	if aliasHover == nil || !strings.Contains(aliasHover.Contents, "Alias for `orders`") || !strings.Contains(aliasHover.Contents, "`order_id`") {
		t.Fatalf("expected alias hover with source and columns, got %#v", aliasHover)
	}

	columnHover := engine.Hover(doc, Position{Line: 0, Character: len("select o.order")})
	if columnHover == nil || !strings.Contains(columnHover.Contents, "**orders.order_id**") || !strings.Contains(columnHover.Contents, "integer") || !strings.Contains(columnHover.Contents, "Order identifier") {
		t.Fatalf("expected column hover with type and description, got %#v", columnHover)
	}
}

func TestEngineDoesNotDiagnoseCTEAsUnresolvedRelation(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `with recent_orders as (
  select order_id from orders
)
select r.order_id
from recent_orders r`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-relation" {
			t.Fatalf("unexpected unresolved relation diagnostic: %#v", diagnostic)
		}
	}
}

func TestEngineIgnoresRelationKeywordsInsideStringLiteralsAndComments(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select
  42 as answer,
  'hello from renart' as greeting,
  'join missing.table and read missing.column' as detail
-- from commented_relation
/* join block_commented_relation */`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == authoringdiag.CodeUnresolvedRelation ||
			diagnostic.Code == authoringdiag.CodeUnresolvedAlias ||
			diagnostic.Code == authoringdiag.CodeUnresolvedColumn {
			t.Fatalf("unexpected diagnostic from literal or comment text: %#v", diagnostic)
		}
	}
}

func TestEngineCompletesColumnsOutsideJinjaRef(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "customer_id"},
			},
		}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select o.
from {{ ref("orders") }} o`}

	items := engine.Complete(doc, Position{Line: 0, Character: len("select o.")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "customer_id") {
		t.Fatalf("expected columns through Jinja ref alias, got %#v", labels)
	}
}

func TestEngineCompletesColumnsInExpressionClauses(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "customer_id"},
			},
		}},
	})

	cases := []struct {
		name string
		sql  string
		line int
		col  int
	}{
		{
			name: "where",
			sql: `select *
from orders o
where `,
			line: 2,
			col:  len("where "),
		},
		{
			name: "group by",
			sql: `select customer_id, count(*)
from orders o
group by `,
			line: 2,
			col:  len("group by "),
		},
		{
			name: "having",
			sql: `select customer_id, count(*)
from orders o
group by customer_id
having `,
			line: 3,
			col:  len("having "),
		},
		{
			name: "order by",
			sql: `select *
from orders o
order by `,
			line: 2,
			col:  len("order by "),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := engine.Complete(TextDocumentItem{URI: "file:///query.sql", Text: tc.sql}, Position{Line: tc.line, Character: tc.col})
			labels := completionLabels(items)
			if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "customer_id") {
				t.Fatalf("expected columns in %s clause, got %#v", tc.name, labels)
			}
		})
	}
}

func TestEngineTreatsDuckDBRangeAsTableFunction(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select range as my_value from range(1,2,1)`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-relation" || diagnostic.Code == "unresolved-column" {
			t.Fatalf("unexpected diagnostic for range table function: %#v", diagnostic)
		}
	}

	items := engine.Complete(TextDocumentItem{URI: "file:///query.sql", Text: `select *
from range(1,2,1)
where `}, Position{Line: 2, Character: len("where ")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "range") {
		t.Fatalf("expected range column completion, got %#v", labels)
	}
}

func TestEngineTreatsDuckDBGenerateSeriesAsTableFunction(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select * from generate_series(10)`}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "unresolved-relation" || diagnostic.Code == "unresolved-column" {
			t.Fatalf("unexpected diagnostic for generate_series table function: %#v", diagnostic)
		}
	}

	items := engine.Complete(TextDocumentItem{URI: "file:///query.sql", Text: `select *
from generate_series(10)
where `}, Position{Line: 2, Character: len("where ")})
	labels := completionLabels(items)
	if !slices.Contains(labels, "generate_series") {
		t.Fatalf("expected generate_series column completion, got %#v", labels)
	}
}

func TestEngineMapsRenderedDiagnosticToTemplateRef(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from {{ ref("missing_orders") }} m`}

	diagnostics := engine.Diagnostics(doc)
	if len(diagnostics) == 0 {
		t.Fatal("expected unresolved relation diagnostic")
	}
	got := diagnostics[0].Range
	want := RangeFromOffsets(doc.Text, len("select *\nfrom "), len(`select *
from {{ ref("missing_orders") }}`))
	if got != want {
		t.Fatalf("expected diagnostic on ref range %#v, got %#v", want, got)
	}
}

func TestEngineDocumentRelationsRetainsResolvedAndUnresolvedPhysicalUses(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:              "sibling-orders",
			Name:            "raw.orders",
			OutputRelations: []string{"relation:raw.orders"},
		}},
		Relations: []RelationNode{{
			ID:      "relation:raw.orders",
			Name:    "raw.orders",
			AssetID: "sibling-orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///report.sql", Text: `with local_orders as (
  select * from raw.orders
)
select *
from local_orders
join missing.customers on true`}

	relations := engine.DocumentRelations(doc)
	if len(relations) != 2 {
		t.Fatalf("expected physical relation uses only, got %#v", relations)
	}
	byName := make(map[string]DocumentRelation, len(relations))
	for _, relation := range relations {
		byName[relation.Name] = relation
	}
	resolved := byName["raw.orders"]
	if resolved.ResolvedName != "raw.orders" || resolved.AssetID != "sibling-orders" {
		t.Fatalf("expected resolved authored relation, got %#v", resolved)
	}
	if got := doc.Text[ByteOffset(doc.Text, resolved.Range.Start):ByteOffset(doc.Text, resolved.Range.End)]; got != "raw.orders" {
		t.Fatalf("expected source range for raw.orders, got %q", got)
	}
	unresolved := byName["missing.customers"]
	if unresolved.ResolvedName != "" || unresolved.AssetID != "" {
		t.Fatalf("expected unresolved physical relation to be retained, got %#v", unresolved)
	}
	if got := doc.Text[ByteOffset(doc.Text, unresolved.Range.Start):ByteOffset(doc.Text, unresolved.Range.End)]; got != "missing.customers" {
		t.Fatalf("expected source range for missing.customers, got %q", got)
	}
}

func TestRenderedSourceMapHandlesRangesAcrossTemplateSegments(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "analytics.orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from {{ ref("orders") }} o`}
	rendered := renderTemplateSQL(doc, engine)

	fromStart := strings.Index(rendered.RenderedSQL, "from")
	relationEnd := strings.Index(rendered.RenderedSQL, "orders") + len("orders")
	gotIntoRef := rendered.TemplateRangeForGeneratedOffsets(doc.Text, fromStart, relationEnd)
	wantIntoRef := RangeFromOffsets(doc.Text, strings.Index(doc.Text, "from"), strings.Index(doc.Text, ` }} o`)+len(" }}"))
	if gotIntoRef != wantIntoRef {
		t.Fatalf("expected literal-to-ref mapping %#v, got %#v", wantIntoRef, gotIntoRef)
	}

	relationStart := strings.Index(rendered.RenderedSQL, "analytics")
	aliasEnd := strings.LastIndex(rendered.RenderedSQL, " o") + len(" o")
	gotFromRef := rendered.TemplateRangeForGeneratedOffsets(doc.Text, relationStart, aliasEnd)
	wantFromRef := RangeFromOffsets(doc.Text, strings.Index(doc.Text, "{{"), len(doc.Text))
	if gotFromRef != wantFromRef {
		t.Fatalf("expected ref-to-literal mapping %#v, got %#v", wantFromRef, gotFromRef)
	}
}

func TestEngineDefinitionInsideJinjaRefTargetsAsset(t *testing.T) {
	targetURI := URI("file:///orders.sql")
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:              "asset:dbt:orders",
			Name:            "orders",
			URI:             targetURI,
			OutputRelations: []string{"relation:dbt:orders"},
		}},
		Relations: []RelationNode{{
			ID:      "relation:dbt:orders",
			Name:    "orders",
			AssetID: "asset:dbt:orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///report.sql", Text: `select *
from {{ ref("orders") }} o`}
	locations := engine.Definition(doc, Position{Line: 1, Character: len(`from {{ ref("ord`)})
	if len(locations) != 1 || locations[0].URI != targetURI {
		t.Fatalf("expected ref definition to %s, got %#v", targetURI, locations)
	}
}

func TestEngineDefinitionOnUnaliasedRelationTargetsAsset(t *testing.T) {
	targetURI := URI("file:///orders.sql")
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:              "asset:renart:orders",
			Name:            "example.orders",
			URI:             targetURI,
			OutputRelations: []string{"relation:renart:example.orders"},
		}},
		Relations: []RelationNode{{
			ID:      "relation:renart:example.orders",
			Name:    "example.orders",
			AssetID: "asset:renart:orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///report.sql", Text: `select *
from example.orders`}

	locations := engine.Definition(doc, Position{Line: 1, Character: len("from example.ord")})
	if len(locations) != 1 || locations[0].URI != targetURI || locations[0].AssetID != "asset:renart:orders" {
		t.Fatalf("expected relation definition to target asset, got %#v", locations)
	}
	if locations[0].URI == doc.URI {
		t.Fatalf("expected relation definition not to target current document, got %#v", locations)
	}
}

func TestEngineReferencesCTEAliasAndColumn(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	sql := `with cte as (select 1 as a)
select c.a
from cte c
where c.a > 0`
	doc := TextDocumentItem{URI: "file:///query.sql", Text: sql}

	cteRefs := engine.References(doc, Position{Line: 2, Character: len("from ct")}, true)
	assertLocationRanges(t, sql, cteRefs, [][2]int{
		{strings.Index(sql, "cte"), strings.Index(sql, "cte") + len("cte")},
		{strings.LastIndex(sql, "cte c"), strings.LastIndex(sql, "cte c") + len("cte")},
	})

	aliasRefs := engine.References(doc, Position{Line: 1, Character: len("select c")}, true)
	aliasStart := strings.LastIndex(sql, " c\nwhere")
	assertLocationRanges(t, sql, aliasRefs, [][2]int{
		{aliasStart + 1, aliasStart + 2},
		{strings.Index(sql, "c.a"), strings.Index(sql, "c.a") + 1},
		{strings.LastIndex(sql, "c.a"), strings.LastIndex(sql, "c.a") + 1},
	})

	columnRefs := engine.References(doc, Position{Line: 1, Character: len("select c.a")}, true)
	columnDecl := strings.Index(sql, "1 as a") + len("1 as ")
	assertLocationRanges(t, sql, columnRefs, [][2]int{
		{columnDecl, columnDecl + len("a")},
		{strings.Index(sql, "c.a") + len("c."), strings.Index(sql, "c.a") + len("c.a")},
		{strings.LastIndex(sql, "c.a") + len("c."), strings.LastIndex(sql, "c.a") + len("c.a")},
	})
}

func TestEngineRenameLocalCTEAliasAndColumn(t *testing.T) {
	engine := NewEngine(CanonicalGraph{Version: 1})
	sql := `with cte as (select 1 as a)
select c.a
from cte c
where c.a > 0`
	doc := TextDocumentItem{URI: "file:///query.sql", Text: sql}

	cteEdit, err := engine.Rename(doc, Position{Line: 2, Character: len("from ct")}, "renamed_cte")
	if err != nil {
		t.Fatal(err)
	}
	assertEditRanges(t, sql, cteEdit, [][2]int{
		{strings.Index(sql, "cte"), strings.Index(sql, "cte") + len("cte")},
		{strings.LastIndex(sql, "cte c"), strings.LastIndex(sql, "cte c") + len("cte")},
	}, "renamed_cte")

	aliasEdit, err := engine.Rename(doc, Position{Line: 1, Character: len("select c")}, "src")
	if err != nil {
		t.Fatal(err)
	}
	aliasStart := strings.LastIndex(sql, " c\nwhere")
	assertEditRanges(t, sql, aliasEdit, [][2]int{
		{aliasStart + 1, aliasStart + 2},
		{strings.Index(sql, "c.a"), strings.Index(sql, "c.a") + 1},
		{strings.LastIndex(sql, "c.a"), strings.LastIndex(sql, "c.a") + 1},
	}, "src")

	columnEdit, err := engine.Rename(doc, Position{Line: 1, Character: len("select c.a")}, "amount")
	if err != nil {
		t.Fatal(err)
	}
	columnDecl := strings.Index(sql, "1 as a") + len("1 as ")
	assertEditRanges(t, sql, columnEdit, [][2]int{
		{columnDecl, columnDecl + len("a")},
		{strings.Index(sql, "c.a") + len("c."), strings.Index(sql, "c.a") + len("c.a")},
		{strings.LastIndex(sql, "c.a") + len("c."), strings.LastIndex(sql, "c.a") + len("c.a")},
	}, "amount")
}

func TestEngineRenameDoesNotRenameWorkspaceRelation(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from orders`}
	edit, err := engine.Rename(doc, Position{Line: 1, Character: len("from ord")}, "customers")
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		t.Fatalf("expected workspace relation rename to be refused, got %#v", edit)
	}
}

func TestEngineRenameReportsTemplatedDocument(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select o.total
from {{ ref("orders") }} o`}
	edit, err := engine.Rename(doc, Position{Line: 0, Character: len("select o")}, "ord")
	if !errors.Is(err, ErrRenameTemplated) {
		t.Fatalf("expected ErrRenameTemplated, got edit=%#v err=%v", edit, err)
	}
	if edit != nil {
		t.Fatalf("expected no edit for a templated document, got %#v", edit)
	}
}

func TestEngineCodeActionsSuggestRelationAndColumnFixes(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "customer_id"},
			},
		}},
	})

	relationDoc := TextDocumentItem{URI: "file:///query.sql", Text: `select *
from ordres`}
	relationActions := engine.CodeActions(relationDoc)
	if len(relationActions) != 1 || relationActions[0].Title != "Change 'ordres' to 'orders'" {
		t.Fatalf("expected relation quick fix, got %#v", relationActions)
	}

	columnDoc := TextDocumentItem{URI: "file:///query.sql", Text: `select o.ordr_id
from orders o`}
	columnActions := engine.CodeActions(columnDoc)
	if len(columnActions) != 1 || columnActions[0].Title != "Change 'ordr_id' to 'order_id'" {
		t.Fatalf("expected column quick fix, got %#v", columnActions)
	}
}

func TestEngineCodeActionsQualifyAmbiguousColumnLosslessly(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "users", Name: "users"},
			{ID: "orders", Name: "orders"},
		},
		Schemas: []SchemaLayer{
			{RelationID: "users", Completeness: "complete", Columns: []ColumnInfo{{Name: "id"}, {Name: "name"}}},
			{RelationID: "orders", Completeness: "complete", Columns: []ColumnInfo{{Name: "id"}, {Name: "user_id"}}},
		},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "select id -- choose an owner\nfrom users AS u join orders o on u.id = o.user_id"}
	actions := engine.CodeActions(doc)
	if len(actions) != 2 {
		t.Fatalf("expected one qualification per matching relation, got %#v; diagnostics=%#v", actions, engine.Diagnostics(doc))
	}
	if actions[0].Title != "Qualify 'id' with 'o'" || actions[1].Title != "Qualify 'id' with 'u'" {
		t.Fatalf("qualification actions are not deterministic: %#v", actions)
	}
	for _, action := range actions {
		edits := action.Edit.Changes[doc.URI]
		if len(edits) != 1 || !strings.HasSuffix(edits[0].NewText, ".id") {
			t.Fatalf("unexpected lossless qualification edit: %#v", action)
		}
		if textInRange(doc.Text, edits[0].Range) != "id" {
			t.Fatalf("quick fix must replace only the ambiguous identifier: %#v", action)
		}
	}
}

func TestEngineResolvesCommaSeparatedQualifiedRelationsAndOffersExternalImport(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "relation:accounts", Name: "public.accounts", AssetID: "accounts"},
			{ID: "relation:jobs", Name: "public.jobs", AssetID: "jobs"},
			{
				ID:   "relation:remote_catalog:postgres-other:public.pipeline_tasks",
				Name: "public.pipeline_tasks",
				Provenance: []Provenance{{
					Provider: "remote_catalog", ProviderID: "postgres-other", Confidence: "low",
				}},
			},
		},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `SELECT
  *
FROM public.accounts join
public.jobs jobs, public.pipeline_tasks tasks`}

	diagnostics := engine.Diagnostics(doc)
	var external *Diagnostic
	for index := range diagnostics {
		diagnostic := &diagnostics[index]
		if diagnostic.Code == authoringdiag.CodeUnresolvedAlias || diagnostic.Code == authoringdiag.CodeUnresolvedRelation {
			t.Fatalf("qualified comma relation was not parsed as a relation: %#v", diagnostics)
		}
		if diagnostic.Code == authoringdiag.CodeExternalRelation {
			external = diagnostic
		}
	}
	if external == nil || !strings.Contains(external.Message, "public.pipeline_tasks") {
		t.Fatalf("expected external relation warning for pipeline_tasks, got %#v", diagnostics)
	}

	actions := engine.CodeActions(doc)
	if len(actions) != 1 || actions[0].Action == nil {
		t.Fatalf("expected reviewed external relation quick fix, got %#v", actions)
	}
	if actions[0].Action.Type != "import-external-relation" || actions[0].Action.RelationID != "relation:remote_catalog:postgres-other:public.pipeline_tasks" {
		t.Fatalf("unexpected external relation action: %#v", actions[0].Action)
	}
}

func TestEngineSemanticTokensIncludesRelationAliasAndColumn(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:analytics.orders",
			Name: "analytics.orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:analytics.orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select o.order_id
from analytics.orders o`}

	tokens := engine.SemanticTokens(doc)
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Fatalf("expected encoded semantic tokens, got %#v", tokens.Data)
	}
	types := map[uint32]bool{}
	for i := 3; i < len(tokens.Data); i += 5 {
		types[tokens.Data[i]] = true
	}
	if !types[semanticTokenSchema] || !types[semanticTokenTable] || !types[semanticTokenAlias] || !types[semanticTokenColumn] {
		t.Fatalf("expected schema, table, alias, and column token types, got %#v", tokens.Data)
	}
}

func TestEngineSemanticTokensIncludeUnqualifiedResolvedColumns(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select order_id
from orders
where order_id > 1
-- order_id in a comment`}

	decoded := decodeSemanticTokenRanges(t, doc.Text, engine.SemanticTokens(doc))
	count := 0
	for _, token := range decoded {
		if token.tokenType == semanticTokenColumn && token.text == "order_id" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected two unqualified order_id column tokens outside comments, got %#v", decoded)
	}
}

func TestEngineDocumentSymbolsIncludesCTEColumnsAndRelations(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `with cte as (
  select 1 as a from orders
)
select a from cte
join orders o on true`}

	symbols := engine.DocumentSymbols(doc)
	var hasCTE, hasColumn, hasRelation bool
	for _, symbol := range symbols {
		if symbol.Name == "cte" && symbol.Detail == "CTE" {
			hasCTE = true
			for _, child := range symbol.Children {
				if child.Name == "a" {
					hasColumn = true
				}
			}
		}
		if symbol.Detail == "relation" {
			hasRelation = true
		}
	}
	if !hasCTE || !hasColumn || !hasRelation {
		t.Fatalf("expected CTE, CTE column, and relation symbols, got %#v", symbols)
	}
}

func TestEngineWorkspaceSymbolsSearchesGraphAssets(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:      1,
		WorkspaceURI: "file:///workspace",
		Assets: []AssetNode{{
			ID:   "asset:orders",
			Name: "analytics.orders",
			Kind: "sql_model",
			URI:  "file:///orders.sql",
		}},
		Relations: []RelationNode{{
			ID:   "relation:external.customers",
			Name: "external.customers",
		}},
	})

	symbols := engine.WorkspaceSymbols("orders")
	if len(symbols) != 1 || symbols[0].Name != "analytics.orders" || symbols[0].Location.AssetID != "asset:orders" {
		t.Fatalf("expected asset workspace symbol, got %#v", symbols)
	}

	relationSymbols := engine.WorkspaceSymbols("customers")
	if len(relationSymbols) != 1 || relationSymbols[0].Name != "external.customers" {
		t.Fatalf("expected relation workspace symbol, got %#v", relationSymbols)
	}
}

func TestEngineReferencesRelationAndJinjaRef(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:              "asset:orders",
			Name:            "orders",
			URI:             "file:///orders.sql",
			OutputRelations: []string{"relation:orders"},
		}},
		Relations: []RelationNode{{
			ID:      "relation:orders",
			Name:    "orders",
			AssetID: "asset:orders",
		}},
	})

	relationSQL := `select * from orders
union all
select * from orders`
	relationDoc := TextDocumentItem{URI: "file:///report.sql", Text: relationSQL}
	relationRefs := engine.References(relationDoc, Position{Line: 0, Character: len("select * from ord")}, false)
	assertLocationRanges(t, relationSQL, relationRefs, [][2]int{
		{strings.Index(relationSQL, "orders"), strings.Index(relationSQL, "orders") + len("orders")},
		{strings.LastIndex(relationSQL, "orders"), strings.LastIndex(relationSQL, "orders") + len("orders")},
	})

	jinjaSQL := `select * from {{ ref("orders") }}
union all
select * from {{ ref("orders") }}`
	jinjaDoc := TextDocumentItem{URI: "file:///report.sql", Text: jinjaSQL}
	jinjaRefs := engine.References(jinjaDoc, Position{Line: 0, Character: len(`select * from {{ ref("ord`)}, false)
	assertLocationRanges(t, jinjaSQL, jinjaRefs, [][2]int{
		{strings.Index(jinjaSQL, "{{"), strings.Index(jinjaSQL, " }}") + len(" }}")},
		{strings.LastIndex(jinjaSQL, "{{"), strings.LastIndex(jinjaSQL, " }}") + len(" }}")},
	})
}

func TestEngineWorkspaceReferencesFindsRelationAcrossDocuments(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
	})
	report := TextDocumentItem{URI: "file:///report.sql", Text: `select *
from orders`}
	downstream := TextDocumentItem{URI: "file:///downstream.sql", Text: `select *
from {{ ref("orders") }} o`}

	locations := engine.WorkspaceReferences(report, Position{Line: 1, Character: len("from ord")}, []TextDocumentItem{report, downstream}, false)
	if len(locations) != 2 {
		t.Fatalf("expected relation references in both documents, got %#v", locations)
	}
	uris := map[URI]bool{}
	for _, location := range locations {
		uris[location.URI] = true
	}
	if !uris[report.URI] || !uris[downstream.URI] {
		t.Fatalf("expected references in report and downstream docs, got %#v", locations)
	}
}

func TestEngineFlagsSelfReferenceAsCircularDependency(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{{
			ID:              "asset:analytics.customers",
			Name:            "analytics.customers",
			URI:             "file:///customers.sql",
			OutputRelations: []string{"relation:analytics.customers"},
		}},
		Relations: []RelationNode{{
			ID:      "relation:analytics.customers",
			Name:    "analytics.customers",
			AssetID: "asset:analytics.customers",
		}},
	})

	doc := TextDocumentItem{URI: "file:///customers.sql", Text: "select *\nfrom analytics.customers"}

	var found bool
	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "circular-dependency" {
			found = true
			if !strings.Contains(diagnostic.Message, "analytics.customers") {
				t.Fatalf("circular-dependency message missing asset name: %q", diagnostic.Message)
			}
		}
		if diagnostic.Code == "unresolved-relation" {
			t.Fatalf("self-reference should not be an unresolved relation: %#v", diagnostic)
		}
	}
	if !found {
		t.Fatalf("expected a circular-dependency diagnostic for a self-referencing asset")
	}
}

func TestEngineDoesNotFlagUpstreamReferenceAsCircular(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Assets: []AssetNode{
			{ID: "asset:analytics.customers", Name: "analytics.customers", URI: "file:///customers.sql", OutputRelations: []string{"relation:analytics.customers"}},
			{ID: "asset:analytics.orders", Name: "analytics.orders", URI: "file:///orders.sql", OutputRelations: []string{"relation:analytics.orders"}},
		},
		Relations: []RelationNode{
			{ID: "relation:analytics.customers", Name: "analytics.customers", AssetID: "asset:analytics.customers"},
			{ID: "relation:analytics.orders", Name: "analytics.orders", AssetID: "asset:analytics.orders"},
		},
	})

	doc := TextDocumentItem{URI: "file:///customers.sql", Text: "select *\nfrom analytics.orders"}

	for _, diagnostic := range engine.Diagnostics(doc) {
		if diagnostic.Code == "circular-dependency" {
			t.Fatalf("referencing a different asset must not be circular: %#v", diagnostic)
		}
	}
}

func TestEngineCompletesRelationsForSchemaQualifierInFromClause(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "r1", Name: "analytics.customers"},
			{ID: "r2", Name: "analytics.orders"},
			{ID: "r3", Name: "marts.summary"},
		},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: "select * from analytics."}
	items := engine.Complete(doc, Position{Line: 0, Character: len("select * from analytics.")})

	labels := completionLabels(items)
	if !slices.Contains(labels, "analytics.customers") || !slices.Contains(labels, "analytics.orders") {
		t.Fatalf("expected analytics.* relations, got %#v", labels)
	}
	if slices.Contains(labels, "marts.summary") {
		t.Fatalf("did not expect out-of-schema relation marts.summary, got %#v", labels)
	}
	for _, item := range items {
		if item.Label == "analytics.customers" && item.InsertText != "customers" {
			t.Fatalf("expected schema-stripped insert text 'customers', got %q", item.InsertText)
		}
	}
}

func TestEngineUsesParserContextForPartialFromKeyword(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "orders", Name: "analytics.orders"}},
		Schemas: []SchemaLayer{{
			RelationID: "orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "SELECT order_id FR"}
	items := engine.Complete(doc, PositionAt(doc.Text, len(doc.Text)))
	labels := completionLabels(items)
	if !slices.Contains(labels, "from") {
		t.Fatalf("expected FROM for a partial clause keyword, got %#v", labels)
	}
	if slices.Contains(labels, "order_id") || slices.Contains(labels, "analytics.orders") {
		t.Fatalf("partial FROM must not be routed as a projection expression, got %#v", labels)
	}
}

func TestEngineUsesParserContextForPartialRelation(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "orders", Name: "analytics.orders"},
			{ID: "customers", Name: "analytics.customers"},
		},
		Schemas: []SchemaLayer{{
			RelationID: "orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "SELECT * FROM ana"}
	items := engine.Complete(doc, PositionAt(doc.Text, len(doc.Text)))
	labels := completionLabels(items)
	if !slices.Contains(labels, "analytics.orders") || !slices.Contains(labels, "analytics.customers") {
		t.Fatalf("expected relation candidates for a partial table, got %#v", labels)
	}
	if slices.Contains(labels, "order_id") || slices.Contains(labels, "where") {
		t.Fatalf("partial table must only use relation completion, got %#v", labels)
	}
}

func TestEngineUsesParserContextInsideIncompleteNestedCTE(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "orders", Name: "orders"}},
		Schemas: []SchemaLayer{{
			RelationID: "orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "customer_id"},
			},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: `WITH recent AS (
  SELECT order_id FROM orders WHERE customer_
)
SELECT * FROM recent`}
	cursor := strings.Index(doc.Text, "customer_") + len("customer_")
	labels := completionLabels(engine.Complete(doc, PositionAt(doc.Text, cursor)))
	if !slices.Contains(labels, "customer_id") || !slices.Contains(labels, "order_id") {
		context, _ := syntacticCompletionContext(doc.Text, cursor, "generic")
		t.Fatalf("expected current CTE-scope columns, got %#v; context=%#v", labels, context)
	}
	if slices.Contains(labels, "orders") {
		t.Fatalf("nested expression must not be routed as a relation, got %#v", labels)
	}
}

func TestEngineUsesParserContextForQualifyExpression(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "orders", Name: "orders"}},
		Schemas: []SchemaLayer{{
			RelationID: "orders",
			Columns:    []ColumnInfo{{Name: "order_id"}, {Name: "row_number"}},
		}},
	})
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "SELECT * FROM orders QUALIFY row_"}
	labels := completionLabels(engine.Complete(doc, PositionAt(doc.Text, len(doc.Text))))
	if !slices.Contains(labels, "row_number") || !slices.Contains(labels, "order_id") {
		context, _ := syntacticCompletionContext(doc.Text, len(doc.Text), "generic")
		t.Fatalf("expected QUALIFY expression columns, got %#v; context=%#v", labels, context)
	}
}

func TestEngineCompletesJoinConditionsWithQueryAliases(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{
			{ID: "left", Name: "prefix.table"},
			{ID: "right", Name: "other.view"},
			{ID: "unrelated", Name: "warehouse.unrelated"},
		},
		Schemas: []SchemaLayer{
			{RelationID: "left", Columns: []ColumnInfo{{Name: "id"}, {Name: "left_value"}}},
			{RelationID: "right", Columns: []ColumnInfo{{Name: "id"}, {Name: "right_value"}}},
			{RelationID: "unrelated", Columns: []ColumnInfo{{Name: "unrelated_value"}}},
		},
	})

	doc := TextDocumentItem{
		URI:  "file:///query.sql",
		Text: "select * from prefix.table as x join other.view as y on ",
	}
	items := engine.Complete(doc, PositionAt(doc.Text, len(doc.Text)))
	requireCompletion := func(label, insertText string) {
		t.Helper()
		for _, item := range items {
			if item.Label != label {
				continue
			}
			if item.Kind != completionKindField || item.InsertText != insertText {
				t.Fatalf("unexpected join completion %#v", item)
			}
			return
		}
		t.Fatalf("missing join completion %q in %#v", label, items)
	}
	requireCompletion("x.*", "x.")
	requireCompletion("y.*", "y.")
	if len(items) != 2 {
		t.Fatalf("expected only query aliases, got %#v", items)
	}

	doc.Text += "x."
	columnItems := engine.Complete(doc, PositionAt(doc.Text, len(doc.Text)))
	labels := completionLabels(columnItems)
	if !slices.Contains(labels, "id") || !slices.Contains(labels, "left_value") {
		t.Fatalf("expected x columns after inserted dot, got %#v", labels)
	}
	if slices.Contains(labels, "right_value") || slices.Contains(labels, "unrelated_value") {
		t.Fatalf("expected only x columns after inserted dot, got %#v", labels)
	}
}

func TestEngineOffersKeywordCompletionsInGeneralPosition(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "r1", Name: "analytics.customers"}},
	})

	doc := TextDocumentItem{URI: "file:///query.sql", Text: "select 1\nfrom analytics.customers\n"}
	items := engine.Complete(doc, Position{Line: 2, Character: 0})

	labels := completionLabels(items)
	for _, keyword := range []string{"where", "group by", "left join", "qualify"} {
		if !slices.Contains(labels, keyword) {
			t.Fatalf("expected keyword %q in general-position completions, got %#v", keyword, labels)
		}
	}
	// Keywords must sort after schema-aware suggestions.
	for _, item := range items {
		if item.Kind == completionKindMethod && !strings.HasPrefix(item.SortText, "z") {
			t.Fatalf("keyword %q should sort last (z-prefixed), got SortText %q", item.Label, item.SortText)
		}
	}
}

func TestEngineDoesNotCompleteColumnsInsideSingleQuotedStrings(t *testing.T) {
	engine := NewEngine(CanonicalGraph{
		Version:   1,
		Relations: []RelationNode{{ID: "orders", Name: "orders"}},
		Schemas: []SchemaLayer{{
			RelationID: "orders",
			Columns:    []ColumnInfo{{Name: "order_id"}},
		}},
	})

	for _, text := range []string{
		"select 'ord from orders",
		"select 'Ada''s ord from orders",
	} {
		doc := TextDocumentItem{URI: "file:///query.sql", Text: text}
		items := engine.Complete(doc, PositionAt(text, strings.Index(text, " from orders")))
		if len(items) != 0 {
			t.Fatalf("expected no completions inside %q, got %#v", text, items)
		}
	}

	doc := TextDocumentItem{URI: "file:///query.sql", Text: `select "ord" from orders`}
	items := engine.Complete(doc, PositionAt(doc.Text, strings.Index(doc.Text, `" from orders`)))
	if !slices.Contains(completionLabels(items), "order_id") {
		t.Fatalf("double-quoted identifiers must retain column completion, got %#v", items)
	}
}

func TestEngineTreatsDuckDBFileRelationsAccordingToFilesystemPolicy(t *testing.T) {
	const uri = URI("file:///workspace/query.sql")
	doc := TextDocumentItem{URI: uri, Text: `select * from "./example.parquet"`}
	graph := CanonicalGraph{
		Version: 1,
		Assets:  []AssetNode{{ID: "query", URI: uri, Dialect: "duckdb"}},
	}

	for _, diagnostic := range NewEngine(graph).Diagnostics(doc) {
		if diagnostic.Code == authoringdiag.CodeUnresolvedRelation || diagnostic.Code == authoringdiag.CodeDuckDBFilesystemAccessDisabled {
			t.Fatalf("enabled DuckDB file relation should not be rejected: %#v", diagnostic)
		}
	}

	diagnostics := NewEngineWithOptions(graph, EngineOptions{DisableDuckDBFilesystemAccess: true}).Diagnostics(doc)
	var disabled []Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == authoringdiag.CodeUnresolvedRelation {
			t.Fatalf("disabled mode must replace unknown-table noise: %#v", diagnostics)
		}
		if diagnostic.Code == authoringdiag.CodeDuckDBFilesystemAccessDisabled {
			disabled = append(disabled, diagnostic)
		}
	}
	if len(disabled) != 1 || !strings.Contains(disabled[0].Message, "./example.parquet") {
		t.Fatalf("expected one clear filesystem-policy diagnostic, got %#v", diagnostics)
	}
}

func completionLabels(items []CompletionItem) []string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	return labels
}

func assertSingleLocationRange(t *testing.T, sql string, locations []Location, start, end int) {
	t.Helper()
	if len(locations) != 1 {
		t.Fatalf("expected one location, got %#v", locations)
	}
	want := RangeFromOffsets(sql, start, end)
	if locations[0].Range != want {
		t.Fatalf("expected range %#v, got %#v", want, locations[0].Range)
	}
}

func assertLocationRanges(t *testing.T, sql string, locations []Location, ranges [][2]int) {
	t.Helper()
	if len(locations) != len(ranges) {
		t.Fatalf("expected %d locations, got %#v", len(ranges), locations)
	}
	want := map[Range]bool{}
	for _, rng := range ranges {
		want[RangeFromOffsets(sql, rng[0], rng[1])] = true
	}
	for _, location := range locations {
		if !want[location.Range] {
			t.Fatalf("unexpected reference range %#v in locations %#v", location.Range, locations)
		}
	}
}

func assertEditRanges(t *testing.T, sql string, edit *WorkspaceEdit, ranges [][2]int, newText string) {
	t.Helper()
	if edit == nil {
		t.Fatal("expected workspace edit, got nil")
	}
	edits := edit.Changes["file:///query.sql"]
	if len(edits) != len(ranges) {
		t.Fatalf("expected %d edits, got %#v", len(ranges), edits)
	}
	want := map[Range]bool{}
	for _, rng := range ranges {
		want[RangeFromOffsets(sql, rng[0], rng[1])] = true
	}
	for _, textEdit := range edits {
		if textEdit.NewText != newText {
			t.Fatalf("expected new text %q, got %#v", newText, textEdit)
		}
		if !want[textEdit.Range] {
			t.Fatalf("unexpected edit range %#v in edits %#v", textEdit.Range, edits)
		}
	}
}

type decodedSemanticToken struct {
	text      string
	tokenType uint32
}

func decodeSemanticTokenRanges(t *testing.T, text string, tokens SemanticTokens) []decodedSemanticToken {
	t.Helper()
	if len(tokens.Data)%5 != 0 {
		t.Fatalf("invalid semantic token data: %#v", tokens.Data)
	}
	line := 0
	character := 0
	var decoded []decodedSemanticToken
	for i := 0; i < len(tokens.Data); i += 5 {
		line += int(tokens.Data[i])
		if tokens.Data[i] == 0 {
			character += int(tokens.Data[i+1])
		} else {
			character = int(tokens.Data[i+1])
		}
		length := int(tokens.Data[i+2])
		start := ByteOffset(text, Position{Line: line, Character: character})
		end := ByteOffset(text, Position{Line: line, Character: character + length})
		decoded = append(decoded, decodedSemanticToken{text: text[start:end], tokenType: tokens.Data[i+3]})
	}
	return decoded
}
