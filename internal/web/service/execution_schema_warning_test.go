package service

import (
	"bytes"
	"context"
	"strings"
	"testing"

	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type runtimeSchemaQuerier struct {
	query  string
	result *query.QueryResult
	err    error
}

func (q *runtimeSchemaQuerier) Ping(context.Context) error { return nil }

func (q *runtimeSchemaQuerier) SelectWithSchema(_ context.Context, statement *query.Query) (*query.QueryResult, error) {
	q.query = statement.Query
	return q.result, q.err
}

func TestRunDirectTaskWarnsWhenResultSchemaDrifts(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{
		Name:       "analytics.orders",
		Type:       pipeline.AssetTypeDuckDBQuery,
		Connection: "warehouse",
		Materialization: pipeline.Materialization{
			Type: pipeline.MaterializationTypeTable,
		},
		Columns: []pipeline.Column{
			{Name: "id", Type: "INTEGER"},
			{Name: "legacy", Type: "TEXT"},
		},
	}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	instance := mainSchemaWarningTask(t, pl, asset)
	seq := &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{
		asset.Type: {scheduler.TaskInstanceTypeMain: testAssetLoggingOperator{}},
	}}
	querier := &runtimeSchemaQuerier{result: &query.QueryResult{
		Columns:     []string{"id", "created_at"},
		ColumnTypes: []string{"INTEGER", "TIMESTAMP"},
	}}
	manager := &stubConnectionManager{conn: querier, connectionType: "postgres"}
	var output bytes.Buffer
	printer := &streamCaptureWriter{buffer: &output}
	ctx, warnings := withExecutionWarnings(context.Background())

	err := (&HybridBruinExecutor{}).runDirectTask(ctx, pl, instance, nil, manager, seq, nil, printer)
	require.NoError(t, err)

	const warning = "Result schema for analytics.orders does not match its declaration: undeclared result columns: created_at (TIMESTAMP); missing result columns: legacy (TEXT)."
	assert.Equal(t, []string{warning}, warnings.snapshot())
	assert.Contains(t, directAssetLogANSI.ReplaceAllString(output.String(), ""), "WARNING: "+warning)
	assert.Equal(t, `SELECT * FROM "analytics"."orders" WHERE 1 = 0`, querier.query)
}

func TestRunDirectTaskKeepsSuccessfulRunWhenSchemaCannotBeObserved(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{
		Name:       "analytics.orders",
		Type:       pipeline.AssetTypeDuckDBQuery,
		Connection: "warehouse",
		Materialization: pipeline.Materialization{
			Type: pipeline.MaterializationTypeTable,
		},
		Columns: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
	}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	instance := mainSchemaWarningTask(t, pl, asset)
	seq := &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{
		asset.Type: {scheduler.TaskInstanceTypeMain: testAssetLoggingOperator{}},
	}}
	querier := &runtimeSchemaQuerier{err: assert.AnError}
	manager := &stubConnectionManager{conn: querier, connectionType: "postgres"}
	ctx, warnings := withExecutionWarnings(context.Background())

	err := (&HybridBruinExecutor{}).runDirectTask(
		ctx,
		pl,
		instance,
		nil,
		manager,
		seq,
		nil,
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.NoError(t, err)
	require.Len(t, warnings.snapshot(), 1)
	assert.Contains(t, warnings.snapshot()[0], "Could not verify materialization target")
	assert.Contains(t, warnings.snapshot()[0], "kept the configured materialization strategy")
}

func TestFormatDeclaredSchemaDriftIgnoresUnspecifiedDeclaredType(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{Name: "analytics.orders", Columns: []pipeline.Column{{Name: "id"}}}
	drift := compareColumnSchemas(asset.Columns, []WorkspaceColumn{{Name: "id", Type: "BIGINT"}})

	assert.Empty(t, formatDeclaredSchemaDriftWarning(asset, drift))
}

func TestQuoteRuntimeRelationUsesWarehouseIdentifierRules(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		connectionType string
		name           string
		want           string
	}{
		"ansi":               {connectionType: "postgres", name: "analytics.order", want: `"analytics"."order"`},
		"bigquery":           {connectionType: "google_cloud_platform", name: "project.analytics.order", want: "`project.analytics.order`"},
		"bigquery canonical": {connectionType: "bigquery", name: "project.analytics.order", want: "`project.analytics.order`"},
		"databricks":         {connectionType: "databricks", name: "catalog.analytics.order", want: "`catalog`.`analytics`.`order`"},
		"sql server":         {connectionType: "mssql", name: "analytics.order", want: "[analytics].[order]"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, quoteRuntimeRelation(test.name, test.connectionType))
		})
	}
}

func TestWithoutRuntimeManagedColumnsIsAssetSpecific(t *testing.T) {
	t.Parallel()

	columns := []WorkspaceColumn{
		{Name: "id", Type: "INTEGER"},
		{Name: "_sling_loaded_at", Type: "TIMESTAMP"},
		{Name: "_is_current", Type: "BOOLEAN"},
	}
	seed := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBSeed}
	scd2 := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery, Materialization: pipeline.Materialization{Strategy: pipeline.MaterializationStrategySCD2ByTime}}
	native := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}

	assert.Equal(t, []string{"id", "_is_current"}, runtimeColumnNames(withoutRuntimeManagedColumns(seed, columns)))
	assert.Equal(t, []string{"id", "_sling_loaded_at"}, runtimeColumnNames(withoutRuntimeManagedColumns(scd2, columns)))
	assert.Equal(t, []string{"id", "_sling_loaded_at", "_is_current"}, runtimeColumnNames(withoutRuntimeManagedColumns(native, columns)))
}

func mainSchemaWarningTask(t *testing.T, pl *pipeline.Pipeline, asset *pipeline.Asset) scheduler.TaskInstance {
	t.Helper()
	for _, instance := range scheduler.NewScheduler(zap.NewNop().Sugar(), pl, "schema-warning-test").GetTaskInstances() {
		if instance.GetAsset() == asset && instance.GetType() == scheduler.TaskInstanceTypeMain {
			return instance
		}
	}
	t.Fatalf("main task was not created for %s", asset.Name)
	return nil
}

func runtimeColumnNames(columns []WorkspaceColumn) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, strings.TrimSpace(column.Name))
	}
	return names
}
