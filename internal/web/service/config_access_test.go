package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/policy"
)

func TestConnectionPolicyLifecycle(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".bruin.yml")
	require.NoError(t, os.WriteFile(configPath, []byte("default_environment: dev\nenvironments:\n  dev:\n    connections: {}\n"), 0o600))
	svc := NewConfigService(root, configPath)
	ro := policy.ReadOnly
	params := UpsertWorkspaceConnectionParams{EnvironmentName: "dev", Name: "source", Type: "duckdb", Values: map[string]any{"path": "source.db"}, AccessMode: &ro}
	change, err := svc.CreateConnectionAndPersist(context.Background(), params)
	require.NoError(t, err)
	response := svc.BuildResponse(change.ConfigPath, change.Config)
	require.Equal(t, policy.ReadOnly, response.Environments[0].Connections[0].EffectiveAccessMode)
	loader := policy.NewLoader(filepath.Join(root, ".renart", "environments.yml"))
	require.Equal(t, ro, loader.For("dev").Connections["source"].AccessMode)
	params.CurrentName, params.Name, params.AccessMode = "source", "renamed", nil
	_, err = svc.UpdateConnectionAndPersist(context.Background(), params)
	require.NoError(t, err)
	require.NotContains(t, loader.For("dev").Connections, "source")
	require.Equal(t, ro, loader.For("dev").Connections["renamed"].AccessMode)
	_, err = svc.CloneEnvironmentAndPersist(context.Background(), "dev", "staging", "", false)
	require.NoError(t, err)
	_, err = svc.UpdateEnvironmentAndPersist(context.Background(), "staging", "prod", "", false)
	require.NoError(t, err)
	require.True(t, loader.For("staging").Zero())
	require.Equal(t, ro, loader.For("prod").Connections["renamed"].AccessMode)
	_, err = svc.DeleteConnectionAndPersist(context.Background(), "dev", "renamed")
	require.NoError(t, err)
	require.NotContains(t, loader.For("dev").Connections, "renamed")
	require.Equal(t, ro, loader.For("prod").Connections["renamed"].AccessMode)
	_, err = svc.DeleteEnvironmentAndPersist(context.Background(), "prod")
	require.NoError(t, err)
	require.True(t, loader.For("prod").Zero())
}

func TestInvalidConnectionModeDoesNotPersistConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".bruin.yml")
	original := []byte("default_environment: dev\nenvironments:\n  dev:\n    connections: {}\n")
	require.NoError(t, os.WriteFile(configPath, original, 0o600))
	svc := NewConfigService(root, configPath)
	invalid := policy.AccessMode("readonly")
	_, err := svc.CreateConnectionAndPersist(context.Background(), UpsertWorkspaceConnectionParams{EnvironmentName: "dev", Name: "source", Type: "duckdb", Values: map[string]any{"path": "source.db"}, AccessMode: &invalid})
	require.Error(t, err)
	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, string(original), string(after))
}

func TestStaleConnectionPolicyUpdateDoesNotPersistConfig(t *testing.T) {
	root, _, _ := readOnlyRuntimeFixture(t)
	svc := NewConfigService(root, filepath.Join(root, ".bruin.yml"))
	snapshot, err := policy.NewLoader(svc.environmentPolicyPath()).Snapshot()
	require.NoError(t, err)
	setReadOnlyAlias(t, root, "source")
	rw := policy.ReadWrite
	_, err = svc.UpdateConnectionAndPersist(t.Context(), UpsertWorkspaceConnectionParams{EnvironmentName: "prod", CurrentName: "source", Name: "source", AccessMode: &rw, PolicyRevision: snapshot.Revision})
	require.ErrorContains(t, err, "access settings changed")
	require.Equal(t, policy.ReadOnly, policy.NewLoader(svc.environmentPolicyPath()).For("prod").Connections["source"].AccessMode)
}
