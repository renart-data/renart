package notebook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportCellWritesCurrentResultAsCSVAndParquet(t *testing.T) {
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000099\nblocks:\n  - cell: export01\n",
		"summary.sql":    "/* @bruin\nid: export01\ntype: duckdb.sql\n@bruin */\nselect cast(42 as bigint) as answer, 'ready' as status\n",
	})
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"), t.TempDir())
	runner := &Runner{Store: store, RenameTables: realRenameTables(t)}
	results, err := runner.RunCells(context.Background(), nb, nb.Cells, RunOptions{})
	if err != nil || len(results) != 1 || results[0].Status != CellRunOK {
		t.Fatalf("run failed: results=%+v err=%v", results, err)
	}

	csvPath := filepath.Join(t.TempDir(), "result.csv")
	if err := store.ExportCell(context.Background(), nb, "export01", CellExportCSV, csvPath, nil); err != nil {
		t.Fatalf("csv export failed: %v", err)
	}
	csv, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(csv); !strings.Contains(text, "answer,status") || !strings.Contains(text, "42,ready") {
		t.Fatalf("unexpected csv export: %q", text)
	}

	parquetPath := filepath.Join(t.TempDir(), "result.parquet")
	if err := store.ExportCell(context.Background(), nb, "export01", CellExportParquet, parquetPath, nil); err != nil {
		t.Fatalf("parquet export failed: %v", err)
	}
	parquet, err := os.ReadFile(parquetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(parquet) < 8 || string(parquet[:4]) != "PAR1" || string(parquet[len(parquet)-4:]) != "PAR1" {
		t.Fatalf("not a parquet file: %x", parquet)
	}

	nb.Cells[0].Asset.ExecutableFile.Content = strings.ReplaceAll(
		nb.Cells[0].Asset.ExecutableFile.Content, "42", "43",
	)
	if err := store.ExportCell(context.Background(), nb, "export01", CellExportCSV, filepath.Join(t.TempDir(), "stale.csv"), nil); !errors.Is(err, ErrCellResultStale) {
		t.Fatalf("expected stale result error, got %v", err)
	}
}
