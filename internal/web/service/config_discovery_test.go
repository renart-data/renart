package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/policy"
)

func TestConnectionDiscoveryRevisionTracksOnlySelectedConfiguration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".bruin.yml")
	contents := `default_environment: default
environments:
  default:
    connections:
      postgres:
        - name: warehouse
          host: database.internal
          database: warehouse
          username: test
          password: private-canary
  dev:
    connections:
      duckdb:
        - name: warehouse
          path: dev.duckdb
`
	require.NoError(t, os.WriteFile(configPath, []byte(contents), 0600))
	svc := NewConfigService(root, configPath)
	environment, entries, revision, err := svc.ConnectionDiscovery("")
	require.NoError(t, err)
	require.Equal(t, "default", environment)
	require.Equal(t, []ConnectionDiscoveryEntry{{Name: "warehouse", Type: "postgres", AccessMode: policy.ReadWrite}}, entries)
	visible, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(visible), "private-canary")
	require.NotContains(t, string(visible), "database.internal")
	for _, content := range []string{
		contents,
		"# Only formatting changed\n" + contents,
		strings.ReplaceAll(contents, "dev.duckdb", "other-dev.duckdb"),
	} {
		require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))
		_, _, got, err := svc.ConnectionDiscovery("default")
		require.NoError(t, err)
		require.Equal(t, revision, got)
	}
	for _, content := range []string{
		strings.ReplaceAll(contents, "database.internal", "another.internal"),
		strings.ReplaceAll(contents, "private-canary", "rotated-canary"),
		strings.ReplaceAll(contents, "database: warehouse", "database: another"),
	} {
		require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))
		_, _, got, err := svc.ConnectionDiscovery("default")
		require.NoError(t, err)
		require.NotEqual(t, revision, got)
	}
	require.NoError(t, os.WriteFile(configPath, []byte(contents), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".renart"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".renart", "environments.yml"), []byte("environments:\n  default:\n    connections:\n      warehouse:\n        access_mode: read_only\n"), 0600))
	_, entries, policyRevision, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err)
	require.Equal(t, policy.ReadOnly, entries[0].AccessMode)
	require.NotEqual(t, revision, policyRevision)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".renart", "secrets.yml"), []byte("version: 1\nenvironments:\n  default:\n    connections:\n      warehouse:\n        password:\n          symbol: WAREHOUSE_PASSWORD\n          ref: local:warehouse/password\n"), 0600))
	_, _, bindingRevision, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err)
	require.NotEqual(t, policyRevision, bindingRevision)
	_, _, restarted, err := NewConfigService(root, configPath).ConnectionDiscovery("default")
	require.NoError(t, err)
	require.NotEqual(t, bindingRevision, restarted, "do not reuse tokens across server lifetimes")
}

func TestConnectionDiscoveryInheritedConfigDoesNotResolveCredentialFiles(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	configPath := filepath.Join(root, ".bruin.yml")
	contents := `default_environment: default
environments:
  default:
    connections:
      google_cloud_platform:
        - name: bigquery
          project_id: analytics
          service_account_file: /missing/private-account.json
      snowflake:
        - name: snowflake
          account: analytics
          username: test
          private_key_path: /missing/private-key.pem
`
	require.NoError(t, os.WriteFile(configPath, []byte(contents), 0600))
	svc := NewConfigService(child, configPath)
	_, entries, revision, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err, "metadata discovery must not open credential files")
	require.Len(t, entries, 2)
	changed := strings.ReplaceAll(contents, "project_id: analytics", "project_id: another-project")
	require.NoError(t, os.WriteFile(configPath, []byte(changed), 0600))
	_, _, next, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err)
	require.NotEqual(t, revision, next)
	files, err := os.ReadDir(child)
	require.NoError(t, err)
	require.Empty(t, files, "discovery must not create nested config or runtime files")
}

func TestConnectionDiscoveryUsesEnvironmentConfig(t *testing.T) {
	contents := "default_environment: default\nenvironments:\n  default:\n    connections:\n      duckdb:\n        - name: warehouse\n          path: warehouse.duckdb\n"
	t.Setenv("BRUIN_CONFIG_FILE_CONTENT", contents)
	svc := NewConfigService(t.TempDir(), "")
	_, _, before, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err)
	t.Setenv("BRUIN_CONFIG_FILE_CONTENT", strings.ReplaceAll(contents, "warehouse.duckdb", "other.duckdb"))
	_, _, after, err := svc.ConnectionDiscovery("default")
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}
