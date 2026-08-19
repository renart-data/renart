// Package notebook implements the notebook-as-folder format: a directory
// holding a notebook.yml manifest plus ordinary Bruin asset files, one per
// cell. Cells are class-tagged assets in a notebook namespace; everything
// that parses, fingerprints, or renders assets operates on them unchanged.
//
// See architecture/notebooks.md. Core invariants:
//  1. No logical name ever enters a fingerprint (cell identity is the
//     frontmatter id, not the filename).
//  2. Every asset carries a class (pipeline | notebook); dependency
//     direction pipeline→notebook is an error.
//  3. Presentation (@viz …) lives in comments, outside fingerprints.
//  4. Physical objects are machine-named (nb_<notebook_id>.cell_<cell_id>).
package notebook

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/presentation"
)

// ManifestFileName is the file that marks a directory as a notebook.
const ManifestFileName = "notebook.yml"

// DefaultCellType is assumed for SQL cells that do not declare a type;
// it matches the built-in DuckDB notebook target.
const DefaultCellType = "duckdb.sql"

// PythonCellType is the asset type for Python cells.
const PythonCellType = "python"

// Class values carried by assets.
const (
	ClassPipeline = "pipeline"
	ClassNotebook = "notebook"
)

// Manifest versions. Version 1 is the original flat cell/markdown shape;
// version 2 gives every manifest-owned block durable identity and adds
// structured visualization blocks.
const (
	ManifestVersionLegacy  = 1
	ManifestVersionCurrent = 2
)

// VisualizationBlock is presentation metadata owned by notebook.yml. It is
// deliberately not a pipeline asset and never enters the execution DAG.
// Definition is a Renart-owned, versioned document interpreted and type-checked
// by the presentation package.
type VisualizationBlock struct {
	ID         string
	Source     string
	Definition map[string]any
}

// Block is one ordered entry of a notebook: a cell reference, markdown prose,
// a control reference, or a visualization. Cell identity lives in Cell;
// manifest-owned blocks use ID. Presentation blocks are not assets and have no
// execution fingerprints.
type Block struct {
	// ID is the durable identity of markdown and visualization blocks. It is
	// empty for cell blocks because Cell is already the durable identity.
	ID string
	// Cell is the durable cell ID this block renders (cell blocks only).
	Cell string
	// Markdown is the prose content (markdown blocks only).
	Markdown string
	// Control is the ID of a notebook parameter rendered at this position
	// (control blocks only). The typed definition remains in Parameters so the
	// runtime has one canonical declaration.
	Control string
	// Visualization is the structured presentation definition (visualization
	// blocks only).
	Visualization *VisualizationBlock
}

// StableID returns the identity used by semantic editors and artifact links.
func (b Block) StableID() string {
	if b.Cell != "" {
		return b.Cell
	}
	if b.Control != "" {
		return "control:" + b.Control
	}
	return b.ID
}

// Cell is a loaded notebook cell: an ordinary Bruin asset plus its durable
// identity and the references it makes outside the notebook.
type Cell struct {
	// ID is the durable cell identifier from the frontmatter `id:` key.
	// It survives renames; all physical naming and history hangs off it.
	ID string
	// Asset is the parsed Bruin asset. Name is always the filename stem.
	Asset *pipeline.Asset
	// Path is the absolute path of the cell file.
	Path string
	// Raw is the full on-disk file content (frontmatter + body), kept so
	// the rename engine can splice references without re-printing.
	Raw string
	// Source is non-nil for a Renart-owned .source.yml file. Source components
	// still participate in the notebook DAG and produce a typed local relation,
	// but they are not parsed or presented as SQL/Python assets.
	Source *SourceDefinition
	// ExternalRefs are referenced table names that did not resolve to a
	// sibling cell — pipeline assets or raw warehouse tables, resolved by
	// the session import machinery.
	ExternalRefs []string
}

// Notebook is a loaded notebook folder.
type Notebook struct {
	// Version is the notebook.yml format version. Missing version means the
	// legacy v1 shape; newly created notebooks use ManifestVersionCurrent.
	Version int
	// UUID is the durable notebook identifier from notebook.yml `id:`.
	UUID string
	// Title is the display title (defaults to the folder name).
	Title string
	// Dir is the absolute folder path.
	Dir string
	// Target optionally overrides where sessions materialize
	// (notebook.yml → environment → project default).
	Target string
	// Parameters are Git-tracked typed defaults. Runtime overrides live in the
	// notebook runtime and never rewrite notebook.yml as a side effect of a run.
	Parameters []presentation.ParameterDefinition
	// Blocks is the ordered presentation list (cells + prose).
	Blocks []Block
	// Cells holds the loaded cell assets, ordered by their first
	// appearance in Blocks.
	Cells []*Cell
	// Problems are non-fatal load/validation findings, surfaced in the UI.
	Problems []string
	// Revision identifies the exact authored notebook snapshot: manifest plus
	// every cell file. It is stable across server restarts and is the CAS
	// boundary for semantic multi-block changes.
	Revision string
	// PythonEnvironmentFingerprint identifies the effective dependency inputs
	// shared by Python cells in this flat notebook folder. It is derived while
	// loading from the same requirements/pyproject precedence the runner uses.
	PythonEnvironmentFingerprint string
}

// CellByID returns the cell with the given durable ID, or nil.
func (n *Notebook) CellByID(id string) *Cell {
	for _, cell := range n.Cells {
		if cell.ID == id {
			return cell
		}
	}
	return nil
}

// CellByName returns the cell with the given name (case-insensitive), or nil.
func (n *Notebook) CellByName(name string) *Cell {
	for _, cell := range n.Cells {
		if strings.EqualFold(cell.Asset.Name, name) {
			return cell
		}
	}
	return nil
}

// UsedTablesFunc extracts the table names referenced by a SQL statement.
// assetType is the Bruin asset type (e.g. "duckdb.sql"); implementations
// map it to a parser dialect. The input SQL is already Jinja-stripped.
type UsedTablesFunc func(sql, assetType string) ([]string, error)

// Loader loads notebook folders into Notebook structs.
type Loader struct {
	fs afero.Fs
	// workspaceRoot bounds Python dependency discovery. An empty value keeps
	// discovery inside the notebook directory, which is useful for standalone
	// package callers and in-memory tests.
	workspaceRoot string
	// creator parses a cell file into a Bruin asset (the existing
	// file-comments task creator).
	creator pipeline.TaskCreator
	// usedTables infers referenced tables for dependency edges. May be
	// nil, in which case only declared `depends:` edges exist.
	usedTables UsedTablesFunc
}

// NewLoader builds a Loader. creator must not be nil.
func NewLoader(filesystem afero.Fs, creator pipeline.TaskCreator, usedTables UsedTablesFunc) *Loader {
	return &Loader{fs: filesystem, creator: creator, usedTables: usedTables}
}

// WithWorkspaceRoot makes loader-side Python dependency discovery match the
// repository boundary used by the execution operator.
func (l *Loader) WithWorkspaceRoot(root string) *Loader {
	l.workspaceRoot = filepath.Clean(root)
	return l
}

// Discover walks root and returns the directories containing a notebook
// manifest, sorted. Dot-directories and node_modules are skipped.
func Discover(filesystem afero.Fs, root string) ([]string, error) {
	dirs := make([]string, 0)
	err := afero.Walk(filesystem, root, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable subtrees are skipped, not fatal
		}
		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "venv" || name == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() == ManifestFileName {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

// Load assembles the notebook folder at dir. It ensures durable IDs exist
// (writing them back into notebook.yml / cell frontmatter when missing),
// derives dependency edges between cells from their SQL, and validates the
// result. Load never reorders or rewrites anything beyond missing IDs.
func (l *Loader) Load(dir string) (*Notebook, error) {
	manifestPath := filepath.Join(dir, ManifestFileName)
	manifest, err := readManifest(l.fs, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}

	uuid, _, err := EnsureNotebookID(l.fs, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to ensure notebook id: %w", manifestPath, err)
	}

	nb := &Notebook{
		Version:    manifest.Version,
		UUID:       uuid,
		Title:      manifest.Title,
		Dir:        dir,
		Target:     manifest.Target,
		Parameters: append([]presentation.ParameterDefinition(nil), manifest.Parameters...),
	}
	pythonEnvironment, environmentErr := PythonEnvironmentFingerprint(l.fs, dir, l.workspaceRoot)
	if environmentErr != nil {
		nb.Problems = append(nb.Problems, "could not fingerprint the Python environment: "+environmentErr.Error())
	} else {
		nb.PythonEnvironmentFingerprint = pythonEnvironment
	}
	if nb.Title == "" {
		nb.Title = filepath.Base(dir)
	}

	cells, problems, err := l.loadCells(dir)
	if err != nil {
		return nil, err
	}
	nb.Problems = append(nb.Problems, problems...)

	nb.Blocks, nb.Cells = reconcileBlocks(manifest.Blocks, cells, &nb.Problems)

	l.deriveDependencies(nb)
	validate(nb)

	revision, revisionErr := SnapshotRevision(l.fs, nb)
	if revisionErr != nil {
		nb.Problems = append(nb.Problems, "could not compute notebook revision: "+revisionErr.Error())
	} else {
		nb.Revision = revision
	}

	return nb, nil
}

// loadCells parses every cell file in the folder (non-recursive; notebooks
// are flat). Files without a Bruin frontmatter block are synthesized into
// minimal cells so a bare .sql file dropped into the folder still works.
func (l *Loader) loadCells(dir string) ([]*Cell, []string, error) {
	entries, err := afero.ReadDir(l.fs, dir)
	if err != nil {
		return nil, nil, err
	}

	problems := make([]string, 0)
	cells := make([]*Cell, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isCellFile(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if IsSourcePath(entry.Name()) {
			content, readErr := afero.ReadFile(l.fs, path)
			if readErr != nil {
				problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), readErr.Error()))
				continue
			}
			definition, parseErr := ParseSourceDefinition(content)
			if parseErr != nil {
				problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), parseErr.Error()))
				continue
			}
			cellID, generated, idErr := EnsureSourceID(l.fs, path)
			if idErr != nil {
				problems = append(problems, fmt.Sprintf("%s: failed to ensure source id: %s", entry.Name(), idErr.Error()))
				continue
			}
			if generated {
				content, readErr = afero.ReadFile(l.fs, path)
				if readErr != nil {
					problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), readErr.Error()))
					continue
				}
				definition, parseErr = ParseSourceDefinition(content)
				if parseErr != nil {
					problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), parseErr.Error()))
					continue
				}
			}
			asset := &pipeline.Asset{
				Name:       SourceCellName(entry.Name()),
				Type:       SourceCellType(definition.Kind),
				Connection: definition.Connection,
				Columns:    definition.Columns,
				Meta: map[string]string{
					SnapshotModeMetaKey:     definition.Snapshot.Mode,
					SnapshotRowLimitMetaKey: fmt.Sprintf("%d", definition.Snapshot.RowLimit),
				},
				ExecutableFile: pipeline.ExecutableFile{
					Name: filepath.Base(path), Path: path, Content: string(content),
				},
			}
			cells = append(cells, &Cell{
				ID: cellID, Asset: asset, Path: path, Raw: string(content), Source: definition,
			})
			continue
		}

		asset, parseErr := l.creator(path)
		if parseErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), parseErr.Error()))
			continue
		}
		if asset == nil {
			// No @bruin block: synthesize a minimal asset from the raw file.
			content, readErr := afero.ReadFile(l.fs, path)
			if readErr != nil {
				problems = append(problems, fmt.Sprintf("%s: %s", entry.Name(), readErr.Error()))
				continue
			}
			asset = &pipeline.Asset{}
			asset.ExecutableFile.Content = string(content)
			asset.ExecutableFile.Path = path
		}

		if asset.Type == "" {
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".py") {
				asset.Type = PythonCellType
			} else {
				asset.Type = DefaultCellType
			}
		}
		// Filename = cell name, always (frontmatter `name:` is ignored for
		// cells; logical names exist only in the editor layer).
		asset.Name = cellNameFromFilename(entry.Name())
		if asset.ExecutableFile.Path == "" {
			asset.ExecutableFile.Path = path
		}

		cellID, _, idErr := EnsureCellID(l.fs, path)
		if idErr != nil {
			problems = append(problems, fmt.Sprintf("%s: failed to ensure cell id: %s", entry.Name(), idErr.Error()))
			continue
		}

		// EnsureCellID may have rewritten the file (inserting an id); read
		// the authoritative full content for the rename engine.
		raw := ""
		if rawBytes, readErr := afero.ReadFile(l.fs, path); readErr == nil {
			raw = string(rawBytes)
		}

		cells = append(cells, &Cell{ID: cellID, Asset: asset, Path: path, Raw: raw})
	}

	return cells, problems, nil
}

// deriveDependencies fills each cell's upstream edges: declared `depends:`
// entries are kept, and referenced sibling cell names found by the SQL
// parser are added. References that do not match a sibling are recorded as
// ExternalRefs (pipeline assets or warehouse tables).
func (l *Loader) deriveDependencies(nb *Notebook) {
	for _, cell := range nb.Cells {
		content := strings.TrimSpace(cell.Asset.ExecutableFile.Content)
		if content == "" {
			continue
		}

		// SQL cells: parse the query for referenced tables (needs the parser).
		// Python cells: there is no SQL to parse, so detect sibling cells whose
		// name appears in the source (the cell reads them by name from the
		// session). Either way the references flow into the same upstream /
		// external reconciliation below.
		var used []string
		if IsPythonCell(cell) {
			used = pythonReferencedSiblings(content, nb, cell)
		} else if l.usedTables != nil && strings.HasSuffix(strings.ToLower(cell.Path), ".sql") {
			parsed, err := l.usedTables(JinjaSafeSQL(content), string(cell.Asset.Type))
			if err != nil {
				// Dependency inference is best-effort. While users type, SQL can be
				// temporarily invalid; surfacing that as a notebook problem causes noisy
				// layout churn without changing the saved cell content.
				continue
			}
			used = parsed
		} else {
			continue
		}

		declared := make(map[string]struct{}, len(cell.Asset.Upstreams))
		for _, upstream := range cell.Asset.Upstreams {
			declared[strings.ToLower(upstream.Value)] = struct{}{}
		}

		externals := make([]string, 0)
		for _, table := range used {
			normalized := strings.ToLower(strings.TrimSpace(table))
			if normalized == "" || normalized == strings.ToLower(cell.Asset.Name) {
				continue
			}
			if sibling := nb.CellByName(table); sibling != nil {
				if _, ok := declared[strings.ToLower(sibling.Asset.Name)]; !ok {
					cell.Asset.Upstreams = append(cell.Asset.Upstreams, pipeline.Upstream{
						Type:  "asset",
						Value: sibling.Asset.Name,
						Mode:  pipeline.UpstreamModeFull,
					})
					declared[strings.ToLower(sibling.Asset.Name)] = struct{}{}
				}
				continue
			}
			externals = append(externals, table)
		}
		sort.Strings(externals)
		cell.ExternalRefs = dedupeStrings(externals)
	}
}

// pythonReferencedSiblings returns the names of sibling cells whose name
// appears as a whole word in a Python cell's source. There is no Python table
// parser, so a name-occurrence scan stands in for the SQL parser: it drives
// run ordering and tells the runner which siblings to make readable. Declared
// `depends:` edges are layered on top by the caller.
func pythonReferencedSiblings(content string, nb *Notebook, self *Cell) []string {
	refs := make([]string, 0)
	for _, sibling := range nb.Cells {
		if sibling.ID == self.ID {
			continue
		}
		name := sibling.Asset.Name
		if name == "" {
			continue
		}
		if matchesWholeWord(content, name) {
			refs = append(refs, name)
		}
	}
	return refs
}

func matchesWholeWord(haystack, word string) bool {
	pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
	return pattern.MatchString(haystack)
}

func isCellFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == ManifestFileName {
		return false
	}
	return strings.HasSuffix(lower, ".sql") || strings.HasSuffix(lower, ".py") || IsSourcePath(lower)
}

// IsPythonCell reports whether a cell is a Python asset (by type or file
// extension).
func IsPythonCell(cell *Cell) bool {
	if cell == nil {
		return false
	}
	if strings.EqualFold(string(cell.Asset.Type), PythonCellType) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(cell.Path), ".py")
}

func cellNameFromFilename(filename string) string {
	if IsSourcePath(filename) {
		return SourceCellName(filename)
	}
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	return stem
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := values[:0]
	for _, value := range values {
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
