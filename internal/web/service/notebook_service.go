package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/notebookdoc"
	"renart/internal/web/presentation"
)

// NotebookDependencies wires the notebook service into the rest of the app.
type NotebookDependencies struct {
	WorkspaceRoot           string
	ConfigPath              string
	DisableFilesystemAccess bool
	SnapshotMaxBytes        int64
	SnapshotTimeout         time.Duration
	// CurrentState returns the latest workspace state (for pipeline asset
	// lookups when importing upstream data).
	CurrentState func() model.WorkspaceState
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
	deps      NotebookDependencies
	store     *notebook.SessionStore
	documents *notebookdoc.Service

	// runtimes holds per-notebook recompute state (staleness, last results,
	// the auto-recompute toggle) for server-driven auto-recompute.
	runtimes *notebookRuntimes
}

// renameTables rewrites cell table references as lossless Golyglot source
// edits. It is concurrency-safe and does not start a Python parser process.
func (s *NotebookService) renameTables(sqlText, dialect string, mapping map[string]string) (string, error) {
	return sqlintelligence.RenameTables(sqlText, dialect, mapping)
}

// validateNotebookPythonQuery applies the same single-SELECT boundary as the
// ordinary Python broker without starting an embedded CPython runtime.
func (s *NotebookService) validateNotebookPythonQuery(sqlText string) error {
	isSelect, err := sqlintelligence.IsReadOnlySingleQuery(sqlText, "duckdb")
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

// usedTables extracts referenced tables for dependency derivation.
func (s *NotebookService) usedTables(sqlText, assetType string) ([]string, error) {
	dialect, dialectErr := bruincompat.AssetTypeToDialect(pipeline.AssetType(assetType))
	if dialectErr != nil || dialect == "" {
		dialect = "duckdb"
	}
	return sqlintelligence.UsedTables(sqlText, dialect)
}

// NewNotebookService constructs the service; session DBs live under
// .renart/notebooks in the workspace.
func NewNotebookService(deps NotebookDependencies) *NotebookService {
	if strings.TrimSpace(deps.ConfigPath) == "" {
		deps.ConfigPath = filepath.Join(deps.WorkspaceRoot, ".bruin.yml")
	}
	store := notebook.NewSessionStore(filepath.Join(deps.WorkspaceRoot, ".renart", "notebooks"), deps.WorkspaceRoot)
	store.DisableFilesystemAccess = deps.DisableFilesystemAccess
	service := &NotebookService{deps: deps, store: store, runtimes: newNotebookRuntimes()}
	service.documents = notebookdoc.New(notebookdoc.Dependencies{
		WorkspaceRoot: deps.WorkspaceRoot,
		NewLoader: func() *notebook.Loader {
			loader, _ := service.newLoader()
			return loader
		},
		ModelMetadata: func(nb *notebook.Notebook) notebookdoc.ModelMetadata {
			return notebookdoc.ModelMetadata{
				Dependencies:     readNotebookDependencies(nb.Dir),
				InstalledModules: notebookInstalledModules(notebookVenvDir(deps.WorkspaceRoot, nb.Dir)),
			}
		},
		PipelineAssetNames:        service.pipelineAssetNameSet,
		PushWorkspaceUpdate:       service.pushUpdate,
		RemoveSession:             store.Remove,
		DropCellObjects:           store.DropCellObjects,
		OnCellChanged:             service.onCellChanged,
		OnCellDeleted:             service.forgetCell,
		OnParametersChanged:       service.onNotebookParametersChanged,
		CheckVisualizations:       service.notebookVisualizationProblems,
		ValidateStorageConnection: service.validateNotebookStorageConnection,
		ResolveSourceAssetType:    service.resolveNotebookSourceAssetType,
	})
	return service
}

func (s *NotebookService) validateNotebookSourceQuery(sqlText, assetType string) error {
	dialect, dialectErr := bruincompat.AssetTypeToDialect(pipeline.AssetType(assetType))
	if dialectErr != nil || strings.TrimSpace(dialect) == "" {
		return fmt.Errorf("cannot determine the SQL dialect for notebook source type %q", assetType)
	}
	isSelect, err := sqlintelligence.IsReadOnlySingleQuery(sqlText, dialect)
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
	return s.documents.LockCell(notebookID, cellID)
}

func (s *NotebookService) lockNotebookEdit(notebookID string) func() {
	return s.documents.LockNotebook(notebookID)
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
	return notebook.NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), s.usedTables).
		WithWorkspaceRoot(s.deps.WorkspaceRoot), func() {}
}

// resolveDir maps an encoded notebook ID to its absolute folder, verifying
// it stays inside the workspace and is a notebook.
func (s *NotebookService) resolveDir(notebookID string) (string, *APIError) {
	return s.documents.ResolveDir(notebookID)
}

func (s *NotebookService) load(notebookID string) (*notebook.Notebook, *APIError) {
	return s.documents.Load(notebookID)
}

// Get returns the notebook in API shape, loaded fresh from disk.
func (s *NotebookService) Get(notebookID string) (model.Notebook, *APIError) {
	return s.documents.Get(notebookID)
}

func (s *NotebookService) toModel(nb *notebook.Notebook) model.Notebook {
	return s.documents.ToModel(nb)
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
	// RefreshImports forces explicit source snapshots and cached upstream
	// imports to be fetched again. The JSON name is retained for compatibility.
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
	requestStartedAt := time.Now()
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
	if s.reconcileRuntimeFingerprints(nb, rt) {
		rt.mu.Lock()
		auto := rt.autoRecompute
		rt.mu.Unlock()
		if auto {
			s.scheduleRecompute(notebookID, nb.UUID)
		}
	}
	existingObjects := map[string]bool{}
	selectsAll := req.All || (req.From == "" && len(req.Cells) == 0)
	if !selectsAll {
		existingObjects = s.existingNotebookCellObjects(nb.UUID)
	}
	cells, selectErr := s.selectRunCellsWithExistingObjects(nb, req, existingObjects)
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
	s.publishCachedRuntime(notebookID, nb.UUID, nil)
	defer func() {
		finishRun()
		s.publishCachedRuntime(notebookID, nb.UUID, nil)
	}()

	runEnvironment := strings.TrimSpace(req.Environment)
	if runEnvironment == "" {
		if selected, selectErr := loadSelectedConfig(s.deps.ConfigPath, ""); selectErr == nil {
			runEnvironment = selected.SelectedEnvironmentName
		}
	}
	runner := s.newRunner(renderer, runEnvironment, parameterValues)

	requestSetupMS := elapsedNotebookServiceMilliseconds(requestStartedAt)
	results, runErr := runner.RunCells(runCtx, nb, cells, notebook.RunOptions{
		RefreshImports:       req.RefreshImports,
		ReuseSourceSnapshots: selectsAll && !req.RefreshImports,
	})
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
	runtimeSyncStartedAt := time.Now()
	runtimeSyncMS := float64(0)
	requestTotalMS := float64(0)
	for index := range results {
		if results[index].Performance == nil {
			results[index].Performance = &notebook.CellRunPerformance{}
		}
		results[index].Performance.RequestSetupMS = &requestSetupMS
		results[index].Performance.RuntimeSyncMS = &runtimeSyncMS
		results[index].Performance.RequestTotalMS = &requestTotalMS
	}
	s.recordResults(rt, results)
	autoPending, delta := s.runtimeResultsDelta(nb, existingObjects, results)
	// A manual run may unblock downstream cells (their upstream is now fresh);
	// let auto-recompute pick them up if it is on.
	rt.mu.Lock()
	auto := rt.autoRecompute
	rt.mu.Unlock()
	if auto {
		s.scheduleRecompute(notebookID, nb.UUID)
	}
	runtimeSyncMS = elapsedNotebookServiceMilliseconds(runtimeSyncStartedAt)
	requestTotalMS = elapsedNotebookServiceMilliseconds(requestStartedAt)
	s.publishRuntime(notebookID, nb.UUID, autoPending, delta)

	status := "ok"
	for _, result := range results {
		if result.Status != notebook.CellRunOK {
			status = "error"
			break
		}
	}
	return RunNotebookResult{Status: status, Results: results}, nil
}

// runtimeResultsDelta recomputes the auto-pending closure and prepares the
// results delta used after a manual run so all clients converge. The caller
// publishes it after finalizing the request phase timings stored on each result.
func (s *NotebookService) runtimeResultsDelta(
	nb *notebook.Notebook,
	existingObjects map[string]bool,
	results []notebook.CellRunResult,
) ([]string, map[string]notebook.CellRunResult) {
	rt := s.runtimes.get(nb.UUID)
	closure := computeAutoRecomputeClosure(s.buildAutoCellsWithExistingObjects(nb, rt, existingObjects))
	delta := make(map[string]notebook.CellRunResult, len(results))
	for _, result := range results {
		delta[result.CellID] = result
	}
	return sortedKeys(closure), delta
}

func elapsedNotebookServiceMilliseconds(startedAt time.Time) float64 {
	return float64(time.Since(startedAt)) / float64(time.Millisecond)
}

// selectRunCells turns the request into an ordered execution list.
func (s *NotebookService) selectRunCells(nb *notebook.Notebook, req RunNotebookRequest) ([]*notebook.Cell, *APIError) {
	return s.selectRunCellsWithExistingObjects(nb, req, nil)
}

func (s *NotebookService) selectRunCellsWithExistingObjects(
	nb *notebook.Notebook,
	req RunNotebookRequest,
	existingObjects map[string]bool,
) ([]*notebook.Cell, *APIError) {
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
	if existingObjects == nil {
		existingObjects = s.existingNotebookCellObjects(nb.UUID)
	}
	for _, cell := range nb.Cells {
		if !wanted[cell.ID] {
			continue
		}
		for _, ancestor := range notebook.Ancestors(nb, cell) {
			if !wanted[ancestor.ID] && !existingObjects[notebook.CellObjectName(ancestor.ID)] {
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

func (s *NotebookService) existingNotebookCellObjects(uuid string) map[string]bool {
	existingObjects, err := s.store.ExistingCellObjects(uuid)
	if err != nil {
		return map[string]bool{}
	}
	return existingObjects
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
// else uses the same typed snapshot transport as explicit warehouse source
// cells.
type pipelineSourceFetcher struct {
	service     *NotebookService
	environment string
	transfer    notebook.NotebookTransferService
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

func (f *pipelineSourceFetcher) Snapshot(ctx context.Context, ref string) (notebook.TabularArtifact, error) {
	asset := f.service.pipelineAssetByName(ref)
	if asset == nil || asset.Connection == "" {
		return notebook.TabularArtifact{}, notebook.ErrUnknownSource
	}
	if f.transfer == nil {
		return notebook.TabularArtifact{}, fmt.Errorf("notebook typed snapshot transport is unavailable")
	}

	query := fmt.Sprintf("select * from %s", QuoteQualifiedIdentifier(asset.Name))
	assetRevision := strings.TrimSpace(asset.ContentRevision)
	if assetRevision == "" {
		assetRevision = notebook.ContentRevision(asset.Content)
	}
	definitionFingerprint := notebook.SQLSnapshotDefinitionFingerprint(
		"pipeline-asset:"+assetRevision+":"+asset.Connection+":"+f.environment,
		query,
	)
	return f.transfer.Snapshot(ctx, notebook.SnapshotRequest{
		Environment:           f.environment,
		Connection:            asset.Connection,
		Query:                 query,
		DefinitionFingerprint: definitionFingerprint,
		SourceKind:            "pipeline_asset",
		Mode:                  notebook.SnapshotModeFull,
	})
}
