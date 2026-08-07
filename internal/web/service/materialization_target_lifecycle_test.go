package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type materializationLifecycleConnection struct {
	result  *query.QueryResult
	query   string
	drops   []string
	inspect error
	dropErr error
}

func (c *materializationLifecycleConnection) SelectWithSchema(_ context.Context, q *query.Query) (*query.QueryResult, error) {
	c.query = q.Query
	return c.result, c.inspect
}

func (c *materializationLifecycleConnection) RunQueryWithoutResult(_ context.Context, q *query.Query) error {
	c.drops = append(c.drops, q.Query)
	return c.dropErr
}

type materializationLifecycleManager struct {
	connection     any
	connectionType string
	details        any
}

func (m *materializationLifecycleManager) GetConnection(string) any { return m.connection }
func (m *materializationLifecycleManager) GetConnectionDetails(string) any {
	return m.details
}
func (m *materializationLifecycleManager) GetConnectionType(string) string {
	return m.connectionType
}

type materializationLifecycleOperator struct{ calls int }

func (o *materializationLifecycleOperator) Run(context.Context, scheduler.TaskInstance) error {
	o.calls++
	return nil
}

func TestRunDirectTaskBootstrapsPositivelyAbsentIncrementalTarget(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeTable, Strategy: pipeline.MaterializationStrategyMerge,
	})
	configured := &materializationLifecycleOperator{}
	full := &materializationLifecycleOperator{}
	configuredSeq := materializationLifecycleSequence(asset.Type, configured)
	fullSeq := materializationLifecycleSequence(asset.Type, full)
	connection := &materializationLifecycleConnection{result: &query.QueryResult{Rows: [][]interface{}{}}}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "postgres",
		details: config.PostgresConnection{Database: "warehouse", Schema: "public"},
	}
	var output bytes.Buffer

	err := (&HybridBruinExecutor{}).runDirectTask(
		context.Background(), pl, instance, nil, manager, configuredSeq, fullSeq,
		&streamCaptureWriter{buffer: &output},
	)

	require.NoError(t, err)
	assert.Zero(t, configured.calls)
	assert.Equal(t, 1, full.calls)
	assert.Contains(t, output.String(), "initializing it with Bruin's full-refresh materializer")
	assert.Contains(t, connection.query, "pg_catalog.pg_class")
	assert.Contains(t, connection.query, "current_database() = 'warehouse'")
}

func TestRunDirectTaskBlocksAbsentRefreshRestrictedIncrementalTarget(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeTable, Strategy: pipeline.MaterializationStrategyAppend,
	})
	restricted := true
	asset.RefreshRestricted = &restricted
	configured := &materializationLifecycleOperator{}
	connection := &materializationLifecycleConnection{result: &query.QueryResult{Rows: [][]interface{}{}}}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "postgres",
		details: config.PostgresConnection{Database: "warehouse"},
	}

	err := (&HybridBruinExecutor{}).runDirectTask(
		context.Background(), pl, instance, nil, manager,
		materializationLifecycleSequence(asset.Type, configured),
		materializationLifecycleSequence(asset.Type, &materializationLifecycleOperator{}),
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh_restricted")
	assert.Zero(t, configured.calls)
}

func TestRunDirectTaskKeepsConfiguredStrategyWhenTargetStateIsUnknown(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeTable, Strategy: pipeline.MaterializationStrategyTruncateInsert,
	})
	configured := &materializationLifecycleOperator{}
	full := &materializationLifecycleOperator{}
	connection := &materializationLifecycleConnection{inspect: errors.New("catalog permission denied")}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "postgres",
		details: config.PostgresConnection{Database: "warehouse"},
	}
	ctx, warnings := withExecutionWarnings(context.Background())

	err := (&HybridBruinExecutor{}).runDirectTask(
		ctx, pl, instance, nil, manager,
		materializationLifecycleSequence(asset.Type, configured),
		materializationLifecycleSequence(asset.Type, full),
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.NoError(t, err)
	assert.Equal(t, 1, configured.calls)
	assert.Zero(t, full.calls)
	require.Len(t, warnings.snapshot(), 1)
	assert.Contains(t, warnings.snapshot()[0], "kept the configured materialization strategy")
}

func TestRunDirectTaskRequiresFullRefreshForOppositeTargetKind(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeView,
	})
	configured := &materializationLifecycleOperator{}
	connection := &materializationLifecycleConnection{result: &query.QueryResult{Rows: [][]interface{}{{"r"}}}}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "postgres",
		details: config.PostgresConnection{Database: "warehouse"},
	}

	err := (&HybridBruinExecutor{}).runDirectTask(
		context.Background(), pl, instance, nil, manager,
		materializationLifecycleSequence(asset.Type, configured),
		materializationLifecycleSequence(asset.Type, &materializationLifecycleOperator{}),
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run a full refresh")
	assert.Zero(t, configured.calls)
	assert.Empty(t, connection.drops)
}

func TestRunDirectTaskDropsOppositePostgresKindOnlyDuringFullRefresh(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeView,
	})
	full := &materializationLifecycleOperator{}
	connection := &materializationLifecycleConnection{result: &query.QueryResult{Rows: [][]interface{}{{"r"}}}}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "postgres",
		details: config.PostgresConnection{Database: "warehouse"},
	}
	ctx := context.WithValue(context.Background(), pipeline.RunConfigFullRefresh, true)

	err := (&HybridBruinExecutor{}).runDirectTask(
		ctx, pl, instance, nil, manager,
		materializationLifecycleSequence(asset.Type, full),
		materializationLifecycleSequence(asset.Type, full),
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.NoError(t, err)
	assert.Equal(t, 1, full.calls)
	assert.Equal(t, []string{`DROP TABLE "public"."events"`}, connection.drops)
}

func TestRunDirectTaskLeavesSnowflakeKindReplacementToBruin(t *testing.T) {
	t.Parallel()

	asset, pl, instance := materializationLifecycleTask(t, pipeline.Materialization{
		Type: pipeline.MaterializationTypeTable,
	})
	asset.Name = "ANALYTICS.PUBLIC.EVENTS"
	full := &materializationLifecycleOperator{}
	connection := &materializationLifecycleConnection{result: &query.QueryResult{Rows: [][]interface{}{{"VIEW"}}}}
	manager := &materializationLifecycleManager{
		connection: connection, connectionType: "snowflake",
		details: config.SnowflakeConnection{Database: "ANALYTICS", Schema: "PUBLIC"},
	}
	ctx := context.WithValue(context.Background(), pipeline.RunConfigFullRefresh, true)

	err := (&HybridBruinExecutor{}).runDirectTask(
		ctx, pl, instance, nil, manager,
		materializationLifecycleSequence(asset.Type, full),
		materializationLifecycleSequence(asset.Type, full),
		&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
	)

	require.NoError(t, err)
	assert.Equal(t, 1, full.calls)
	assert.Empty(t, connection.drops)
	assert.Contains(t, connection.query, `FROM "ANALYTICS".INFORMATION_SCHEMA.TABLES`)
}

func TestMaterializationTargetInspectionSQLIsTargetedByAdapter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		connectionType string
		lookup         materializationTargetLookup
		contains       []string
	}{
		{
			name: "duckdb", connectionType: "duckdb",
			lookup:   materializationTargetLookup{schema: "main", table: "events"},
			contains: []string{"information_schema.tables", "table_catalog = current_database()", "table_schema = 'main'"},
		},
		{
			name: "postgres", connectionType: "postgres",
			lookup:   materializationTargetLookup{catalog: "warehouse", schema: "public", table: "events"},
			contains: []string{"pg_catalog.pg_class", "current_database() = 'warehouse'", "c.relname = 'events'"},
		},
		{
			name: "snowflake", connectionType: "snowflake",
			lookup:   materializationTargetLookup{catalog: "WAREHOUSE", schema: "PUBLIC", table: "EVENTS"},
			contains: []string{`"WAREHOUSE".INFORMATION_SCHEMA.TABLES`, "table_schema = 'PUBLIC'", "table_name = 'EVENTS'"},
		},
		{
			name: "bigquery", connectionType: "google_cloud_platform",
			lookup:   materializationTargetLookup{catalog: "project", schema: "analytics", table: "events"},
			contains: []string{"`project.analytics.INFORMATION_SCHEMA.TABLES`", "table_name = 'events'"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sql, err := materializationTargetInspectionSQL(test.connectionType, test.lookup)
			require.NoError(t, err)
			for _, fragment := range test.contains {
				assert.Contains(t, sql, fragment)
			}
		})
	}
}

func TestRenderMaterializationTargetLifecycleStageDescribesConditionalBootstrap(t *testing.T) {
	t.Parallel()

	stage, ok := renderMaterializationTargetLifecycleStage(&pipeline.Asset{
		Name: "analytics.events", Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{
			Type: pipeline.MaterializationTypeTable, Strategy: pipeline.MaterializationStrategyMerge,
		},
	}, "dev_analytics.events", false)

	require.True(t, ok)
	assert.Equal(t, "condition", stage.Kind)
	assert.True(t, stage.Conditional)
	assert.Equal(t, AssetRenderFidelitySemantic, stage.Fidelity)
	assert.Contains(t, stage.Content, `"absent_target": "initialize_with_full_refresh_materializer"`)
	assert.Contains(t, stage.Content, `"target": "dev_analytics.events"`)
}

func materializationLifecycleTask(
	t *testing.T,
	materialization pipeline.Materialization,
) (*pipeline.Asset, *pipeline.Pipeline, scheduler.TaskInstance) {
	t.Helper()
	asset := &pipeline.Asset{
		Name: "public.events", Type: pipeline.AssetTypePostgresQuery,
		Connection: "warehouse", Materialization: materialization,
	}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	s := scheduler.NewScheduler(zap.NewNop().Sugar(), pl, "lifecycle-test")
	s.MarkAll(scheduler.Skipped)
	require.True(t, s.MarkAsset(asset, scheduler.Pending, false))
	for _, instance := range s.GetTaskInstancesByStatus(scheduler.Pending) {
		if instance.GetType() == scheduler.TaskInstanceTypeMain {
			return asset, pl, instance
		}
	}
	t.Fatal("main task instance not found")
	return nil, nil, nil
}

func materializationLifecycleSequence(
	assetType pipeline.AssetType,
	operator bruinexecutor.Operator,
) *bruinexecutor.Sequential {
	return &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{
		assetType: {scheduler.TaskInstanceTypeMain: operator},
	}}
}
