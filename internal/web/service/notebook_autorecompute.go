package service

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"renart/internal/web/notebook"
)

// The notebook recompute engine lives on the server: it owns per-notebook
// staleness and last results, and — when auto-recompute is on — recomputes the
// safe-to-run cells itself after every save, streaming results to clients over
// SSE. This replaces the per-wave save/parse/run round-trips the client used to
// orchestrate (see architecture/notebooks.md).

const (
	notebookRuntimeEventType = "notebook.runtime"
	// How long to coalesce edits before a recompute pass — short, because the
	// validation gate (not this delay) is what keeps a downstream from running
	// against an out-of-date parse.
	autoRecomputeDebounce = 200 * time.Millisecond
)

// NotebookRuntimeEvent is pushed on the workspace SSE stream whenever a
// notebook's staleness, running set, or results change.
type NotebookRuntimeEvent struct {
	Type            string         `json:"type"`
	NotebookID      string         `json:"notebook_id"`
	AutoRecompute   bool           `json:"auto_recompute"`
	ParameterValues map[string]any `json:"parameter_values"`
	// Stale is every cell that needs recomputing. AutoPending is the subset
	// auto-recompute will refresh on its own (this wave or a later one), so the
	// client shows the stale treatment only on Stale minus AutoPending.
	Stale       []string `json:"stale"`
	AutoPending []string `json:"auto_pending"`
	Running     []string `json:"running"`
	// Results carries the results that changed in this update (a delta the
	// client merges into its map).
	Results map[string]notebook.CellRunResult `json:"results,omitempty"`
}

// notebookRuntime is the in-memory recompute state for one notebook.
type notebookRuntime struct {
	mu            sync.Mutex
	stale         map[string]bool
	results       map[string]notebook.CellRunResult
	autoFailed    map[string]bool
	autoPending   map[string]bool
	autoRecompute bool
	// parameterValues are local runtime overrides resolved against the
	// Git-tracked definitions. parameterDefinitionKey detects definition edits
	// without resetting values for unrelated cell changes.
	parameterValues        map[string]any
	parameterDefinitionKey string
	// environment selects the connection environment for upstream imports in
	// auto runs (set by the client via the settings endpoint).
	environment string

	debounce           *time.Timer
	passActive         bool
	recomputeRequested bool

	// Active manual and auto runs are registered before they enter the shared
	// notebook session. The cancel endpoint cancels them and waits for their
	// done channels, so returning from Stop is a barrier: a following run can
	// acquire the session instead of racing a still-unwinding DuckDB query.
	manualRuns map[*activeNotebookRun]struct{}
	autoRun    *activeNotebookRun
	cancelling bool
	// hydrateOnce reconstructs restart-safe result summaries from the notebook
	// DuckDB exactly once for this server process. Authored edits still flow
	// through the ordinary stale/result state after hydration.
	hydrateOnce sync.Once
}

type activeNotebookRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	cells  []string
}

func newNotebookRuntime() *notebookRuntime {
	return &notebookRuntime{
		stale:         map[string]bool{},
		results:       map[string]notebook.CellRunResult{},
		autoFailed:    map[string]bool{},
		autoPending:   map[string]bool{},
		autoRecompute: true,
		manualRuns:    map[*activeNotebookRun]struct{}{},
	}
}

func (rt *notebookRuntime) beginManualRun(parent context.Context, cells []string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	run := &activeNotebookRun{cancel: cancel, done: make(chan struct{}), cells: uniqueNotebookCellIDs(cells)}

	rt.mu.Lock()
	rt.manualRuns[run] = struct{}{}
	cancelling := rt.cancelling
	rt.mu.Unlock()
	if cancelling {
		cancel()
	}

	return ctx, func() {
		cancel()
		rt.mu.Lock()
		delete(rt.manualRuns, run)
		close(run.done)
		rt.mu.Unlock()
	}
}

func (rt *notebookRuntime) beginAutoRun(parent context.Context, cells []string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	run := &activeNotebookRun{cancel: cancel, done: make(chan struct{}), cells: uniqueNotebookCellIDs(cells)}

	rt.mu.Lock()
	rt.autoRun = run
	cancelling := rt.cancelling
	rt.mu.Unlock()
	if cancelling {
		cancel()
	}

	return ctx, func() {
		cancel()
		rt.mu.Lock()
		if rt.autoRun == run {
			rt.autoRun = nil
		}
		close(run.done)
		rt.mu.Unlock()
	}
}

func uniqueNotebookCellIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// runningCellsLocked returns the union of every active manual run and the
// current auto-recompute wave. Callers must hold rt.mu. Tracking the cells on
// the active run itself keeps concurrent manual runs honest without one run
// clearing another run's state as it finishes.
func (rt *notebookRuntime) runningCellsLocked() []string {
	running := map[string]bool{}
	for run := range rt.manualRuns {
		for _, id := range run.cells {
			running[id] = true
		}
	}
	if rt.autoRun != nil {
		for _, id := range rt.autoRun.cells {
			running[id] = true
		}
	}
	return sortedKeys(running)
}

// cancelActiveRuns cancels every run currently registered for this notebook
// and waits until each one has completely unwound. Runs that enter while the
// barrier is active are cancelled too; a run entering after it returns is a
// new request and may proceed normally.
func (rt *notebookRuntime) cancelActiveRuns(ctx context.Context) error {
	rt.mu.Lock()
	rt.cancelling = true
	rt.mu.Unlock()

	for {
		rt.mu.Lock()
		runs := make([]*activeNotebookRun, 0, len(rt.manualRuns)+1)
		for run := range rt.manualRuns {
			runs = append(runs, run)
		}
		if rt.autoRun != nil {
			runs = append(runs, rt.autoRun)
		}
		if len(runs) == 0 {
			rt.cancelling = false
			rt.mu.Unlock()
			return nil
		}
		rt.mu.Unlock()

		for _, run := range runs {
			run.cancel()
		}
		for _, run := range runs {
			select {
			case <-run.done:
			case <-ctx.Done():
				rt.mu.Lock()
				rt.cancelling = false
				rt.mu.Unlock()
				return ctx.Err()
			}
		}
	}
}

// sortedKeys returns the truthy keys of a set as a sorted slice (stable event
// payloads and snapshots).
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		if set[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type notebookRuntimes struct {
	mu   sync.Mutex
	byID map[string]*notebookRuntime
}

func newNotebookRuntimes() *notebookRuntimes {
	return &notebookRuntimes{byID: map[string]*notebookRuntime{}}
}

func (m *notebookRuntimes) get(uuid string) *notebookRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.byID[uuid]
	if !ok {
		rt = newNotebookRuntime()
		m.byID[uuid] = rt
	}
	return rt
}

// autoCellInfo is the per-cell input to the eligibility computation, mirroring
// the client's AutoRecomputeCell.
type autoCellInfo struct {
	cellID         string
	stale          bool
	ranOk          bool
	isPython       bool
	isRemoteSource bool
	isSelectOnly   bool
	hasSqlError    bool
	statusLoaded   bool
	autoFailed     bool
	upstreamIDs    []string
}

func autoIsFresh(c autoCellInfo) bool { return !c.stale && c.ranOk }

// computeAutoRecomputeWave returns the stale cell ids safe to recompute in this
// wave: a clean single-SELECT cell with no errors whose upstreams are all
// already fresh. Downstreams of a stale-but-recomputable upstream wait for a
// later pass, after that upstream's new output re-validates them.
func computeAutoRecomputeWave(cells []autoCellInfo) []string {
	byID := make(map[string]autoCellInfo, len(cells))
	for _, c := range cells {
		byID[c.cellID] = c
	}
	upstreamReady := func(id string) bool {
		c, ok := byID[id]
		if !ok {
			return true // external import — always available
		}
		return autoIsFresh(c)
	}
	eligible := func(c autoCellInfo) bool {
		if !c.stale {
			return false
		}
		if c.isPython || c.isRemoteSource || !c.statusLoaded || !c.isSelectOnly || c.hasSqlError || c.autoFailed {
			return false
		}
		for _, up := range c.upstreamIDs {
			if !upstreamReady(up) {
				return false
			}
		}
		return true
	}
	var out []string
	for _, c := range cells {
		if eligible(c) {
			out = append(out, c.cellID)
		}
	}
	return out
}

// computeAutoRecomputeClosure returns every stale cell auto-recompute will
// eventually refresh (this wave or later), following the chain of recomputable
// upstreams. Used for presentation: a cell in this set is not flagged stale.
func computeAutoRecomputeClosure(cells []autoCellInfo) map[string]bool {
	byID := make(map[string]autoCellInfo, len(cells))
	for _, c := range cells {
		byID[c.cellID] = c
	}
	memo := map[string]bool{}
	visiting := map[string]bool{}
	var willRecompute func(id string) bool
	willRecompute = func(id string) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		c, ok := byID[id]
		if !ok {
			return true // external import
		}
		if visiting[id] {
			return false // cycle
		}
		if autoIsFresh(c) {
			memo[id] = true
			return true
		}
		if c.isPython || c.isRemoteSource || !c.statusLoaded || !c.isSelectOnly || c.hasSqlError || c.autoFailed {
			memo[id] = false
			return false
		}
		visiting[id] = true
		ok = true
		for _, up := range c.upstreamIDs {
			if !willRecompute(up) {
				ok = false
				break
			}
		}
		delete(visiting, id)
		memo[id] = ok
		return ok
	}
	closure := map[string]bool{}
	for _, c := range cells {
		if c.stale && willRecompute(c.cellID) {
			closure[c.cellID] = true
		}
	}
	return closure
}

// publishRuntime emits a runtime event for a notebook. autoPending is an id
// list and results is a (possibly nil) delta of changed results. The stale,
// running, and toggle state are read from the runtime so an unrelated update
// cannot accidentally make an active run look idle.
func (s *NotebookService) publishRuntime(notebookID, uuid string, autoPending []string, results map[string]notebook.CellRunResult) {
	rt := s.runtimes.get(uuid)
	rt.mu.Lock()
	// When auto-recompute is off, nothing is "pending" — stale cells stay
	// flagged for the user, since the server won't refresh them.
	if !rt.autoRecompute {
		autoPending = nil
	}
	rt.autoPending = make(map[string]bool, len(autoPending))
	for _, id := range autoPending {
		if id = strings.TrimSpace(id); id != "" {
			rt.autoPending[id] = true
		}
	}
	event := NotebookRuntimeEvent{
		Type:            notebookRuntimeEventType,
		NotebookID:      notebookID,
		AutoRecompute:   rt.autoRecompute,
		ParameterValues: cloneNotebookParameterValues(rt.parameterValues),
		Stale:           sortedKeys(rt.stale),
		AutoPending:     sortedKeys(rt.autoPending),
		Running:         rt.runningCellsLocked(),
		Results:         results,
	}
	rt.mu.Unlock()
	if s.deps.PublishEvent == nil {
		return
	}
	s.deps.PublishEvent(event)
}

// publishCurrentRuntime recomputes the pending closure before publishing a
// running-only transition. It preserves the full-snapshot SSE contract while a
// run starts or finishes instead of momentarily clearing unrelated pending
// cells in another tab.
func (s *NotebookService) publishCurrentRuntime(notebookID, uuid string) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		s.publishRuntime(notebookID, uuid, nil, nil)
		return
	}
	rt := s.runtimes.get(uuid)
	closure := computeAutoRecomputeClosure(s.buildAutoCells(nb, rt))
	s.publishRuntime(notebookID, uuid, sortedKeys(closure), nil)
}

// NotebookRuntimeSnapshot is the recompute state embedded in the notebook GET
// payload, so a freshly opened tab renders correct staleness and results.
type NotebookRuntimeSnapshot struct {
	AutoRecompute   bool                              `json:"auto_recompute"`
	ParameterValues map[string]any                    `json:"parameter_values"`
	Stale           []string                          `json:"stale"`
	AutoPending     []string                          `json:"auto_pending"`
	Running         []string                          `json:"running"`
	Results         map[string]notebook.CellRunResult `json:"results"`
}

// runtimeSnapshot returns the current recompute state for a notebook UUID,
// validating staleness to derive the auto-pending set.
func (s *NotebookService) runtimeSnapshot(nb *notebook.Notebook) NotebookRuntimeSnapshot {
	s.hydrateRuntime(nb)
	rt := s.runtimes.get(nb.UUID)

	// Eligibility inspection touches the notebook session. During an active run
	// that session is deliberately serialized, so recomputing here would make a
	// newly opened tab wait behind the query it is trying to observe or cancel.
	// Runtime events cache the last authoritative pending closure; use it for the
	// active fast path and keep this endpoint lock-free with respect to DuckDB.
	rt.mu.Lock()
	if running := rt.runningCellsLocked(); len(running) > 0 {
		snapshot := runtimeSnapshotLocked(rt, running, sortedKeys(rt.autoPending))
		rt.mu.Unlock()
		return snapshot
	}
	rt.mu.Unlock()

	cells := s.buildAutoCells(nb, rt)
	closure := computeAutoRecomputeClosure(cells)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.autoPending = closure
	autoPending := sortedKeys(rt.autoPending)
	if !rt.autoRecompute {
		autoPending = []string{}
		rt.autoPending = map[string]bool{}
	}
	return runtimeSnapshotLocked(rt, rt.runningCellsLocked(), autoPending)
}

func runtimeSnapshotLocked(rt *notebookRuntime, running, autoPending []string) NotebookRuntimeSnapshot {
	results := make(map[string]notebook.CellRunResult, len(rt.results))
	for id, result := range rt.results {
		results[id] = result
	}
	return NotebookRuntimeSnapshot{
		AutoRecompute:   rt.autoRecompute,
		ParameterValues: cloneNotebookParameterValues(rt.parameterValues),
		Stale:           sortedKeys(rt.stale),
		AutoPending:     autoPending,
		Running:         running,
		Results:         results,
	}
}

func (s *NotebookService) hydrateRuntime(nb *notebook.Notebook) {
	if nb == nil {
		return
	}
	rt := s.runtimes.get(nb.UUID)
	rt.hydrateOnce.Do(func() {
		results, stale, err := s.store.RestoreCellRunResults(context.Background(), nb, 100)
		if err != nil {
			return
		}
		rt.mu.Lock()
		defer rt.mu.Unlock()
		for cellID, result := range results {
			if _, alreadyRecorded := rt.results[cellID]; !alreadyRecorded {
				rt.results[cellID] = result
			}
		}
		for cellID := range stale {
			rt.stale[cellID] = true
		}
	})
	ensureNotebookParameterRuntime(nb, rt)
}

// forgetCell drops a deleted cell from the runtime so it leaves no ghost stale
// entry, then republishes the (now smaller) stale set.
func (s *NotebookService) forgetCell(notebookID, uuid, cellID string) {
	rt := s.runtimes.get(uuid)
	rt.mu.Lock()
	delete(rt.stale, cellID)
	delete(rt.results, cellID)
	delete(rt.autoFailed, cellID)
	rt.mu.Unlock()
	s.publishRuntime(notebookID, uuid, nil, nil)
}

// Runtime returns the recompute snapshot for a notebook (by encoded id).
func (s *NotebookService) Runtime(notebookID string) (NotebookRuntimeSnapshot, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return NotebookRuntimeSnapshot{}, apiErr
	}
	return s.runtimeSnapshot(nb), nil
}

// SetAutoRecompute updates the per-notebook toggle (and import environment).
// Turning it on triggers a pass for any already-stale cells.
func (s *NotebookService) SetAutoRecompute(notebookID string, enabled bool, environment string, parameterValues map[string]any) *APIError {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	s.hydrateRuntime(nb)
	if parameterValues != nil {
		if _, parameterErr := s.updateNotebookParameterValues(notebookID, nb, parameterValues, false); parameterErr != nil {
			return parameterErr
		}
	}
	rt := s.runtimes.get(nb.UUID)
	environment = strings.TrimSpace(environment)
	rt.mu.Lock()
	rt.autoRecompute = enabled
	previousEnvironment := strings.TrimSpace(rt.environment)
	if environment != "" {
		rt.environment = environment
	}
	changedEnvironment := environment != "" && !strings.EqualFold(previousEnvironment, environment)
	if changedEnvironment {
		for _, cell := range nb.Cells {
			if strings.TrimSpace(cell.Asset.Connection) == "" && !notebook.IsSourceCell(cell) {
				continue
			}
			// On the first settings sync after a restart, a restored snapshot from
			// the same environment remains valid. A real environment switch makes
			// the source and every downstream transform stale.
			if previousEnvironment == "" {
				if result, ok := rt.results[cell.ID]; ok && result.Snapshot != nil &&
					strings.EqualFold(strings.TrimSpace(result.Snapshot.Environment), environment) {
					continue
				}
			}
			rt.stale[cell.ID] = true
			for _, descendant := range notebook.Descendants(nb, cell) {
				rt.stale[descendant.ID] = true
			}
		}
	}
	rt.mu.Unlock()
	if enabled {
		s.scheduleRecompute(notebookID, nb.UUID)
	}
	return nil
}

// CancelRuns stops manual and auto-recompute work and waits for the notebook
// session to be released. Stale auto cells are parked until edited so the pass
// does not immediately restart work the user explicitly stopped.
func (s *NotebookService) CancelRuns(ctx context.Context, notebookID string) *APIError {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	rt := s.runtimes.get(nb.UUID)
	rt.mu.Lock()
	for id := range rt.stale {
		rt.autoFailed[id] = true
	}
	rt.mu.Unlock()
	if err := rt.cancelActiveRuns(ctx); err != nil {
		return &APIError{
			Status:  http.StatusRequestTimeout,
			Code:    "notebook_cancel_interrupted",
			Message: "notebook cancellation was interrupted before the active run stopped",
		}
	}
	s.publishRuntime(notebookID, nb.UUID, nil, nil)
	return nil
}
