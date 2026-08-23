package sqllsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadGraphFromDirBruinDemo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "analytics/pipeline.yml", "name: analytics\n")
	writeFile(t, root, "analytics/assets/analytics/orders.sql", `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as order_id, 100 as total_amount`)
	writeFile(t, root, "analytics/assets/analytics/order_rollup.sql", `/* @bruin
type: duckdb.sql
depends:
  - analytics.orders
@bruin */

select o.order_id, o.total_amount
from analytics.orders o`)

	graph, err := LoadGraphFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(graph.Assets))
	}
	if len(graph.Renderings) != 2 {
		t.Fatalf("expected render artifacts for SQL assets, got %d", len(graph.Renderings))
	}
	if !relationNames(graph).Contains("analytics.orders") {
		t.Fatalf("expected analytics.orders relation, got %#v", relationNames(graph))
	}
	engine := NewEngine(graph)
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "select o.\nfrom analytics.orders o"}
	labels := completionLabels(engine.Complete(doc, Position{Line: 0, Character: len("select o.")}))
	if !slices.Contains(labels, "order_id") || !slices.Contains(labels, "total_amount") {
		t.Fatalf("expected order columns from Bruin graph, got %#v", labels)
	}
}

func TestLoadGraphFromDirPreservesBruinColumnConstraints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "analytics/pipeline.yml", "name: analytics\n")
	writeFile(t, root, "analytics/assets/users.sql", `/* @bruin
name: analytics.users
type: duckdb.sql
columns:
  - name: id
    type: integer
    nullable: false
    primary_key: true
@bruin */
select 1 as id`)
	writeFile(t, root, "analytics/assets/orders.sql", `/* @bruin
name: analytics.orders
type: duckdb.sql
columns:
  - name: user_id
    type: integer
    foreign_key:
      table: analytics.users
      column: id
@bruin */
select 1 as user_id`)

	graph, err := LoadGraphFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	constraints := ValidationSchemaConstraints(graph)
	users := constraints["analytics.users"].Columns["id"]
	if users.Nullable == nil || *users.Nullable || !users.PrimaryKey {
		t.Fatalf("user constraints were not preserved: %#v", users)
	}
	orders := constraints["analytics.orders"].Columns["user_id"]
	if orders.ForeignKey == nil || orders.ForeignKey.Table != "analytics.users" || orders.ForeignKey.Column != "id" {
		t.Fatalf("foreign key was not preserved: %#v", orders)
	}
}

func TestLoadGraphFromDirDBTDemo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "dbt_project.yml", "name: demo\nversion: '1.0'\n")
	writeFile(t, root, "models/schema.yml", `version: 2
models:
  - name: orders
    columns:
      - name: order_id
        type: integer
sources:
  - name: raw
    tables:
      - name: customers
        columns:
          - name: customer_id
            type: integer
`)
	writeFile(t, root, "models/orders.sql", `select customer_id as order_id from {{ source('raw', 'customers') }}`)
	writeFile(t, root, "models/report.sql", `select o.order_id from {{ ref('orders') }} o`)

	graph, err := LoadGraphFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	names := relationNames(graph)
	if !names.Contains("orders") || !names.Contains("raw.customers") {
		t.Fatalf("expected dbt model and source relations, got %#v", names)
	}
	var reportRendering *RenderedSQL
	for i := range graph.Renderings {
		if graph.Renderings[i].AssetID == "asset:dbt:report" {
			reportRendering = &graph.Renderings[i]
		}
	}
	if reportRendering == nil || !strings.Contains(reportRendering.RenderedSQL, "orders") || len(reportRendering.SourceMap.Segments) == 0 {
		t.Fatalf("expected dbt report rendered SQL artifact with source map, got %#v", reportRendering)
	}
	engine := NewEngine(graph)
	doc := TextDocumentItem{URI: "file:///query.sql", Text: "select o.\nfrom orders o"}
	labels := completionLabels(engine.Complete(doc, Position{Line: 0, Character: len("select o.")}))
	if !slices.Contains(labels, "order_id") {
		t.Fatalf("expected dbt model columns, got %#v", labels)
	}
}

type relationNameList []string

func (names relationNameList) Contains(name string) bool {
	return slices.Contains(names, name)
}

func relationNames(graph CanonicalGraph) relationNameList {
	names := make(relationNameList, 0, len(graph.Relations))
	for _, relation := range graph.Relations {
		names = append(names, relation.Name)
	}
	return names
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDialectFromAssetTypeSupportsQuerySensors(t *testing.T) {
	tests := map[string]string{
		"duckdb.sensor.query":     "duckdb",
		"bq.sensor.query":         "bigquery",
		"pg.sensor.query":         "postgres",
		"sf.sensor.query":         "snowflake",
		"synapse.sensor.query":    "tsql",
		"clickhouse.sql":          "clickhouse",
		"clickhouse.sensor.query": "clickhouse",
		"my.sql":                  "mysql",
		"my.sensor.query":         "mysql",
		"oracle.sql":              "oracle",
		"doris.sql":               "doris",
		"dremio.sensor.query":     "trino",
		"sail.sql":                "trino",
		"fabric.sql":              "fabric",
		"vertica.sql":             "postgres",
		"motherduck.sql":          "duckdb",
	}
	for assetType, expected := range tests {
		t.Run(assetType, func(t *testing.T) {
			if actual := DialectFromAssetType(assetType); actual != expected {
				t.Fatalf("expected %q for %q, got %q", expected, assetType, actual)
			}
		})
	}
}

// Regression: stripTemplates used to consume only up to the ref/source name's
// closing quote, leaving a dangling `) }}` fragment that leaked into relation
// extraction.
func TestStripTemplatesConsumesFullRefCall(t *testing.T) {
	got := stripTemplates(`select * from {{ ref('orders') }}`)
	if strings.Contains(got, "{{") || strings.Contains(got, "}}") {
		t.Fatalf("stripTemplates left template residue: %q", got)
	}
	relations := analyzeSQL(got).relations
	if len(relations) != 1 || relations[0].name != "orders" {
		t.Fatalf("expected a single `orders` relation, got %#v (stripped=%q)", relations, got)
	}
}

func TestStripTemplatesConsumesFullSourceCall(t *testing.T) {
	got := stripTemplates(`select * from {{ source('raw', 'events') }}`)
	if strings.Contains(got, "{{") || strings.Contains(got, "}}") {
		t.Fatalf("stripTemplates left template residue: %q", got)
	}
	relations := analyzeSQL(got).relations
	if len(relations) != 1 || relations[0].name != "raw.events" {
		t.Fatalf("expected a single `raw.events` relation, got %#v (stripped=%q)", relations, got)
	}
}

// Regression: mergeColumns must not write into the caller's slice via
// append(left, right...) when left has spare capacity.
func TestMergeColumnsDoesNotMutateLeftBackingArray(t *testing.T) {
	base := make([]ColumnInfo, 1, 4)
	base[0] = ColumnInfo{Name: "a"}
	mergeColumns(base, []ColumnInfo{{Name: "b"}, {Name: "c"}})
	spare := base[:cap(base)]
	for i := 1; i < len(spare); i++ {
		if spare[i].Name != "" {
			t.Fatalf("mergeColumns wrote into caller's spare capacity: %#v", spare)
		}
	}
}
