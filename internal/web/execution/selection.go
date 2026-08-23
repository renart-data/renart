package execution

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/staleness"
)

// StaleAssetPlan is one stale asset to rebuild. Windows carries the uncovered
// intervals of a partially covered incremental; empty means one build over the
// selected default window.
type StaleAssetPlan struct {
	AssetName string
	Windows   []TimeWindow
	Reason    string
}

type SelectedPlanAsset struct {
	Asset   *pipeline.Asset
	Reasons []string
	Windows []TimeWindow
}

type OrderedStaleStep struct {
	Asset *pipeline.Asset
	Plan  StaleAssetPlan
}

// BuildStalePlan translates current staleness classifications into execution
// work. A non-nil include set is authoritative, including when it is empty.
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
		item := StaleAssetPlan{AssetName: status.AssetName, Reason: StalenessReason(status)}
		for _, gap := range status.Gaps {
			item.Windows = append(item.Windows, TimeWindow{Start: gap.Start, End: gap.End})
		}
		plan = append(plan, item)
	}
	return plan
}

// SelectPlanAssets resolves one normalized selection against the parsed DAG
// and preserves deterministic topological order.
func SelectPlanAssets(
	parsed *pipeline.Pipeline,
	req PlanSelectionRequest,
	statuses []staleness.AssetStatus,
	dataStateAvailable bool,
) ([]SelectedPlanAsset, error) {
	if parsed == nil {
		return nil, fmt.Errorf("pipeline is unavailable")
	}
	statusByName := make(map[string]staleness.AssetStatus, len(statuses))
	for _, status := range statuses {
		statusByName[status.AssetName] = status
	}

	plans := make([]StaleAssetPlan, 0, len(parsed.Assets))
	reasons := make(map[string][]string, len(parsed.Assets))
	switch req.Mode {
	case PlanSelectionNeeded:
		if !dataStateAvailable {
			return []SelectedPlanAsset{}, nil
		}
		plans = BuildStalePlan(statuses, nil)
		for _, plan := range plans {
			reasons[plan.AssetName] = []string{StalenessReason(statusByName[plan.AssetName])}
		}
	case PlanSelectionAll:
		for _, asset := range parsed.Assets {
			if asset == nil {
				continue
			}
			plans = append(plans, StaleAssetPlan{AssetName: asset.Name})
			reasons[asset.Name] = []string{"entire_pipeline"}
		}
	case PlanSelectionAsset:
		assetByName, downstream := planGraph(parsed)
		target := assetByName[req.AssetName]
		if target == nil {
			return nil, fmt.Errorf("selected asset was not found")
		}
		include := map[string]struct{}{target.Name: {}}
		reasons[target.Name] = []string{"explicit"}
		if req.Scope == "asset_with_upstreams" || req.Scope == "asset_with_upstreams_and_downstreams" {
			for name := range UpstreamClosure(target.Name, assetByName) {
				include[name] = struct{}{}
				reasons[name] = append(reasons[name], "required_upstream")
			}
		}
		if req.Scope == "asset_with_downstreams" || req.Scope == "asset_with_upstreams_and_downstreams" {
			for name := range DownstreamClosure(target.Name, downstream) {
				include[name] = struct{}{}
				reasons[name] = append(reasons[name], "selected_downstream")
			}
		}
		for name := range include {
			plans = append(plans, StaleAssetPlan{AssetName: name})
		}
	case PlanSelectionSelector, PlanSelectionSelectorNeeded:
		matched, err := pipeline.ResolveSelectorAssets(req.Selector, parsed)
		if err != nil {
			return nil, fmt.Errorf("resolve selector: %w", err)
		}
		matchedNames := make(map[string]struct{}, len(matched))
		for _, asset := range matched {
			if asset != nil {
				matchedNames[asset.Name] = struct{}{}
			}
		}
		if req.Mode == PlanSelectionSelectorNeeded {
			if !dataStateAvailable {
				return []SelectedPlanAsset{}, nil
			}
			for _, plan := range BuildStalePlan(statuses, nil) {
				if _, ok := matchedNames[plan.AssetName]; !ok {
					continue
				}
				plans = append(plans, plan)
				reasons[plan.AssetName] = []string{
					StalenessReason(statusByName[plan.AssetName]),
					"selector_match",
				}
			}
		} else {
			for _, asset := range matched {
				if asset == nil {
					continue
				}
				plans = append(plans, StaleAssetPlan{AssetName: asset.Name})
				reasons[asset.Name] = []string{"selector_match"}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported plan selection %q", req.Mode)
	}

	ordered, unknown := OrderStalePlan(parsed, plans)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("selection contains unknown assets")
	}
	items := make([]SelectedPlanAsset, 0, len(ordered))
	for _, step := range ordered {
		items = append(items, SelectedPlanAsset{
			Asset: step.Asset, Reasons: dedupeStrings(reasons[step.Asset.Name]), Windows: step.Plan.Windows,
		})
	}
	return items, nil
}

// OrderStalePlan resolves entries against a pipeline and orders them
// topologically, using declaration order for stable ties.
func OrderStalePlan(parsed *pipeline.Pipeline, plan []StaleAssetPlan) (steps []OrderedStaleStep, unknown []string) {
	if parsed == nil {
		for _, item := range plan {
			unknown = append(unknown, item.AssetName)
		}
		return nil, unknown
	}
	planByName := make(map[string]StaleAssetPlan, len(plan))
	for _, item := range plan {
		planByName[item.AssetName] = item
	}
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
		}
	}
	for _, item := range plan {
		if assetByName[item.AssetName] == nil {
			unknown = append(unknown, item.AssetName)
		}
	}
	for _, asset := range PipelineAssetsInTopologicalOrder(parsed) {
		if item, ok := planByName[asset.Name]; ok {
			steps = append(steps, OrderedStaleStep{Asset: asset, Plan: item})
		}
	}
	return steps, unknown
}

func PipelineAssetsInTopologicalOrder(parsed *pipeline.Pipeline) []*pipeline.Asset {
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
		for _, upstream := range asset.Upstreams {
			name := strings.TrimSpace(upstream.Value)
			if name == "" || assetByName[name] == nil {
				continue
			}
			indegree[asset.Name]++
			downstream[name] = append(downstream[name], asset.Name)
		}
	}
	queue := make([]string, 0, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		if asset != nil && indegree[asset.Name] == 0 {
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
		for _, downstreamName := range downstream[name] {
			indegree[downstreamName]--
			if indegree[downstreamName] == 0 {
				queue = append(queue, downstreamName)
			}
		}
	}
	for _, asset := range parsed.Assets {
		if asset != nil && !visited[asset.Name] {
			ordered = append(ordered, asset)
		}
	}
	return ordered
}

func EffectiveMaxActiveSteps(parsed *pipeline.Pipeline) int {
	if parsed == nil || parsed.MaxActiveSteps == nil || *parsed.MaxActiveSteps < 1 {
		return 1
	}
	return *parsed.MaxActiveSteps
}

// BindPlanExecutionDependencies records a stable execution-unit DAG. Multiple
// windows for one asset are chained; full selected upstreams gate the first
// window while symbolic dependencies remain lineage-only.
func BindPlanExecutionDependencies(parsed *pipeline.Pipeline, units []PlanExecutionUnit) error {
	if len(units) == 0 {
		return nil
	}
	if parsed == nil {
		return errors.New("pipeline is required")
	}
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	positionsByAsset := make(map[string][]int, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
		}
	}
	for position := range units {
		name := strings.TrimSpace(units[position].AssetName)
		if assetByName[name] == nil {
			return fmt.Errorf("execution unit %d references unknown asset %q", position, name)
		}
		positionsByAsset[name] = append(positionsByAsset[name], position)
	}
	for assetName, positions := range positionsByAsset {
		asset := assetByName[assetName]
		for index, position := range positions {
			dependencies := make([]int, 0, len(asset.Upstreams)+1)
			if index > 0 {
				dependencies = append(dependencies, positions[index-1])
			} else {
				for _, upstream := range asset.Upstreams {
					if upstream.Mode == pipeline.UpstreamModeSymbolic {
						continue
					}
					upstreamPositions := positionsByAsset[strings.TrimSpace(upstream.Value)]
					if len(upstreamPositions) > 0 {
						dependencies = append(dependencies, upstreamPositions[len(upstreamPositions)-1])
					}
				}
			}
			sort.Ints(dependencies)
			dependencies = dedupeSortedInts(dependencies)
			for _, dependency := range dependencies {
				if dependency < 0 || dependency >= position {
					return fmt.Errorf("execution unit %d has non-topological dependency %d", position, dependency)
				}
			}
			units[position].DependencyPositions = dependencies
		}
	}
	return nil
}

func StalenessReason(status staleness.AssetStatus) string {
	switch status.Status {
	case staleness.StatusStaleEdited:
		return "stale_edited"
	case staleness.StatusStaleDeployment:
		return "stale_deployment"
	case staleness.StatusStaleUpstream:
		return "stale_upstream"
	case staleness.StatusPartial:
		return "uncovered_interval"
	case staleness.StatusNeverBuilt:
		return "never_built"
	case staleness.StatusMissing:
		return "missing_output"
	case staleness.StatusVolatile:
		return "volatile_sensor"
	default:
		return string(status.Status)
	}
}

func planGraph(parsed *pipeline.Pipeline) (map[string]*pipeline.Asset, map[string][]string) {
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	downstream := make(map[string][]string)
	for _, asset := range parsed.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
		}
	}
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		for _, upstream := range asset.Upstreams {
			name := strings.TrimSpace(upstream.Value)
			if assetByName[name] != nil {
				downstream[name] = append(downstream[name], asset.Name)
			}
		}
	}
	return assetByName, downstream
}

func UpstreamClosure(name string, assets map[string]*pipeline.Asset) map[string]struct{} {
	result := make(map[string]struct{})
	queue := []string{name}
	for len(queue) > 0 {
		current := assets[queue[0]]
		queue = queue[1:]
		if current == nil {
			continue
		}
		for _, upstream := range current.Upstreams {
			upstreamName := strings.TrimSpace(upstream.Value)
			if upstreamName == name || assets[upstreamName] == nil {
				continue
			}
			if _, exists := result[upstreamName]; exists {
				continue
			}
			result[upstreamName] = struct{}{}
			queue = append(queue, upstreamName)
		}
	}
	return result
}

func DownstreamClosure(name string, downstream map[string][]string) map[string]struct{} {
	result := make(map[string]struct{})
	queue := append([]string(nil), downstream[name]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == name {
			continue
		}
		if _, exists := result[current]; exists {
			continue
		}
		result[current] = struct{}{}
		queue = append(queue, downstream[current]...)
	}
	return result
}

func dedupeSortedInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
