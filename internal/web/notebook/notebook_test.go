package notebook

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

// fakeUsedTables matches the word after FROM/JOIN, enough to exercise the
// dependency wiring without the real parser.
func fakeUsedTables(sql, _ string) ([]string, error) {
	tables := make([]string, 0)
	fields := strings.Fields(strings.ToLower(sql))
	for index, field := range fields {
		if (field == "from" || field == "join") && index+1 < len(fields) {
			tables = append(tables, strings.Trim(fields[index+1], "();,"))
		}
	}
	return tables, nil
}

func writeNotebookFixture(t *testing.T, fs afero.Fs, dir string) {
	t.Helper()
	files := map[string]string{
		ManifestFileName: `id: 11111111-2222-3333-4444-555555555555
title: Revenue exploration
blocks:
  - markdown: |-
      ## Revenue
      Quick look at daily revenue.
  - cell: aaaa1111
  - cell: bbbb2222
`,
		"clean_sales.sql": `/* @bruin
id: aaaa1111
class: notebook
type: duckdb.sql
@bruin */

select * from marts.fct_orders where amount > 0
`,
		"by_month.sql": `/* @bruin
id: bbbb2222
class: notebook
type: duckdb.sql
@bruin */

-- @viz(bar, x: month, y: revenue)
select date_trunc('month', day) as month, sum(amount) as revenue
from clean_sales
group by 1
`,
	}
	for name, content := range files {
		if err := afero.WriteFile(fs, filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestLoader(fs afero.Fs) *Loader {
	return NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), fakeUsedTables)
}

func TestDependencyScanFailureDoesNotCreateNotebookProblem(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/draft"
	files := map[string]string{
		ManifestFileName: "id: 11111111-2222-3333-4444-555555555555\nblocks:\n  - cell: aaaa1111\n",
		"draft.sql": `/* @bruin
id: aaaa1111
class: notebook
type: duckdb.sql
@bruin */

select * from range(1, 10, 1) where range %
`,
	}
	for name, content := range files {
		if err := afero.WriteFile(fs, filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	loader := NewLoader(
		fs,
		pipeline.CreateTaskFromFileComments(fs),
		func(string, string) ([]string, error) {
			return nil, errors.New("required keyword: expression missing")
		},
	)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb.Problems) != 0 {
		t.Fatalf("unexpected problems: %v", nb.Problems)
	}
}

func TestLoadAssemblesDAG(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if nb.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("unexpected notebook uuid: %q", nb.UUID)
	}
	if nb.Title != "Revenue exploration" {
		t.Fatalf("unexpected title: %q", nb.Title)
	}
	if len(nb.Problems) != 0 {
		t.Fatalf("unexpected problems: %v", nb.Problems)
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(nb.Cells))
	}
	if len(nb.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(nb.Blocks))
	}
	if nb.Blocks[0].Markdown == "" || nb.Blocks[1].Cell != "aaaa1111" || nb.Blocks[2].Cell != "bbbb2222" {
		t.Fatalf("unexpected block order: %+v", nb.Blocks)
	}

	byMonth := nb.CellByName("by_month")
	if byMonth == nil {
		t.Fatal("by_month cell missing")
	}
	if len(byMonth.Asset.Upstreams) != 1 || byMonth.Asset.Upstreams[0].Value != "clean_sales" {
		t.Fatalf("expected by_month → clean_sales edge, got %+v", byMonth.Asset.Upstreams)
	}

	cleanSales := nb.CellByName("clean_sales")
	if cleanSales == nil {
		t.Fatal("clean_sales cell missing")
	}
	if len(cleanSales.ExternalRefs) != 1 || cleanSales.ExternalRefs[0] != "marts.fct_orders" {
		t.Fatalf("expected external ref to marts.fct_orders, got %v", cleanSales.ExternalRefs)
	}
}

func TestLoadAssignsMissingIDs(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/scratch"
	if err := afero.WriteFile(fs, filepath.Join(dir, ManifestFileName), []byte("title: Scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A cell with frontmatter but no id, and a bare SQL file with none.
	if err := afero.WriteFile(fs, filepath.Join(dir, "with_header.sql"), []byte("/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, filepath.Join(dir, "bare.sql"), []byte("select 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if nb.UUID == "" {
		t.Fatal("expected generated notebook uuid")
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d (problems: %v)", len(nb.Cells), nb.Problems)
	}
	for _, cell := range nb.Cells {
		if cell.ID == "" {
			t.Fatalf("cell %q has no id", cell.Asset.Name)
		}
	}

	// IDs must be persisted: reloading yields the same IDs.
	idsBefore := map[string]string{}
	for _, cell := range nb.Cells {
		idsBefore[cell.Asset.Name] = cell.ID
	}
	reloaded, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range reloaded.Cells {
		if idsBefore[cell.Asset.Name] != cell.ID {
			t.Fatalf("cell %q id changed across reloads: %q → %q", cell.Asset.Name, idsBefore[cell.Asset.Name], cell.ID)
		}
	}

	// The bare file gained a full frontmatter block and still executes the
	// same SQL.
	bare := reloaded.CellByName("bare")
	if bare == nil {
		t.Fatal("bare cell missing after reload")
	}
	if !strings.Contains(bare.Asset.ExecutableFile.Content, "select 2") {
		t.Fatalf("bare cell content lost: %q", bare.Asset.ExecutableFile.Content)
	}
}

func TestLoadPythonCellAndDependencyDetection(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/py"
	if err := afero.WriteFile(fs, filepath.Join(dir, ManifestFileName), []byte("title: Py\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A SQL cell named raw_sales, and a Python cell that reads it by name.
	if err := afero.WriteFile(fs, filepath.Join(dir, "raw_sales.sql"), []byte("/* @bruin\nid: aaaa1111\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect 1 as amount\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pythonCell := `""" @bruin
id: bbbb2222
class: notebook
type: python
materialization:
  type: table
@bruin """

import pandas as pd


def materialize():
    return con.sql("select sum(amount) as total from raw_sales").df()
`
	if err := afero.WriteFile(fs, filepath.Join(dir, "rollup.py"), []byte(pythonCell), 0o644); err != nil {
		t.Fatal(err)
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d (problems: %v)", len(nb.Cells), nb.Problems)
	}

	rollup := nb.CellByName("rollup")
	if rollup == nil {
		t.Fatal("python cell not loaded")
	}
	if !IsPythonCell(rollup) {
		t.Fatalf("rollup should be a python cell, type=%q", rollup.Asset.Type)
	}

	// The reference to raw_sales in the Python source must become an upstream.
	found := false
	for _, upstream := range rollup.Asset.Upstreams {
		if strings.EqualFold(upstream.Value, "raw_sales") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected raw_sales upstream on python cell, got %+v", rollup.Asset.Upstreams)
	}

	// raw_sales is a sibling, so it must not leak into external refs.
	if len(rollup.ExternalRefs) != 0 {
		t.Fatalf("expected no external refs, got %v", rollup.ExternalRefs)
	}
}

func TestManifestRoundTripIsStable(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)

	loader := newTestLoader(fs)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := SaveManifest(fs, nb); err != nil {
		t.Fatal(err)
	}
	first, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}

	nb2, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(fs, nb2); err != nil {
		t.Fatal(err)
	}
	second, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Fatalf("manifest not stable across load/save cycles:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestLegacyManifestLoadsWithoutImplicitUpgrade(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)
	path := filepath.Join(dir, ManifestFileName)
	before, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatal(err)
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if nb.Version != ManifestVersionLegacy {
		t.Fatalf("legacy manifest loaded as version %d", nb.Version)
	}
	if nb.Revision == "" {
		t.Fatal("notebook-wide revision is empty")
	}
	after, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("loading implicitly rewrote the legacy manifest:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestManifestV2LoadsStructuredPresentationBlocks(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/v2"
	manifest := `version: 2
id: 11111111-2222-3333-4444-555555555555
title: Revenue
parameters:
  - id: region
    label: Region
    type: select
    default: eu
    options:
      values:
        - eu
        - us
blocks:
  - markdown:
      id: md_intro
      content: |-
        ## Revenue
        Explore monthly revenue.
  - cell: aaaa1111
  - visualization:
      id: viz_monthly
      source: aaaa1111
      definition:
        version: 1
        type: line
        encoding:
          x:
            field: month
          y:
            - field: revenue
`
	if err := afero.WriteFile(fs, filepath.Join(dir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, filepath.Join(dir, "monthly.sql"), []byte("/* @bruin\nid: aaaa1111\ntype: duckdb.sql\nclass: notebook\n@bruin */\nselect 1 as revenue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if nb.Version != ManifestVersionCurrent || len(nb.Blocks) != 3 {
		t.Fatalf("unexpected v2 notebook: version=%d blocks=%+v", nb.Version, nb.Blocks)
	}
	if len(nb.Parameters) != 1 || nb.Parameters[0].ID != "region" || nb.Parameters[0].Default != "eu" {
		t.Fatalf("typed parameters were not loaded: %+v", nb.Parameters)
	}
	if nb.Blocks[0].ID != "md_intro" || nb.Blocks[0].Markdown == "" {
		t.Fatalf("markdown identity/content lost: %+v", nb.Blocks[0])
	}
	viz := nb.Blocks[2].Visualization
	if nb.Blocks[2].ID != "viz_monthly" || viz == nil || viz.Source != "aaaa1111" || viz.Definition["type"] != "line" {
		t.Fatalf("visualization lost: %+v", nb.Blocks[2])
	}
	if len(nb.Problems) != 0 {
		t.Fatalf("unexpected v2 problems: %v", nb.Problems)
	}

	if err := SaveManifest(fs, nb); err != nil {
		t.Fatal(err)
	}
	first, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(fs, reloaded); err != nil {
		t.Fatal(err)
	}
	second, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("v2 manifest is not stable:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestUpgradeManifestV2IsExplicitAndDeterministic(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)
	loader := newTestLoader(fs)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := UpgradeManifestV2(fs, nb)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || nb.Version != ManifestVersionCurrent || !strings.HasPrefix(nb.Blocks[0].ID, "md_") {
		t.Fatalf("unexpected upgrade result: changed=%v version=%d block=%+v", changed, nb.Version, nb.Blocks[0])
	}
	first, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "version: 2") || !strings.Contains(string(first), "content: |-") {
		t.Fatalf("upgrade did not emit v2 structure:\n%s", first)
	}

	reloaded, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = UpgradeManifestV2(fs, reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("already-upgraded manifest changed again")
	}
	second, err := afero.ReadFile(fs, filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("upgrade is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestSnapshotRevisionCoversManifestCellNamesAndContent(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)
	loader := newTestLoader(fs)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := nb.Revision
	if original == "" {
		t.Fatal("initial revision is empty")
	}

	cell := nb.CellByName("clean_sales")
	content, err := afero.ReadFile(fs, cell.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, cell.Path, append(content, []byte("\n-- changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	changedContent, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changedContent.Revision == original {
		t.Fatal("cell content change did not advance notebook revision")
	}

	newPath := filepath.Join(dir, "renamed.sql")
	if err := fs.Rename(cell.Path, newPath); err != nil {
		t.Fatal(err)
	}
	renamed, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Revision == changedContent.Revision {
		t.Fatal("cell filename change did not advance notebook revision")
	}
}

func TestReconcileAppendsUnreferencedCells(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/revenue"
	writeNotebookFixture(t, fs, dir)
	// A cell on disk that the manifest does not know about.
	if err := afero.WriteFile(fs, filepath.Join(dir, "growth.sql"), []byte("/* @bruin\nid: cccc3333\ntype: duckdb.sql\n@bruin */\nselect * from by_month\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb.Cells) != 3 {
		t.Fatalf("expected 3 cells, got %d", len(nb.Cells))
	}
	last := nb.Blocks[len(nb.Blocks)-1]
	if last.Cell != "cccc3333" {
		t.Fatalf("expected growth appended as last block, got %+v", last)
	}
	growth := nb.CellByName("growth")
	if growth == nil || len(growth.Asset.Upstreams) != 1 || growth.Asset.Upstreams[0].Value != "by_month" {
		t.Fatalf("expected growth → by_month edge, got %+v", growth)
	}
}

func TestValidateFindsCyclesAndUnknownBlockRefs(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/cyclic"
	files := map[string]string{
		ManifestFileName: "id: 99999999-0000-0000-0000-000000000000\nblocks:\n  - cell: a1\n  - cell: b2\n  - cell: missing\n",
		"alpha.sql":      "/* @bruin\nid: a1\ntype: duckdb.sql\n@bruin */\nselect * from beta\n",
		"beta.sql":       "/* @bruin\nid: b2\ntype: duckdb.sql\n@bruin */\nselect * from alpha\n",
	}
	for name, content := range files {
		if err := afero.WriteFile(fs, filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	nb, err := newTestLoader(fs).Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	var hasCycle, hasUnknown bool
	for _, problem := range nb.Problems {
		if strings.Contains(problem, "dependency cycle") {
			hasCycle = true
		}
		if strings.Contains(problem, "unknown cell") {
			hasUnknown = true
		}
	}
	if !hasCycle {
		t.Fatalf("expected a cycle problem, got %v", nb.Problems)
	}
	if !hasUnknown {
		t.Fatalf("expected an unknown-cell problem, got %v", nb.Problems)
	}
}

func TestDiscoverFindsManifests(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, dir := range []string{"/ws/notebooks/a", "/ws/deep/nested/b"} {
		if err := afero.WriteFile(fs, filepath.Join(dir, ManifestFileName), []byte("title: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := afero.WriteFile(fs, "/ws/.hidden/c/notebook.yml", []byte("title: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := Discover(fs, "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 notebooks, got %v", dirs)
	}
}

func TestJinjaSafeSQL(t *testing.T) {
	input := "select '{{ start_date }}', {{ var.x }} from t {% if a %}where 1{% endif %} {# note #}"
	out := JinjaSafeSQL(input)
	if strings.Contains(out, "{{") || strings.Contains(out, "{%") || strings.Contains(out, "{#") {
		t.Fatalf("jinja left in output: %q", out)
	}
}

func TestCellAssetIDUsesCellID(t *testing.T) {
	id := CellAssetID("nb-uuid", "aaaa1111")
	if id != "nb-uuid:aaaa1111" {
		t.Fatalf("unexpected cell asset id: %q", id)
	}
}
