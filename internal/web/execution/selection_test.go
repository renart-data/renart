package execution

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectPlanAssetsReturnsNeighborhoodInTopologicalOrder(t *testing.T) {
	parsed := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		selectionAsset("analytics.report", "analytics.orders"),
		selectionAsset("analytics.orders", "analytics.raw"),
		selectionAsset("analytics.raw"),
	}}

	selected, err := SelectPlanAssets(parsed, PlanSelectionRequest{
		Mode: PlanSelectionAsset, AssetName: "analytics.orders", Scope: "asset_with_upstreams_and_downstreams",
	}, nil, false)
	require.NoError(t, err)
	require.Len(t, selected, 3)
	assert.Equal(t, "analytics.raw", selected[0].Asset.Name)
	assert.Equal(t, []string{"required_upstream"}, selected[0].Reasons)
	assert.Equal(t, "analytics.orders", selected[1].Asset.Name)
	assert.Equal(t, []string{"explicit"}, selected[1].Reasons)
	assert.Equal(t, "analytics.report", selected[2].Asset.Name)
	assert.Equal(t, []string{"selected_downstream"}, selected[2].Reasons)
}

func TestBindPlanExecutionDependenciesChainsWindowsAndFullUpstreams(t *testing.T) {
	parsed := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		selectionAsset("analytics.up"),
		selectionAsset("analytics.down", "analytics.up"),
	}}
	units := []PlanExecutionUnit{
		{AssetName: "analytics.up"},
		{AssetName: "analytics.up"},
		{AssetName: "analytics.down"},
	}

	require.NoError(t, BindPlanExecutionDependencies(parsed, units))
	assert.Empty(t, units[0].DependencyPositions)
	assert.Equal(t, []int{0}, units[1].DependencyPositions)
	assert.Equal(t, []int{1}, units[2].DependencyPositions)
}

func selectionAsset(name string, upstreams ...string) *pipeline.Asset {
	asset := &pipeline.Asset{Name: name}
	for _, upstream := range upstreams {
		asset.Upstreams = append(asset.Upstreams, pipeline.Upstream{Type: "asset", Value: upstream})
	}
	return asset
}
