package notebook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/sqlintelligence"
)

// renameFixture builds a five-cell DAG that all reference `base`, used by
// the invariant test.
func renameFixture(t *testing.T) (*Notebook, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "revenue")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000001\nblocks:\n  - cell: base0001\n  - cell: ref00001\n  - cell: ref00002\n  - cell: ref00003\n  - cell: ref00004\n  - cell: ref00005\n",
		"base.sql":       "/* @bruin\nid: base0001\ntype: duckdb.sql\n@bruin */\n-- @viz(table)\nselect 1 as id, 'base' as label, 10 as amount\n",
		"a.sql":          "/* @bruin\nid: ref00001\ntype: duckdb.sql\n@bruin */\nselect sum(amount) as total from base\n",
		"b.sql":          "/* @bruin\nid: ref00002\ntype: duckdb.sql\n@bruin */\nselect id, amount from base where label = 'base'\n",
		"c.sql":          "/* @bruin\nid: ref00003\ntype: duckdb.sql\n@bruin */\nselect b.id from base b join base b2 on b.id = b2.id\n",
		"d.sql":          "/* @bruin\nid: ref00004\ntype: duckdb.sql\n@bruin */\nselect '{{ start_date }}' as ds, count(*) from base\n",
		"e.sql":          "/* @bruin\nid: ref00005\ntype: duckdb.sql\n@bruin */\n-- references base in a comment, must not change\nselect 1\n",
	}
	for name, content := range files {
		if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mustLoad(t, dir), dir
}

func mustLoad(t *testing.T, dir string) *Notebook {
	t.Helper()
	fs := afero.NewOsFs()
	loader := NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), func(sql, assetType string) ([]string, error) {
		return sqlintelligence.UsedTables(sql, "duckdb")
	})
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return nb
}

// TestRenameChangesNoFingerprint is the permanent guard for invariant 1.
func TestRenameChangesNoFingerprint(t *testing.T) {
	nb, dir := renameFixture(t)

	before := NotebookFingerprints(nb)

	edits, err := PlanRename(nb, "base0001", "revenue")
	if err != nil {
		t.Fatal(err)
	}
	applyEdits(t, edits)

	after := NotebookFingerprints(mustLoad(t, dir))

	if len(before) != len(after) {
		t.Fatalf("cell count changed: %d → %d", len(before), len(after))
	}
	for id, fp := range before {
		if after[id] != fp {
			t.Fatalf("fingerprint changed for cell %s after rename: %s → %s", id, fp, after[id])
		}
	}
}

func TestRenameRewritesReferencesNotStringsOrComments(t *testing.T) {
	nb, dir := renameFixture(t)

	edits, err := PlanRename(nb, "base0001", "revenue")
	if err != nil {
		t.Fatal(err)
	}
	applyEdits(t, edits)

	reloaded := mustLoad(t, dir)

	// b referenced base and a string literal 'base'; only the table ref
	// should change, and the original lowercase `from` keyword is preserved
	// (span splice, not a re-print).
	b := reloaded.CellByName("b")
	if b == nil {
		t.Fatal("cell b missing")
	}
	if !strings.Contains(b.Asset.ExecutableFile.Content, "from revenue") {
		t.Fatalf("table reference not rewritten (or formatting lost): %q", b.Asset.ExecutableFile.Content)
	}
	if !strings.Contains(b.Asset.ExecutableFile.Content, "'base'") {
		t.Fatalf("string literal 'base' must not be rewritten: %q", b.Asset.ExecutableFile.Content)
	}

	// e only mentions base in a comment, so its file is untouched.
	e := reloaded.CellByName("e")
	if e == nil || !strings.Contains(e.Asset.ExecutableFile.Content, "references base in a comment") {
		t.Fatalf("comment mention should be preserved: %+v", e)
	}

	// The renamed file exists, the old one does not.
	if _, err := os.Stat(filepath.Join(dir, "revenue.sql")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "base.sql")); !os.IsNotExist(err) {
		t.Fatal("old file still present after rename")
	}
}

func TestValidateCellName(t *testing.T) {
	nb, _ := renameFixture(t)
	pipelineNames := map[string]bool{"marts.orders": true, "orders": true}

	cases := []struct {
		name    string
		wantErr bool
	}{
		{"revenue", false},
		{"a", true},        // collides with sibling
		{"orders", true},   // collides with pipeline asset
		{"select", true},   // reserved
		{"bad-name", true}, // charset
		{"base", false},    // its own name (excluded)
	}
	for _, testCase := range cases {
		msg := ValidateCellName(nb, testCase.name, "base0001", pipelineNames)
		if testCase.wantErr && msg == "" {
			t.Errorf("%q: expected error, got none", testCase.name)
		}
		if !testCase.wantErr && msg != "" {
			t.Errorf("%q: unexpected error %q", testCase.name, msg)
		}
	}
}

func applyEdits(t *testing.T, edits []RenameEdit) {
	t.Helper()
	for _, edit := range edits {
		if edit.NewContent != "" {
			if err := os.WriteFile(edit.Path, []byte(edit.NewContent), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if edit.NewPath != "" {
			if err := os.Rename(edit.Path, edit.NewPath); err != nil {
				t.Fatal(err)
			}
		}
	}
}
