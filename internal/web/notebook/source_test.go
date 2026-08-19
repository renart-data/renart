package notebook

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

func TestLoaderTreatsSourceDefinitionAsTypedNotebookCell(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	dir := "/ws/notebooks/sources"
	files := map[string]string{
		ManifestFileName: `version: 2
id: notebook-source-test
blocks:
  - cell: source_events
`,
		"events.source.yml": `version: 1
id: source_events
kind: http
request:
  url: https://example.test/events
  method: POST
  body:
    after: "{{ start_datetime }}"
response:
  records_path: data.items
snapshot:
  mode: sample
  row_limit: 25
`,
	}
	for name, content := range files {
		if err := afero.WriteFile(filesystem, filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loader := NewLoader(filesystem, pipeline.CreateTaskFromFileComments(filesystem), fakeUsedTables)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb.Problems) != 0 || len(nb.Cells) != 1 {
		t.Fatalf("unexpected loaded notebook: problems=%v cells=%d", nb.Problems, len(nb.Cells))
	}
	cell := nb.Cells[0]
	if !IsSourceCell(cell) || cell.ID != "source_events" || cell.Asset.Name != "events" || cell.Asset.Type != SourceCellTypeHTTP {
		t.Fatalf("unexpected source cell: %+v", cell)
	}
	if cell.Source.Request.Method != "POST" || cell.Source.Response.RecordsPath != "data.items" {
		t.Fatalf("HTTP definition was not preserved: %+v", cell.Source)
	}
	mode, limit, err := SourceSnapshotPolicy(cell)
	if err != nil || mode != SnapshotModeSample || limit != 25 {
		t.Fatalf("unexpected snapshot policy: mode=%q limit=%d err=%v", mode, limit, err)
	}
}

func TestSourceIDAndRenamePreserveCompoundFileSuffix(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	dir := "/ws/notebooks/sources"
	if err := afero.WriteFile(filesystem, filepath.Join(dir, ManifestFileName), []byte("version: 2\nid: notebook-source-test\nblocks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.source.yaml")
	if err := afero.WriteFile(filesystem, path, []byte("kind: file\nuri: data/events.parquet\nsnapshot:\n  mode: full\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, generated, err := EnsureSourceID(filesystem, path)
	if err != nil || !generated || !strings.HasPrefix(id, "source_") {
		t.Fatalf("source id was not generated: id=%q generated=%v err=%v", id, generated, err)
	}
	loader := NewLoader(filesystem, pipeline.CreateTaskFromFileComments(filesystem), fakeUsedTables)
	nb, err := loader.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	edits, err := PlanRename(nb, id, "archived_events")
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || !strings.HasSuffix(edits[0].NewPath, "archived_events.source.yaml") {
		t.Fatalf("source rename lost its compound suffix: %+v", edits)
	}
}

func TestSourceFingerprintIgnoresDisplayName(t *testing.T) {
	definition := &SourceDefinition{
		Version: SourceDefinitionVersionCurrent, ID: "source_events", Kind: SourceKindFile,
		URI: "data/events.parquet", Snapshot: SourceSnapshotConfig{Mode: SnapshotModeFull},
	}
	cell := &Cell{ID: definition.ID, Source: definition, Asset: &pipeline.Asset{Name: "events", Type: SourceCellType(definition.Kind)}}
	nb := &Notebook{Cells: []*Cell{cell}}
	before := CellFingerprint(nb, cell)
	cell.Asset.Name = "renamed_events"
	if after := CellFingerprint(nb, cell); before != after {
		t.Fatalf("source display name changed fingerprint: %q != %q", before, after)
	}
}
