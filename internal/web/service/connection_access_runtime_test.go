package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"renart/internal/web/policy"
)

func readOnlyRuntimeFixture(t *testing.T) (string, *ResolvedConnectionFactory, *config.Config) {
	t.Helper()
	root := t.TempDir()
	output, initErr := exec.Command("git", "init", "--quiet", root).CombinedOutput()
	require.NoError(t, initErr, "%s", output)
	dbPath := filepath.Join(root, "source.db")
	client, err := duck.NewClient(duck.Config{Path: dbPath})
	require.NoError(t, err)
	require.NoError(t, client.RunQueryWithoutResult(t.Context(), &query.Query{Query: "create table orders as select 42 as id"}))
	client.Close()
	configPath := filepath.Join(root, ".bruin.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf("default_environment: prod\nenvironments:\n  prod:\n    connections:\n      duckdb:\n        - name: source\n          path: %q\n        - name: destination\n          path: %q\n", dbPath, filepath.Join(root, "destination.db"))), 0o600))
	cfg, err := loadSelectedConfig(configPath, "prod")
	require.NoError(t, err)
	return root, NewResolvedConnectionFactory(root, configPath, "test", nil), cfg
}

func setReadOnlyAlias(t *testing.T, root, name string) {
	t.Helper()
	require.NoError(t, policy.Save(filepath.Join(root, ".renart", "environments.yml"), policy.Config{Environments: map[string]policy.EnvironmentPolicy{"prod": {Connections: map[string]policy.ConnectionPolicy{name: {AccessMode: policy.ReadOnly}}}}}))
}

func TestReadOnlyNativeDuckDBAndModeChange(t *testing.T) {
	root, factory, _ := readOnlyRuntimeFixture(t)
	manager, err := factory.NewConnectionManager(t.Context(), "prod")
	require.NoError(t, err)
	_, err = resolveRuntimeConnection(manager, "source")
	require.NoError(t, err)
	setReadOnlyAlias(t, root, "source")
	_, err = resolveRuntimeConnection(manager, "source")
	require.ErrorContains(t, err, "access changed")
	fresh, err := factory.NewConnectionManager(t.Context(), "prod")
	require.NoError(t, err)
	connection, err := resolveRuntimeConnection(fresh, "source")
	require.NoError(t, err)
	rows, err := connection.(directSchemaQuerier).SelectWithSchema(t.Context(), &query.Query{Query: "select id from orders"})
	require.NoError(t, err)
	require.EqualValues(t, 42, rows.Rows[0][0])
	_, err = connection.(directSchemaQuerier).SelectWithSchema(t.Context(), &query.Query{Query: "create table forbidden as select 1"})
	require.Error(t, err, "native driver must remain read-only even below the Renart guard")
	uri, err := loadConnectionURI(fresh, "source")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(uri), &payload))
	require.Equal(t, true, payload["read_only"])
}

func TestReadOnlyNativeChangeAndRemovedAliasInvalidateExistingManager(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		for _, change := range []string{"native restriction", "removed alias"} {
			t.Run(fmt.Sprintf("%s/initialized=%t", change, initialize), func(t *testing.T) {
				root, factory, _ := readOnlyRuntimeFixture(t)
				manager, err := factory.NewConnectionManager(t.Context(), "prod")
				require.NoError(t, err)
				if initialize {
					_, err = resolveRuntimeConnection(manager, "source")
					require.NoError(t, err)
				}
				path := filepath.Join(root, ".bruin.yml")
				contents, err := os.ReadFile(path)
				require.NoError(t, err)
				replacement := "name: source\n          read_only: true"
				if change == "removed alias" {
					replacement = "name: renamed"
				}
				require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(contents), "name: source", replacement, 1)), 0o600))
				_, err = resolveRuntimeConnection(manager, "source")
				require.Error(t, err, "a new operation must load the current connection configuration")
			})
		}
	}
}

func TestReadOnlySQLTaskNeverDelegatesToMaterializingOperator(t *testing.T) {
	root, _, _ := readOnlyRuntimeFixture(t)
	setReadOnlyAlias(t, root, "source")
	asset := &pipeline.Asset{Name: "analytics.orders", Type: pipeline.AssetTypeDuckDBQuery, Connection: "source", ExecutableFile: pipeline.ExecutableFile{Content: "select 42"}, Hooks: pipeline.Hooks{Pre: []pipeline.Hook{{Query: "select 1"}}, Post: []pipeline.Hook{{Query: "select 2"}}}}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	instances := scheduler.NewScheduler(zap.NewNop().Sugar(), pl, "read-only").GetTaskInstancesByStatus(scheduler.Pending)
	require.Len(t, instances, 1)
	connection := &hookParityBatchConnection{}
	seq := &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{asset.Type: {scheduler.TaskInstanceTypeMain: testAssetLoggingOperator{}}}}
	var output bytes.Buffer
	ctx := context.WithValue(t.Context(), config.EnvironmentNameContextKey, "prod")
	ctx = context.WithValue(ctx, pipeline.RunConfigFullRefresh, true)
	err := NewHybridBruinExecutor(root, "", nil, nil).runDirectTask(ctx, pl, instances[0], nil, &stubConnectionManager{conn: connection}, seq, seq, &streamCaptureWriter{buffer: &output})
	require.NoError(t, err)
	require.Equal(t, []string{"select 1", "select 42", "select 2"}, connection.queries)
	require.NotContains(t, output.String(), "operator line one", "no materializing operator or shared write session may run")
}

type forbiddenAccessManager struct{ calls int }

func (m *forbiddenAccessManager) GetConnection(string) any { m.calls++; return nil }

func TestReadOnlyLoadDestinationDeniedBeforeEitherConnectionInitializes(t *testing.T) {
	root, _, cfg := readOnlyRuntimeFixture(t)
	setReadOnlyAlias(t, root, "destination")
	executor := NewHybridBruinExecutor(root, "", nil, nil)
	manager := &forbiddenAccessManager{}
	ctx := context.WithValue(t.Context(), config.EnvironmentNameContextKey, "prod")
	ctx = context.WithValue(ctx, config.EnvironmentContextKey, cfg.SelectedEnvironment)
	asset := &pipeline.Asset{Name: "copied", Type: loadAssetType, Connection: "destination", Parameters: pipeline.ParameterMap{"source_connection": "source", "source_table": "orders"}}
	_, err := executor.runLoadAsset(ctx, &pipeline.Pipeline{}, asset, manager, nil)
	require.ErrorContains(t, err, "load_destination")
	require.Zero(t, manager.calls)
	require.NoFileExists(t, filepath.Join(root, "destination.db"))
}

func TestReadOnlyPolicyIdentityIsScopedAndSeparateFromData(t *testing.T) {
	root, _, cfg := readOnlyRuntimeFixture(t)
	requirements := []policy.Requirement{{Connection: "source", Effect: policy.Read, Operation: "load_source"}}
	before, err := connectionAccessIdentity(root, cfg, requirements)
	require.NoError(t, err)
	setReadOnlyAlias(t, root, "destination")
	unrelated, err := connectionAccessIdentity(root, cfg, requirements)
	require.NoError(t, err)
	require.Equal(t, before, unrelated)
	setReadOnlyAlias(t, root, "source")
	changed, err := connectionAccessIdentity(root, cfg, requirements)
	require.NoError(t, err)
	require.NotEqual(t, before, changed)
}

func TestReadOnlyWholePipelineAdmissionBeforeFirstWrite(t *testing.T) {
	root, _, _ := readOnlyRuntimeFixture(t)
	dir := filepath.Join(root, "analytics", "assets")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "analytics", "pipeline.yml"), []byte("name: analytics\ndefault_connections:\n  duckdb: destination\n"), 0o644))
	for _, asset := range []struct{ name, connection, dependency string }{
		{"first", "destination", ""}, {"second", "source", "depends: [first]\n"},
	} {
		content := fmt.Sprintf("/* @bruin\nname: %s\ntype: duckdb.sql\nconnection: %s\n%smaterialization:\n  type: table\n@bruin */\nselect 1 as id", asset.name, asset.connection, asset.dependency)
		require.NoError(t, os.WriteFile(filepath.Join(dir, asset.name+".sql"), []byte(content), 0o644))
	}
	setReadOnlyAlias(t, root, "source")
	executor := NewHybridBruinExecutor(root, "", nil, nil)
	executor.newPipelineBuilder = newCompatDirectExecutor(root, "").newPipelineBuilder
	_, err := executor.RunPipeline(t.Context(), RunPipelineRequest{Target: "analytics", ConfigPath: filepath.Join(root, ".bruin.yml"), Environment: "prod"}, nil)
	require.ErrorContains(t, err, "read-only")
	require.NoFileExists(t, filepath.Join(root, "destination.db"), "the earlier writable sibling must not run")
}

func TestReadOnlyDraftValidationDoesNotCreateDatabase(t *testing.T) {
	root, _, cfg := readOnlyRuntimeFixture(t)
	svc := NewConfigService(root, filepath.Join(root, ".bruin.yml"))
	ro := policy.ReadOnly
	missing := filepath.Join(root, "not-created.db")
	_, err := svc.TestConnection(t.Context(), cfg, TestWorkspaceConnectionParams{EnvironmentName: "prod", Name: "draft", Type: "duckdb", Values: map[string]any{"path": missing}, AccessMode: &ro})
	// DuckDB currently has no Ping implementation; an unsupported-validation
	// response is also valid, but it must never create the draft database.
	if err != nil {
		require.NotEmpty(t, err.Error())
	}
	require.NoFileExists(t, missing)
	require.NoFileExists(t, filepath.Join(root, ".renart", "environments.yml"))
}

func TestReadOnlyInspectRejectsMutatingSQLDespiteUnknownParse(t *testing.T) {
	root, factory, _ := readOnlyRuntimeFixture(t)
	setReadOnlyAlias(t, root, "source")
	executor := NewHybridBruinExecutor(root, "", factory.NewConnectionManager, nil)
	for _, sql := range []string{"create table forbidden as select 1", "select 1; delete from orders", "not valid sql"} {
		_, err := executor.QueryConnection(t.Context(), QueryConnectionRequest{ConnectionName: "source", Environment: "prod", Query: sql, Output: "json"})
		require.ErrorContains(t, err, "read-only")
	}
	output, err := executor.QueryConnection(t.Context(), QueryConnectionRequest{ConnectionName: "source", Environment: "prod", Query: "select id from orders", Output: "json"})
	require.NoError(t, err)
	require.Contains(t, string(output), "42")
}
