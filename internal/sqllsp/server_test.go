package sqllsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadMessageRejectsOversizedContentLength(t *testing.T) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageBytes+1)
	_, err := readMessage(bufio.NewReader(strings.NewReader(header)))
	if err == nil {
		t.Fatal("expected an error for a Content-Length above the limit")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected a size-limit error, got %v", err)
	}
}

func TestServerCompletionOverLSP(t *testing.T) {
	graph := CanonicalGraph{
		Version: 1,
		Relations: []RelationNode{{
			ID:   "relation:orders",
			Name: "orders",
		}},
		Schemas: []SchemaLayer{{
			RelationID: "relation:orders",
			Columns: []ColumnInfo{
				{Name: "order_id"},
				{Name: "total_amount"},
			},
		}},
	}
	server := NewServer(graph)
	input := bytes.Join([][]byte{
		EncodeMessage(mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})),
		EncodeMessage(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"method":  "textDocument/didOpen",
			"params": map[string]any{
				"textDocument": map[string]any{
					"uri":        "file:///query.sql",
					"languageId": "sql",
					"version":    1,
					"text":       "select o.\nfrom orders o",
				},
			},
		})),
		EncodeMessage(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "textDocument/completion",
			"params": map[string]any{
				"textDocument": map[string]any{"uri": "file:///query.sql"},
				"position":     map[string]any{"line": 0, "character": len("select o.")},
			},
		})),
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := server.Serve(ctx, bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "order_id") || !strings.Contains(output.String(), "total_amount") {
		t.Fatalf("expected completion labels in LSP output, got %s", output.String())
	}
}

func TestWorkspaceServerReloadsGraphAfterWatchedFileChange(t *testing.T) {
	root := t.TempDir()
	writeLSPTestFile(t, root, "analytics/pipeline.yml", "name: analytics\n")
	ordersPath := writeLSPTestFile(t, root, "analytics/assets/analytics/orders.sql", `/* @bruin
type: duckdb.sql
@bruin */

select 1 as order_id`)
	graph, err := LoadGraphFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWorkspaceServer(root, graph)

	open := EncodeMessage(mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":        "file:///query.sql",
				"languageId": "sql",
				"version":    1,
				"text":       "select o.\nfrom analytics.orders o",
			},
		},
	}))
	firstCompletion := EncodeMessage(mustJSON(t, completionRequest(1)))
	if err := server.Serve(context.Background(), bytes.NewReader(bytes.Join([][]byte{open, firstCompletion}, nil)), ioDiscard{}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ordersPath, []byte(`/* @bruin
type: duckdb.sql
@bruin */

select 1 as order_id, 2 as customer_id`), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	input := bytes.Join([][]byte{
		EncodeMessage(mustJSON(t, map[string]any{"jsonrpc": "2.0", "method": "workspace/didChangeWatchedFiles", "params": map[string]any{"changes": []any{}}})),
		EncodeMessage(mustJSON(t, completionRequest(2))),
	}, nil)
	if err := server.Serve(context.Background(), bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "customer_id") {
		t.Fatalf("expected reloaded column in output, got %s", output.String())
	}
}

func TestWorkspaceServerBruinDemoProtocol(t *testing.T) {
	root := t.TempDir()
	writeLSPTestFile(t, root, "analytics/pipeline.yml", "name: analytics\n")
	writeLSPTestFile(t, root, "analytics/assets/analytics/orders.sql", `/* @bruin
type: duckdb.sql
@bruin */

select 1 as order_id, 2 as customer_id`)
	writeLSPTestFile(t, root, "analytics/assets/analytics/report.sql", `/* @bruin
type: duckdb.sql
depends:
  - analytics.orders
@bruin */

select o.order_id
from analytics.orders o`)

	output := runWorkspaceProtocol(t, root, "select o.\nfrom analytics.orders o")
	if !strings.Contains(output, "order_id") || !strings.Contains(output, "customer_id") {
		t.Fatalf("expected Bruin demo completions, got %s", output)
	}
}

func TestWorkspaceServerDBTDemoProtocol(t *testing.T) {
	root := t.TempDir()
	writeLSPTestFile(t, root, "dbt_project.yml", "name: demo\nversion: '1.0'\n")
	writeLSPTestFile(t, root, "models/schema.yml", `version: 2
models:
  - name: orders
    columns:
      - name: order_id
        type: integer
      - name: customer_id
        type: integer
`)
	writeLSPTestFile(t, root, "models/orders.sql", "select 1 as order_id, 2 as customer_id")
	writeLSPTestFile(t, root, "models/report.sql", `select o.order_id
from {{ ref("orders") }} o`)

	output := runWorkspaceProtocol(t, root, "select o.\nfrom {{ ref(\"orders\") }} o")
	if !strings.Contains(output, "order_id") || !strings.Contains(output, "customer_id") {
		t.Fatalf("expected dbt demo completions through ref alias, got %s", output)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func completionRequest(id int) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": "file:///query.sql"},
			"position":     map[string]any{"line": 0, "character": len("select o.")},
		},
	}
}

func runWorkspaceProtocol(t *testing.T, root, sql string) string {
	t.Helper()
	graph, err := LoadGraphFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWorkspaceServer(root, graph)
	input := bytes.Join([][]byte{
		EncodeMessage(mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})),
		EncodeMessage(mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"method":  "textDocument/didOpen",
			"params": map[string]any{
				"textDocument": map[string]any{
					"uri":        "file:///query.sql",
					"languageId": "sql",
					"version":    1,
					"text":       sql,
				},
			},
		})),
		EncodeMessage(mustJSON(t, completionRequest(2))),
	}, nil)
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func writeLSPTestFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
