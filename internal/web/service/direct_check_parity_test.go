package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

type directCheckParityConnection struct {
	query string
}

func (c *directCheckParityConnection) Select(_ context.Context, q *query.Query) ([][]interface{}, error) {
	if q != nil {
		c.query = q.Query
	}
	return [][]interface{}{{int64(0)}}, nil
}

type directCheckParityManager struct {
	connection      *directCheckParityConnection
	connectionTypes map[string]string
	requested       []string
}

func (m *directCheckParityManager) GetConnection(name string) any {
	m.requested = append(m.requested, name)
	return m.connection
}

func (m *directCheckParityManager) GetConnectionDetails(string) any { return nil }

func (m *directCheckParityManager) GetConnectionType(name string) string {
	return m.connectionTypes[name]
}

func parityCheckedAsset(name string, assetType pipeline.AssetType) *pipeline.Asset {
	values := []int{1, 2}
	return &pipeline.Asset{
		Name: name,
		Type: assetType,
		Columns: []pipeline.Column{{
			Name: "id",
			Checks: []pipeline.ColumnCheck{{
				Name:  "accepted_values",
				Value: pipeline.ColumnCheckValue{IntArray: &values},
			}},
		}},
	}
}

func parityTaskInstance(t *testing.T, pl *pipeline.Pipeline, asset *pipeline.Asset, taskType scheduler.TaskInstanceType) scheduler.TaskInstance {
	t.Helper()
	for _, instance := range scheduler.NewScheduler(zap.NewNop().Sugar(), pl, "check-parity-test").GetTaskInstances() {
		if instance.GetAsset() == asset && instance.GetType() == taskType {
			return instance
		}
	}
	t.Fatalf("task instance %s was not created for %s", taskType, asset.Name)
	return nil
}

func TestDestinationAwareChecksUseResolvedWarehouseDialect(t *testing.T) {
	for _, test := range []struct {
		name      string
		assetType pipeline.AssetType
	}{
		{name: "python", assetType: pipeline.AssetTypePython},
		{name: "api", assetType: pipeline.AssetType(apiAssetType)},
		{name: "load", assetType: pipeline.AssetType(loadAssetType)},
	} {
		t.Run(test.name, func(t *testing.T) {
			asset := parityCheckedAsset("analytics.output", test.assetType)
			pl := &pipeline.Pipeline{
				Name:               "analytics",
				DefaultConnections: pipeline.EmptyStringMap{"google_cloud_platform": "warehouse"},
				Assets:             []*pipeline.Asset{asset},
			}
			connection := &directCheckParityConnection{}
			manager := &directCheckParityManager{
				connection:      connection,
				connectionTypes: map[string]string{"warehouse": "google_cloud_platform"},
			}
			executors, err := buildDirectCheckExecutors(manager, nil)
			require.NoError(t, err)

			instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeColumnCheck)
			err = executors[asset.Type][scheduler.TaskInstanceTypeColumnCheck].Run(context.Background(), instance)
			require.NoError(t, err)

			const expected = `SELECT COUNT(*) FROM analytics.output WHERE CAST(id as STRING) NOT IN ("1","2")`
			assert.Equal(t, expected, connection.query)
			assert.Equal(t, expected, instance.(*scheduler.ColumnCheckInstance).ExecutedQuery)
			assert.Equal(t, []string{"warehouse"}, manager.requested)
		})
	}
}

func TestDestinationAwareCustomCheckCopiesRenderedQueryBack(t *testing.T) {
	asset := &pipeline.Asset{
		Name:       "analytics.output",
		Type:       pipeline.AssetType(apiAssetType),
		Connection: "warehouse",
		CustomChecks: []pipeline.CustomCheck{{
			Name:  "row count",
			Value: 0,
			Query: "select count(*) from analytics.output",
		}},
	}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	connection := &directCheckParityConnection{}
	manager := &directCheckParityManager{
		connection:      connection,
		connectionTypes: map[string]string{"warehouse": "duckdb"},
	}
	executors, err := buildDirectCheckExecutors(manager, nil)
	require.NoError(t, err)

	instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeCustomCheck)
	err = executors[asset.Type][scheduler.TaskInstanceTypeCustomCheck].Run(context.Background(), instance)
	require.NoError(t, err)

	const expected = "select count(*) from analytics.output"
	assert.Equal(t, expected, connection.query)
	assert.Equal(t, expected, instance.(*scheduler.CustomCheckInstance).ExecutedQuery)
	assert.Equal(t, []string{"warehouse"}, manager.requested)
}

func TestOracleColumnChecksUseOracleOperator(t *testing.T) {
	asset := parityCheckedAsset("analytics.output", pipeline.AssetTypeOracleQuery)
	asset.Connection = "oracle-warehouse"
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	connection := &directCheckParityConnection{}
	manager := &directCheckParityManager{
		connection:      connection,
		connectionTypes: map[string]string{"oracle-warehouse": "oracle"},
	}
	executors, err := buildDirectCheckExecutors(manager, nil)
	require.NoError(t, err)

	instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeColumnCheck)
	err = executors[asset.Type][scheduler.TaskInstanceTypeColumnCheck].Run(context.Background(), instance)
	require.NoError(t, err)

	const expected = "SELECT COUNT(*) FROM analytics.output WHERE CAST(id as VARCHAR2(4000)) NOT IN ('1','2')"
	assert.Equal(t, expected, connection.query)
	assert.Equal(t, expected, instance.(*scheduler.ColumnCheckInstance).ExecutedQuery)
	assert.Equal(t, []string{"oracle-warehouse"}, manager.requested)
}

func TestDestinationAwareChecksRejectNonQueryableTargetsWithoutExposingConnectionName(t *testing.T) {
	asset := parityCheckedAsset("analytics.export", pipeline.AssetType(loadAssetType))
	asset.Connection = "credential-bearing-output"
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	manager := &directCheckParityManager{
		connection:      &directCheckParityConnection{},
		connectionTypes: map[string]string{"credential-bearing-output": "csv"},
	}
	executors, err := buildDirectCheckExecutors(manager, nil)
	require.NoError(t, err)

	instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeColumnCheck)
	err = executors[asset.Type][scheduler.TaskInstanceTypeColumnCheck].Run(context.Background(), instance)
	require.Error(t, err)
	assert.ErrorIs(t, err, errDirectCheckDestinationUnsupported)
	assert.Contains(t, err.Error(), `destination type "csv"`)
	assert.NotContains(t, err.Error(), "credential-bearing-output")
	assert.Empty(t, manager.requested, "unsupported checks must not resolve or query the connection")
	assert.Empty(t, instance.(*scheduler.ColumnCheckInstance).ExecutedQuery)
}

func TestAssetRenderUsesDestinationTypeForPythonAPIAndLoadChecks(t *testing.T) {
	for _, test := range []struct {
		name    string
		path    string
		content string
		asset   string
	}{
		{
			name:  "python",
			path:  "checked.py",
			asset: "analytics.checked_python",
			content: `
""" @bruin
name: analytics.checked_python
type: python
connection: duckdb-default
columns:
  - name: id
    checks:
      - name: not_null
@bruin """

def materialize():
    return None
`,
		},
		{
			name:  "api",
			path:  "checked_api.asset.yml",
			asset: "analytics.checked_api",
			content: `
name: analytics.checked_api
type: api
connection: duckdb-default
parameters:
  request:
    url: https://example.invalid/items
    method: GET
  response:
    records_path: items
columns:
  - name: id
    checks:
      - name: not_null
`,
		},
		{
			name:  "load",
			path:  "checked_load.asset.yml",
			asset: "analytics.checked_load",
			content: `
name: analytics.checked_load
type: load
connection: duckdb-default
parameters:
  source_connection: duckdb-default
  source_table: raw.items
columns:
  - name: id
    checks:
      - name: not_null
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, root := writeTypeCheckWorkspace(t, `
name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{test.path: test.content})

			result, err := NewAssetRenderService(root).RenderPath(context.Background(), filepath.ToSlash(filepath.Join("analytics/assets", test.path)), AssetRenderRequest{})
			require.NoError(t, err)

			var checkStage *AssetRenderStage
			for i := range result.Stages {
				if result.Stages[i].Kind == "check" {
					checkStage = &result.Stages[i]
					break
				}
			}
			require.NotNil(t, checkStage)
			assert.Equal(t, AssetRenderStageStatusOK, checkStage.Status)
			assert.Equal(t, AssetRenderFidelityExact, checkStage.Fidelity)
			assert.Equal(t, "SELECT count(*) FROM "+test.asset+" WHERE id IS NULL", checkStage.Content)
		})
	}
}

func TestAssetRenderMarksChecksUnsupportedForNonQueryableLoadTarget(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"export.asset.yml": `
name: analytics.export
type: load
connection: output-file
parameters:
  source_connection: duckdb-default
  source_table: raw.items
columns:
  - name: id
    checks:
      - name: not_null
`,
	})
	require.NoError(t, os.WriteFile(filepath.Join(root, ".bruin.yml"), []byte(strings.TrimSpace(`
default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: local.db
      csv:
        - name: output-file
          path: ./exports
`)+"\n"), 0o644))

	result, err := NewAssetRenderService(root).RenderPath(context.Background(), "analytics/assets/export.asset.yml", AssetRenderRequest{})
	require.NoError(t, err)
	assert.Equal(t, AssetRenderStatusPartial, result.Status)

	var checkStage *AssetRenderStage
	for i := range result.Stages {
		if result.Stages[i].Kind == "check" {
			checkStage = &result.Stages[i]
			break
		}
	}
	require.NotNil(t, checkStage)
	assert.Equal(t, AssetRenderStageStatusUnsupported, checkStage.Status)
	assert.Equal(t, AssetRenderFidelityUnsupported, checkStage.Fidelity)
	assert.Contains(t, checkStage.Message, `destination type "csv"`)
	assert.NotContains(t, checkStage.Message, "output-file")
	assert.Empty(t, checkStage.Content)
	require.NotEmpty(t, result.Issues)
	assert.Equal(t, "check_render_unsupported", result.Issues[len(result.Issues)-1].Code)
	assert.Equal(t, "warning", result.Issues[len(result.Issues)-1].Severity)
}

type parityCountingOperator struct {
	calls int
}

func (o *parityCountingOperator) Run(context.Context, scheduler.TaskInstance) error {
	o.calls++
	return nil
}

func TestRunDirectTaskDoesNotRerunAPIOrLoadMainForChecks(t *testing.T) {
	for _, assetType := range []pipeline.AssetType{pipeline.AssetType(apiAssetType), pipeline.AssetType(loadAssetType)} {
		t.Run(string(assetType), func(t *testing.T) {
			asset := parityCheckedAsset("analytics.output", assetType)
			asset.Connection = "warehouse"
			pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
			instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeColumnCheck)
			operator := &parityCountingOperator{}
			seq := &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{
				assetType: {scheduler.TaskInstanceTypeColumnCheck: operator},
			}}
			printer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil)}

			err := (&HybridBruinExecutor{}).runDirectTask(
				context.Background(),
				pl,
				instance,
				nil,
				&stubConnectionManager{},
				seq,
				nil,
				printer,
			)
			require.NoError(t, err)
			assert.Equal(t, 1, operator.calls, "the check adapter must run once without invoking the side-effecting main loader")
		})
	}
}

func TestRunDirectTaskTreatsAPIOrLoadMetadataPushAsExplicitNoOp(t *testing.T) {
	for _, assetType := range []pipeline.AssetType{pipeline.AssetType(apiAssetType), pipeline.AssetType(loadAssetType)} {
		t.Run(string(assetType), func(t *testing.T) {
			asset := &pipeline.Asset{Name: "analytics.output", Type: assetType, Connection: "warehouse"}
			pl := &pipeline.Pipeline{
				Name:         "analytics",
				Assets:       []*pipeline.Asset{asset},
				MetadataPush: pipeline.MetadataPush{Global: true},
			}
			instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeMetadataPush)
			executors, err := buildDirectCheckExecutors(&stubConnectionManager{}, nil)
			require.NoError(t, err)
			_, isNoOp := executors[assetType][scheduler.TaskInstanceTypeMetadataPush].(bruinexecutor.NoOpOperator)
			require.True(t, isNoOp, "metadata behavior must be explicit instead of falling through to the main loader")
			seq := &bruinexecutor.Sequential{TaskTypeMap: executors}

			err = (&HybridBruinExecutor{}).runDirectTask(
				context.Background(),
				pl,
				instance,
				nil,
				&stubConnectionManager{},
				seq,
				nil,
				&streamCaptureWriter{buffer: bytes.NewBuffer(nil)},
			)
			require.NoError(t, err)
			assert.Empty(t, directTaskDuckDBConnectionNames(pl, instance))
		})
	}
}

func TestDirectCheckDuckDBCoordinationUsesOnlyResolvedTarget(t *testing.T) {
	asset := parityCheckedAsset("analytics.output", pipeline.AssetType(loadAssetType))
	asset.Connection = "destination"
	asset.Parameters = pipeline.ParameterMap{"source_connection": "source", "source_table": "raw.items"}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	instance := parityTaskInstance(t, pl, asset, scheduler.TaskInstanceTypeColumnCheck)

	assert.Equal(t, []string{"destination"}, directTaskDuckDBConnectionNames(pl, instance))

	native := &pipeline.Asset{Name: "analytics.native", Type: pipeline.AssetTypeDuckDBQuery, Connection: "duckdb-default"}
	nativePipeline := &pipeline.Pipeline{
		Name:         "analytics",
		Assets:       []*pipeline.Asset{native},
		MetadataPush: pipeline.MetadataPush{Global: true},
	}
	metadata := parityTaskInstance(t, nativePipeline, native, scheduler.TaskInstanceTypeMetadataPush)
	assert.Equal(t, []string{"duckdb-default"}, directTaskDuckDBConnectionNames(nativePipeline, metadata))
}

func TestUnsupportedDirectCheckDestinationSentinelIsStable(t *testing.T) {
	err := unsupportedDirectCheckDestination("csv")
	assert.True(t, errors.Is(err, errDirectCheckDestinationUnsupported))
}
