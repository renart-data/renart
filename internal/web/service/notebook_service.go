package service

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
)

// NotebookDependencies wires the notebook service into the rest of the app.
type NotebookDependencies struct {
	WorkspaceRoot           string
	ConfigPath              string
	DisableFilesystemAccess bool
	// CurrentState returns the latest workspace state (for pipeline asset
	// lookups when importing upstream data).
	CurrentState func() model.WorkspaceState
	// RunConnectionQuery executes a query on a named connection; used as
	// the generic import fetch backend.
	RunConnectionQuery func(ctx context.Context, connection, environment, query string) ([]string, []map[string]any, error)
	// NewConnectionManager resolves the selected environment through Renart's
	// runtime connection factory, including managed secrets and warehouse-
	// specific compatibility. Warehouse notebook snapshots use it only long
	// enough to derive Sling's private source connection payload.
	NewConnectionManager func(ctx context.Context, environment string) (config.ConnectionAndDetailsGetter, error)
	// PushWorkspaceUpdate triggers a workspace refresh + SSE push after
	// mutations (optional).
	PushWorkspaceUpdate func(ctx context.Context, eventType, eventPath string)
	// ValidateSQL runs the shared parse-context validator for a cell against
	// the given sibling schema, used by server-side auto-recompute to decide
	// which cells are safe to run. Optional (auto-recompute is disabled if nil).
	ValidateSQL func(ctx context.Context, assetID, content string, schemaTables []ParseContextSchemaTable) (ParseContextResult, *APIError)
	// PublishEvent pushes a payload on the workspace SSE stream (notebook
	// runtime events). Optional.
	PublishEvent func(payload any)
}

// NotebookJinjaContext is the authored parameter contract plus the current
// local runtime values used to render one notebook cell. It deliberately omits
// paths and wider notebook state so SQL intelligence can consume the same
// typed context as execution without gaining another persistence surface.
type NotebookJinjaContext struct {
	Definitions []presentation.ParameterDefinition
	Values      map[string]any
}

// NotebookService implements notebook CRUD and cell execution on top of the
// notebook package.
type NotebookService struct {
	deps  NotebookDependencies
	store *notebook.SessionStore

	// cellEditLocks serialize the compare-and-write section for each cell.
	// The content revision prevents stale writers; the lock makes that check
	// atomic within this server process.
	cellEditMu    sync.Mutex
	cellEditLocks map[string]*cellEditLock
	// notebookEditLocks serialize authored snapshot mutations across manifest
	// and cell files. Cell-specific locks retain the precise conflict behavior
	// for current editors; this wider lock is the substrate for notebook-wide
	// revision-checked change sets.
	notebookEditMu    sync.Mutex
	notebookEditLocks map[string]*cellEditLock

	// Bruin's SQL parser is created lazily and reused across every load and run
	// instead of being spun up per operation.
	parserMu     sync.Mutex
	cachedParser *sqlparser.SQLParser

	// runtimes holds per-notebook recompute state (staleness, last results,
	// the auto-recompute toggle) for server-driven auto-recompute.
	runtimes *notebookRuntimes
}

type cellEditLock struct {
	mu   sync.Mutex
	refs int
}

// ensureParserLocked returns the shared parser, creating it on first use. The
// caller must hold parserMu.
func (s *NotebookService) ensureParserLocked() (*sqlparser.SQLParser, error) {
	if s.cachedParser == nil {
		parser, err := sqlparser.NewSQLParser(false)
		if err != nil {
			return nil, err
		}
		s.cachedParser = parser
	}
	return s.cachedParser, nil
}

// renameTables rewrites cell table references using the shared parser.
// Serialized because the embedded-Python parser is not concurrency safe;
// notebook work is interactive and low-concurrency.
func (s *NotebookService) renameTables(sqlText, dialect string, mapping map[string]string) (string, error) {
	s.parserMu.Lock()
	defer s.parserMu.Unlock()
	parser, err := s.ensureParserLocked()
	if err != nil {
		return "", err
	}
	return parser.RenameTables(sqlText, dialect, mapping)
}

// validateNotebookPythonQuery applies the same single-SELECT boundary as the
// ordinary Python broker while reusing the notebook service's long-lived SQL
// parser. Reuse avoids initializing another parser for every Python cell.
func (s *NotebookService) validateNotebookPythonQuery(sqlText string) error {
	s.parserMu.Lock()
	defer s.parserMu.Unlock()
	parser, err := s.ensureParserLocked()
	if err != nil {
		return fmt.Errorf("could not initialize SQL validation: %w", err)
	}
	isSelect, err := parser.IsSingleSelectQuery(sqlText, "duckdb")
	if err != nil {
		return fmt.Errorf("could not validate notebook query: %w", err)
	}
	if !isSelect {
		return fmt.Errorf("renart.query() only runs read-only single SELECT statements; use the cell's materialize() result for writes")
	}
	return nil
}

// newNotebookJinjaRenderer builds a Jinja renderer for a run's execution
// window. A notebook is not a pipeline, so it renders with date windows only
// (no pipeline variables or macros) — the same constructs the editor preview
// resolves for date-driven cells. start/end are RFC3339; empty uses the
// default daily window.
func (s *NotebookService) newNotebookJinjaRenderer(
	start, end string,
	definitions []presentation.ParameterDefinition,
	parameterValues map[string]any,
) (*jinja.Renderer, error) {
	return buildNotebookJinjaPreviewRenderer(start, end, NotebookJinjaContext{
		Definitions: definitions,
		Values:      parameterValues,
	})
}

func buildNotebookJinjaPreviewRenderer(start, end string, notebookContext NotebookJinjaContext) (*jinja.Renderer, error) {
	now := time.Now().UTC()
	window, err := ResolveExecutionTimeWindow("", start, end, now)
	if err != nil {
		return nil, err
	}
	resolved, findings := presentation.ResolveParameterValues(notebookContext.Definitions, notebookContext.Values)
	if len(findings) > 0 {
		return nil, fmt.Errorf("invalid notebook parameter values: %s", findings[0].Message)
	}
	literals, err := presentation.ParameterSQLLiterals(notebookContext.Definitions, resolved)
	if err != nil {
		return nil, err
	}
	renderer := jinja.NewRendererWithStartEndDatesAndMacros(
		&window.Start, &window.End, &now, "renart-notebook", "renart-notebook-run", jinja.Context(resolved), "",
	)
	// `parameter` is the safe SQL-interpolation surface. `parameters` exposes
	// typed values for Jinja conditions and non-SQL source templates.
	renderer.SetContextValue("parameter", literals)
	renderer.SetContextValue("parameters", resolved)
	return renderer, nil
}

// JinjaContextForAsset resolves a notebook cell asset ID to the same typed
// parameter snapshot used by execution. The boolean is false for ordinary
// pipeline assets, allowing shared preview/LSP callers to fall back to the
// pipeline renderer without guessing from synthetic pipeline metadata.
func (s *NotebookService) JinjaContextForAsset(_ context.Context, assetID string) (NotebookJinjaContext, bool, error) {
	relPath, err := DecodeID(assetID)
	if err != nil {
		return NotebookJinjaContext{}, false, nil
	}
	absPath, err := SafeJoin(s.deps.WorkspaceRoot, relPath)
	if err != nil {
		return NotebookJinjaContext{}, false, err
	}
	dir := filepath.Dir(absPath)
	if _, err := os.Stat(filepath.Join(dir, notebook.ManifestFileName)); err != nil {
		if os.IsNotExist(err) {
			return NotebookJinjaContext{}, false, nil
		}
		return NotebookJinjaContext{}, false, err
	}

	relDir, err := filepath.Rel(s.deps.WorkspaceRoot, dir)
	if err != nil {
		return NotebookJinjaContext{}, false, err
	}
	nb, apiErr := s.load(EncodeID(filepath.ToSlash(relDir)))
	if apiErr != nil {
		return NotebookJinjaContext{}, true, fmt.Errorf("%s", apiErr.Message)
	}
	found := false
	for _, cell := range nb.Cells {
		if filepath.Clean(cell.Path) == filepath.Clean(absPath) {
			found = true
			break
		}
	}
	if !found {
		return NotebookJinjaContext{}, false, nil
	}

	return NotebookJinjaContext{
		Definitions: append([]presentation.ParameterDefinition(nil), nb.Parameters...),
		Values:      s.currentNotebookParameterValues(nb),
	}, true, nil
}

// usedTables extracts referenced tables for dependency derivation using the
// shared parser.
func (s *NotebookService) usedTables(sqlText, assetType string) ([]string, error) {
	dialect, dialectErr := sqlparser.AssetTypeToDialect(pipeline.AssetType(assetType))
	if dialectErr != nil || dialect == "" {
		dialect = "duckdb"
	}
	s.parserMu.Lock()
	defer s.parserMu.Unlock()
	parser, err := s.ensureParserLocked()
	if err != nil {
		return nil, err
	}
	return parser.UsedTables(sqlText, dialect)
}

// NewNotebookService constructs the service; session DBs live under
// .renart/notebooks in the workspace.
func NewNotebookService(deps NotebookDependencies) *NotebookService {
	if strings.TrimSpace(deps.ConfigPath) == "" {
		deps.ConfigPath = filepath.Join(deps.WorkspaceRoot, ".bruin.yml")
	}
	store := notebook.NewSessionStore(filepath.Join(deps.WorkspaceRoot, ".renart", "notebooks"), deps.WorkspaceRoot)
	store.DisableFilesystemAccess = deps.DisableFilesystemAccess
	return &NotebookService{
		deps:              deps,
		store:             store,
		cellEditLocks:     make(map[string]*cellEditLock),
		notebookEditLocks: make(map[string]*cellEditLock),
		runtimes:          newNotebookRuntimes(),
	}
}

func (s *NotebookService) validateNotebookSourceQuery(sqlText, assetType string) error {
	dialect, dialectErr := sqlparser.AssetTypeToDialect(pipeline.AssetType(assetType))
	if dialectErr != nil || strings.TrimSpace(dialect) == "" {
		return fmt.Errorf("cannot determine the SQL dialect for notebook source type %q", assetType)
	}
	s.parserMu.Lock()
	defer s.parserMu.Unlock()
	parser, err := s.ensureParserLocked()
	if err != nil {
		return fmt.Errorf("could not initialize SQL validation: %w", err)
	}
	isSelect, err := parser.IsSingleSelectQuery(sqlText, dialect)
	if err != nil {
		return fmt.Errorf("could not validate warehouse source query: %w", err)
	}
	if !isSelect {
		return fmt.Errorf("warehouse notebook sources only run a read-only single SELECT statement")
	}
	return nil
}

// lockCellEdit serializes optimistic-revision checks and writes for one cell.
// It is keyed by durable notebook/cell identity rather than a path so renames
// cannot create a second lock for the same logical document.
func (s *NotebookService) lockCellEdit(notebookID, cellID string) func() {
	key := notebookID + ":" + cellID
	s.cellEditMu.Lock()
	lock, ok := s.cellEditLocks[key]
	if !ok {
		lock = &cellEditLock{}
		s.cellEditLocks[key] = lock
	}
	lock.refs++
	s.cellEditMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.cellEditMu.Lock()
		lock.refs--
		if lock.refs == 0 && s.cellEditLocks[key] == lock {
			delete(s.cellEditLocks, key)
		}
		s.cellEditMu.Unlock()
	}
}

func (s *NotebookService) lockNotebookEdit(notebookID string) func() {
	s.notebookEditMu.Lock()
	lock, ok := s.notebookEditLocks[notebookID]
	if !ok {
		lock = &cellEditLock{}
		s.notebookEditLocks[notebookID] = lock
	}
	lock.refs++
	s.notebookEditMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.notebookEditMu.Lock()
		lock.refs--
		if lock.refs == 0 && s.notebookEditLocks[notebookID] == lock {
			delete(s.notebookEditLocks, notebookID)
		}
		s.notebookEditMu.Unlock()
	}
}

// SessionStore exposes the store for startup sweeps.
func (s *NotebookService) SessionStore() *notebook.SessionStore {
	return s.store
}

// SweepSessions removes session DB files for notebooks that no longer
// exist; called once at startup.
func (s *NotebookService) SweepSessions() ([]string, error) {
	// Finish or roll back an interrupted authored-file transaction before any
	// notebook is loaded. This keeps the filesystem snapshot coherent after a
	// process or machine failure midway through a multi-file apply.
	if err := recoverNotebookFileTransactions(s.deps.WorkspaceRoot); err != nil {
		return nil, err
	}
	fs := afero.NewOsFs()
	dirs, err := notebook.Discover(fs, s.deps.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		if uuid, _, idErr := notebook.EnsureNotebookID(fs, filepath.Join(dir, notebook.ManifestFileName)); idErr == nil {
			active[uuid] = true
		}
	}
	return s.store.Sweep(active)
}

func (s *NotebookService) newLoader() (*notebook.Loader, func()) {
	fs := afero.NewOsFs()
	// Reuse the shared parser instead of spinning up a fresh embedded-Python
	// instance per load; cleanup is a no-op (the parser outlives the loader).
	return notebook.NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), s.usedTables), func() {}
}

// resolveDir maps an encoded notebook ID to its absolute folder, verifying
// it stays inside the workspace and is a notebook.
func (s *NotebookService) resolveDir(notebookID string) (string, *APIError) {
	relDir, err := DecodeID(notebookID)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_id", Message: "invalid notebook id"}
	}
	absDir := filepath.Join(s.deps.WorkspaceRoot, filepath.FromSlash(relDir))
	cleanRoot := filepath.Clean(s.deps.WorkspaceRoot)
	if absDir != cleanRoot && !strings.HasPrefix(absDir, cleanRoot+string(filepath.Separator)) {
		return "", &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_id", Message: "notebook path escapes the workspace"}
	}
	if _, statErr := os.Stat(filepath.Join(absDir, notebook.ManifestFileName)); statErr != nil {
		return "", &APIError{Status: http.StatusNotFound, Code: "notebook_not_found", Message: "notebook not found"}
	}
	return absDir, nil
}

func (s *NotebookService) load(notebookID string) (*notebook.Notebook, *APIError) {
	absDir, apiErr := s.resolveDir(notebookID)
	if apiErr != nil {
		return nil, apiErr
	}
	loader, cleanup := s.newLoader()
	defer cleanup()
	nb, err := loader.Load(absDir)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "notebook_load_failed", Message: err.Error()}
	}
	return nb, nil
}

// Get returns the notebook in API shape, loaded fresh from disk.
func (s *NotebookService) Get(notebookID string) (model.Notebook, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	return s.toModel(nb), nil
}

func (s *NotebookService) toModel(nb *notebook.Notebook) model.Notebook {
	workspace := &WorkspaceService{workspaceRoot: s.deps.WorkspaceRoot}
	return workspace.notebookToModel(nb)
}

var notebookSlugSanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)

// CreateNotebookRequest creates a new notebook folder.
type CreateNotebookRequest struct {
	Title string `json:"title"`
	Path  string `json:"path,omitempty"`
}

// Create makes a new notebook folder with a manifest (no cells yet).
func (s *NotebookService) Create(req CreateNotebookRequest) (model.Notebook, *APIError) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "untitled"
	}

	relDir := strings.TrimSpace(req.Path)
	if relDir == "" {
		slug := notebookSlugSanitizer.ReplaceAllString(strings.ToLower(title), "-")
		slug = strings.Trim(slug, "-")
		if slug == "" {
			slug = "untitled"
		}
		relDir = filepath.ToSlash(filepath.Join("notebooks", slug))
	}

	absDir := filepath.Join(s.deps.WorkspaceRoot, filepath.FromSlash(relDir))
	if _, err := os.Stat(filepath.Join(absDir, notebook.ManifestFileName)); err == nil {
		return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "notebook_exists", Message: fmt.Sprintf("a notebook already exists at %s", relDir)}
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_create_failed", Message: err.Error()}
	}

	// Seed a small example cell so a new notebook is immediately runnable. Its
	// concise generated name keeps relation completion readable from the start.
	exampleID := notebook.NewCellID()
	exampleName := nextCellAutoname(&notebook.Notebook{}, s.pipelineAssetNameSet())
	exampleContent := fmt.Sprintf(
		"/* @bruin\nid: %s\ntype: %s\nclass: %s\n@bruin */\n\nselect 'hello' as greeting, 42 as answer\n",
		exampleID, notebook.DefaultCellType, notebook.ClassNotebook)
	if err := os.WriteFile(filepath.Join(absDir, exampleName+".sql"), []byte(exampleContent), 0o644); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_create_failed", Message: err.Error()}
	}
	manifest := &notebook.Notebook{
		Version: notebook.ManifestVersionCurrent,
		Title:   title,
		Dir:     absDir,
		Blocks:  []notebook.Block{{Cell: exampleID}},
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), manifest); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_create_failed", Message: err.Error()}
	}

	encodedID := EncodeID(filepath.ToSlash(relDir))
	result, apiErr := s.Get(encodedID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	s.pushUpdate(absDir)
	return result, nil
}

// Delete removes the notebook folder and its session database. Cleanup is
// "delete the file" — nothing else to reconcile for the DuckDB target.
func (s *NotebookService) Delete(notebookID string) *APIError {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	if err := os.RemoveAll(nb.Dir); err != nil {
		return &APIError{Status: http.StatusInternalServerError, Code: "notebook_delete_failed", Message: err.Error()}
	}
	if err := s.store.Remove(nb.UUID); err != nil {
		return &APIError{Status: http.StatusInternalServerError, Code: "notebook_session_delete_failed", Message: err.Error()}
	}
	s.pushUpdate(nb.Dir)
	return nil
}

// CloseSession deletes the notebook's session database file (the default
// close-notebook cleanup); the notebook itself is untouched.
func (s *NotebookService) CloseSession(notebookID string) *APIError {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	if err := s.store.Remove(nb.UUID); err != nil {
		return &APIError{Status: http.StatusInternalServerError, Code: "notebook_session_delete_failed", Message: err.Error()}
	}
	return nil
}

var cellNamePattern = regexp.MustCompile(`^\w+$`)

// CreateCellRequest adds a cell to a notebook.
type CreateCellRequest struct {
	Name string `json:"name,omitempty"`
	// Language selects the cell kind: "sql" (default) or "python".
	Language string `json:"language,omitempty"`
}

// UpdateCellRequest replaces a cell snapshot. BaseRevision is optional for
// backwards-compatible API callers; interactive editors always provide it so
// stale full-document writes fail explicitly instead of losing newer text.
type UpdateCellRequest struct {
	Content      string `json:"content"`
	BaseRevision string `json:"base_revision,omitempty"`
}

// CreateCell writes a new cell file and appends it to the blocks.
func (s *NotebookService) CreateCell(notebookID string, req CreateCellRequest) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = nextCellAutoname(nb, s.pipelineAssetNameSet())
	}
	if !cellNamePattern.MatchString(name) {
		return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_cell_name", Message: "cell names may only contain letters, digits, and underscores"}
	}
	if nb.CellByName(name) != nil {
		return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "cell_exists", Message: fmt.Sprintf("a cell named %q already exists", name)}
	}
	if conflict := s.pipelineAssetByName(name); conflict != nil {
		return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "cell_name_collides", Message: fmt.Sprintf("%q is already a pipeline asset name", name)}
	}

	cellID := notebook.NewCellID()
	for nb.CellByID(cellID) != nil {
		cellID = notebook.NewCellID()
	}
	python := strings.EqualFold(strings.TrimSpace(req.Language), "python")
	ext, template := ".sql", notebook.CellFileTemplate(cellID)
	if python {
		ext, template = ".py", notebook.PythonCellFileTemplate(cellID)
	}
	path := filepath.Join(nb.Dir, name+ext)
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "cell_create_failed", Message: err.Error()}
	}

	nb.Blocks = append(nb.Blocks, notebook.Block{Cell: cellID})
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "cell_create_failed", Message: err.Error()}
	}

	s.pushUpdate(path)
	return s.Get(notebookID)
}

// RenameCell renames a cell's display name: it rewrites references in
// sibling cells (span splice, formatting preserved), moves the cell file,
// and drops the old session object. Zero fingerprints change (invariant 1),
// so nothing goes stale and the warehouse is untouched.
func (s *NotebookService) RenameCell(notebookID, cellID, newName string) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: "cell not found"}
	}

	newName = strings.TrimSpace(newName)
	pipelineNames := s.pipelineAssetNameSet()
	if message := notebook.ValidateCellName(nb, newName, cellID, pipelineNames); message != "" {
		return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "invalid_cell_name", Message: message}
	}

	edits, err := notebook.PlanRename(nb, cellID, newName)
	if err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "rename_failed", Message: err.Error()}
	}

	// Apply content rewrites first, then the file move, so a failure midway
	// never leaves a dangling rename with stale references.
	for _, edit := range edits {
		if edit.NewContent != "" {
			if writeErr := os.WriteFile(edit.Path, []byte(edit.NewContent), 0o644); writeErr != nil {
				return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "rename_failed", Message: writeErr.Error()}
			}
		}
	}
	for _, edit := range edits {
		if edit.NewPath != "" {
			if renameErr := os.Rename(edit.Path, edit.NewPath); renameErr != nil {
				return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "rename_failed", Message: renameErr.Error()}
			}
		}
	}

	// The renamed cell's session view is named by ID, so it survives; but a
	// stale object under the *old* name never existed (objects are
	// cell_<id>). Nothing to drop.

	s.pushUpdate(cell.Path)
	return s.Get(notebookID)
}

// UpdateCell replaces a cell file's content. The frontmatter id is forced
// back to the cell's durable id — identity is not editable.
func (s *NotebookService) UpdateCell(notebookID, cellID string, req UpdateCellRequest) (model.Notebook, *APIError) {
	unlock := s.lockCellEdit(notebookID, cellID)
	defer unlock()
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: "cell not found"}
	}
	currentContent := cell.Raw
	if currentContent == "" {
		if raw, readErr := os.ReadFile(cell.Path); readErr == nil {
			currentContent = string(raw)
		}
	}
	if req.BaseRevision != "" && req.BaseRevision != notebook.ContentRevision(currentContent) {
		return model.Notebook{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "cell_edit_conflict",
			Message: "This cell changed after editing began. Your draft was kept; reload or reconcile the newer content before saving.",
		}
	}

	normalized := notebook.NormalizeCellID(req.Content, cellID, notebook.IsPythonCell(cell))
	if normalized == notebook.NormalizeCellID(currentContent, cellID, notebook.IsPythonCell(cell)) {
		// Blur and focus transitions can race the client's acknowledgement of
		// an autosave. Treat an identical revision-checked save as a true no-op:
		// rewriting the file would emit workspace churn and, more importantly,
		// incorrectly mark the cell and all descendants stale.
		return s.toModel(nb), nil
	}
	if err := os.WriteFile(cell.Path, []byte(normalized), 0o644); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "cell_update_failed", Message: err.Error()}
	}

	s.pushUpdate(cell.Path)
	// Reload against the new content so the dependency graph (and thus the
	// descendant closure marked stale) reflects this edit, then trigger
	// server-side recompute.
	if fresh, freshErr := s.load(notebookID); freshErr == nil {
		s.onCellChanged(notebookID, fresh, cellID)
	}
	return s.Get(notebookID)
}

// DeleteCell removes the cell file, its block entry, and its materialized
// session objects.
func (s *NotebookService) DeleteCell(notebookID, cellID string) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: "cell not found"}
	}

	if err := os.Remove(cell.Path); err != nil && !os.IsNotExist(err) {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "cell_delete_failed", Message: err.Error()}
	}

	blocks := make([]notebook.Block, 0, len(nb.Blocks))
	for _, block := range nb.Blocks {
		if block.Cell == cellID || (block.Visualization != nil && block.Visualization.Source == cellID) {
			continue
		}
		blocks = append(blocks, block)
	}
	nb.Blocks = blocks
	remaining := make([]*notebook.Cell, 0, len(nb.Cells))
	for _, candidate := range nb.Cells {
		if candidate.ID != cellID {
			remaining = append(remaining, candidate)
		}
	}
	nb.Cells = remaining
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "cell_delete_failed", Message: err.Error()}
	}

	_ = s.store.DropCellObjects(nb.UUID, cellID)

	s.pushUpdate(cell.Path)
	s.forgetCell(notebookID, nb.UUID, cellID)
	return s.Get(notebookID)
}

// UpdateBlocks replaces the notebook's ordered blocks (markdown edits and
// reordering). Cell blocks must reference existing cells; every cell must
// remain referenced exactly once.
func (s *NotebookService) UpdateBlocks(notebookID string, blocks []model.NotebookBlock) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}

	seen := map[string]bool{}
	seenBlockIDs := map[string]bool{}
	next := make([]notebook.Block, 0, len(blocks))
	for _, block := range blocks {
		if block.Cell != "" {
			if block.Visualization != nil || block.Markdown != "" || block.Control != "" || block.ID != "" {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_block", Message: "a cell block cannot also contain presentation content"}
			}
			if nb.CellByID(block.Cell) == nil {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "unknown_cell", Message: fmt.Sprintf("block references unknown cell %q", block.Cell)}
			}
			if seen[block.Cell] {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "duplicate_cell_block", Message: fmt.Sprintf("cell %q appears more than once", block.Cell)}
			}
			seen[block.Cell] = true
			next = append(next, notebook.Block{Cell: block.Cell})
			continue
		}

		if block.Control != "" {
			if nb.Version < notebook.ManifestVersionCurrent {
				return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "notebook_upgrade_required", Message: "upgrade this notebook before placing controls"}
			}
			if block.Visualization != nil || block.Markdown != "" || block.ID != "" {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_block", Message: "a control block cannot also contain other presentation content"}
			}
			parameterID := strings.TrimSpace(block.Control)
			known := false
			for _, parameter := range nb.Parameters {
				if parameter.ID == parameterID {
					known = true
					break
				}
			}
			if !known {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "unknown_notebook_control", Message: fmt.Sprintf("control references unknown parameter %q", parameterID)}
			}
			stableID := "control:" + parameterID
			if seenBlockIDs[stableID] {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "duplicate_notebook_block", Message: fmt.Sprintf("control %q appears more than once", parameterID)}
			}
			seenBlockIDs[stableID] = true
			next = append(next, notebook.Block{Control: parameterID})
			continue
		}

		if block.Visualization != nil {
			if nb.Version < notebook.ManifestVersionCurrent {
				return model.Notebook{}, &APIError{Status: http.StatusConflict, Code: "notebook_upgrade_required", Message: "upgrade this notebook before adding structured visualizations"}
			}
			id := strings.TrimSpace(block.ID)
			visualizationID := strings.TrimSpace(block.Visualization.ID)
			if id != "" && visualizationID != "" && id != visualizationID {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_block", Message: "visualization block ids do not match"}
			}
			if id == "" {
				id = visualizationID
			}
			if id == "" {
				id = notebook.NewBlockID("viz")
			}
			if seenBlockIDs[id] {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "duplicate_notebook_block", Message: fmt.Sprintf("block id %q appears more than once", id)}
			}
			seenBlockIDs[id] = true
			source := strings.TrimSpace(block.Visualization.Source)
			if nb.CellByID(source) == nil {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "unknown_visualization_source", Message: fmt.Sprintf("visualization %q references unknown source cell %q", id, source)}
			}
			if len(block.Visualization.Definition) == 0 {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_visualization_definition", Message: fmt.Sprintf("visualization %q has no definition", id)}
			}
			next = append(next, notebook.Block{
				ID: id,
				Visualization: &notebook.VisualizationBlock{
					ID:         id,
					Source:     source,
					Definition: cloneStringAnyMap(block.Visualization.Definition),
				},
			})
			continue
		}

		id := strings.TrimSpace(block.ID)
		if nb.Version >= notebook.ManifestVersionCurrent {
			if id == "" {
				id = notebook.NewBlockID("md")
			}
			if seenBlockIDs[id] {
				return model.Notebook{}, &APIError{Status: http.StatusBadRequest, Code: "duplicate_notebook_block", Message: fmt.Sprintf("block id %q appears more than once", id)}
			}
			seenBlockIDs[id] = true
		}
		next = append(next, notebook.Block{ID: id, Markdown: block.Markdown})
	}
	for _, cell := range nb.Cells {
		if !seen[cell.ID] {
			next = append(next, notebook.Block{Cell: cell.ID})
		}
	}

	nb.Blocks = next
	if _, blocking := s.notebookVisualizationProblems(context.Background(), nb); len(blocking) > 0 {
		return model.Notebook{}, &APIError{
			Status: http.StatusBadRequest, Code: "invalid_visualization_definition",
			Message: strings.Join(blocking, "; "),
		}
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "blocks_update_failed", Message: err.Error()}
	}

	s.pushUpdate(filepath.Join(nb.Dir, notebook.ManifestFileName))
	return s.Get(notebookID)
}

// UpgradeManifest upgrades a legacy notebook.yml to the identity-bearing v2
// block format. BaseRevision is required by interactive callers so an external
// edit cannot be overwritten by the migration.
func (s *NotebookService) UpgradeManifest(notebookID, baseRevision string) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	if baseRevision != "" && baseRevision != nb.Revision {
		return model.Notebook{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_edit_conflict",
			Message: "This notebook changed after the upgrade was prepared. Reload it before upgrading.",
		}
	}
	changed, err := notebook.UpgradeManifestV2(afero.NewOsFs(), nb)
	if err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_upgrade_failed", Message: err.Error()}
	}
	if changed {
		s.pushUpdate(filepath.Join(nb.Dir, notebook.ManifestFileName))
	}
	return s.Get(notebookID)
}

// RunNotebookRequest selects which cells to execute.
type RunNotebookRequest struct {
	// All runs every cell in dependency order.
	All bool `json:"all,omitempty"`
	// From runs the given cell and its descendants (run-from-here).
	From string `json:"from,omitempty"`
	// Cells runs exactly these cells (plus any ancestors whose session
	// objects do not exist yet), in dependency order.
	Cells []string `json:"cells,omitempty"`
	// RefreshImports forces re-fetching cached upstream imports.
	RefreshImports bool `json:"refresh_imports,omitempty"`
	// Environment selects the connection environment for imports.
	Environment string `json:"environment,omitempty"`
	// StartDate/EndDate are the RFC3339 Jinja execution window. Empty falls
	// back to the default daily window, matching the editor's preview.
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	// Parameters are partial local runtime overrides. They are validated against
	// notebook.yml and persist only in this server process.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// RunNotebookResult is the batch outcome.
type RunNotebookResult struct {
	Status  string                   `json:"status"`
	Results []notebook.CellRunResult `json:"results"`
}

// Run executes the selected cells in the notebook's session.
func (s *NotebookService) Run(ctx context.Context, notebookID string, req RunNotebookRequest) (RunNotebookResult, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return RunNotebookResult{}, apiErr
	}
	s.hydrateRuntime(nb)
	parameterValues := s.currentNotebookParameterValues(nb)
	if req.Parameters != nil {
		var parameterErr *APIError
		parameterValues, parameterErr = s.updateNotebookParameterValues(notebookID, nb, req.Parameters, false)
		if parameterErr != nil {
			return RunNotebookResult{}, parameterErr
		}
	}
	rt := s.runtimes.get(nb.UUID)
	cells, selectErr := s.selectRunCells(nb, req)
	if selectErr != nil {
		return RunNotebookResult{}, selectErr
	}
	if len(cells) == 0 {
		return RunNotebookResult{Status: "ok", Results: []notebook.CellRunResult{}}, nil
	}

	renderer, renderErr := s.newNotebookJinjaRenderer(req.StartDate, req.EndDate, nb.Parameters, parameterValues)
	if renderErr != nil {
		return RunNotebookResult{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_render_context", Message: renderErr.Error()}
	}
	cellIDs := make([]string, 0, len(cells))
	for _, cell := range cells {
		cellIDs = append(cellIDs, cell.ID)
	}
	runCtx, finishRun := rt.beginManualRun(ctx, cellIDs)
	s.publishCurrentRuntime(notebookID, nb.UUID)
	defer func() {
		finishRun()
		s.publishCurrentRuntime(notebookID, nb.UUID)
	}()

	runner := s.newRunner(renderer, req.Environment, parameterValues)

	results, runErr := runner.RunCells(runCtx, nb, cells, notebook.RunOptions{RefreshImports: req.RefreshImports})
	if runErr != nil {
		// A cancelled run is an expected response, whether cancellation came
		// from the request context or the explicit Stop endpoint.
		if runCtx.Err() != nil {
			return RunNotebookResult{Status: "cancelled", Results: []notebook.CellRunResult{}}, nil
		}
		return RunNotebookResult{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_run_failed", Message: runErr.Error()}
	}

	// Fold a manual run into the runtime so the server stays the source of
	// truth, then push the update to any other open tabs.
	s.recordResults(rt, results)
	s.publishRuntimeResultsDelta(notebookID, nb.UUID, results)
	// A manual run may unblock downstream cells (their upstream is now fresh);
	// let auto-recompute pick them up if it is on.
	rt.mu.Lock()
	auto := rt.autoRecompute
	rt.mu.Unlock()
	if auto {
		s.scheduleRecompute(notebookID, nb.UUID)
	}

	status := "ok"
	for _, result := range results {
		if result.Status != notebook.CellRunOK {
			status = "error"
			break
		}
	}
	return RunNotebookResult{Status: status, Results: results}, nil
}

// publishRuntimeResultsDelta recomputes the auto-pending closure and pushes a
// results delta — used after a manual run so all clients converge.
func (s *NotebookService) publishRuntimeResultsDelta(notebookID, uuid string, results []notebook.CellRunResult) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return
	}
	rt := s.runtimes.get(uuid)
	closure := computeAutoRecomputeClosure(s.buildAutoCells(nb, rt))
	delta := make(map[string]notebook.CellRunResult, len(results))
	for _, result := range results {
		delta[result.CellID] = result
	}
	s.publishRuntime(notebookID, uuid, sortedKeys(closure), delta)
}

// selectRunCells turns the request into an ordered execution list.
func (s *NotebookService) selectRunCells(nb *notebook.Notebook, req RunNotebookRequest) ([]*notebook.Cell, *APIError) {
	ordered := notebook.TopoOrder(nb)
	if req.All || (req.From == "" && len(req.Cells) == 0) {
		return ordered, nil
	}

	wanted := map[string]bool{}
	if req.From != "" {
		from := nb.CellByID(req.From)
		if from == nil {
			return nil, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: fmt.Sprintf("cell %q not found", req.From)}
		}
		wanted[from.ID] = true
		for _, descendant := range notebook.Descendants(nb, from) {
			wanted[descendant.ID] = true
		}
	}
	for _, cellID := range req.Cells {
		cell := nb.CellByID(cellID)
		if cell == nil {
			return nil, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: fmt.Sprintf("cell %q not found", cellID)}
		}
		wanted[cell.ID] = true
	}

	// Pull in ancestors whose session objects are missing, so a first run
	// of a downstream cell does not fail on absent views.
	existing, existErr := s.store.ExistingCellObjects(nb.UUID)
	if existErr != nil {
		existing = map[string]bool{}
	}
	for _, cell := range nb.Cells {
		if !wanted[cell.ID] {
			continue
		}
		for _, ancestor := range notebook.Ancestors(nb, cell) {
			if !wanted[ancestor.ID] && !existing[notebook.CellObjectName(ancestor.ID)] {
				wanted[ancestor.ID] = true
			}
		}
	}

	result := make([]*notebook.Cell, 0, len(wanted))
	for _, cell := range ordered {
		if wanted[cell.ID] {
			result = append(result, cell)
		}
	}
	return result, nil
}

var cellNameAdjectives = [...]string{
	"amber", "brisk", "calm", "clever", "cozy", "crisp", "eager", "gentle",
	"golden", "happy", "hidden", "kind", "lively", "lucid", "merry", "nimble",
	"quiet", "rapid", "ready", "silver", "smooth", "steady", "sunny", "swift",
	"tidy", "vivid", "warm", "wise", "bright", "fresh", "playful", "soft",
}

var cellNameNouns = [...]string{
	"badger", "beacon", "brook", "cedar", "comet", "coral", "dune", "ember",
	"fern", "fox", "grove", "harbor", "heron", "hill", "iris", "lake",
	"maple", "meadow", "moon", "otter", "pine", "river", "robin", "sparrow",
	"stone", "summit", "tiger", "valley", "willow", "wren", "orchid", "pebble",
}

func nextCellAutoname(nb *notebook.Notebook, pipelineAssetNames map[string]bool) string {
	var random [8]byte
	seed := uint64(time.Now().UnixNano())
	if _, err := cryptorand.Read(random[:]); err == nil {
		seed = binary.LittleEndian.Uint64(random[:])
	}
	return cellAutonameFromSeed(nb, pipelineAssetNames, seed)
}

func cellAutonameFromSeed(nb *notebook.Notebook, pipelineAssetNames map[string]bool, seed uint64) string {
	total := len(cellNameAdjectives) * len(cellNameNouns)
	start := int(seed % uint64(total))
	for attempt := 0; attempt < total; attempt++ {
		index := (start + attempt) % total
		candidate := cellNameAdjectives[index/len(cellNameNouns)] + "_" + cellNameNouns[index%len(cellNameNouns)]
		if notebook.ValidateCellName(nb, candidate, "", pipelineAssetNames) == "" {
			return candidate
		}
	}

	// Exhausting all 1,024 pairs is improbable, but a suffix keeps the function
	// total and collision-safe for generated or adversarial workspaces.
	base := cellNameAdjectives[start/len(cellNameNouns)] + "_" + cellNameNouns[start%len(cellNameNouns)]
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if notebook.ValidateCellName(nb, candidate, "", pipelineAssetNames) == "" {
			return candidate
		}
	}
}

func (s *NotebookService) pipelineAssetNameSet() map[string]bool {
	names := map[string]bool{}
	if s.deps.CurrentState == nil {
		return names
	}
	for _, p := range s.deps.CurrentState().Pipelines {
		for _, asset := range p.Assets {
			names[strings.ToLower(asset.Name)] = true
		}
	}
	return names
}

func (s *NotebookService) pipelineAssetByName(name string) *model.Asset {
	if s.deps.CurrentState == nil {
		return nil
	}
	state := s.deps.CurrentState()
	for _, p := range state.Pipelines {
		for index := range p.Assets {
			if strings.EqualFold(p.Assets[index].Name, name) {
				return &p.Assets[index]
			}
		}
	}
	return nil
}

func (s *NotebookService) pushUpdate(path string) {
	if s.deps.PushWorkspaceUpdate != nil {
		s.deps.PushWorkspaceUpdate(context.Background(), "workspace.changed", path)
	}
}

// pipelineSourceFetcher resolves external cell references against pipeline
// assets: DuckDB-backed assets are imported zero-copy via ATTACH, anything
// else is fetched through its connection with a row cap. This is the
// swappable read path the cloud gateway will later implement.
type pipelineSourceFetcher struct {
	service     *NotebookService
	environment string
}

func (f *pipelineSourceFetcher) LocalDuckDBPath(_ context.Context, ref string) (string, bool) {
	asset := f.service.pipelineAssetByName(ref)
	if asset == nil || asset.Connection == "" {
		return "", false
	}

	cfg, err := loadSelectedConfig(f.service.deps.ConfigPath, f.environment)
	if err != nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return "", false
	}
	for _, connection := range cfg.SelectedEnvironment.Connections.DuckDB {
		if !strings.EqualFold(connection.Name, asset.Connection) {
			continue
		}
		path := connection.Path
		if path == "" {
			return "", false
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(f.service.deps.WorkspaceRoot, path)
		}
		return path, true
	}
	return "", false
}

func (f *pipelineSourceFetcher) Fetch(ctx context.Context, ref string, limit int) ([]string, [][]any, error) {
	asset := f.service.pipelineAssetByName(ref)
	if asset == nil || asset.Connection == "" {
		return nil, nil, notebook.ErrUnknownSource
	}
	if f.service.deps.RunConnectionQuery == nil {
		return nil, nil, fmt.Errorf("no connection query backend configured")
	}

	query := fmt.Sprintf("select * from %s limit %d", QuoteQualifiedIdentifier(asset.Name), limit)
	columns, rows, err := f.service.deps.RunConnectionQuery(ctx, asset.Connection, f.environment, query)
	if err != nil {
		return nil, nil, err
	}

	ordered := make([][]any, 0, len(rows))
	for _, row := range rows {
		values := make([]any, len(columns))
		for index, column := range columns {
			values[index] = row[column]
		}
		ordered = append(ordered, values)
	}
	return columns, ordered, nil
}
