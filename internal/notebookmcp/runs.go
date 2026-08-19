package notebookmcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

const (
	maxRememberedRuns = 64
	runRetention      = 2 * time.Hour
	maxRunResults     = 100
	maxRunErrorBytes  = 4 << 10
)

type storedRun struct {
	id         string
	notebookID string
	status     string
	error      string
	startedAt  time.Time
	finishedAt time.Time
	results    []RunCellSummary
	cancel     context.CancelFunc
}

type runStore struct {
	root    context.Context
	backend Backend

	mu   sync.Mutex
	runs map[string]*storedRun
	now  func() time.Time
}

func newRunStore(ctx context.Context, backend Backend) *runStore {
	return &runStore{root: ctx, backend: backend, runs: map[string]*storedRun{}, now: time.Now}
}

func (s *runStore) start(notebookID string, request service.RunNotebookRequest) (*storedRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if len(s.runs) >= maxRememberedRuns {
		var oldest *storedRun
		for _, candidate := range s.runs {
			if candidate.finishedAt.IsZero() {
				continue
			}
			if oldest == nil || candidate.finishedAt.Before(oldest.finishedAt) {
				oldest = candidate
			}
		}
		if oldest != nil {
			delete(s.runs, oldest.id)
		}
	}
	if len(s.runs) >= maxRememberedRuns {
		return nil, fmt.Errorf("too many notebook runs are active; cancel or wait for one to finish")
	}
	id, err := randomOpaqueID("run")
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(s.root)
	run := &storedRun{
		id: id, notebookID: notebookID, status: "queued", startedAt: s.now().UTC(), cancel: cancel,
	}
	s.runs[id] = run
	go s.execute(runCtx, id, notebookID, request)
	clone := *run
	return &clone, nil
}

func (s *runStore) execute(ctx context.Context, runID, notebookID string, request service.RunNotebookRequest) {
	s.update(runID, func(run *storedRun) { run.status = "running" })
	result, err := s.backend.Run(ctx, notebookID, request)
	s.update(runID, func(run *storedRun) {
		run.finishedAt = s.now().UTC()
		run.cancel = nil
		switch {
		case ctx.Err() != nil || result.Status == "cancelled" || run.status == "cancelling":
			run.status = "cancelled"
		case err != nil:
			run.status = "failed"
			run.error, _ = truncateUTF8(err.Error(), maxRunErrorBytes)
		default:
			run.status = "succeeded"
			run.results = summarizeRunResults(result)
		}
	})
}

func (s *runStore) update(id string, apply func(*storedRun)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[id]; run != nil {
		apply(run)
	}
}

func (s *runStore) status(id string) (*storedRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	run := s.runs[id]
	if run == nil {
		return nil, fmt.Errorf("notebook run %q was not found or has expired", id)
	}
	clone := *run
	clone.results = append([]RunCellSummary(nil), run.results...)
	return &clone, nil
}

func (s *runStore) requestCancel(id string) (*storedRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	run := s.runs[id]
	if run == nil {
		return nil, fmt.Errorf("notebook run %q was not found or has expired", id)
	}
	if run.status == "queued" || run.status == "running" {
		run.status = "cancelling"
		if run.cancel != nil {
			run.cancel()
		}
	}
	clone := *run
	clone.results = append([]RunCellSummary(nil), run.results...)
	return &clone, nil
}

func (s *runStore) sweepLocked() {
	cutoff := s.now().Add(-runRetention)
	for id, run := range s.runs {
		if !run.finishedAt.IsZero() && run.finishedAt.Before(cutoff) {
			delete(s.runs, id)
		}
	}
}

func (s *Server) runNotebook(ctx context.Context, _ *mcp.CallToolRequest, input RunNotebookInput) (*mcp.CallToolResult, RunAcceptedOutput, error) {
	if strings.TrimSpace(input.NotebookID) == "" {
		return nil, RunAcceptedOutput{}, fmt.Errorf("notebook_id is required")
	}
	selectionCount := 0
	if input.All {
		selectionCount++
	}
	if strings.TrimSpace(input.From) != "" {
		selectionCount++
	}
	if len(input.Cells) > 0 {
		selectionCount++
	}
	if selectionCount != 1 {
		return nil, RunAcceptedOutput{}, fmt.Errorf("select exactly one of all, from, or cells")
	}
	nb, err := s.backend.Notebook(ctx, input.NotebookID)
	if err != nil {
		return nil, RunAcceptedOutput{}, err
	}
	selected, err := selectedCells(nb, input)
	if err != nil {
		return nil, RunAcceptedOutput{}, err
	}
	if !input.AllowPython {
		for _, cell := range selected {
			if cellLanguage(cell) == "python" {
				return nil, RunAcceptedOutput{}, fmt.Errorf("this selection may execute Python cell %q; set allow_python=true after reviewing its code", cell.CellID)
			}
		}
	}
	run, err := s.runs.start(input.NotebookID, service.RunNotebookRequest{
		All: input.All, From: input.From, Cells: append([]string(nil), input.Cells...),
		RefreshImports: input.RefreshSources, Environment: input.Environment,
		StartDate: input.StartDate, EndDate: input.EndDate,
	})
	if err != nil {
		return nil, RunAcceptedOutput{}, err
	}
	return nil, RunAcceptedOutput{
		SchemaVersion: SchemaVersion, RunID: run.id, NotebookID: run.notebookID, Status: run.status,
	}, nil
}

func (s *Server) cancelRun(ctx context.Context, _ *mcp.CallToolRequest, input RunInput) (*mcp.CallToolResult, RunStatusOutput, error) {
	run, err := s.runs.requestCancel(strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, RunStatusOutput{}, err
	}
	if run.status == "cancelling" {
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if cancelErr := s.backend.Cancel(cancelCtx, run.notebookID); cancelErr != nil {
			return nil, RunStatusOutput{}, cancelErr
		}
	}
	latest, err := s.runs.status(run.id)
	if err != nil {
		return nil, RunStatusOutput{}, err
	}
	return nil, runStatusOutput(latest), nil
}

func (s *Server) getRunStatus(_ context.Context, _ *mcp.CallToolRequest, input RunInput) (*mcp.CallToolResult, RunStatusOutput, error) {
	run, err := s.runs.status(strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, RunStatusOutput{}, err
	}
	return nil, runStatusOutput(run), nil
}

func selectedCells(nb model.Notebook, input RunNotebookInput) ([]model.Asset, error) {
	byID := make(map[string]model.Asset, len(nb.Cells))
	byName := make(map[string]model.Asset, len(nb.Cells))
	downstream := make(map[string][]string, len(nb.Cells))
	for _, cell := range nb.Cells {
		byID[cell.CellID] = cell
		byName[strings.ToLower(cell.Name)] = cell
	}
	for _, cell := range nb.Cells {
		for _, upstreamName := range cell.Upstreams {
			if upstream, ok := byName[strings.ToLower(upstreamName)]; ok {
				downstream[upstream.CellID] = append(downstream[upstream.CellID], cell.CellID)
			}
		}
	}
	wanted := map[string]bool{}
	if input.All {
		for id := range byID {
			wanted[id] = true
		}
	}
	if input.From != "" {
		if _, ok := byID[input.From]; !ok {
			return nil, fmt.Errorf("notebook cell %q was not found", input.From)
		}
		markDownstream(input.From, downstream, wanted)
	}
	for _, id := range input.Cells {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("notebook cell %q was not found", id)
		}
		wanted[id] = true
	}
	// The runtime may need missing upstream objects. Conservatively include all
	// ancestors for the Python approval check even when a cached object exists.
	var markAncestors func(string)
	markAncestors = func(id string) {
		cell := byID[id]
		for _, upstreamName := range cell.Upstreams {
			upstream, ok := byName[strings.ToLower(upstreamName)]
			if !ok || wanted[upstream.CellID] {
				continue
			}
			wanted[upstream.CellID] = true
			markAncestors(upstream.CellID)
		}
	}
	selectedIDs := make([]string, 0, len(wanted))
	for id := range wanted {
		selectedIDs = append(selectedIDs, id)
	}
	for _, id := range selectedIDs {
		markAncestors(id)
	}
	result := make([]model.Asset, 0, len(wanted))
	for _, cell := range nb.Cells {
		if wanted[cell.CellID] {
			result = append(result, cell)
		}
	}
	return result, nil
}

func markDownstream(id string, downstream map[string][]string, wanted map[string]bool) {
	if wanted[id] {
		return
	}
	wanted[id] = true
	for _, child := range downstream[id] {
		markDownstream(child, downstream, wanted)
	}
}

func summarizeRunResults(result service.RunNotebookResult) []RunCellSummary {
	limit := min(len(result.Results), maxRunResults)
	summaries := make([]RunCellSummary, 0, limit)
	for index := 0; index < limit; index++ {
		cell := result.Results[index]
		errorMessage, _ := truncateUTF8(cell.Error, maxRunErrorBytes)
		summaries = append(summaries, RunCellSummary{
			CellID: cell.CellID, Name: cell.Name, Status: cell.Status, Error: errorMessage,
			Columns: resultColumns(cell.Columns, cell.ColumnTypes), RowCount: cell.TotalRows,
			Sampled: cell.Sampled, DurationMS: cell.DurationMS,
		})
	}
	return summaries
}

func runStatusOutput(run *storedRun) RunStatusOutput {
	result := RunStatusOutput{
		SchemaVersion: SchemaVersion, RunID: run.id, NotebookID: run.notebookID,
		Status: run.status, Error: run.error, Results: append([]RunCellSummary(nil), run.results...),
	}
	if !run.startedAt.IsZero() {
		result.StartedAt = run.startedAt.Format(time.RFC3339)
	}
	if !run.finishedAt.IsZero() {
		result.FinishedAt = run.finishedAt.Format(time.RFC3339)
	}
	return result
}
