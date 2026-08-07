package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scd2MigrationParityConnection struct {
	operations []string
}

func (c *scd2MigrationParityConnection) RunQueryWithoutResult(_ context.Context, q *query.Query) error {
	c.operations = append(c.operations, "execute:"+q.Query)
	return nil
}

func (c *scd2MigrationParityConnection) Select(_ context.Context, q *query.Query) ([][]interface{}, error) {
	c.operations = append(c.operations, "inspect:"+q.Query)
	return nil, nil
}

func (c *scd2MigrationParityConnection) SelectWithSchema(context.Context, *query.Query) (*query.QueryResult, error) {
	return &query.QueryResult{}, nil
}

func (c *scd2MigrationParityConnection) CreateSchemaIfNotExist(context.Context, *pipeline.Asset) error {
	c.operations = append(c.operations, "schema")
	return nil
}

func (c *scd2MigrationParityConnection) PushColumnDescriptions(context.Context, *pipeline.Asset) error {
	return nil
}

func (c *scd2MigrationParityConnection) BuildTableExistsQuery(string) (string, error) {
	return "SELECT 1", nil
}

func (c *scd2MigrationParityConnection) Ping(context.Context) error { return nil }

func (c *scd2MigrationParityConnection) GetDatabaseSummary(context.Context) (*ansisql.DBDatabase, error) {
	return &ansisql.DBDatabase{}, nil
}

func TestDirectStringSCD2MigrationStagesMatchRuntimeOrderAndGate(t *testing.T) {
	tests := []struct {
		name             string
		assetType        pipeline.AssetType
		connectionFamily string
		operation        string
	}{
		{name: "postgres", assetType: pipeline.AssetTypePostgresQuery, connectionFamily: "postgres", operation: "migrate_postgres_scd2_target"},
		{name: "redshift", assetType: pipeline.AssetTypeRedshiftQuery, connectionFamily: "redshift", operation: "migrate_postgres_scd2_target"},
		{name: "mysql", assetType: pipeline.AssetTypeMySQLQuery, connectionFamily: "mysql", operation: "migrate_mysql_scd2_target"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pipelineYAML := fmt.Sprintf(`
name: analytics
default_connections:
  %s: warehouse-default
`, test.connectionFamily)
			assetSQL := fmt.Sprintf(`
/* @bruin
name: analytics.history
type: %s
materialization:
  type: table
  strategy: scd2_by_column
columns:
  - name: id
    type: INTEGER
    primary_key: true
  - name: label
    type: VARCHAR(50)
@bruin */
select 1 as id, 'first' as label
`, test.assetType)
			_, root := writeTypeCheckWorkspace(t, pipelineYAML, map[string]string{"history.sql": assetSQL})

			result, err := NewAssetRenderService(root).RenderPath(
				context.Background(),
				"analytics/assets/history.sql",
				AssetRenderRequest{},
			)
			require.NoError(t, err)
			assert.Equal(t, []string{"compiled_query", "condition", "schema_preparation", "scd2_migration", "execution_sql"}, renderStageKinds(result.Stages))
			lifecycle := result.Stages[1]
			assert.Equal(t, AssetRenderFidelitySemantic, lifecycle.Fidelity)
			assert.True(t, lifecycle.Conditional)
			assert.Contains(t, lifecycle.Content, `"operation": "inspect_materialization_target"`)
			migration := result.Stages[3]
			assert.Equal(t, AssetRenderFidelityRuntimeOnly, migration.Fidelity)
			assert.True(t, migration.Conditional)
			assert.Contains(t, migration.Content, `"operation": "`+test.operation+`"`)

			connection := &scd2MigrationParityConnection{}
			executor := newCompatDirectExecutor(root, "")
			executor.newConnectionManager = func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
				return &stubConnectionManager{conn: connection}, nil
			}
			_, err = executor.RunAsset(context.Background(), RunAssetRequest{
				AssetPath: filepath.Join(root, "analytics", "assets", "history.sql"),
			}, nil)
			require.NoError(t, err)
			require.Len(t, connection.operations, 3)
			assert.Equal(t, "schema", connection.operations[0])
			assert.True(t, strings.HasPrefix(connection.operations[1], "inspect:"), "migration must inspect the target before execution")
			assert.True(t, strings.HasPrefix(connection.operations[2], "execute:"), "materializer SQL must run after migration")

			fullRefreshResult, err := NewAssetRenderService(root).RenderPath(
				context.Background(),
				"analytics/assets/history.sql",
				AssetRenderRequest{FullRefresh: true},
			)
			require.NoError(t, err)
			assert.NotContains(t, renderStageKinds(fullRefreshResult.Stages), "scd2_migration")

			connection.operations = nil
			_, err = executor.RunAsset(context.Background(), RunAssetRequest{
				AssetPath:   filepath.Join(root, "analytics", "assets", "history.sql"),
				FullRefresh: true,
			}, nil)
			require.NoError(t, err)
			require.Len(t, connection.operations, 2)
			assert.Equal(t, "schema", connection.operations[0])
			assert.True(t, strings.HasPrefix(connection.operations[1], "execute:"), "full refresh skips the incremental migration")
		})
	}
}
