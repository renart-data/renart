package service

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"renart/internal/web/notebook"
)

// onCellChanged is called after a cell is saved. It marks the cell and its
// descendants stale and (when auto-recompute is on) kicks off a debounced
// recompute pass. Safe to call for any mutation that changes a cell's content.
func (s *NotebookService) onCellChanged(notebookID string, nb *notebook.Notebook, cellID string) {
	s.onCellsChanged(notebookID, nb, []string{cellID})
}

// onCellsChanged marks multiple authored execution roots and their descendants
// in one runtime transition. Dependency-file changes use this path for every
// Python cell without publishing a separate transient state per cell.
func (s *NotebookService) onCellsChanged(notebookID string, nb *notebook.Notebook, cellIDs []string) {
	s.hydrateRuntime(nb)
	rt := s.runtimes.get(nb.UUID)

	closure := make(map[string]bool, len(cellIDs))
	for _, cellID := range cellIDs {
		cell := nb.CellByID(cellID)
		if cell == nil {
			continue
		}
		closure[cellID] = true
		for _, descendant := range notebook.Descendants(nb, cell) {
			closure[descendant.ID] = true
		}
	}
	if len(closure) == 0 {
		return
	}

	rt.mu.Lock()
	parameterValues := cloneNotebookParameterValues(rt.parameterValues)
	for _, cellID := range cellIDs {
		if cell := nb.CellByID(cellID); cell != nil {
			rt.authoredFingerprints[cellID] = notebook.CellFingerprintWithParameters(nb, cell, parameterValues)
		}
	}
	for id := range closure {
		rt.stale[id] = true
		// A fresh edit gives the whole affected subgraph another auto attempt.
		delete(rt.autoFailed, id)
	}
	autoRun := rt.autoRun
	auto := rt.autoRecompute
	// Optimistically treat all stale cells as auto-pending so an edit does not
	// flash the stale treatment; the pass demotes any that won't refresh
	// (Python, non-SELECT, errors) within a debounce.
	optimisticPending := sortedKeys(rt.stale)
	rt.mu.Unlock()

	// Interrupt any wave in flight so the pass re-loops against the new content.
	if autoRun != nil {
		autoRun.cancel()
	}

	s.publishRuntime(notebookID, nb.UUID, optimisticPending, nil)
	if auto {
		s.scheduleRecompute(notebookID, nb.UUID)
	}
}

// scheduleRecompute (re)arms the debounce timer; when it fires it ensures a
// single recompute pass is running for the notebook.
func (s *NotebookService) scheduleRecompute(notebookID, uuid string) {
	rt := s.runtimes.get(uuid)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.debounce != nil {
		rt.debounce.Stop()
	}
	rt.debounce = time.AfterFunc(autoRecomputeDebounce, func() {
		rt.mu.Lock()
		rt.debounce = nil
		if !rt.autoRecompute {
			rt.mu.Unlock()
			return
		}
		if rt.passActive {
			// Do not lose the edit when an older pass is just about to park
			// because the previous SQL was invalid. The active pass consumes
			// this wake-up before it exits.
			rt.recomputeRequested = true
			rt.mu.Unlock()
			return
		}
		rt.passActive = true
		rt.recomputeRequested = false
		rt.mu.Unlock()
		s.runRecomputePass(notebookID, uuid)
	})
}

// runRecomputePass recomputes safe cells wave by wave until none remain,
// streaming results to clients. It assumes rt.passActive is already set; it
// clears it (atomically with the stale check) before returning.
func (s *NotebookService) runRecomputePass(notebookID, uuid string) {
	rt := s.runtimes.get(uuid)
	for {
		rt.mu.Lock()
		if !rt.autoRecompute || len(rt.stale) == 0 {
			rt.passActive = false
			rt.recomputeRequested = false
			rt.mu.Unlock()
			s.publishRuntime(notebookID, uuid, nil, nil)
			return
		}
		rt.mu.Unlock()

		nb, apiErr := s.load(notebookID)
		if apiErr != nil {
			rt.mu.Lock()
			rt.passActive = false
			rt.recomputeRequested = false
			rt.mu.Unlock()
			return
		}

		existingObjects := s.existingNotebookCellObjects(nb.UUID)
		cells := s.buildAutoCellsWithExistingObjects(nb, rt, existingObjects)
		closure := computeAutoRecomputeClosure(cells)
		wave := computeAutoRecomputeWave(cells)
		if len(wave) == 0 {
			s.publishRuntime(notebookID, uuid, sortedKeys(closure), nil)
			rt.mu.Lock()
			if rt.recomputeRequested {
				rt.recomputeRequested = false
				rt.mu.Unlock()
				continue
			}
			rt.passActive = false
			rt.mu.Unlock()
			return
		}

		runCtx, finishRun := rt.beginAutoRun(context.Background(), wave)
		s.publishRuntime(notebookID, uuid, sortedKeys(closure), nil)
		results, cancelled := s.runAutoWave(runCtx, uuid, nb, wave, existingObjects)
		finishRun()
		if cancelled {
			// Superseded by an edit; loop with the new state.
			s.publishCurrentRuntime(notebookID, uuid)
			continue
		}
		if len(results) == 0 {
			// The wave failed wholesale (runner error, no per-cell results).
			// Park its cells like individual failures, otherwise the next
			// iteration recomputes the identical wave and loops forever.
			rt.mu.Lock()
			for _, id := range wave {
				rt.autoFailed[id] = true
			}
			rt.mu.Unlock()
			continue
		}
		s.recordResults(rt, results)
		delta := make(map[string]notebook.CellRunResult, len(results))
		for _, result := range results {
			delta[result.CellID] = result
		}
		s.publishRuntime(notebookID, uuid, sortedKeys(closure), delta)
	}
}

// runAutoWave executes the given cell ids in one session pass with a cancellable
// context (stored so an edit can interrupt it). cancelled is true when the wave
// was interrupted rather than completing.
func (s *NotebookService) runAutoWave(
	ctx context.Context,
	uuid string,
	nb *notebook.Notebook,
	wave []string,
	existingObjects map[string]bool,
) (results []notebook.CellRunResult, cancelled bool) {
	rt := s.runtimes.get(uuid)
	rt.mu.Lock()
	environment := rt.environment
	parameterValues := cloneNotebookParameterValues(rt.parameterValues)
	rt.mu.Unlock()

	cells, selectErr := s.selectRunCellsWithExistingObjects(nb, RunNotebookRequest{Cells: wave}, existingObjects)
	if selectErr != nil || len(cells) == 0 {
		return nil, false
	}
	renderer, renderErr := s.newNotebookJinjaRenderer("", "", nb.Parameters, parameterValues)
	if renderErr != nil {
		return nil, false
	}

	runner := s.newRunner(renderer, environment, parameterValues)
	results, runErr := runner.RunCells(ctx, nb, cells, notebook.RunOptions{})
	if ctx.Err() != nil {
		return nil, true
	}
	if runErr != nil {
		return nil, false
	}
	return results, false
}

// recordResults folds a wave's results into the runtime: ok cells become fresh,
// failed/blocked cells stay stale and are remembered so they are not retried
// until edited.
func (s *NotebookService) recordResults(rt *notebookRuntime, results []notebook.CellRunResult) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, result := range results {
		rt.results[result.CellID] = result
		if result.Fingerprint != "" {
			rt.authoredFingerprints[result.CellID] = result.Fingerprint
		}
		if result.Status == notebook.CellRunOK {
			delete(rt.stale, result.CellID)
			delete(rt.autoFailed, result.CellID)
		} else {
			rt.stale[result.CellID] = true
			rt.autoFailed[result.CellID] = true
		}
	}
}

// buildAutoCells assembles the eligibility inputs for every cell, validating the
// SQL of each stale, non-Python cell against its siblings' current output
// columns (the same parse-context the editor uses — so a broken upstream change
// surfaces as an error and blocks the downstream).
func (s *NotebookService) buildAutoCells(nb *notebook.Notebook, rt *notebookRuntime) []autoCellInfo {
	return s.buildAutoCellsWithExistingObjects(nb, rt, s.existingNotebookCellObjects(nb.UUID))
}

func (s *NotebookService) buildAutoCellsWithExistingObjects(
	nb *notebook.Notebook,
	rt *notebookRuntime,
	existingObjects map[string]bool,
) []autoCellInfo {
	rt.mu.Lock()
	stale := make(map[string]bool, len(rt.stale))
	for id := range rt.stale {
		stale[id] = rt.stale[id]
	}
	ranOk := map[string]bool{}
	hasResult := map[string]bool{}
	for id, result := range rt.results {
		hasResult[id] = true
		ranOk[id] = result.Status == notebook.CellRunOK
	}
	autoFailed := make(map[string]bool, len(rt.autoFailed))
	for id := range rt.autoFailed {
		autoFailed[id] = rt.autoFailed[id]
	}
	rt.mu.Unlock()

	nameToID := map[string]string{}
	for _, cell := range nb.Cells {
		nameToID[strings.ToLower(cell.Asset.Name)] = cell.ID
	}

	// The runtime maps are in-memory only. After a server restart a cell that
	// materialized in an earlier session has no result entry, so its downstreams
	// would never become eligible (their upstream reads as never-run) and an
	// edited cell could never recompute. A persisted session object from a
	// previous run counts as fresh — the same trust the manual-run path applies
	// when it skips ancestors whose objects already exist.
	out := make([]autoCellInfo, 0, len(nb.Cells))
	for _, cell := range nb.Cells {
		isPython := notebook.IsPythonCell(cell)
		info := autoCellInfo{
			cellID:         cell.ID,
			stale:          stale[cell.ID],
			ranOk:          ranOk[cell.ID] || (!hasResult[cell.ID] && existingObjects[notebook.CellObjectName(cell.ID)]),
			isPython:       isPython,
			isRemoteSource: strings.TrimSpace(cell.Asset.Connection) != "" || notebook.IsSourceCell(cell),
			autoFailed:     autoFailed[cell.ID],
		}
		for _, upstream := range cell.Asset.Upstreams {
			if id, ok := nameToID[strings.ToLower(upstream.Value)]; ok {
				info.upstreamIDs = append(info.upstreamIDs, id)
			}
		}
		// Only stale, non-Python cells need a validity verdict (a fresh upstream
		// short-circuits eligibility; Python is never auto-run).
		if info.stale && !isPython && !info.isRemoteSource {
			selectOnly, hasErr, ok := s.validateCellSQL(nb, cell, rt)
			info.statusLoaded = ok
			info.isSelectOnly = selectOnly
			info.hasSqlError = hasErr
		}
		out = append(out, info)
	}
	return out
}

// validateCellSQL runs the shared parse-context validator for a cell against the
// current sibling output columns. ok is false when validation could not run.
func (s *NotebookService) validateCellSQL(nb *notebook.Notebook, cell *notebook.Cell, rt *notebookRuntime) (isSelectOnly, hasErr, ok bool) {
	if s.deps.ValidateSQL == nil {
		return false, false, false
	}
	assetID := s.cellAssetID(cell)
	if assetID == "" {
		return false, false, false
	}
	rt.mu.Lock()
	parameterValues := cloneNotebookParameterValues(rt.parameterValues)
	rt.mu.Unlock()
	renderer, err := s.newNotebookJinjaRenderer("", "", nb.Parameters, parameterValues)
	if err != nil {
		return false, false, false
	}
	renderedSQL, err := renderer.Render(cell.Asset.ExecutableFile.Content)
	if err != nil {
		return false, false, false
	}
	schemaTables := s.buildCellSchemaTables(nb, cell, rt)
	result, apiErr := s.deps.ValidateSQL(context.Background(), assetID, renderedSQL, schemaTables)
	if apiErr != nil {
		return false, false, false
	}
	hasErr = len(result.Errors) > 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity == "error" || diagnostic.Severity == "fatal" {
			hasErr = true
			break
		}
	}
	// Auto-recompute any read-only result query (SELECT, CTE, or a UNION/
	// INTERSECT/EXCEPT set operation) — not just a plain single SELECT. They are
	// side-effect-free and the runner wraps them in a view anyway.
	return result.IsReadOnlyResult, hasErr, true
}

// buildCellSchemaTables describes the tables a cell can read: every sibling cell
// (its latest run columns when available, else its declared columns) and the
// cell's external references. Pipeline assets contribute their declared schema
// so valid cross-pipeline columns do not look unresolved during the safety check.
func (s *NotebookService) buildCellSchemaTables(nb *notebook.Notebook, cell *notebook.Cell, rt *notebookRuntime) []ParseContextSchemaTable {
	rt.mu.Lock()
	resultsCopy := make(map[string]notebook.CellRunResult, len(rt.results))
	for id, result := range rt.results {
		if result.Status == notebook.CellRunOK && len(result.Columns) > 0 {
			resultsCopy[id] = result
		}
	}
	rt.mu.Unlock()

	tables := make([]ParseContextSchemaTable, 0, len(nb.Cells))
	seen := map[string]bool{}
	for _, sibling := range nb.Cells {
		if sibling.ID == cell.ID {
			continue
		}
		name := sibling.Asset.Name
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		var columns []ParseContextSchemaColumn
		if runResult, ok := resultsCopy[sibling.ID]; ok {
			for index, column := range runResult.Columns {
				columnType := ""
				if index < len(runResult.ColumnTypes) {
					columnType = runResult.ColumnTypes[index]
				}
				columns = append(columns, ParseContextSchemaColumn{Name: column, Type: columnType, SourceMethods: []string{"notebook-run"}})
			}
		} else {
			for _, column := range sibling.Asset.Columns {
				columns = append(columns, ParseContextSchemaColumn{Name: column.Name, Type: column.SQLType()})
			}
		}
		tables = append(tables, ParseContextSchemaTable{Name: name, Columns: columns})
	}
	for _, ref := range cell.ExternalRefs {
		if ref == "" || seen[strings.ToLower(ref)] {
			continue
		}
		seen[strings.ToLower(ref)] = true
		var columns []ParseContextSchemaColumn
		if asset := s.pipelineAssetByName(ref); asset != nil {
			columns = make([]ParseContextSchemaColumn, 0, len(asset.Columns))
			for index := range asset.Columns {
				column := &asset.Columns[index]
				columns = append(columns, ParseContextSchemaColumn{Name: column.Name, Type: column.Type})
			}
		}
		tables = append(tables, ParseContextSchemaTable{Name: ref, Columns: columns})
	}
	return tables
}

// cellAssetID encodes a cell file path into the asset id the parse-context
// resolver expects (a workspace-relative, slash-encoded path).
func (s *NotebookService) cellAssetID(cell *notebook.Cell) string {
	rel, err := filepath.Rel(s.deps.WorkspaceRoot, cell.Path)
	if err != nil {
		return ""
	}
	return EncodeID(filepath.ToSlash(rel))
}

// newRunner builds a cell runner sharing the service's parser, store, fetcher,
// and Python materializer.
func (s *NotebookService) newRunner(renderer *jinja.Renderer, environment string, parameterValues map[string]any) *notebook.Runner {
	transfer := &slingNotebookTransferService{
		workspaceRoot:        s.deps.WorkspaceRoot,
		configPath:           s.deps.ConfigPath,
		newConnectionManager: s.deps.NewConnectionManager,
		maxBytes:             s.deps.SnapshotMaxBytes,
		timeout:              s.deps.SnapshotTimeout,
	}
	var renderSQL func(string) (string, error)
	if renderer != nil {
		renderSQL = renderer.Render
	}
	return &notebook.Runner{
		Store:              s.store,
		RenameTables:       s.renameTables,
		RenderSQL:          renderSQL,
		Fetcher:            &pipelineSourceFetcher{service: s, environment: environment, transfer: transfer},
		WarehouseExecutor:  &warehouseSQLSourceExecutor{transfer: transfer, validate: s.validateNotebookSourceQuery},
		SourceExecutor:     &notebookSourceExecutor{transfer: transfer, renderer: renderer},
		Environment:        environment,
		PythonMaterializer: s.materializePythonCell,
		ParameterValues:    cloneNotebookParameterValues(parameterValues),
	}
}
