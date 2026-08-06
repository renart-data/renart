package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type directImportSummaryConnection struct {
	summary *ansisql.DBDatabase
}

func (c *directImportSummaryConnection) GetDatabaseSummaryForSchemas(context.Context, []string) (*ansisql.DBDatabase, error) {
	return c.summary, nil
}

func TestDirectDatabaseImportPreviewsColumnsAndRejectsCollisions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pipelineRoot := filepath.Join(root, "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\nschedule: daily\nstart_date: '2024-01-01'\n"), 0o644))

	connection := &directImportSummaryConnection{summary: &ansisql.DBDatabase{
		Name: "warehouse",
		Schemas: []*ansisql.DBSchema{{
			Name: "external",
			Tables: []*ansisql.DBTable{{
				Name: "orders",
				Type: ansisql.DBTableTypeTable,
				Columns: []*ansisql.DBColumn{
					{Name: "order_id", Type: "bigint"},
					{Name: "amount", Type: "decimal"},
				},
			}},
		}},
	}}
	executor := newCompatDirectExecutor(root, "")
	executor.newConnectionManager = func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: connection, connectionType: "postgres"}, nil
	}

	request := ImportDatabaseRequest{
		PipelinePath:   "analytics",
		ConnectionName: "warehouse",
		Schema:         "external",
		Tables:         []string{"external.orders"},
		Environment:    "dev",
		PreviewOnly:    true,
		RejectExisting: true,
	}
	output, err := executor.ImportDatabase(context.Background(), request)
	require.NoError(t, err)
	var preview directImportDatabaseResponse
	require.NoError(t, json.Unmarshal(output, &preview))
	assert.True(t, preview.Preview)
	assert.Equal(t, 1, preview.ImportedTables)
	require.Len(t, preview.Assets, 1)
	assert.Equal(t, "external.orders", preview.Assets[0].Name)
	assert.Equal(t, "analytics/assets/external/orders.asset.yml", preview.Assets[0].Path)
	assert.Equal(t, "pg.source", preview.Assets[0].Type)
	assert.Equal(t, []SQLColumn{{Name: "order_id", Type: "bigint"}, {Name: "amount", Type: "decimal"}}, preview.Assets[0].Columns)
	assetPath := filepath.Join(pipelineRoot, "assets", "external", "orders.asset.yml")
	_, statErr := os.Stat(assetPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "preview must not write the proposed asset")

	request.PreviewOnly = false
	output, err = executor.ImportDatabase(context.Background(), request)
	require.NoError(t, err)
	var imported directImportDatabaseResponse
	require.NoError(t, json.Unmarshal(output, &imported))
	assert.False(t, imported.Preview)
	content, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "name: external.orders")
	assert.Contains(t, string(content), "name: order_id")
	assert.Contains(t, string(content), "type: bigint")

	_, err = executor.ImportDatabase(context.Background(), request)
	require.ErrorContains(t, err, `asset "external.orders" already exists`)
}
