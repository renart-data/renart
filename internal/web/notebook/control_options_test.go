package notebook

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReadCellOptionsUsesBoundedDistinctLocalResult(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	const notebookID = "11111111-0000-0000-0000-000000000301"
	const cellID = "source01"

	session, err := store.Open(notebookID)
	if err != nil {
		t.Fatal(err)
	}
	object := quoteIdent(CellObjectName(cellID))
	if err := session.Exec(context.Background(), "create table "+object+" (code varchar, label varchar)"); err != nil {
		t.Fatal(err)
	}
	if err := session.Exec(context.Background(), "insert into "+object+" values ('us', 'United States'), ('de', 'Germany'), ('us', 'United States'), ('fr', 'France'), (null, 'Unknown')"); err != nil {
		t.Fatal(err)
	}
	session.Close()

	result, err := store.ReadCellOptions(context.Background(), notebookID, cellID, "code", "label", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.TotalRows != 3 || len(result.Rows) != 2 {
		t.Fatalf("unexpected option bounds: %+v", result)
	}
	if len(result.Columns) != 2 || result.Columns[0] != "code" || result.Columns[1] != "label" {
		t.Fatalf("unexpected option columns: %+v", result.Columns)
	}
	if result.Rows[0][0] != "de" || result.Rows[1][0] != "fr" {
		t.Fatalf("options were not distinct and deterministic: %+v", result.Rows)
	}
}

func TestReadCellOptionsDoesNotCreateMissingSession(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	_, err := store.ReadCellOptions(context.Background(), "missing", "source01", "code", "", 10)
	if !errors.Is(err, ErrCellResultUnavailable) {
		t.Fatalf("expected unavailable result, got %v", err)
	}
}
