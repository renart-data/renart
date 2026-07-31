package notebook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotebookDuckDBClientCancelsActiveStatement(t *testing.T) {
	client, err := newNotebookDuckDBClient(t.Context(), filepath.Join(t.TempDir(), "session.duckdb"), "", false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, queryErr := client.query(ctx, "select count(*) from range(4000000) t1(x), range(4000000) t2(y) where (x * y) % 7 = 0")
		result <- queryErr
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case queryErr := <-result:
		if queryErr == nil || !strings.Contains(strings.ToLower(queryErr.Error()), "interrupt") {
			t.Fatalf("expected an interrupted-query error, got %v", queryErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DuckDB statement did not stop after context cancellation")
	}
}

func TestNotebookDuckDBClientPreservesResultsAndWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rows.csv"), []byte("id,name\n1,Ada\n2,Grace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newNotebookDuckDBClient(t.Context(), filepath.Join(root, "session.duckdb"), root, false)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.query(t.Context(), `
		select
			count(*) as row_count,
			['Ada', 'Grace']::varchar[] as names,
			{'owner': 'Renart', 'active': true} as metadata,
			12.34::decimal(4, 2) as ratio
		from './rows.csv'
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(result.Columns), "[row_count names metadata ratio]"; got != want {
		t.Fatalf("columns = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(result.ColumnTypes), "[BIGINT LIST STRUCT DECIMAL(4,2)]"; got != want {
		t.Fatalf("column types = %s, want %s", got, want)
	}
	if len(result.Rows) != 1 || fmt.Sprint(result.Rows[0][0]) != "2" {
		t.Fatalf("unexpected rows: %#v", result.Rows)
	}
	if got := fmt.Sprint(result.Rows[0][1]); got != "[Ada Grace]" {
		t.Fatalf("list value = %s", got)
	}
	metadata, ok := result.Rows[0][2].(map[string]any)
	if !ok || metadata["owner"] != "Renart" || metadata["active"] != true {
		t.Fatalf("unexpected struct value: %#v", result.Rows[0][2])
	}
	if got := result.Rows[0][3]; got != 12.34 {
		t.Fatalf("decimal value = %#v, want 12.34", got)
	}
}

func TestNotebookDuckDBClientCanDisableLocalFilesystemAccess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rows.csv"), []byte("id\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newNotebookDuckDBClient(t.Context(), filepath.Join(root, "session.duckdb"), root, true)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.query(t.Context(), `select * from './rows.csv'`); err == nil || !strings.Contains(err.Error(), "LocalFileSystem") {
		t.Fatalf("expected LocalFileSystem policy error, got %v", err)
	}
	result, err := client.query(t.Context(), `select 1 as value`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(result.Rows); got != "[[1]]" {
		t.Fatalf("ordinary query result = %s, want [[1]]", got)
	}
}
