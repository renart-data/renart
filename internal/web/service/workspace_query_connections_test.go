package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"renart/internal/web/model"
)

func TestWorkspaceQueryConnectionsUsesBackendQueryRuntimeMapping(t *testing.T) {
	t.Parallel()

	connections := workspaceQueryConnections(map[string]string{
		"warehouse-z": "databricks",
		"warehouse-a": "duckdb",
		"api":         "http",
	})

	assert.Equal(t, []model.WorkspaceQueryConnection{
		{Name: "warehouse-a", ConnectionType: "duckdb", AssetType: "duckdb.sql", Dialect: "duckdb"},
		{Name: "warehouse-z", ConnectionType: "databricks", AssetType: "databricks.sql", Dialect: "databricks"},
	}, connections)
}
