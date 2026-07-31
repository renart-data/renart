package service

import (
	"context"
	"testing"

	"github.com/bruin-data/bruin/pkg/bigquery"
	"github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/postgres"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/bruin-data/bruin/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAssetRenderMetadataPushUsesSchedulerGateAndPostTaskOrder(t *testing.T) {
	t.Parallel()

	assetSQL := `
/* @bruin
name: analytics.report
type: pg.sql
description: Curated report
materialization:
  type: table
columns:
  - name: id
    type: integer
    description: Stable identifier
    checks:
      - name: not_null
@bruin */
select 1 as id
`

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		_, root := writeTypeCheckWorkspace(t, `
name: analytics
default_connections:
  postgres: postgres-default
`, map[string]string{"report.sql": assetSQL})

		result, err := NewAssetRenderService(root).RenderPath(context.Background(), "analytics/assets/report.sql", AssetRenderRequest{})
		require.NoError(t, err)
		assert.Equal(t, -1, renderStageIndex(result.Stages, "metadata_push"))
	})

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()
		_, root := writeTypeCheckWorkspace(t, `
name: analytics
default_connections:
  postgres: postgres-default
metadata_push:
  bigquery: true
`, map[string]string{"report.sql": assetSQL})

		result, err := NewAssetRenderService(root).RenderPath(context.Background(), "analytics/assets/report.sql", AssetRenderRequest{})
		require.NoError(t, err)

		executionIndex := renderStageIndex(result.Stages, "execution_sql")
		checkIndex := renderStageIndex(result.Stages, "check")
		metadataIndex := renderStageIndex(result.Stages, "metadata_push")
		require.NotEqual(t, -1, executionIndex)
		require.NotEqual(t, -1, checkIndex)
		require.NotEqual(t, -1, metadataIndex)
		assert.Less(t, executionIndex, checkIndex)
		assert.Less(t, checkIndex, metadataIndex)

		metadata := result.Stages[metadataIndex]
		assert.Equal(t, "Metadata push · PostgreSQL", metadata.Label)
		assert.Equal(t, AssetRenderStageStatusOK, metadata.Status)
		assert.Equal(t, AssetRenderFidelityRuntimeOnly, metadata.Fidelity)
		assert.True(t, metadata.Conditional)
		assert.Contains(t, metadata.Content, `"operation": "push_metadata"`)
		assert.Contains(t, metadata.Content, `"table_description": "Curated report"`)
		assert.Contains(t, metadata.Content, `"description": "Stable identifier"`)
		assert.NotContains(t, metadata.Content, "postgres-default")
		assert.Equal(t, AssetRenderStatusPartial, result.Status)
	})
}

func TestMetadataPushAndChecksAreSiblingPostTasks(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{
		Name: "analytics.report",
		Type: pipeline.AssetTypePostgresQuery,
		Columns: []pipeline.Column{{
			Name: "id",
			Checks: []pipeline.ColumnCheck{{
				Name: "not_null",
			}},
		}},
	}
	pl := &pipeline.Pipeline{
		Name:         "analytics",
		Assets:       []*pipeline.Asset{asset},
		MetadataPush: pipeline.MetadataPush{Global: true},
	}
	taskGraph := scheduler.NewScheduler(zap.NewNop().Sugar(), pl, assetRenderPreviewRunID)

	var mainTask, checkTask, metadataTask scheduler.TaskInstance
	for _, instance := range taskGraph.GetTaskInstances() {
		switch instance.GetType() {
		case scheduler.TaskInstanceTypeMain:
			mainTask = instance
		case scheduler.TaskInstanceTypeColumnCheck:
			checkTask = instance
		case scheduler.TaskInstanceTypeMetadataPush:
			metadataTask = instance
		}
	}
	require.NotNil(t, mainTask)
	require.NotNil(t, checkTask)
	require.NotNil(t, metadataTask)
	assert.Equal(t, []scheduler.TaskInstance{mainTask}, checkTask.GetUpstream())
	assert.Equal(t, []scheduler.TaskInstance{mainTask}, metadataTask.GetUpstream())
	assert.NotContains(t, checkTask.GetDownstream(), metadataTask)
	assert.NotContains(t, metadataTask.GetDownstream(), checkTask)
	assert.False(t, metadataTask.Blocking())
}

func TestDirectMetadataPushBackendMatchesExecutorRegistry(t *testing.T) {
	t.Parallel()

	postgresTypes := []pipeline.AssetType{
		pipeline.AssetTypePostgresQuery,
		pipeline.AssetTypePostgresSeed,
		pipeline.AssetTypePostgresQuerySensor,
		pipeline.AssetTypePostgresTableSensor,
		pipeline.AssetTypeRedshiftTableSensor,
	}
	bigQueryTypes := []pipeline.AssetType{
		pipeline.AssetTypeBigqueryQuery,
		pipeline.AssetTypeBigquerySeed,
		pipeline.AssetTypeBigqueryQuerySensor,
		pipeline.AssetTypeBigqueryTableSensor,
	}
	snowflakeTypes := []pipeline.AssetType{
		pipeline.AssetTypeSnowflakeQuery,
		pipeline.AssetTypeSnowflakeSeed,
		pipeline.AssetTypeSnowflakeTableSensor,
	}

	executors, err := buildDirectMainExecutors(&stubConnectionManager{}, nil, nil, &pipeline.Pipeline{}, nil, nil, nil, nil, "", false, false, sensorModeOnce)
	require.NoError(t, err)

	for _, assetType := range postgresTypes {
		backend, ok := directMetadataPushBackendForAssetType(assetType)
		assert.True(t, ok, assetType)
		assert.Equal(t, directMetadataPushPostgres, backend, assetType)
		assert.IsType(t, &postgres.MetadataOperator{}, executors[assetType][scheduler.TaskInstanceTypeMetadataPush], assetType)
	}
	for _, assetType := range bigQueryTypes {
		backend, ok := directMetadataPushBackendForAssetType(assetType)
		assert.True(t, ok, assetType)
		assert.Equal(t, directMetadataPushBigQuery, backend, assetType)
		assert.IsType(t, &bigquery.MetadataPushOperator{}, executors[assetType][scheduler.TaskInstanceTypeMetadataPush], assetType)
	}
	for _, assetType := range snowflakeTypes {
		backend, ok := directMetadataPushBackendForAssetType(assetType)
		assert.True(t, ok, assetType)
		assert.Equal(t, directMetadataPushSnowflake, backend, assetType)
		assert.IsType(t, &snowflake.MetadataOperator{}, executors[assetType][scheduler.TaskInstanceTypeMetadataPush], assetType)
	}

	for _, assetType := range []pipeline.AssetType{
		pipeline.AssetTypeDuckDBQuery,
		pipeline.AssetTypeRedshiftQuery,
		pipeline.AssetTypeSnowflakeQuerySensor,
	} {
		_, ok := directMetadataPushBackendForAssetType(assetType)
		assert.False(t, ok, assetType)
		_, isNoOp := executors[assetType][scheduler.TaskInstanceTypeMetadataPush].(executor.NoOpOperator)
		assert.True(t, isNoOp, assetType)
	}
}

func TestAssetRenderMetadataPushClassifiesNoOpErrorsAndUnsupportedTasks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		asset        *pipeline.Asset
		wantStatus   AssetRenderStageStatus
		wantFidelity AssetRenderFidelity
		wantResult   AssetRenderStatus
		wantMessage  string
	}{
		{
			name: "bigquery without authored descriptions is a no-op",
			asset: &pipeline.Asset{
				Name: "analytics.report",
				Type: pipeline.AssetTypeBigqueryQuery,
			},
			wantStatus:   AssetRenderStageStatusOK,
			wantFidelity: AssetRenderFidelitySemantic,
			wantResult:   AssetRenderStatusOK,
			wantMessage:  "successful no-op",
		},
		{
			name: "postgres without metadata fails its non-blocking task",
			asset: &pipeline.Asset{
				Name: "analytics.report",
				Type: pipeline.AssetTypePostgresQuery,
			},
			wantStatus:   AssetRenderStageStatusError,
			wantFidelity: AssetRenderFidelitySemantic,
			wantResult:   AssetRenderStatusPartial,
			wantMessage:  "neither a table description nor declared columns",
		},
		{
			name: "snowflake view is skipped",
			asset: &pipeline.Asset{
				Name: "analytics.report",
				Type: pipeline.AssetTypeSnowflakeQuery,
				Materialization: pipeline.Materialization{
					Type: pipeline.MaterializationTypeView,
				},
			},
			wantStatus:   AssetRenderStageStatusOK,
			wantFidelity: AssetRenderFidelitySemantic,
			wantResult:   AssetRenderStatusOK,
			wantMessage:  "skips views",
		},
		{
			name: "unregistered direct publisher is explicit",
			asset: &pipeline.Asset{
				Name: "analytics.report",
				Type: pipeline.AssetTypeDuckDBQuery,
			},
			wantStatus:   AssetRenderStageStatusUnsupported,
			wantFidelity: AssetRenderFidelityUnsupported,
			wantResult:   AssetRenderStatusPartial,
			wantMessage:  "treats it as a no-op",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pl := &pipeline.Pipeline{
				Name:         "analytics",
				Assets:       []*pipeline.Asset{test.asset},
				MetadataPush: pipeline.MetadataPush{Global: true},
			}
			outcome := renderAssetMetadataPushStages(pl, test.asset)
			require.Len(t, outcome.stages, 1)
			stage := outcome.stages[0]
			assert.Equal(t, test.wantStatus, stage.Status)
			assert.Equal(t, test.wantFidelity, stage.Fidelity)
			assert.Equal(t, test.wantResult, outcome.status)
			assert.Contains(t, stage.Message, test.wantMessage)
		})
	}
}

func renderStageIndex(stages []AssetRenderStage, kind string) int {
	for index, stage := range stages {
		if stage.Kind == kind {
			return index
		}
	}
	return -1
}
