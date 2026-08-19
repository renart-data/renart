package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"renart/internal/web/notebook"
)

func TestNotebookFileSourceUsesBoundedSharedSlingTransfer(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "events.csv"), []byte("id\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argsPath, _ := installNotebookSourceFakeSling(t, root)
	definition := &notebook.SourceDefinition{
		Version: 1, ID: "source_events", Kind: "file", URI: "data/{{ parameters.filename }}", Format: "csv",
		Snapshot: notebook.SourceSnapshotConfig{Mode: "sample", RowLimit: 3},
	}
	cell := notebookSourceTestCell(t, root, "events", definition)
	nb := &notebook.Notebook{UUID: "notebook-source-test", Cells: []*notebook.Cell{cell}}
	renderer := jinja.NewRendererWithYesterday("renart-notebook", "source-test")
	renderer.SetContextValue("parameters", map[string]any{"filename": "events.csv"})
	executor := &notebookSourceExecutor{
		transfer: &slingNotebookTransferService{workspaceRoot: root, maxBytes: 10 << 20}, renderer: renderer,
	}
	output, err := executor.Execute(context.Background(), notebook.ExecuteBlockInput{
		Notebook: nb, Cell: cell, ParameterValues: map[string]any{"filename": "events.csv"},
	})
	if err != nil {
		t.Fatalf("execute file source: %v", err)
	}
	if output.Cleanup != nil {
		defer output.Cleanup()
	}
	if output.Artifact == nil || !output.Artifact.Sampled || output.Artifact.Complete || output.Artifact.RowCount != 1 {
		t.Fatalf("unexpected file artifact: %+v", output.Artifact)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--limit\n3") || !strings.Contains(string(args), "events.csv") {
		t.Fatalf("file source did not preserve explicit sample/path: %s", args)
	}
}

func TestNotebookHTTPSourceUsesNativeRequestBodyAndStopsAtSampleLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var receivedBody string
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.Path
		buffer, _ := io.ReadAll(request.Body)
		receivedBody = string(buffer)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"items":[{"id":1},{"id":2},{"id":3}]}}`))
	}))
	defer server.Close()
	_, sourceCopy := installNotebookSourceFakeSling(t, root)
	definition := &notebook.SourceDefinition{
		Version: 1, ID: "source_http", Kind: "http",
		Request: notebook.SourceHTTPRequest{
			URL: server.URL + "/{{ parameters.region }}", Method: "POST",
			Body: map[string]any{"active": "{{ parameters.active }}"},
		},
		Response: notebook.SourceHTTPResponse{RecordsPath: "data.items"},
		Snapshot: notebook.SourceSnapshotConfig{Mode: "sample", RowLimit: 2},
	}
	cell := notebookSourceTestCell(t, root, "accounts", definition)
	nb := &notebook.Notebook{UUID: "notebook-source-http", Cells: []*notebook.Cell{cell}}
	renderer := jinja.NewRendererWithYesterday("renart-notebook", "source-test")
	renderer.SetContextValue("parameters", map[string]any{"region": "eu", "active": true})
	executor := &notebookSourceExecutor{
		transfer: &slingNotebookTransferService{workspaceRoot: root, maxBytes: 10 << 20},
		renderer: renderer,
	}
	output, err := executor.Execute(context.Background(), notebook.ExecuteBlockInput{
		Notebook: nb, Cell: cell, ParameterValues: map[string]any{"region": "eu", "active": true},
	})
	if err != nil {
		t.Fatalf("execute HTTP source: %v", err)
	}
	if output.Cleanup != nil {
		defer output.Cleanup()
	}
	if !strings.Contains(receivedBody, `"active":true`) {
		t.Fatalf("HTTP request body did not use native API request construction: %q", receivedBody)
	}
	if receivedPath != "/eu" {
		t.Fatalf("HTTP request URL did not render notebook parameters: %q", receivedPath)
	}
	jsonl, err := os.ReadFile(sourceCopy)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 2 || strings.Contains(string(jsonl), `"id":3`) {
		t.Fatalf("HTTP sample fetched beyond its authored limit: %s", jsonl)
	}
}

func TestNotebookLocalSourceRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("id\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNotebookLocalSourcePath(root, outside); err == nil || !strings.Contains(err.Error(), "inside the workspace") {
		t.Fatalf("workspace escape was not rejected clearly: %v", err)
	}
}

func notebookSourceTestCell(t *testing.T, root, name string, definition *notebook.SourceDefinition) *notebook.Cell {
	t.Helper()
	content, err := notebook.MarshalSourceDefinition(*definition)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name+".source.yml")
	return &notebook.Cell{
		ID: definition.ID, Path: path, Raw: string(content), Source: definition,
		Asset: &pipeline.Asset{
			Name: name, Type: notebook.SourceCellType(definition.Kind), Connection: definition.Connection,
			ExecutableFile: pipeline.ExecutableFile{Path: path, Content: string(content)},
		},
	}
}

func installNotebookSourceFakeSling(t *testing.T, root string) (argsPath, sourceCopy string) {
	t.Helper()
	fixture := filepath.Join(root, "fixture.parquet")
	client, err := duck.NewClient(duck.Config{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	copySQL := fmt.Sprintf("copy (select 1::bigint as id) to '%s' (format parquet)", strings.ReplaceAll(fixture, "'", "''"))
	if err := client.RunQueryWithoutResult(context.Background(), &query.Query{Query: copySQL}); err != nil {
		client.Close()
		t.Fatal(err)
	}
	client.Close()

	argsPath = filepath.Join(root, "sling-args.txt")
	sourceCopy = filepath.Join(root, "source-copy.jsonl")
	fakeSling := filepath.Join(root, "fake-sling")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$RENART_TEST_SLING_ARGS"
target=""
source=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--tgt-object" ]; then shift; target="$1"; fi
  if [ "$1" = "--src-stream" ]; then shift; source="$1"; fi
  shift
done
target="${target#file://}"
source="${source#file://}"
if [ -n "$RENART_TEST_SOURCE_COPY" ] && [ -f "$source" ]; then cp "$source" "$RENART_TEST_SOURCE_COPY"; fi
cp "$RENART_TEST_PARQUET" "$target"
`
	if err := os.WriteFile(fakeSling, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RENART_SLING_BINARY", fakeSling)
	t.Setenv("RENART_TEST_SLING_ARGS", argsPath)
	t.Setenv("RENART_TEST_SOURCE_COPY", sourceCopy)
	t.Setenv("RENART_TEST_PARQUET", fixture)
	return argsPath, sourceCopy
}
