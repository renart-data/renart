package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
	"renart/internal/web/notebook"
)

func TestSlingNotebookTransferUsesSQLFileAndProducesTypedParquet(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, ".bruin.yml")
	writeWorkspaceFile(t, root, ".bruin.yml", `
default_environment: default
environments:
  default:
    connections:
      postgres:
        - name: postgres-other
          host: db.internal
          port: 5432
          database: analytics
          username: renart
          password: secret
`)

	fixture := filepath.Join(root, "fixture.parquet")
	client, err := duck.NewClient(duck.Config{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	copySQL := fmt.Sprintf(
		"copy (select cast(9.99 as decimal(8,2)) as amount, timestamp '2026-08-12 12:00:00' as observed_at) to '%s' (format parquet)",
		strings.ReplaceAll(fixture, "'", "''"),
	)
	if err := client.RunQueryWithoutResult(context.Background(), &query.Query{Query: copySQL}); err != nil {
		client.Close()
		t.Fatal(err)
	}
	client.Close()

	argsPath := filepath.Join(root, "sling-args.txt")
	connectionPath := filepath.Join(root, "sling-connection.txt")
	fakeSling := filepath.Join(root, "fake-sling")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$RENART_TEST_SLING_ARGS"
printf '%s' "$RENART_SLING_SOURCE" > "$RENART_TEST_SLING_CONNECTION"
replication=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--src-stream" ]; then
    printf 'ambiguous SQL source flag was used\n' >&2
    exit 29
  fi
  if [ "$1" = "--replication" ]; then
    shift
    replication="$1"
  fi
  shift
done
target="$(tr ',' '\n' < "$replication" | awk -F'"' '$2 == "object" { print $4; exit }')"
target="${target#file://}"
cp "$RENART_TEST_PARQUET" "$target"
`
	if err := os.WriteFile(fakeSling, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RENART_SLING_BINARY", fakeSling)
	t.Setenv("RENART_TEST_SLING_ARGS", argsPath)
	t.Setenv("RENART_TEST_SLING_CONNECTION", connectionPath)
	t.Setenv("RENART_TEST_PARQUET", fixture)

	transfer := &slingNotebookTransferService{
		workspaceRoot: root,
		configPath:    configPath,
		newConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return loadConnectionManagerWithDetails{
				connection:     "postgresql://renart:secret@db.internal:5432/analytics",
				connectionType: "postgres",
			}, nil
		},
		maxBytes: 10 << 20,
	}
	const sourceQuery = "select amount, observed_at from private.revenue where account_id = 42"
	artifact, err := transfer.Snapshot(context.Background(), notebook.SnapshotRequest{
		NotebookID: "notebook-id", BlockID: "cell-id", Environment: "default",
		Connection: "postgres-other", Query: sourceQuery, Mode: "full",
		DefinitionFingerprint: "nb1:definition",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer artifact.Cleanup()
	if !artifact.Complete || artifact.Sampled || artifact.RowCount != 1 {
		t.Fatalf("unexpected snapshot state: %+v", artifact)
	}
	if len(artifact.Schema) != 2 || !strings.Contains(artifact.Schema[0].Type, "DECIMAL") || artifact.Schema[1].Type != "TIMESTAMP" {
		t.Fatalf("Parquet schema was not preserved: %+v", artifact.Schema)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	if strings.Contains(string(argsBytes), sourceQuery) || strings.Contains(string(argsBytes), "private.revenue") {
		t.Fatalf("source query leaked onto Sling argv: %s", argsBytes)
	}
	replicationPath := argumentAfter(args, "--replication")
	replicationBytes, err := os.ReadFile(replicationPath)
	if err != nil {
		t.Fatalf("read staged Sling replication: %v", err)
	}
	var replication notebookSlingReplication
	if err := json.Unmarshal(replicationBytes, &replication); err != nil {
		t.Fatalf("parse staged Sling replication: %v", err)
	}
	if replication.Source != slingSourceConnectionEnv || replication.Target != "LOCAL" || replication.Defaults.Mode != "full-refresh" {
		t.Fatalf("unexpected Sling replication envelope: %+v", replication)
	}
	stream, ok := replication.Streams["renart_notebook_snapshot"]
	if !ok || stream.TargetOptions.Format != "parquet" {
		t.Fatalf("missing typed notebook snapshot stream: %+v", replication.Streams)
	}
	parsedSource, err := url.Parse(stream.SQL)
	if err != nil {
		t.Fatal(err)
	}
	queryBytes, err := os.ReadFile(parsedSource.Path)
	if err != nil {
		t.Fatalf("read staged SQL file: %v", err)
	}
	if strings.TrimSpace(string(queryBytes)) != sourceQuery {
		t.Fatalf("staged SQL differs from source query: %q", queryBytes)
	}
	if info, err := os.Stat(parsedSource.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("query file permissions are not private: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(replicationPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("replication file permissions are not private: info=%v err=%v", info, err)
	}
	connectionBytes, err := os.ReadFile(connectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsBytes), "secret") || !strings.Contains(string(connectionBytes), "postgres") {
		t.Fatalf("connection was not isolated in the Sling environment: args=%q connection=%q", argsBytes, connectionBytes)
	}
}

func TestSlingNotebookTransferIncludesCommandOutputOnFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeSling := filepath.Join(root, "fake-sling")
	if err := os.WriteFile(fakeSling, []byte("#!/bin/sh\nprintf 'snapshot diagnostic from sling\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RENART_SLING_BINARY", fakeSling)

	transfer := &slingNotebookTransferService{workspaceRoot: root, maxBytes: 10 << 20}
	_, err := transfer.snapshotFromSling(context.Background(), notebook.SnapshotModeFull, notebook.SnapshotProvenance{},
		func(_ context.Context, _ string, _ io.Writer) (notebookSnapshotSource, error) {
			return notebookSnapshotSource{Args: []string{"--src-stream", "file:///tmp/input.csv"}}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "snapshot diagnostic from sling") {
		t.Fatalf("Sling stderr was not preserved in the snapshot error: %v", err)
	}
}

func TestWarehouseNotebookSourceRejectsSideEffectingSQL(t *testing.T) {
	root := t.TempDir()
	service := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	if err := service.validateNotebookSourceQuery("select 1", "pg.sql"); err != nil {
		t.Fatalf("single SELECT rejected: %v", err)
	}
	if err := service.validateNotebookSourceQuery("delete from public.orders", "pg.sql"); err == nil ||
		!strings.Contains(err.Error(), "read-only single SELECT") {
		t.Fatalf("side-effecting source SQL was not rejected clearly: %v", err)
	}
}

func argumentAfter(args []string, flag string) string {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}
