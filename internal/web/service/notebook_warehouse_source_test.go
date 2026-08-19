package service

import (
	"context"
	"fmt"
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
target=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--tgt-object" ]; then
    shift
    target="$1"
  fi
  shift
done
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
	sourceStream := argumentAfter(args, "--src-stream")
	parsedSource, err := url.Parse(sourceStream)
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
	connectionBytes, err := os.ReadFile(connectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsBytes), "secret") || !strings.Contains(string(connectionBytes), "postgres") {
		t.Fatalf("connection was not isolated in the Sling environment: args=%q connection=%q", argsBytes, connectionBytes)
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
