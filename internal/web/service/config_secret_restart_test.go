package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/secretstore"
)

func TestStorageCredentialBindingSurvivesConfigServiceRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ".bruin.yml")
	resolver, provider := newStatefulSecretResolver(t)
	first := NewConfigService(root, path, WithSecretResolver(resolver))
	_, err := first.CreateConnectionAndPersist(t.Context(), UpsertWorkspaceConnectionParams{
		EnvironmentName: "default", Name: "storage", Type: "s3",
		Values: map[string]any{"bucket_name": "fixture"},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"access_key_id":     {Action: "replace", Value: "access-canary"},
			"secret_access_key": {Action: "replace", Value: "secret-canary"},
		},
	})
	require.NoError(t, err)
	projectID := first.ProjectIdentity().ID
	configBefore, err := os.ReadFile(path)
	require.NoError(t, err)
	manifestPath := filepath.Join(root, ".renart", "secrets.yml")
	manifestBefore, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	for _, state := range []struct {
		name   string
		status secretstore.StatusState
		err    error
	}{
		{"available", secretstore.StatusConfigured, nil},
		{"locked", secretstore.StatusPermissionRequired, secretstore.ErrPermissionRequired},
		{"unavailable", secretstore.StatusUnavailable, secretstore.ErrUnavailable},
	} {
		t.Run(state.name, func(t *testing.T) {
			provider.statState, provider.statErr = state.status, state.err
			restarted := NewConfigService(root, path, WithSecretResolver(resolver))
			require.Equal(t, projectID, restarted.ProjectIdentity().ID)
			cfg, _, err := restarted.LoadReadOnly()
			require.NoError(t, err)
			response := restarted.BuildResponse(path, cfg)
			require.Empty(t, response.SecretBindingsError)
			require.Len(t, response.Environments, 1)
			require.Len(t, response.Environments[0].Connections, 1)
			for _, field := range []string{"access_key_id", "secret_access_key"} {
				descriptor := response.Environments[0].Connections[0].SecretFields[field]
				require.Equal(t, "local", descriptor.Provider)
				require.Equal(t, "local:storage/"+field, descriptor.Reference)
				require.Equal(t, string(state.status), descriptor.Status)
			}

			// Recheck in the same process after the store becomes available:
			// neither its identity nor a transient failure may be cached.
			provider.statState, provider.statErr = "", nil
			response = restarted.BuildResponse(path, cfg)
			for _, field := range []string{"access_key_id", "secret_access_key"} {
				descriptor := response.Environments[0].Connections[0].SecretFields[field]
				require.Equal(t, "local", descriptor.Provider)
				require.Equal(t, "configured", descriptor.Status)
			}
			configAfter, err := os.ReadFile(path)
			require.NoError(t, err)
			manifestAfter, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.Equal(t, configBefore, configAfter)
			require.Equal(t, manifestBefore, manifestAfter)
		})
	}

	t.Run("different project root explains missing binding", func(t *testing.T) {
		// A different launch directory can share .bruin.yml while looking up
		// .renart/secrets.yml relative to another project root. Do not guess a
		// credential provider or silently borrow another project's secrets.
		other := NewConfigService(filepath.Join(root, "nested"), path, WithSecretResolver(resolver))
		cfg, _, err := other.LoadReadOnly()
		require.NoError(t, err)
		response := other.BuildResponse(path, cfg)
		descriptor := response.Environments[0].Connections[0].SecretFields["secret_access_key"]
		require.Equal(t, "env", descriptor.Provider)
		require.Contains(t, descriptor.Message, ".renart/secrets.yml")
		require.Contains(t, descriptor.Message, "original project")
	})
}
