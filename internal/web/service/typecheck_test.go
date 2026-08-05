package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/authoringdiag"
	"renart/internal/web/model"
)

// writeTypeCheckWorkspace lays out a minimal bruin workspace (a `.git` marker, a
// `.bruin.yml`, a pipeline.yml, and the given asset files keyed by their path
// relative to the assets dir) and returns the loaded pipeline plus the
// workspace root.
func writeTypeCheckWorkspace(t *testing.T, pipelineYML string, assets map[string]string) (*pipeline.Pipeline, string) {
	t.Helper()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte(strings.TrimSpace(`
default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: local.db
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(pipelineYML)+"\n"), 0o644))

	for name, content := range assets {
		path := filepath.Join(assetsRoot, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644))
	}

	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).
		CreatePipelineFromPath(context.Background(), pipelineRoot, pipeline.WithMutate())
	require.NoError(t, err)
	return parsed, workspaceRoot
}

func runTypeCheck(t *testing.T, parsed *pipeline.Pipeline, workspaceRoot string) TypeCheckReport {
	t.Helper()
	tw, err := ResolveExecutionTimeWindow(string(parsed.Schedule), "", "", time.Now().UTC())
	require.NoError(t, err)
	report := CheckPipeline(context.Background(), afero.NewOsFs(), parsed, workspaceRoot, tw)
	for _, asset := range report.Assets {
		for _, finding := range asset.Findings {
			require.NotEmpty(t, finding.Code, "finding has no stable code: asset=%s finding=%#v", asset.Name, finding)
			_, registered := authoringdiag.TypeCheckDelivery(finding.Code)
			require.True(t, registered, "finding code %q has no editor delivery: asset=%s finding=%#v", finding.Code, asset.Name, finding)
		}
	}
	return report
}

func findAsset(t *testing.T, report TypeCheckReport, name string) TypeCheckAsset {
	t.Helper()
	for _, asset := range report.Assets {
		if asset.Name == name {
			return asset
		}
	}
	t.Fatalf("asset %q not found in report (have %d assets)", name, len(report.Assets))
	return TypeCheckAsset{}
}

func hasFinding(asset TypeCheckAsset, severity, substring string) bool {
	for _, finding := range asset.Findings {
		if finding.Severity == severity && strings.Contains(finding.Message, substring) {
			return true
		}
	}
	return false
}

func TestCheckPipelineFlagsUnresolvedColumnAgainstKnownUpstream(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: INTEGER
  - name: amount
    type: DOUBLE
@bruin */
select 1 as id, 2.0 as amount
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.up
@bruin */
select id, missing_column from analytics.up
`,
	})

	report := runTypeCheck(t, parsed, root)

	down := findAsset(t, report, "analytics.down")
	assert.Equal(t, typeCheckStatusError, down.Status)
	assert.True(t, hasFinding(down, typeCheckSeverityError, "Unresolved column: missing_column"),
		"expected unresolved-column error, got %+v", down.Findings)

	up := findAsset(t, report, "analytics.up")
	assert.Equal(t, typeCheckStatusOK, up.Status)
	assert.Empty(t, up.Findings)

	// A genuine error must report a 1-based location into the rendered SQL.
	require.NotEmpty(t, down.Findings)
	assert.Greater(t, down.Findings[0].Line, 0)
	assert.Equal(t, typeCheckStatusError, report.Status)
}

func TestCheckPipelineValidatesCustomCheckSQLAgainstMaterializedAsset(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: table
columns:
  - name: order_id
    type: INTEGER
custom_checks:
  - name: positive totals
    value: 0
    query: |
      select missing_total
      from analytics.orders
@bruin */
select 1 as order_id
`,
	})

	report := runTypeCheck(t, parsed, root)
	orders := findAsset(t, report, "analytics.orders")
	assert.Equal(t, typeCheckStatusError, orders.Status)
	assert.True(
		t,
		hasFinding(orders, typeCheckSeverityError, `Custom check "positive totals": Unresolved column: missing_total`),
		"expected custom-check SQL diagnostic, got %+v",
		orders.Findings,
	)
	for _, finding := range orders.Findings {
		if strings.Contains(finding.Message, `Custom check "positive totals"`) {
			assert.Zero(t, finding.Line, "custom-check ranges must not point into the asset body")
		}
	}
}

func TestCheckPipelineWarnsForCrossConnectionReferenceThroughLSP(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: pg.sql
connection: postgres-default
materialization:
  type: table
columns:
  - name: order_id
    type: bigint
@bruin */
select 1 as order_id
`,
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
connection: duckdb-default
materialization:
  type: view
depends:
  - analytics.orders
@bruin */
select order_id from analytics.orders
`,
	})

	report := runTypeCheck(t, parsed, root)
	asset := findAsset(t, report, "analytics.report")
	assert.Equal(t, typeCheckStatusWarning, asset.Status)
	assert.True(
		t,
		hasFinding(asset, typeCheckSeverityWarning, "Cross-connection reference"),
		"expected LSP cross-connection warning, got %+v",
		asset.Findings,
	)
	assert.Equal(t, 0, report.Summary.Errors)
	assert.GreaterOrEqual(t, report.Summary.Warnings, 1)
}

func TestCheckPipelineValidatesQuerySensorParameterSQL(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"events.sql": `
/* @bruin
name: analytics.events
type: duckdb.sql
materialization:
  type: table
columns:
  - name: event_id
    type: integer
@bruin */
select 1 as event_id
`,
		"events_ready.asset.yml": `
name: analytics.events_ready
type: duckdb.sensor.query
depends:
  - analytics.events
parameters:
  query: select count(*) from analytics.events
  poke_interval: 5
`,
	})

	report := runTypeCheck(t, parsed, root)
	sensor := findAsset(t, report, "analytics.events_ready")
	assert.Equal(t, typeCheckStatusOK, sensor.Status)
	assert.Empty(t, sensor.Findings)
}

func TestCheckPipelinePropagatesColumnsThroughSelectStarCTEs(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: chess", map[string]string{
		"game_results.sql": `
/* @bruin
name: chess.game_results
type: duckdb.sql
materialization:
  type: table
columns:
  - name: name
    type: VARCHAR
  - name: username
    type: VARCHAR
  - name: color
    type: VARCHAR
  - name: time_class
    type: VARCHAR
  - name: eco_code
    type: VARCHAR
  - name: opening
    type: VARCHAR
  - name: score
    type: DOUBLE
  - name: accuracy
    type: DOUBLE
@bruin */
select 'Magnus' as name, 'MagnusCarlsen' as username, 'white' as color,
       'blitz' as time_class, 'C20' as eco_code, 'King Pawn' as opening,
       1.0 as score, 98.0 as accuracy
`,
		"opening_repertoire.sql": `
/* @bruin
name: chess.opening_repertoire
type: duckdb.sql
materialization:
  type: table
depends:
  - chess.game_results
@bruin */
WITH opening_rollup AS (
    SELECT
        name,
        username,
        color,
        time_class,
        eco_code,
        min(opening) AS opening,
        count(*) AS games,
        round(100 * avg(score), 1) AS score_percent,
        round(avg(accuracy), 1) AS average_accuracy
    FROM chess.game_results
    WHERE eco_code IS NOT NULL
    GROUP BY name, username, color, time_class, eco_code
),
ranked_openings AS (
    SELECT
        *,
        row_number() OVER (
            PARTITION BY username, color, time_class
            ORDER BY games DESC, score_percent DESC, eco_code
        ) AS repertoire_rank
    FROM opening_rollup
)
SELECT * EXCLUDE (repertoire_rank)
FROM ranked_openings
ORDER BY username, time_class, color, games DESC, score_percent DESC
`,
	})

	report := runTypeCheck(t, parsed, root)
	repertoire := findAsset(t, report, "chess.opening_repertoire")
	assert.Equal(t, typeCheckStatusOK, repertoire.Status, "unexpected findings: %+v", repertoire.Findings)
	assert.Empty(t, repertoire.Findings)
}

func TestCheckPipelineRequiresColumnsForUninferableNonSQLAssets(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"loader.py": `
""" @bruin
name: analytics.loader
type: python
materialization:
  type: table
@bruin """

def materialize():
    return [{"id": 1}]
`,
	})

	report := runTypeCheck(t, parsed, root)

	loader := findAsset(t, report, "analytics.loader")
	assert.Equal(t, typeCheckStatusError, loader.Status)
	assert.True(t, hasFinding(loader, typeCheckSeverityError, "Output schema cannot be inferred"),
		"expected missing-columns error, got %+v", loader.Findings)
	assert.Equal(t, 0, report.Summary.Warnings)
	assert.Equal(t, 1, report.Summary.Errors)
}

func TestCheckPipelineDoesNotWarnWhenNonSQLDeclaresColumns(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"loader.py": `
""" @bruin
name: analytics.loader
type: python
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
@bruin """
print("hi")
`,
	})

	report := runTypeCheck(t, parsed, root)
	loader := findAsset(t, report, "analytics.loader")
	assert.Equal(t, typeCheckStatusOK, loader.Status)
	assert.Empty(t, loader.Findings)
}

func TestCheckPipelineAcceptsNonSQLSchemaDerivedFromDefinition(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, `
name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"source.sql": `
/* @bruin
name: analytics.source
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
@bruin */
select 1 as id
`,
		"copy.asset.yml": `
name: analytics.copy
type: load
connection: duckdb-default
parameters:
  source_connection: duckdb-default
  source_table: analytics.source
  destination_connection: duckdb-default
  destination_object: analytics.copy
materialization:
  type: table
  strategy: create+replace
depends:
  - analytics.source
`,
		"events.asset.yml": `
name: analytics.events
type: api
connection: duckdb-default
parameters:
  request:
    url: https://example.invalid/events
  response:
    fields:
      id: id
materialization:
  type: table
  strategy: create+replace
`,
	})

	report := runTypeCheck(t, parsed, root)
	for _, name := range []string{"analytics.copy", "analytics.events"} {
		asset := findAsset(t, report, name)
		assert.False(t, hasFinding(asset, typeCheckSeverityError, "Output schema cannot be inferred"),
			"definition-derived asset %s was rejected: %+v", name, asset.Findings)
	}
}

func TestCheckPipelineDoesNotRequireColumnsForNonMaterializingPythonAsset(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"notify.py": `
""" @bruin
name: analytics.notify
type: python
@bruin """
print("done")
`,
	})

	report := runTypeCheck(t, parsed, root)
	notify := findAsset(t, report, "analytics.notify")
	assert.Equal(t, typeCheckStatusOK, notify.Status)
	assert.Empty(t, notify.Findings)
}

func TestCheckPipelineRequiresPersistedColumnsForSeedSchema(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"events.asset.yml": `
name: analytics.events
type: duckdb.seed
parameters:
  path: ./events.csv
  file_type: csv
`,
		"events.csv": "id,name\n1,Ada\n",
	})

	report := runTypeCheck(t, parsed, root)
	events := findAsset(t, report, "analytics.events")
	assert.True(t, hasFinding(events, typeCheckSeverityError, "Output schema cannot be inferred"),
		"seed without persisted columns should not silently disable downstream checks: %+v", events.Findings)
}

func TestCheckPipelineWarnsForUndeclaredPythonQueryAsset(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
@bruin */
select 1 as id
`,
		"reader.py": `
""" @bruin
name: analytics.reader
type: python
columns:
  - name: id
    type: BIGINT
@bruin """
from renart import query

def materialize():
    return query("select id from analytics.up")
`,
	})

	report := runTypeCheck(t, parsed, root)
	reader := findAsset(t, report, "analytics.reader")
	assert.True(t, hasFinding(reader, typeCheckSeverityWarning, "without declaring it in depends"),
		"expected undeclared query dependency warning, got %+v", reader.Findings)
	assert.Equal(t, typeCheckStatusWarning, reader.Status)
	for _, finding := range reader.Findings {
		if strings.Contains(finding.Message, "without declaring it in depends") {
			assert.Greater(t, finding.Line, 0)
			assert.Greater(t, finding.Column, 0)
		}
	}
}

func TestPythonEditorPublishesUndeclaredQueryDependency(t *testing.T) {
	state := model.WorkspaceState{Pipelines: []model.Pipeline{{
		ID: "analytics",
		Assets: []model.Asset{
			{ID: "up", Name: "analytics.up", Type: "duckdb.sql"},
			{ID: "reader", Name: "analytics.reader", Type: "python"},
		},
	}}}
	service := NewAssetService(AssetDependencies{
		CurrentState: func() WorkspaceState { return state },
	})
	diagnostics := service.pythonQueryDependencyDiagnostics(
		context.Background(),
		"reader",
		"from renart import query\n\ndef materialize():\n    return query(\"select id from analytics.up\")\n",
	)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, authoringdiag.CodePythonUndeclaredQueryDependency, diagnostics[0].Code)
	assert.Equal(t, authoringdiag.SourceRenart, diagnostics[0].Source)
	assert.Equal(t, "warning", diagnostics[0].Severity)
	require.NotNil(t, diagnostics[0].Range)
	assert.Equal(t, PythonPosition{Line: 4, Column: 12}, diagnostics[0].Range.Start)
}

func TestCheckPipelineAcceptsDeclaredPythonQueryAsset(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
@bruin */
select 1 as id
`,
		"reader.py": `
""" @bruin
name: analytics.reader
type: python
depends:
  - analytics.up
columns:
  - name: id
    type: BIGINT
@bruin """
from renart import query

def materialize():
    return query("select id from analytics.up")
`,
	})

	report := runTypeCheck(t, parsed, root)
	reader := findAsset(t, report, "analytics.reader")
	assert.False(t, hasFinding(reader, typeCheckSeverityWarning, "without declaring it in depends"),
		"declared query dependency should not warn: %+v", reader.Findings)
	assert.Equal(t, typeCheckStatusOK, reader.Status)
}

func TestPythonQueryStringLiteralsIgnoresDynamicAndNonCodeCalls(t *testing.T) {
	t.Parallel()
	source := strings.Join([]string{
		`# query("select * from commented")`,
		`text = "query('select * from a_string')"`,
		`dynamic = query(sql)`,
		`formatted = query(f"select * from {table}")`,
		`first = query("select * from analytics.orders")`,
		`second = renart.query(r'''select * from analytics.customers''')`,
	}, "\n")

	literals := pythonQueryStringLiterals(source)
	require.Len(t, literals, 2)
	assert.Equal(t, "select * from analytics.orders", literals[0].SQL)
	assert.Equal(t, 5, literals[0].Line)
	assert.Equal(t, "select * from analytics.customers", literals[1].SQL)
	assert.Equal(t, 6, literals[1].Line)
}

func TestCheckPipelineSuppressesCascadeFromUndeclaredUpstream(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"loader.py": `
""" @bruin
name: analytics.loader
type: python
materialization:
  type: table
@bruin """

def materialize():
    return [{"some_unknowable_column": 1}]
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.loader
@bruin */
select some_unknowable_column from analytics.loader
`,
	})

	report := runTypeCheck(t, parsed, root)

	// The producer is rejected; the consumer must NOT receive a speculative
	// columns we cannot verify against an undeclared upstream.
	loader := findAsset(t, report, "analytics.loader")
	assert.Equal(t, typeCheckStatusError, loader.Status)

	down := findAsset(t, report, "analytics.down")
	assert.Equal(t, typeCheckStatusOK, down.Status, "unexpected findings: %+v", down.Findings)
	assert.Equal(t, 1, report.Summary.Errors)
}

func TestCheckPipelineRendersJinjaVariablesAndDates(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, `
name: analytics
variables:
  threshold:
    type: integer
    default: 100
`, map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
  - name: amount
    type: DOUBLE
@bruin */
select 1 as id, 2.0 as amount
`,
		// Uses a pipeline variable and a date macro; if either failed to render
		// the query would be invalid SQL and produce a finding.
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.up
@bruin */
select id, amount from analytics.up
where amount > {{ var.threshold }} and '{{ start_date }}' <= '{{ end_date }}'
`,
	})

	report := runTypeCheck(t, parsed, root)
	down := findAsset(t, report, "analytics.down")
	assert.Equal(t, typeCheckStatusOK, down.Status, "unexpected findings (Jinja likely unrendered): %+v", down.Findings)
}

func TestCheckPipelineIgnoresTableValuedFunctionColumns(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		// `range` is the implicit output column of DuckDB's range() table
		// function — the parser cannot introspect it, so it must not be flagged.
		"times.sql": `
/* @bruin
name: analytics.times
type: duckdb.sql
materialization:
  type: view
columns:
  - name: t
    type: BIGINT
@bruin */
select range as t from range(1, 11, 1)
`,
	})

	report := runTypeCheck(t, parsed, root)
	times := findAsset(t, report, "analytics.times")
	assert.Equal(t, typeCheckStatusOK, times.Status, "unexpected findings: %+v", times.Findings)
}

func TestCheckPipelineIgnoresResolvableScalarSubqueryColumnDiagnostic(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: view
columns:
  - name: range
    type: BIGINT
@bruin */
select range from range(1, 11, 1)
`,
		"outer.sql": `
/* @bruin
name: analytics.outer
type: duckdb.sql
materialization:
  type: view
columns:
  - name: range
    type: BIGINT
@bruin */
select range from range(1, 11, 1)
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.up
  - analytics.outer
columns:
  - name: range
    type: BIGINT
@bruin */
select
  *,
  (select first(range) from analytics.up)
from analytics.outer
`,
	})

	report := runTypeCheck(t, parsed, root)
	down := findAsset(t, report, "analytics.down")
	assert.Equal(t, typeCheckStatusOK, down.Status, "unexpected findings: %+v", down.Findings)
}

func TestCheckPipelineResolvesShortQualifierForSchemaQualifiedTable(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: example", map[string]string{
		"parabola.sql": `
/* @bruin
name: example.parabola
type: duckdb.sql
materialization:
  type: view
columns:
  - name: x
    type: BIGINT
  - name: y
    type: BIGINT
@bruin */
select 1 as x, 1 as y
`,
		"range_10.sql": `
/* @bruin
name: example.range_10
type: duckdb.sql
materialization:
  type: view
columns:
  - name: range
    type: BIGINT
@bruin */
select range from range(10)
`,
		"downstream.sql": `
/* @bruin
name: example.downstream
type: duckdb.sql
materialization:
  type: view
depends:
  - example.parabola
  - example.range_10
@bruin */
select *
from example.parabola p
join example.range_10
  on range_10.range = p.x
`,
	})

	report := runTypeCheck(t, parsed, root)
	downstream := findAsset(t, report, "example.downstream")
	assert.Equal(t, typeCheckStatusOK, downstream.Status, "unexpected findings: %+v", downstream.Findings)
}

func TestCheckPipelineReportsAssetIDs(t *testing.T) {
	t.Parallel()
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: BIGINT
@bruin */
select 1 as id
`,
	})

	report := runTypeCheck(t, parsed, root)
	up := findAsset(t, report, "analytics.up")
	// The report ID must match the workspace asset-id encoding so the UI can map
	// findings back to canvas nodes.
	assert.Equal(t, EncodeID("analytics/assets/up.sql"), up.ID)
}

func TestMaterializationTypeCheckFindings(t *testing.T) {
	t.Parallel()

	t.Run("dedicated seed runtime has no generic materialization finding", func(t *testing.T) {
		asset := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBSeed}

		assert.Empty(t, materializationTypeCheckFindings(asset))
	})

	t.Run("merge and update key errors", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type:    pipeline.AssetType("api"),
			Columns: []pipeline.Column{{Name: "id", Type: "integer"}},
			Materialization: pipeline.Materialization{
				Type:           pipeline.MaterializationTypeTable,
				Strategy:       pipeline.MaterializationStrategyMerge,
				IncrementalKey: "updated_at",
			},
		}

		findings := materializationTypeCheckFindings(asset)
		assert.Len(t, findings, 2)
		assert.Contains(t, findings[0].Message, "primary-key")
		assert.Contains(t, findings[1].Message, "updated_at is not declared")
	})

	t.Run("valid sling merge", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type:       pipeline.AssetType("load"),
			Connection: "duckdb-default",
			Parameters: pipeline.ParameterMap{
				loadParamSourceConnection: "postgres-default",
				loadParamSourceTable:      "public.orders",
			},
			Columns: []pipeline.Column{
				{Name: "id", Type: "integer", PrimaryKey: true},
				{Name: "updated_at", Type: "timestamp"},
			},
			Materialization: pipeline.Materialization{
				Type:           pipeline.MaterializationTypeTable,
				Strategy:       pipeline.MaterializationStrategyMerge,
				IncrementalKey: "updated_at",
			},
		}

		assert.Empty(t, materializationTypeCheckFindings(asset))
	})

	t.Run("unsupported sling strategy", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type: pipeline.AssetType("load"),
			Materialization: pipeline.Materialization{
				Type:     pipeline.MaterializationTypeTable,
				Strategy: pipeline.MaterializationStrategyTimeInterval,
			},
		}

		findings := materializationTypeCheckFindings(asset)
		require.Len(t, findings, 1)
		assert.Contains(t, findings[0].Message, "not supported")
	})

	t.Run("inactive strategy metadata is preserved without blocking", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type: pipeline.AssetTypeDuckDBQuery,
			Columns: []pipeline.Column{{
				Name:          "id",
				Type:          "BIGINT",
				UpdateOnMerge: true,
			}},
			Materialization: pipeline.Materialization{
				Type:           pipeline.MaterializationTypeTable,
				Strategy:       pipeline.MaterializationStrategyCreateReplace,
				IncrementalKey: "previously_used_key",
			},
		}

		findings := materializationTypeCheckFindings(asset)
		require.Len(t, findings, 1)
		assert.Equal(t, typeCheckSeverityWarning, findings[0].Severity)
		assert.Contains(t, findings[0].Message, "Inactive materialization metadata")
		require.Len(t, findings[0].Resolutions, 1)
		assert.Equal(t, "Delete inactive merge settings", findings[0].Resolutions[0].Title)
		assert.Equal(t, TxColumnMergeSettingsClear, findings[0].Resolutions[0].Transaction.Type)
		assert.Equal(t, "id", findings[0].Resolutions[0].Transaction.Column)
	})

	t.Run("time interval requires a key and granularity", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type: pipeline.AssetTypeDuckDBQuery,
			Materialization: pipeline.Materialization{
				Type:     pipeline.MaterializationTypeTable,
				Strategy: pipeline.MaterializationStrategyTimeInterval,
			},
		}

		findings := materializationTypeCheckFindings(asset)
		require.Len(t, findings, 2)
		assert.Contains(t, findings[0].Message, "incremental key")
		assert.Contains(t, findings[1].Message, "time granularity")
	})

	t.Run("invalid time granularity is rejected", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type:    pipeline.AssetTypeDuckDBQuery,
			Columns: []pipeline.Column{{Name: "event_at", Type: "TIMESTAMP"}},
			Materialization: pipeline.Materialization{
				Type:            pipeline.MaterializationTypeTable,
				Strategy:        pipeline.MaterializationStrategyTimeInterval,
				IncrementalKey:  "event_at",
				TimeGranularity: pipeline.MaterializationTimeGranularity("hour"),
			},
		}

		findings := materializationTypeCheckFindings(asset)
		require.Len(t, findings, 1)
		assert.Contains(t, findings[0].Message, "must be date or timestamp")
	})

	t.Run("inactive warehouse layout fields are warnings", func(t *testing.T) {
		asset := &pipeline.Asset{
			Type: pipeline.AssetTypeDuckDBQuery,
			Materialization: pipeline.Materialization{
				Type:        pipeline.MaterializationTypeTable,
				Strategy:    pipeline.MaterializationStrategyCreateReplace,
				PartitionBy: "event_date",
				ClusterBy:   []string{"customer_id"},
			},
		}

		findings := materializationTypeCheckFindings(asset)
		require.Len(t, findings, 2)
		assert.Equal(t, typeCheckSeverityWarning, findings[0].Severity)
		assert.Contains(t, findings[0].Message, "partition_by")
		require.Len(t, findings[0].Resolutions, 1)
		assert.Equal(t, TxMaterializationPartitionByClear, findings[0].Resolutions[0].Transaction.Type)
		assert.Contains(t, findings[1].Message, "cluster_by")
		require.Len(t, findings[1].Resolutions, 1)
		assert.Equal(t, TxMaterializationClusterByClear, findings[1].Resolutions[0].Transaction.Type)
	})
}

func TestCheckPipelineAcceptsSeedRuntimeWithoutGenericMaterialization(t *testing.T) {
	t.Parallel()

	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"customer_seed.asset.yml": `
name: analytics.customer_seed
type: duckdb.seed
parameters:
  path: ./customer_seed.csv
columns:
  - name: customer_id
    type: integer
  - name: customer_name
    type: varchar
`,
		"customer_seed.csv": "customer_id,customer_name\n1,Ada\n",
	})

	report := runTypeCheck(t, parsed, root)
	seed := findAsset(t, report, "analytics.customer_seed")
	assert.Equal(t, typeCheckStatusOK, seed.Status)
	assert.Empty(t, seed.Findings)
}
