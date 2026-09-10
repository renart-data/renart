package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/service"
)

func TestDataBrowserReferencesSurviveUnrelatedWorkspaceChanges(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".bruin.yml")
	contents := "default_environment: default\nenvironments:\n  default:\n    connections:\n      duckdb:\n        - name: warehouse\n          path: warehouse.duckdb\n"
	require.NoError(t, os.WriteFile(configPath, []byte(contents), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "orders.csv"), []byte("id\n1\n"), 0600))
	server := &webServer{
		configSvc:      service.NewConfigService(root, configPath),
		workspaceCoord: service.NewWorkspaceCoordinator(service.WorkspaceCoordinatorDependencies{}),
	}
	server.workspaceCoord.SetState(service.WorkspaceState{SelectedEnvironment: "default", Revision: 1})
	configureDataBrowserService(server, root)
	before, apiErr := server.dataBrowserSvc.Connections(t.Context(), "default")
	require.Nil(t, apiErr)
	local := before.Connections[len(before.Connections)-1]
	files, apiErr := server.dataBrowserSvc.Children(t.Context(), local.ID, "", "default")
	require.Nil(t, apiErr)
	require.Len(t, files.Nodes, 1)
	for _, selected := range []string{"default", "another-environment"} {
		// Notebook saves, first canvas insertions and selecting an independent
		// execution environment must not invalidate an unchanged source.
		server.workspaceCoord.SetState(service.WorkspaceState{SelectedEnvironment: selected, Revision: 50})
		after, apiErr := server.dataBrowserSvc.Connections(t.Context(), "default")
		require.Nil(t, apiErr)
		require.Equal(t, before, after)
		_, apiErr = server.dataBrowserSvc.NotebookSource(t.Context(), files.Nodes[0].ID, "default")
		require.Nil(t, apiErr)
	}
	// Retargeting the same connection name/type must still fail closed.
	require.NoError(t, os.WriteFile(configPath, []byte(strings.ReplaceAll(contents, "warehouse.duckdb", "other.duckdb")), 0600))
	_, apiErr = server.dataBrowserSvc.Children(t.Context(), local.ID, "", "default")
	require.NotNil(t, apiErr)
	require.Equal(t, "data_browser_revision_stale", apiErr.Code)
}
