package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	webmodel "renart/internal/web/model"
	"renart/internal/web/staleness"
)

func stalePipeline(assets ...*pipeline.Asset) *pipeline.Pipeline {
	return &pipeline.Pipeline{Assets: assets}
}

func staleAsset(name string, upstreams ...string) *pipeline.Asset {
	asset := &pipeline.Asset{Name: name}
	for _, upstream := range upstreams {
		asset.Upstreams = append(asset.Upstreams, pipeline.Upstream{Type: "asset", Value: upstream})
	}
	return asset
}

func planNames(steps []stalePlanStep) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.asset.Name)
	}
	return names
}

func TestOrderStalePlanBuildsUpstreamsFirst(t *testing.T) {
	// Declaration order is deliberately anti-topological: c depends on b
	// depends on a, but the plan and pipeline list them downstream-first.
	parsed := stalePipeline(
		staleAsset("c", "b"),
		staleAsset("b", "a"),
		staleAsset("a"),
		staleAsset("fresh_leaf", "a"),
	)
	plan := []StaleAssetPlan{{AssetName: "c"}, {AssetName: "a"}, {AssetName: "b"}}

	steps, unknown := orderStalePlan(parsed, plan)
	if len(unknown) != 0 {
		t.Fatalf("expected no unknown assets, got %v", unknown)
	}
	if got, want := planNames(steps), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected topological order %v, got %v", want, got)
	}
}

func TestOrderStalePlanReportsUnknownAndKeepsCycles(t *testing.T) {
	parsed := stalePipeline(
		staleAsset("x", "y"),
		staleAsset("y", "x"),
	)
	plan := []StaleAssetPlan{{AssetName: "x"}, {AssetName: "gone"}, {AssetName: "y"}}

	steps, unknown := orderStalePlan(parsed, plan)
	if !reflect.DeepEqual(unknown, []string{"gone"}) {
		t.Fatalf("expected unknown [gone], got %v", unknown)
	}
	// Cyclic assets never reach indegree zero but must still be built, in
	// declaration order.
	if got, want := planNames(steps), []string{"x", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected cycle members %v, got %v", want, got)
	}
}

func TestPipelineAssetsInTopologicalOrderUsesDeclarationOrderForTies(t *testing.T) {
	parsed := stalePipeline(
		staleAsset("early_downstream", "root_b"),
		staleAsset("later_downstream", "root_a"),
		staleAsset("root_a"),
		staleAsset("root_b"),
	)

	ordered := pipelineAssetsInTopologicalOrder(parsed)
	names := make([]string, 0, len(ordered))
	for _, asset := range ordered {
		names = append(names, asset.Name)
	}
	assert.Equal(
		t,
		[]string{"root_a", "later_downstream", "root_b", "early_downstream"},
		names,
	)
}

func TestFailedUpstreamForWalksTransitively(t *testing.T) {
	parsed := stalePipeline(
		staleAsset("a"),
		staleAsset("b", "a"),
		staleAsset("c", "b"),
		staleAsset("d"),
	)
	failed := map[string]bool{"a": true}

	if got := failedUpstreamFor(parsed.Assets[2], parsed, failed); got != "a" {
		t.Fatalf("expected transitive failed upstream a, got %q", got)
	}
	if got := failedUpstreamFor(parsed.Assets[3], parsed, failed); got != "" {
		t.Fatalf("expected no failed upstream for d, got %q", got)
	}
}

func TestPipelineUpstreamNamesReturnsTransitiveClosure(t *testing.T) {
	view := webmodel.Pipeline{Assets: []webmodel.Asset{
		{Name: "raw"},
		{Name: "clean", Upstreams: []string{"raw"}},
		{Name: "report", Upstreams: []string{"clean", "external.table"}},
		{Name: "unrelated"},
	}}

	upstreams, ok := PipelineUpstreamNames(view, "report")
	if !ok {
		t.Fatal("expected report to resolve")
	}
	want := map[string]struct{}{"raw": {}, "clean": {}}
	if !reflect.DeepEqual(upstreams, want) {
		t.Fatalf("expected upstream closure %v, got %v", want, upstreams)
	}
	if _, ok := PipelineUpstreamNames(view, "missing"); ok {
		t.Fatal("expected an unknown target to fail resolution")
	}
}

func TestPipelineUpstreamNamesExcludesTargetInCycle(t *testing.T) {
	view := webmodel.Pipeline{Assets: []webmodel.Asset{
		{Name: "a", Upstreams: []string{"b"}},
		{Name: "b", Upstreams: []string{"a"}},
	}}

	upstreams, ok := PipelineUpstreamNames(view, "a")
	if !ok {
		t.Fatal("expected a to resolve")
	}
	want := map[string]struct{}{"b": {}}
	if !reflect.DeepEqual(upstreams, want) {
		t.Fatalf("expected cycle-safe closure %v, got %v", want, upstreams)
	}
}

func TestBuildStalePlanFiltersToSelectedUpstreamsAndPreservesGaps(t *testing.T) {
	start := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	statuses := []staleness.AssetStatus{
		{AssetName: "fresh", Status: staleness.StatusFresh},
		{AssetName: "external", Status: staleness.StatusExternal},
		{AssetName: "sensor", Status: staleness.StatusVolatile, Volatile: true},
		{AssetName: "selected", Status: staleness.StatusPartial, Gaps: []staleness.Interval{{Start: start, End: end}}},
		{AssetName: "deployed", Status: staleness.StatusStaleDeployment},
		{AssetName: "other", Status: staleness.StatusNeverBuilt},
	}

	plan := BuildStalePlan(statuses, map[string]struct{}{"selected": {}})
	if len(plan) != 1 || plan[0].AssetName != "selected" {
		t.Fatalf("expected only selected in the plan, got %+v", plan)
	}
	if len(plan[0].Windows) != 1 || !plan[0].Windows[0].Start.Equal(start) || !plan[0].Windows[0].End.Equal(end) {
		t.Fatalf("expected the selected gap window, got %+v", plan[0].Windows)
	}
	if plan[0].Reason != "uncovered_interval" {
		t.Fatalf("expected the partial selection reason to survive planning, got %q", plan[0].Reason)
	}
	if plan := BuildStalePlan(statuses, map[string]struct{}{}); len(plan) != 0 {
		t.Fatalf("expected an empty selected set to build nothing, got %+v", plan)
	}
	if plan := BuildStalePlan(statuses, nil); len(plan) != 4 {
		t.Fatalf("expected an unrestricted plan to keep stale and volatile assets, got %+v", plan)
	} else if plan[2].Reason != "stale_deployment" {
		t.Fatalf("expected deployed-source drift to survive planning, got %+v", plan)
	}
}

func TestMaterializeStaleAssetsHoldsWorkspaceLeaseThroughPhysicalWork(t *testing.T) {
	t.Parallel()

	_, workspaceRoot := writeTypeCheckWorkspace(t, `
name: analytics
id: 0b73db88-ab55-4ed1-8f50-ef38089fc2d2
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`,
	})

	leaseHeld := false
	acquired := 0
	released := 0
	executor := &stubExecutionExecutor{
		onRunAsset: func() { assert.True(t, leaseHeld) },
	}
	svc := NewExecutionService(ExecutionDependencies{
		WorkspaceRoot: workspaceRoot,
		Executor:      executor,
		NewPipelineBuilder: func() *pipeline.Builder {
			return NewRenartPipelineBuilder(afero.NewOsFs())
		},
		FindInspectIDs: func(ids ...string) []string { return ids },
		AcquireExecutionLease: func(context.Context) (func() error, error) {
			acquired++
			leaseHeld = true
			return func() error {
				released++
				leaseHeld = false
				return nil
			}, nil
		},
	})

	result := svc.MaterializeStaleAssetsStream(
		context.Background(),
		EncodeID("analytics/pipeline.yml"),
		"default",
		[]StaleAssetPlan{{AssetName: "analytics.orders"}},
		"", "", nil, nil,
	)

	require.Equal(t, "ok", result.Status, result.Error)
	assert.Equal(t, 1, acquired)
	assert.Equal(t, 1, released)
	assert.False(t, leaseHeld)
}
