package sqllsp

import (
	"strings"
	"testing"
)

func functionCompletionFixture(t testing.TB, dialect, sql string) (*Engine, TextDocumentItem, Position) {
	t.Helper()
	prefix, suffix, ok := strings.Cut(sql, "/*cursor*/")
	if !ok {
		t.Fatal("missing cursor")
	}
	const uri URI = "file:///functions.sql"
	doc := TextDocumentItem{URI: uri, Text: prefix + suffix}
	e := NewEngine(CanonicalGraph{
		Version: 1, Assets: []AssetNode{{ID: "query", URI: uri, Dialect: dialect}},
		Relations: []RelationNode{{ID: "orders", Name: "orders"}},
		Schemas:   []SchemaLayer{{RelationID: "orders", Columns: []ColumnInfo{{Name: "cost", Type: "DOUBLE"}, {Name: "count", Type: "INTEGER"}}}},
	})
	return e, doc, PositionAt(doc.Text, len(prefix))
}

func TestDialectFunctionCompletions(t *testing.T) {
	for _, tc := range []struct{ dialect, sql, want, absent string }{
		{"duckdb", `SELECT co/*cursor*/ FROM orders`, "count", "read_parquet"},
		{"duckdb", `SELECT coal/*cursor*/ FROM orders`, "coalesce", "generate_series"},
		{"postgresql", `SELECT nul/*cursor*/ FROM orders`, "nullif", "read_csv"},
		{"duckdb", `SELECT /*cursor*/ FROM orders`, "count", "read_parquet"},
		{"duckdb", `SELECT round(round(/*cursor*/)) FROM orders`, "round", "read_parquet"},
		{"duckdb", "WITH sample AS (SELECT 1 AS cost, 2 AS rounding)\r\nSELECT round(round(/*cursor*/))\r\nFROM sample", "round", "read_parquet"},
		{"duckdb", `SELECT * FROM /*cursor*/`, "read_parquet", "count"},
		{"postgresql", `SELECT lower(/*cursor*/) FROM orders`, "lower", "generate_series"},
		{"clickhouse", `SELECT round(/*cursor*/) FROM orders`, "toString", "numbers"},
		{"duckdb", `SELECT * FROM read_/*cursor*/`, "read_parquet", "count"},
		{"postgresql", `SELECT low/*cursor*/ FROM orders`, "lower", "read_parquet"},
		{"postgresql", `SELECT * FROM generate_/*cursor*/`, "generate_series", "count"},
		{"clickhouse", `SELECT toSt/*cursor*/ FROM orders`, "toString", "generate_series"},
		{"clickhouse", `SELECT * FROM num/*cursor*/`, "numbers", "count"},
		{"duckdb", `SELECT * FROM orders WHERE co/*cursor*/`, "contains", "count"},
		{"duckdb", `SELECT orders./*cursor*/ FROM orders`, "", "count"},
		{"duckdb", `SELECT "co/*cursor*/" FROM orders`, "", "count"},
		{"redshift", `SELECT low/*cursor*/ FROM orders`, "", "lower"},
	} {
		t.Run(tc.dialect+tc.sql, func(t *testing.T) {
			e, doc, pos := functionCompletionFixture(t, tc.dialect, tc.sql)
			found := false
			for _, item := range e.Complete(doc, pos) {
				if item.Kind != 3 {
					continue
				}
				if item.Label == tc.absent {
					t.Errorf("unexpected function %s", item.Label)
				}
				if item.Label == tc.want {
					found = true
					if item.Documentation == "" || item.Detail == "" {
						t.Error("missing catalog context")
					}
				}
			}
			if tc.want != "" && !found {
				t.Errorf("missing function %s", tc.want)
			}
		})
	}
}

const nestedRoundCompletionSQL = `SELECT
    event_id,
    magnitude,
    place,
    observed_at_ms,
    significance,
    detail_url,
    round(round(/*cursor*/))
FROM earthquakes.events
WHERE magnitude >= {{ var.notable_magnitude }}
ORDER BY magnitude DESC, observed_at_ms DESC`

func nestedRoundCompletionFixture(t testing.TB) (*Engine, TextDocumentItem, Position) {
	t.Helper()
	prefix, suffix, _ := strings.Cut(nestedRoundCompletionSQL, "/*cursor*/")
	const uri URI = "file:///notable_events.sql"
	doc := TextDocumentItem{URI: uri, Text: prefix + suffix}
	projection := ProjectRenderedSQL(uri, doc.Text, strings.ReplaceAll(doc.Text, "{{ var.notable_magnitude }}", "5"))
	doc.Projection = &projection
	engine := NewEngine(CanonicalGraph{
		Version: 1, Assets: []AssetNode{{ID: "query", URI: uri, Dialect: "duckdb"}},
		Relations: []RelationNode{{ID: "events", Name: "earthquakes.events"}},
		Schemas: []SchemaLayer{{RelationID: "events", Columns: []ColumnInfo{
			{Name: "event_id", Type: "VARCHAR"}, {Name: "magnitude", Type: "DOUBLE"},
			{Name: "place", Type: "VARCHAR"}, {Name: "observed_at_ms", Type: "BIGINT"},
			{Name: "significance", Type: "INTEGER"}, {Name: "detail_url", Type: "VARCHAR"},
		}}},
	})
	return engine, doc, PositionAt(doc.Text, len(prefix))
}

func TestNestedFunctionArgumentsIncludeColumnsAndFunctions(t *testing.T) {
	engine, doc, pos := nestedRoundCompletionFixture(t)
	items := engine.Complete(doc, pos)
	var column, function *CompletionItem
	for i := range items {
		if items[i].Kind == completionKindField && items[i].Label == "magnitude" {
			column = &items[i]
		}
		if items[i].Kind == completionKindFunction && items[i].Label == "round" {
			function = &items[i]
		}
	}
	if column == nil || function == nil {
		t.Fatalf("nested argument must offer both magnitude and round (column=%v, function=%v)", column != nil, function != nil)
	}
	if column.SortText >= function.SortText {
		t.Fatal("columns must sort ahead of functions")
	}
}

func BenchmarkNestedFunctionCompletion(b *testing.B) {
	engine, doc, pos := nestedRoundCompletionFixture(b)
	engine.Complete(doc, pos) // exclude first-use catalog initialization
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.Complete(doc, pos)
	}
}

func TestFunctionCompletionResultsDoNotMutateCachedCandidates(t *testing.T) {
	engine, doc, pos := nestedRoundCompletionFixture(t)
	items := engine.Complete(doc, pos)
	for i := range items {
		if items[i].Kind == completionKindFunction {
			items[i].Label = "modified"
			items[i].Documentation = "modified"
		}
	}
	var found bool
	for _, item := range engine.Complete(doc, pos) {
		if item.Kind == completionKindFunction && item.Label == "round" {
			found = true
			if item.Documentation == "" || item.Documentation == "modified" {
				t.Fatal("completion text was corrupted by an earlier request")
			}
		}
	}
	if !found {
		t.Fatal("cached round function was lost")
	}
}

func TestColumnsSortBeforeFunctionsIncludingNameCollisions(t *testing.T) {
	e, doc, pos := functionCompletionFixture(t, "duckdb", `SELECT co/*cursor*/ FROM orders`)
	var cost, columnCount, fnCount *CompletionItem
	items := e.Complete(doc, pos)
	for i := range items {
		item := &items[i]
		if item.Label == "cost" && item.Kind == 5 {
			cost = item
		}
		if item.Label == "count" && item.Kind == 5 {
			columnCount = item
		}
		if item.Label == "count" && item.Kind == 3 {
			fnCount = item
		}
	}
	if cost == nil || columnCount == nil || fnCount == nil {
		t.Fatalf("missing candidates: %#v", items)
	}
	if cost.SortText >= fnCount.SortText || columnCount.SortText >= fnCount.SortText {
		t.Fatal("functions outrank columns")
	}
	// Name-only insertion works both before an existing '(' and a fresh call,
	// without committing the user to a guessed overload/arity.
	if fnCount.InsertText != "count" {
		t.Fatalf("unsafe call insertion %q", fnCount.InsertText)
	}
}

func TestBuiltinFunctionSignatureHelp(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		active    int
	}{
		{`SELECT lower(/*cursor*/)`, "lower", 0},
		{`SELECT round(1.25, /*cursor*/)`, "round", 1},
		{`SELECT concat(lower('x'), /*cursor*/)`, "concat", 1},
		{`SELECT concat('a,b', /*cursor*/)`, "concat", 1},
		{`SELECT coalesce(cost = /*cursor*/, false) FROM orders`, "coalesce", 0},
		{`SELECT * FROM range(1, /*cursor*/)`, "range", 1},
		{`SELECT abs(/* comma, ) */ /*cursor*/)`, "abs", 0},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e, doc, pos := functionCompletionFixture(t, "duckdb", tc.sql)
			help := e.SignatureHelp(doc, pos)
			if help == nil || len(help.Signatures) == 0 {
				t.Fatal("missing signature")
			}
			if !strings.HasPrefix(help.Signatures[help.ActiveSignature].Label, tc.name+"(") {
				t.Fatalf("wrong signature: %#v", help)
			}
			if help.ActiveParameter != tc.active {
				t.Fatalf("argument = %d, want %d", help.ActiveParameter, tc.active)
			}
		})
	}
	for _, sql := range []string{`SELECT custom.lower(/*cursor*/)`, `SELECT 1 -- lower(/*cursor*/`, `SELECT lower('x') /*cursor*/`, `SELECT * FROM read_csv('a.csv', nonexistent_option = /*cursor*/)`} {
		e, doc, pos := functionCompletionFixture(t, "duckdb", sql)
		if help := e.SignatureHelp(doc, pos); help != nil {
			t.Errorf("unexpected builtin signature for %s: %#v", sql, help)
		}
	}
}

func TestBuiltinNamedArgumentSignatureHelp(t *testing.T) {
	for _, tc := range []struct{ sql, parameter string }{
		{`SELECT * FROM read_csv('local.csv', header = /*cursor*/)`, "header"},
		{`SELECT * FROM read_csv('local.csv', delim = ',', header = /*cursor*/)`, "header"},
		{`SELECT * FROM read_csv('local.csv', header = coalesce(true, /*cursor*/))`, "… ANY"},
		{`SELECT * FROM read_parquet('local.parquet', union_by_name = /*cursor*/)`, "union_by_name"},
		{`SELECT * FROM read_parquet('local.parquet', union_by_name /* comment */ = /*cursor*/)`, "union_by_name"},
		{`SELECT * FROM read_csv('local.csv', columns = {'cost': 'DOUBLE'}, header = /*cursor*/)`, "header"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e, doc, pos := functionCompletionFixture(t, "duckdb", tc.sql)
			help := e.SignatureHelp(doc, pos)
			if help == nil || len(help.Signatures) == 0 {
				t.Fatal("missing signature help")
			}
			sig := help.Signatures[help.ActiveSignature]
			if help.ActiveParameter >= len(sig.Parameters) || !strings.HasPrefix(strings.TrimPrefix(sig.Parameters[help.ActiveParameter].Label, "["), tc.parameter) {
				t.Fatalf("wanted active parameter %s, got index %d in %s", tc.parameter, help.ActiveParameter, sig.Label)
			}
		})
	}
}
