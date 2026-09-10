package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
	"renart/internal/web/preview"
)

func TestRetainedPreviewDoesNotReevaluateView(t *testing.T) {
	ctx := context.Background()
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000099\nblocks:\n  - cell: preview1\n",
		"sample.sql":     "/* @bruin\nid: preview1\ntype: duckdb.sql\n@bruin */\nselect random() as value, cast(9223372036854775807 as bigint) as large_id from range(250)\n",
	})
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	runner := &Runner{Store: store, RenameTables: realRenameTables(t), Environment: "dev"}
	results, err := runner.RunCells(ctx, nb, nb.Cells, RunOptions{})
	if err != nil || len(results) != 1 || results[0].Status != CellRunOK {
		t.Fatalf("run: %+v %v", results, err)
	}
	first := results[0]
	if len(first.Rows) != 100 || first.Preview == nil || first.Preview.Continuation != "snapshot" {
		t.Fatalf("initial: %+v", first)
	}
	read := func(id, env string, limit int) (CellPreviewResult, error) {
		return store.ReadPreview(ctx, nb.UUID, "preview1", id, env, limit, func() (*Notebook, map[string]any, error) { return nb, nil, nil })
	}
	more, err := read(first.Preview.ResultID, "dev", 200)
	if err != nil || len(more.Rows) != 200 {
		t.Fatalf("more: %+v %v", more, err)
	}
	a, _ := json.Marshal(first.Rows)
	b, _ := json.Marshal(more.Rows[:100])
	if string(a) != string(b) || more.Preview.ResultID != first.Preview.ResultID {
		t.Fatal("preview was re-evaluated")
	}
	all, err := read(first.Preview.ResultID, "dev", 400)
	if err != nil || len(all.Rows) != 250 || all.Preview.HasMore {
		t.Fatalf("exhaustion: %+v %v", all, err)
	}
	if _, err := read(first.Preview.ResultID, "prod", 200); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("environment: %v", err)
	}
	if _, err := runner.RunCells(ctx, nb, nb.Cells, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := read(first.Preview.ResultID, "dev", 200); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("rerun: %v", err)
	}
	if err := store.Remove(nb.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := read(first.Preview.ResultID, "dev", 200); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("reset: %v", err)
	}
}

func TestSavedPythonPreviewDoesNotRunPythonAgain(t *testing.T) {
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000009\nblocks:\n  - cell: python01\n",
		"analysis.py":    "\"\"\" @bruin\nid: python01\nclass: notebook\ntype: python\n@bruin \"\"\"\ndef materialize():\n    return None\n",
	})
	store := NewSessionStore(t.TempDir())
	calls := 0
	runner := &Runner{Store: store, PythonMaterializer: func(ctx context.Context, _ *Cell, path string, _ PythonQueryFunc, _ map[string]any) (PythonMaterializationOutput, error) {
		calls++
		client, err := duck.NewClient(duck.Config{Path: ""})
		if err != nil {
			return PythonMaterializationOutput{}, err
		}
		defer client.Close()
		err = client.RunQueryWithoutResult(ctx, &query.Query{Query: "copy (select range as id from range(250)) to " + sqlStringLiteral(path) + " (format parquet)"})
		return PythonMaterializationOutput{EnvironmentFingerprint: "resolved-after-run"}, err
	}}
	results, err := runner.RunCells(t.Context(), nb, nb.Cells, RunOptions{})
	if err != nil || results[0].Status != CellRunOK {
		t.Fatalf("run: %+v %v", results, err)
	}
	nb.PythonEnvironmentFingerprint = "resolved-after-run"
	more, err := store.ReadPreview(t.Context(), nb.UUID, "python01", results[0].Preview.ResultID, "", 200, func() (*Notebook, map[string]any, error) { return nb, nil, nil })
	if err != nil || len(more.Rows) != 200 || calls != 1 {
		t.Fatalf("preview: %v rows=%d calls=%d", err, len(more.Rows), calls)
	}
}

func TestPreviewBudgetsExpiryAndRestart(t *testing.T) {
	ctx := context.Background()
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000099\nblocks:\n  - cell: preview1\n",
		"sample.sql":     "/* @bruin\nid: preview1\ntype: duckdb.sql\n@bruin */\nselect range as value from range(1500)\n",
	})
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	runner := &Runner{Store: store, RenameTables: realRenameTables(t)}
	results, err := runner.RunCells(ctx, nb, nb.Cells, RunOptions{})
	if err != nil || results[0].Status != CellRunOK {
		t.Fatalf("run: %+v %v", results, err)
	}
	read := func(target *SessionStore) (CellPreviewResult, error) {
		return target.ReadPreview(ctx, nb.UUID, "preview1", results[0].Preview.ResultID, "", 2000, func() (*Notebook, map[string]any, error) { return nb, nil, nil })
	}
	all, err := read(store)
	if err != nil || len(all.Rows) != 1000 || all.Preview.Reason != "row_limit" || all.Preview.Continuation != "none" {
		t.Fatalf("ceiling: %+v %v", all, err)
	}
	if _, err := read(NewSessionStore(store.Root)); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("restart: %v", err)
	}
	session, err := store.Open(nb.UUID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		cell := *nb.Cells[0]
		cell.ID = fmt.Sprintf("saved%d", i)
		result := results[0]
		result.Rows = [][]any{{strings.Repeat("x", preview.MaxRowBytes)}}
		result.TotalRows = 1
		if err := session.retainPreview(ctx, store.previewEpoch, nb, &cell, &result, "", nil, 100); err != nil {
			t.Fatal(err)
		}
		if result.Preview.Reason != "byte_limit" || len(result.Rows) != 0 {
			t.Fatal("wide row not bounded")
		}
	}
	count, err := session.Query(ctx, "select count(*) from __renart_preview_rows")
	if err != nil || toInt64(count.Rows[0][0]) != 4 {
		t.Fatalf("retention: %+v %v", count, err)
	}
	if _, err := session.savedPreview(ctx, "preview1"); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("eviction: %v", err)
	}
	if err := session.Exec(ctx, "update __renart_preview_rows set created_at = now() - interval 31 minute"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.savedPreview(ctx, "saved4"); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("TTL: %v", err)
	}
	session.Close()
}

func TestUpstreamRecomputeAndEditsInvalidateSavedPreview(t *testing.T) {
	ctx := context.Background()
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000001\nblocks:\n  - cell: base0001\n  - cell: next0001\n",
		"base.sql":       "/* @bruin\nid: base0001\nname: base\ntype: duckdb.sql\n@bruin */\nselect range as value from range(250)\n",
		"next.sql":       "/* @bruin\nid: next0001\nname: next\ntype: duckdb.sql\ndepends_on: [base]\n@bruin */\nselect * from base\n",
	})
	store := NewSessionStore(t.TempDir())
	runner := &Runner{Store: store, RenameTables: realRenameTables(t)}
	results, err := runner.RunCells(ctx, nb, nb.Cells, RunOptions{})
	if err != nil || results[1].Status != CellRunOK {
		t.Fatalf("run: %+v %v", results, err)
	}
	read := func() error {
		_, err := store.ReadPreview(ctx, nb.UUID, "next0001", results[1].Preview.ResultID, "", 200, func() (*Notebook, map[string]any, error) { return nb, nil, nil })
		return err
	}
	if err := read(); err != nil {
		t.Fatal(err)
	}
	base := nb.CellByID("base0001")
	old := base.Asset.ExecutableFile.Content
	base.Asset.ExecutableFile.Content = strings.ReplaceAll(old, "250", "260")
	if err := read(); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("upstream edit: %v", err)
	}
	base.Asset.ExecutableFile.Content = old
	if _, err := runner.RunCells(ctx, nb, []*Cell{base}, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := read(); !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("upstream run: %v", err)
	}
}

func TestPreviewWaitIsCancellable(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	session, err := store.Open("locked")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = store.ReadPreview(ctx, "locked", "cell", "id", "", 200, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
}
