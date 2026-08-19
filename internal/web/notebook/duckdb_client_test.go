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
	defer client.close()

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

	// Cancellation is statement-scoped: the persistent session connection must
	// remain usable for the next run.
	next, err := client.query(t.Context(), "select 1 as value")
	if err != nil {
		t.Fatalf("query after cancellation failed: %v", err)
	}
	if got := fmt.Sprint(next.Rows); got != "[[1]]" {
		t.Fatalf("query after cancellation returned %s", got)
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
	defer client.close()

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
	defer client.close()
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

func TestNotebookDuckDBClientTrustedExecUsesSeparateFilesystemPolicy(t *testing.T) {
	root := t.TempDir()
	client, err := newNotebookDuckDBClient(t.Context(), filepath.Join(root, "session.duckdb"), root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()

	outputPath := filepath.Join(root, "trusted.parquet")
	statement := fmt.Sprintf("copy (select 1 as value) to '%s' (format parquet)", strings.ReplaceAll(outputPath, "'", "''"))
	if err := client.trustedExec(t.Context(), statement); err != nil {
		t.Fatalf("trusted export should bypass authored filesystem policy: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("trusted export did not create output: %v", err)
	}
}
