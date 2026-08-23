package execution

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func IsTerminalExecutionUnitStatus(status string) bool {
	switch canonicalRuntimeStatus(status) {
	case "success", "failed", "cancelled", "skipped":
		return true
	default:
		return false
	}
}

func ExecutionUnitAt(plan *ExecutionPlan, position int) (ExecutionUnit, bool) {
	if plan == nil || position < 0 || position >= len(plan.Units) {
		return ExecutionUnit{}, false
	}
	unit := plan.Units[position]
	if unit.Position != position {
		return ExecutionUnit{}, false
	}
	return unit, true
}

func ExecutionUnitCompletionID(base string, position int) string {
	return fmt.Sprintf("%s/unit/%d", strings.TrimSpace(base), position)
}

func ExecutionUnitWindow(unit ExecutionUnit) (TimeWindow, error) {
	window, err := ParseTimeWindow(unit.StartDate, unit.EndDate)
	if err != nil {
		return TimeWindow{}, fmt.Errorf("planned execution unit %d: %w", unit.Position, err)
	}
	return window, nil
}

func ExecutionWasCancelled(ctx context.Context, runErr error) bool {
	return errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded) ||
		(ctx != nil && ctx.Err() != nil)
}

type AssetStepEvents struct {
	plan  *ExecutionPlan
	first map[string]int
	last  map[string]int
}

func NewAssetStepEvents(plan *ExecutionPlan) *AssetStepEvents {
	events := &AssetStepEvents{
		plan:  plan,
		first: make(map[string]int),
		last:  make(map[string]int),
	}
	if plan == nil || plan.Version < ExecutionPlanVersionV3 {
		return events
	}
	for position, unit := range plan.Units {
		if _, exists := events.first[unit.AssetName]; !exists {
			events.first[unit.AssetName] = position
		}
		events.last[unit.AssetName] = position
	}
	return events
}

// ShouldPersist keeps one durable run step per asset while execution units
// retain the status of every asset/window pair. The first window opens the
// aggregate step, the last successful window closes it, and any terminal
// failure or cancellation closes it immediately.
func (e *AssetStepEvents) ShouldPersist(event AssetEvent) (bool, error) {
	if e == nil || e.plan == nil || e.plan.Version < ExecutionPlanVersionV3 {
		return true, nil
	}
	if !event.HasUnitPosition || event.UnitPosition < 0 || event.UnitPosition >= len(e.plan.Units) {
		return false, fmt.Errorf("execution asset %s has no confirmed unit position", event.Asset)
	}
	unit := e.plan.Units[event.UnitPosition]
	if unit.AssetName != event.Asset {
		return false, fmt.Errorf(
			"execution unit %d belongs to %s, not %s",
			event.UnitPosition, unit.AssetName, event.Asset,
		)
	}
	switch canonicalRuntimeStatus(event.Status) {
	case "running", "skipped":
		return event.UnitPosition == e.first[event.Asset], nil
	case "success":
		return event.UnitPosition == e.last[event.Asset], nil
	default:
		return true, nil
	}
}

func PipelineDisplayName(name, target, workspaceRoot string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	cleaned := filepath.Clean(strings.TrimSpace(target))
	if cleaned == "." || cleaned == "" {
		workspaceName := strings.TrimSpace(filepath.Base(filepath.Clean(workspaceRoot)))
		if workspaceName != "" && workspaceName != "." {
			return workspaceName
		}
		return "workspace"
	}
	if name := strings.TrimSpace(filepath.Base(cleaned)); name != "" && name != "." {
		return name
	}
	return "pipeline"
}

func canonicalRuntimeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "ok", "finished":
		return "success"
	case "failed", "failure", "error", "errored":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "skipped":
		return "skipped"
	default:
		return "running"
	}
}
