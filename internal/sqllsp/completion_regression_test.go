package sqllsp

import (
	"slices"
	"strings"
	"testing"
)

func TestCompletionQueryShapeRegressions(t *testing.T) {
	for _, tc := range []struct {
		name, dialect, sql string
		want, forbidden    []string
		empty              bool
	}{
		{name: "CTE relation", sql: `WITH recent AS (SELECT order_id FROM orders) SELECT * FROM /*cursor*/`, want: []string{"recent", "orders"}},
		{name: "nested CTE relation", sql: `WITH report AS (WITH local_orders AS (SELECT order_id FROM orders) SELECT * FROM /*cursor*/) SELECT * FROM report`, want: []string{"local_orders", "orders"}},
		{name: "nested CTE does not leak", sql: `WITH report AS (WITH local_orders AS (SELECT order_id FROM orders) SELECT * FROM local_orders) SELECT * FROM /*cursor*/`, want: []string{"report"}, forbidden: []string{"local_orders"}},
		{name: "later sibling is not visible", sql: `WITH first AS (SELECT * FROM /*cursor*/), later AS (SELECT * FROM orders) SELECT * FROM first`, want: []string{"orders"}, forbidden: []string{"first", "later"}},
		{name: "recursive CTE column list", sql: `WITH RECURSIVE walk(step) AS (SELECT 1 UNION ALL SELECT step + 1 FROM walk WHERE step < 3) SELECT w./*cursor*/ FROM walk w`, want: []string{"step"}, forbidden: []string{"order_id", "cost"}},
		{name: "recursive body self reference", sql: `WITH RECURSIVE walk(step) AS (SELECT 1 UNION ALL SELECT w./*cursor*/ FROM walk w) SELECT * FROM walk`, want: []string{"step"}, forbidden: []string{"order_id"}},
		{name: "CTE column rename", sql: `WITH recent(order_key) AS (SELECT order_id FROM orders) SELECT r./*cursor*/ FROM recent r`, want: []string{"order_key"}, forbidden: []string{"order_id"}},
		{name: "previous statement CTE does not leak", sql: `WITH recent AS (SELECT order_id FROM orders) SELECT * FROM recent; SELECT * FROM /*cursor*/`, want: []string{"orders"}, forbidden: []string{"recent"}},
		{name: "inner CTE shadows outer", sql: `WITH data AS (SELECT cost FROM orders) SELECT * FROM (WITH data AS (SELECT email FROM customers) SELECT d./*cursor*/ FROM data d) q`, want: []string{"email"}, forbidden: []string{"cost"}},
		{name: "materialized CTE rename", dialect: "postgresql", sql: `WITH recent(order_key) AS NOT MATERIALIZED (SELECT order_id FROM orders) SELECT r./*cursor*/ FROM recent r`, want: []string{"order_key"}, forbidden: []string{"order_id"}},
		{name: "shadowed alias", sql: `SELECT * FROM orders x WHERE EXISTS (SELECT x./*cursor*/ FROM customers x)`, want: []string{"email"}, forbidden: []string{"cost", "order_id"}},
		{name: "correlated alias", sql: `SELECT * FROM orders o WHERE EXISTS (SELECT 1 FROM customers c WHERE c.customer_id = o./*cursor*/)`, want: []string{"cost", "order_id"}, forbidden: []string{"email"}},
		{name: "quoted alias with spaces", sql: `SELECT "Order Lines"./*cursor*/ FROM orders AS "Order Lines"`, want: []string{"cost", "order_id"}, forbidden: []string{"email"}},
		{name: "escaped quoted alias with prefix", sql: `SELECT "Order ""Lines""".co/*cursor*/ FROM orders AS "Order ""Lines"""`, want: []string{"cost"}, forbidden: []string{"email"}},
		{name: "quoted case-sensitive aliases", dialect: "postgresql", sql: `SELECT "O"./*cursor*/ FROM customers AS "O" JOIN orders AS "o" ON true`, want: []string{"email"}, forbidden: []string{"cost", "order_id"}},
		{name: "incomplete nested CTE", sql: `WITH recent AS (SELECT o./*cursor*/ FROM orders o`, want: []string{"order_id", "cost"}, forbidden: []string{"email"}},
		{name: "incomplete call", sql: `SELECT coalesce(o./*cursor*/ FROM orders o`, want: []string{"cost"}, forbidden: []string{"email"}},
		{name: "CTE nested call with CRLF", sql: "WITH sample AS (SELECT 1 AS cost, 2 AS rounding)\r\nSELECT round(round(/*cursor*/))\r\nFROM sample", want: []string{"cost", "rounding", "round"}, forbidden: []string{"email"}},
		{name: "DuckDB qualify", sql: `SELECT *, row_number() OVER () AS row_no FROM orders QUALIFY co/*cursor*/`, want: []string{"cost"}, forbidden: []string{"email"}},
		{name: "PostgreSQL distinct on", dialect: "postgresql", sql: `SELECT DISTINCT ON (co/*cursor*/) * FROM orders`, want: []string{"cost"}, forbidden: []string{"email"}},
		{name: "ClickHouse limit by", dialect: "clickhouse", sql: `SELECT * FROM orders LIMIT 2 BY co/*cursor*/`, want: []string{"cost"}, forbidden: []string{"email"}},
		{name: "line comment", sql: "SELECT * FROM orders -- co/*cursor*/", empty: true},
		{name: "unfinished block comment", sql: `SELECT * FROM orders /* co/*cursor*/`, empty: true},
		{name: "unfinished string", sql: `SELECT 'co/*cursor*/ FROM orders`, empty: true},
		{name: "PostgreSQL dollar string", dialect: "postgresql", sql: `SELECT $body$ co/*cursor*/ $body$ FROM orders`, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, suffix, found := strings.Cut(tc.sql, "/*cursor*/")
			if !found {
				t.Fatal("missing cursor marker")
			}
			dialect := tc.dialect
			if dialect == "" {
				dialect = "duckdb"
			}
			const uri URI = "file:///completion.sql"
			engine := NewEngine(CanonicalGraph{
				Version:   1,
				Assets:    []AssetNode{{ID: "query", URI: uri, Dialect: dialect}},
				Relations: []RelationNode{{ID: "orders", Name: "orders"}, {ID: "customers", Name: "customers"}},
				Schemas: []SchemaLayer{
					{RelationID: "orders", Columns: []ColumnInfo{{Name: "order_id", Type: "INTEGER"}, {Name: "cost", Type: "DOUBLE"}, {Name: "customer_id", Type: "INTEGER"}}},
					{RelationID: "customers", Columns: []ColumnInfo{{Name: "customer_id", Type: "INTEGER"}, {Name: "email", Type: "VARCHAR"}}},
				},
			})
			doc := TextDocumentItem{URI: uri, Text: prefix + suffix}
			items := engine.Complete(doc, PositionAt(doc.Text, len(prefix)))
			labels := completionLabels(items)
			if tc.empty && len(items) != 0 {
				t.Fatalf("expected no suggestions, got %v", labels)
			}
			for _, want := range tc.want {
				if !slices.Contains(labels, want) {
					t.Errorf("missing %q in %v", want, labels)
				}
			}
			for _, forbidden := range tc.forbidden {
				if slices.Contains(labels, forbidden) {
					t.Errorf("out-of-scope/unwanted %q in %v", forbidden, labels)
				}
			}
		})
	}
}
