package service

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"
)

// base (stale, clean) -> doubled (stale, clean): only base runs this wave.
func TestComputeAutoRecomputeWaveOnlyReadyUpstreams(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "base", stale: true, isSelectOnly: true, statusLoaded: true},
		{cellID: "doubled", stale: true, isSelectOnly: true, statusLoaded: true, upstreamIDs: []string{"base"}},
	}
	got := computeAutoRecomputeWave(cells)
	if !reflect.DeepEqual(got, []string{"base"}) {
		t.Fatalf("expected only base in wave, got %v", got)
	}
}

// Once base is fresh, doubled (clean) becomes eligible.
func TestComputeAutoRecomputeWaveDownstreamAfterUpstreamFresh(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "base", stale: false, ranOk: true, isSelectOnly: true, statusLoaded: true},
		{cellID: "doubled", stale: true, isSelectOnly: true, statusLoaded: true, upstreamIDs: []string{"base"}},
	}
	got := computeAutoRecomputeWave(cells)
	if !reflect.DeepEqual(got, []string{"doubled"}) {
		t.Fatalf("expected doubled in wave, got %v", got)
	}
}

// A downstream with a SQL error is never run and is not in the closure (so it
// stays flagged stale); the clean upstream is.
func TestComputeAutoRecomputeBreakingDownstream(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "base", stale: false, ranOk: true, isSelectOnly: true, statusLoaded: true},
		{cellID: "broken", stale: true, isSelectOnly: true, statusLoaded: true, hasSqlError: true, upstreamIDs: []string{"base"}},
	}
	if got := computeAutoRecomputeWave(cells); len(got) != 0 {
		t.Fatalf("expected empty wave, got %v", got)
	}
	closure := computeAutoRecomputeClosure(cells)
	if closure["broken"] {
		t.Fatalf("broken cell must not be in the auto-pending closure")
	}
}

// The closure spans a clean chain even when middle cells are still stale.
func TestComputeAutoRecomputeClosureSpansChain(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "a", stale: true, isSelectOnly: true, statusLoaded: true},
		{cellID: "b", stale: true, isSelectOnly: true, statusLoaded: true, upstreamIDs: []string{"a"}},
		{cellID: "c", stale: true, isSelectOnly: true, statusLoaded: true, upstreamIDs: []string{"b"}},
		{cellID: "py", stale: true, isPython: true, upstreamIDs: []string{"c"}},
	}
	closure := computeAutoRecomputeClosure(cells)
	got := make([]string, 0, len(closure))
	for id := range closure {
		got = append(got, id)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("expected a,b,c in closure (py excluded), got %v", got)
	}
}

// A Python cell and an unloaded cell are never eligible.
func TestComputeAutoRecomputeWaveExcludesPythonAndUnloaded(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "py", stale: true, isPython: true},
		{cellID: "unloaded", stale: true, isSelectOnly: true, statusLoaded: false},
		{cellID: "notselect", stale: true, isSelectOnly: false, statusLoaded: true},
		{cellID: "failed", stale: true, isSelectOnly: true, statusLoaded: true, autoFailed: true},
	}
	if got := computeAutoRecomputeWave(cells); len(got) != 0 {
		t.Fatalf("expected empty wave, got %v", got)
	}
}

func TestComputeAutoRecomputeNeverRefreshesRemoteSourcesImplicitly(t *testing.T) {
	cells := []autoCellInfo{
		{cellID: "source", stale: true, isRemoteSource: true, isSelectOnly: true, statusLoaded: true},
		{cellID: "local", stale: true, isSelectOnly: true, statusLoaded: true, upstreamIDs: []string{"source"}},
	}
	if got := computeAutoRecomputeWave(cells); len(got) != 0 {
		t.Fatalf("remote source refresh must require an explicit run, got wave %v", got)
	}
	if closure := computeAutoRecomputeClosure(cells); len(closure) != 0 {
		t.Fatalf("remote source and blocked downstream must remain user-visible stale, got %v", closure)
	}
}

func TestScheduleRecomputeRecordsWakeupWhilePassIsActive(t *testing.T) {
	svc := &NotebookService{runtimes: newNotebookRuntimes()}
	rt := svc.runtimes.get("notebook-uuid")
	rt.mu.Lock()
	rt.passActive = true
	rt.mu.Unlock()

	svc.scheduleRecompute("notebook-id", "notebook-uuid")

	deadline := time.Now().Add(2 * time.Second)
	for {
		rt.mu.Lock()
		requested := rt.recomputeRequested
		rt.mu.Unlock()
		if requested {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recompute request was dropped while another pass was active")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNotebookRuntimeCancelActiveRunsWaitsForRelease(t *testing.T) {
	rt := newNotebookRuntime()
	manualCtx, finishManual := rt.beginManualRun(context.Background(), []string{"manual-cell"})
	autoCtx, finishAuto := rt.beginAutoRun(context.Background(), []string{"auto-cell"})

	manualCancelled := make(chan struct{})
	autoCancelled := make(chan struct{})
	releaseManual := make(chan struct{})
	releaseAuto := make(chan struct{})
	go func() {
		<-manualCtx.Done()
		close(manualCancelled)
		<-releaseManual
		finishManual()
	}()
	go func() {
		<-autoCtx.Done()
		close(autoCancelled)
		<-releaseAuto
		finishAuto()
	}()

	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cancelled := make(chan error, 1)
	go func() { cancelled <- rt.cancelActiveRuns(cancelCtx) }()

	for name, observed := range map[string]<-chan struct{}{
		"manual": manualCancelled,
		"auto":   autoCancelled,
	} {
		select {
		case <-observed:
		case <-time.After(time.Second):
			t.Fatalf("%s run was not cancelled", name)
		}
	}
	select {
	case err := <-cancelled:
		t.Fatalf("cancel returned before either run released the session: %v", err)
	default:
	}

	close(releaseManual)
	select {
	case err := <-cancelled:
		t.Fatalf("cancel returned while the auto run was still active: %v", err)
	default:
	}

	close(releaseAuto)
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("cancel failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not return after both runs released the session")
	}
}

func TestNotebookRuntimeTracksActiveCellUnion(t *testing.T) {
	rt := newNotebookRuntime()
	_, finishFirst := rt.beginManualRun(context.Background(), []string{"shared", "manual", "manual"})
	_, finishSecond := rt.beginManualRun(context.Background(), []string{"second"})
	_, finishAuto := rt.beginAutoRun(context.Background(), []string{"shared", "auto"})

	rt.mu.Lock()
	running := rt.runningCellsLocked()
	rt.mu.Unlock()
	if !reflect.DeepEqual(running, []string{"auto", "manual", "second", "shared"}) {
		t.Fatalf("unexpected active cell union: %v", running)
	}

	finishFirst()
	rt.mu.Lock()
	running = rt.runningCellsLocked()
	rt.mu.Unlock()
	if !reflect.DeepEqual(running, []string{"auto", "second", "shared"}) {
		t.Fatalf("finishing one manual run cleared another active run: %v", running)
	}

	finishSecond()
	finishAuto()
	rt.mu.Lock()
	running = rt.runningCellsLocked()
	rt.mu.Unlock()
	if len(running) != 0 {
		t.Fatalf("finished runs remained active: %v", running)
	}
}
