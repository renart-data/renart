package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"

	webmodel "renart/internal/web/model"
	"renart/internal/web/policy"
	"renart/internal/web/staleness"
)

// StaleAssetPlan is one stale asset to rebuild. Windows carries the uncovered
// gap intervals of a partially-covered incremental; empty means one build over
// the selection (or the pipeline's default) window.
type StaleAssetPlan struct {
	AssetName string
	Windows   []ExecutionTimeWindow
	Reason    string
}

// PipelineUpstreamNames returns the transitive in-pipeline upstream closure
// for targetAssetName. The target itself is excluded, including when a cycle
// points back to it. Unknown/URI dependencies are ignored; pipeline type-check
// reports those separately before execution.
func PipelineUpstreamNames(view webmodel.Pipeline, targetAssetName string) (map[string]struct{}, bool) {
	assetByName := make(map[string]webmodel.Asset, len(view.Assets))
	for _, asset := range view.Assets {
		assetByName[asset.Name] = asset
	}
	targetAssetName = strings.TrimSpace(targetAssetName)
	target, ok := assetByName[targetAssetName]
	if !ok {
		return nil, false
	}

	upstreams := make(map[string]struct{})
	queue := append([]string(nil), target.Upstreams...)
	for len(queue) > 0 {
		name := strings.TrimSpace(queue[0])
		queue = queue[1:]
		if name == "" || name == targetAssetName {
			continue
		}
		if _, seen := upstreams[name]; seen {
			continue
		}
		asset, exists := assetByName[name]
		if !exists {
			continue
		}
		upstreams[name] = struct{}{}
		queue = append(queue, asset.Upstreams...)
	}
	return upstreams, true
}

// BuildStalePlan translates staleness classifications into executable plan
// items. When include is non-nil, only those asset names are considered; an
// empty non-nil set therefore means there is deliberately nothing to build.
func BuildStalePlan(statuses []staleness.AssetStatus, include map[string]struct{}) []StaleAssetPlan {
	plan := make([]StaleAssetPlan, 0, len(statuses))
	for _, status := range statuses {
		if status.Status == staleness.StatusFresh || status.Status == staleness.StatusExternal {
			continue
		}
		if include != nil {
			if _, selected := include[status.AssetName]; !selected {
				continue
			}
		}
		item := StaleAssetPlan{AssetName: status.AssetName, Reason: pipelinePlanStalenessReason(status)}
		for _, gap := range status.Gaps {
			item.Windows = append(item.Windows, ExecutionTimeWindow{Start: gap.Start, End: gap.End})
		}
		plan = append(plan, item)
	}
	return plan
}

// StaleBuildEvent reports per-asset progress of a stale build stream.
type StaleBuildEvent struct {
	AssetName string `json:"asset_name"`
	Status    string `json:"status"` // "running" / "succeeded" / "failed" / "skipped"
	Step      int    `json:"step"`
	Total     int    `json:"total"`
}

// MaterializeStaleAssetsStream normalizes Build-needed work into the same
// reviewed execution-unit DAG as full and scheduled runs. Unit completion,
// freshness, cancellation, resource gates, and dependency-scoped failure are
// therefore owned by one execution path.
func (s *ExecutionService) MaterializeStaleAssetsStream(
	ctx context.Context,
	pipelineID, environment string,
	plan []StaleAssetPlan,
	startDate, endDate string,
	onChunk func([]byte),
	onEvent func(StaleBuildEvent),
) MaterializeResult {
	policyRequest := policy.RunRequest{Environment: environment, Interactive: true}
	if err := s.checkRunPolicy(policyRequest); err != nil {
		return MaterializeResult{Status: "error", Error: err.Error(), ExitCode: 1}
	}

	relPipelinePath, err := DecodeID(pipelineID)
	if err != nil {
		return MaterializeResult{Status: "error", Error: "invalid pipeline id", ExitCode: 1}
	}
	operation := runOperation(relPipelinePath, pipelineID, "", environment)
	if len(plan) == 0 {
		return MaterializeResult{
			Status: "ok", Operation: operation,
			Output: "Everything is fresh; nothing to build.\n",
		}
	}

	absPipelinePath, err := NewWorkspaceResolver(s.deps.WorkspaceRoot, nil).JoinPath(relPipelinePath)
	if err != nil {
		return MaterializeResult{Status: "error", Operation: operation, Error: err.Error(), ExitCode: 1}
	}
	parsed, err := s.deps.NewPipelineBuilder().CreatePipelineFromPath(
		ctx,
		absPipelinePath,
		pipeline.WithMutate(),
	)
	if err != nil {
		return MaterializeResult{Status: "error", Operation: operation, Error: err.Error(), ExitCode: 1}
	}
	executionTime := time.Now().UTC()
	defaultWindow, err := ResolveExecutionTimeWindow(
		string(parsed.Schedule),
		startDate,
		endDate,
		executionTime,
	)
	if err != nil {
		return MaterializeResult{Status: "error", Operation: operation, Error: err.Error(), ExitCode: 1}
	}
	operation = withOperationTimeWindow(operation, defaultWindow)

	ordered, unknown := orderStalePlan(parsed, plan)
	pipelineUUID := strings.TrimSpace(parsed.LegacyID)
	units := staleBuildExecutionUnits(
		s.deps.WorkspaceRoot,
		pipelineUUID,
		ordered,
		defaultWindow,
	)
	executionPlan, err := stalePipelineExecutionPlan(parsed, units)
	if err != nil {
		return MaterializeResult{
			Status: "error", Operation: operation,
			Error: "build execution graph: " + err.Error(), ExitCode: 1,
		}
	}

	var prefix strings.Builder
	for _, name := range unknown {
		line := fmt.Sprintf("Skipping %s: not found in the pipeline.\n", name)
		prefix.WriteString(line)
		if onChunk != nil {
			onChunk([]byte(line))
		}
	}
	tracker := newStaleBuildTracker(ordered, units, onEvent)
	result := s.MaterializePipelineRun(ctx, PipelineRunSpec{
		PipelineID:    pipelineID,
		PipelineUUID:  pipelineUUID,
		Environment:   environment,
		SensorMode:    sensorModeOnce,
		StartDate:     defaultWindow.StartRFC3339(),
		EndDate:       defaultWindow.EndRFC3339(),
		ExecutionTime: executionTime.Format(time.RFC3339Nano),
		Plan:          executionPlan,
		OnUnit:        tracker.handle,
	}, onChunk, nil)
	result.Operation = operation
	result.Output = prefix.String() + result.Output
	return result
}

type stalePlanStep struct {
	asset *pipeline.Asset
	plan  StaleAssetPlan
}

type staleBuildExecutionUnit struct {
	position  int
	asset     *pipeline.Asset
	assetID   string
	assetPath string
	window    ExecutionTimeWindow
	reason    string
}

func staleBuildExecutionUnits(
	workspaceRoot, pipelineUUID string,
	ordered []stalePlanStep,
	defaultWindow ExecutionTimeWindow,
) []staleBuildExecutionUnit {
	units := make([]staleBuildExecutionUnit, 0, len(ordered))
	for _, step := range ordered {
		windows := step.plan.Windows
		if len(windows) == 0 {
			windows = []ExecutionTimeWindow{defaultWindow}
		}
		reason := strings.TrimSpace(step.plan.Reason)
		if reason == "" {
			reason = "needed"
			if len(step.plan.Windows) > 0 {
				reason = "uncovered_interval"
			}
		}
		assetID := encodePipelineAssetID(workspaceRoot, step.asset)
		if strings.TrimSpace(pipelineUUID) != "" {
			assetID = identityForPipelineAsset(
				&pipeline.Pipeline{LegacyID: strings.TrimSpace(pipelineUUID)},
				step.asset,
			)
		}
		for _, window := range windows {
			units = append(units, staleBuildExecutionUnit{
				position: len(units), asset: step.asset, assetID: assetID,
				assetPath: assetRunPathForPipelineAsset(workspaceRoot, step.asset),
				window:    window, reason: reason,
			})
		}
	}
	return units
}

func stalePipelineExecutionPlan(
	parsed *pipeline.Pipeline,
	units []staleBuildExecutionUnit,
) (*PipelineExecutionPlan, error) {
	reviewed := make([]PipelinePlanExecutionUnit, 0, len(units))
	for _, unit := range units {
		reviewed = append(reviewed, PipelinePlanExecutionUnit{
			AssetID: unit.assetID, AssetName: unit.asset.Name,
			StartDate: unit.window.StartRFC3339(), EndDate: unit.window.EndRFC3339(),
			RenderIndex: 0, Reason: unit.reason,
		})
	}
	if err := bindPipelinePlanExecutionDependencies(parsed, reviewed); err != nil {
		return nil, err
	}
	executionUnits := make([]PipelineExecutionUnit, 0, len(units))
	for position, unit := range units {
		executionUnits = append(executionUnits, PipelineExecutionUnit{
			Position: position, AssetID: unit.assetID, AssetName: unit.asset.Name,
			AssetPath: unit.assetPath,
			StartDate: unit.window.StartRFC3339(), EndDate: unit.window.EndRFC3339(),
			RenderIndex: reviewed[position].RenderIndex, Reason: unit.reason,
			DependencyPositions: append(
				[]int(nil),
				reviewed[position].DependencyPositions...,
			),
		})
	}
	return &PipelineExecutionPlan{
		Version:        PipelineExecutionPlanVersionV3,
		SelectionMode:  PipelinePlanSelectionNeeded,
		MaxActiveSteps: effectivePipelineMaxActiveSteps(parsed),
		Units:          executionUnits,
	}, nil
}

type staleBuildAssetProgress struct {
	step      int
	remaining int
	started   bool
	failed    bool
	skipped   bool
}

type staleBuildTracker struct {
	mu       sync.Mutex
	total    int
	byAsset  map[string]*staleBuildAssetProgress
	unitName map[int]string
	onEvent  func(StaleBuildEvent)
}

func newStaleBuildTracker(
	ordered []stalePlanStep,
	units []staleBuildExecutionUnit,
	onEvent func(StaleBuildEvent),
) *staleBuildTracker {
	tracker := &staleBuildTracker{
		total: len(ordered), byAsset: make(map[string]*staleBuildAssetProgress, len(ordered)),
		unitName: make(map[int]string, len(units)), onEvent: onEvent,
	}
	for index, step := range ordered {
		tracker.byAsset[step.asset.Name] = &staleBuildAssetProgress{step: index + 1}
	}
	for _, unit := range units {
		tracker.unitName[unit.position] = unit.asset.Name
		tracker.byAsset[unit.asset.Name].remaining++
	}
	return tracker
}

func (t *staleBuildTracker) handle(event PipelineExecutionUnitEvent) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	assetName := t.unitName[event.Position]
	progress := t.byAsset[assetName]
	if progress == nil {
		return fmt.Errorf("build progress references unknown execution unit %d", event.Position)
	}
	status := strings.ToLower(strings.TrimSpace(event.Status))
	if status == "running" {
		if !progress.started {
			progress.started = true
			t.emit(assetName, "running", progress.step)
		}
		return nil
	}
	if !terminalPipelineExecutionUnitStatus(status) {
		return nil
	}
	if progress.remaining > 0 {
		progress.remaining--
	}
	switch status {
	case "failed", "cancelled", "canceled":
		progress.failed = true
	case "skipped":
		progress.skipped = true
	}
	if progress.remaining > 0 {
		return nil
	}
	switch {
	case progress.failed:
		t.emit(assetName, "failed", progress.step)
	case progress.skipped:
		t.emit(assetName, "skipped", progress.step)
	default:
		t.emit(assetName, "succeeded", progress.step)
	}
	return nil
}

func (t *staleBuildTracker) emit(assetName, status string, step int) {
	if t.onEvent != nil {
		t.onEvent(StaleBuildEvent{
			AssetName: assetName, Status: status, Step: step, Total: t.total,
		})
	}
}

// orderStalePlan resolves plan entries against the parsed pipeline and orders
// them topologically (Kahn over the full dependency graph, ties broken by
// pipeline declaration order) so upstreams always build before downstreams.
func orderStalePlan(parsed *pipeline.Pipeline, plan []StaleAssetPlan) (steps []stalePlanStep, unknown []string) {
	planByName := make(map[string]StaleAssetPlan, len(plan))
	for _, item := range plan {
		planByName[item.AssetName] = item
	}

	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		assetByName[asset.Name] = asset
	}
	for _, item := range plan {
		if assetByName[item.AssetName] == nil {
			unknown = append(unknown, item.AssetName)
		}
	}

	for _, asset := range pipelineAssetsInTopologicalOrder(parsed) {
		if item, ok := planByName[asset.Name]; ok {
			steps = append(steps, stalePlanStep{asset: asset, plan: item})
		}
	}
	return steps, unknown
}

// pipelineAssetsInTopologicalOrder applies Kahn's algorithm to the full
// dependency graph. Declaration order breaks ties and is retained for cycle
// members, allowing the execution graph binder to report the cycle instead of
// silently dropping assets.
func pipelineAssetsInTopologicalOrder(parsed *pipeline.Pipeline) []*pipeline.Asset {
	if parsed == nil {
		return nil
	}
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	declarationOrder := make(map[string]int, len(parsed.Assets))
	for index, asset := range parsed.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
			declarationOrder[asset.Name] = index
		}
	}
	indegree := make(map[string]int, len(assetByName))
	downstream := make(map[string][]string)
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		indegree[asset.Name] += 0
		for _, up := range asset.Upstreams {
			upName := strings.TrimSpace(up.Value)
			if upName == "" || assetByName[upName] == nil {
				continue
			}
			indegree[asset.Name]++
			downstream[upName] = append(downstream[upName], asset.Name)
		}
	}

	queue := make([]string, 0, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		if indegree[asset.Name] == 0 {
			queue = append(queue, asset.Name)
		}
	}
	visited := make(map[string]bool, len(parsed.Assets))
	ordered := make([]*pipeline.Asset, 0, len(assetByName))
	for len(queue) > 0 {
		next := 0
		for index := 1; index < len(queue); index++ {
			if declarationOrder[queue[index]] < declarationOrder[queue[next]] {
				next = index
			}
		}
		name := queue[next]
		queue = append(queue[:next], queue[next+1:]...)
		if visited[name] {
			continue
		}
		visited[name] = true
		ordered = append(ordered, assetByName[name])
		for _, next := range downstream[name] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	// Cycles never reach indegree zero. Keep declaration-order resolution so
	// the graph binder can reject the non-topological dependency explicitly.
	for _, asset := range parsed.Assets {
		if asset != nil && !visited[asset.Name] {
			ordered = append(ordered, asset)
		}
	}
	return ordered
}

// failedUpstreamFor remains as a small graph helper used by compatibility
// tests. Runtime failure propagation is owned by executiongraph.
func failedUpstreamFor(asset *pipeline.Asset, parsed *pipeline.Pipeline, failed map[string]bool) string {
	if len(failed) == 0 {
		return ""
	}
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	for _, candidate := range parsed.Assets {
		assetByName[candidate.Name] = candidate
	}
	seen := make(map[string]bool)
	queue := []string{asset.Name}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		current := assetByName[name]
		if current == nil {
			continue
		}
		for _, up := range current.Upstreams {
			upName := strings.TrimSpace(up.Value)
			if upName == "" || seen[upName] {
				continue
			}
			seen[upName] = true
			if failed[upName] {
				return upName
			}
			queue = append(queue, upName)
		}
	}
	return ""
}
