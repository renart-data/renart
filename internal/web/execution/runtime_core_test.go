package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionUnitWindowValidatesReviewedInterval(t *testing.T) {
	start := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	window, err := ExecutionUnitWindow(ExecutionUnit{
		Position: 2, StartDate: start.Format(time.RFC3339Nano),
		EndDate: start.Add(time.Hour).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	assert.Equal(t, start, window.Start)
	assert.Equal(t, start.Add(time.Hour), window.End)

	_, err = ExecutionUnitWindow(ExecutionUnit{Position: 2, StartDate: "invalid", EndDate: "invalid"})
	require.ErrorContains(t, err, "planned execution unit 2")
}

func TestAssetStepEventsCollapsesRepeatedAssetWindows(t *testing.T) {
	plan := &ExecutionPlan{Version: ExecutionPlanVersionV3, Units: []ExecutionUnit{
		{Position: 0, AssetName: "analytics.orders"},
		{Position: 1, AssetName: "analytics.orders"},
	}}
	events := NewAssetStepEvents(plan)

	persist, err := events.ShouldPersist(AssetEvent{
		Asset: "analytics.orders", Status: "running", UnitPosition: 0, HasUnitPosition: true,
	})
	require.NoError(t, err)
	assert.True(t, persist)
	persist, err = events.ShouldPersist(AssetEvent{
		Asset: "analytics.orders", Status: "succeeded", UnitPosition: 0, HasUnitPosition: true,
	})
	require.NoError(t, err)
	assert.False(t, persist)
	persist, err = events.ShouldPersist(AssetEvent{
		Asset: "analytics.orders", Status: "succeeded", UnitPosition: 1, HasUnitPosition: true,
	})
	require.NoError(t, err)
	assert.True(t, persist)
}

func TestAssetStepEventsRejectsMismatchedUnit(t *testing.T) {
	events := NewAssetStepEvents(&ExecutionPlan{
		Version: ExecutionPlanVersionV3,
		Units:   []ExecutionUnit{{Position: 0, AssetName: "analytics.orders"}},
	})
	_, err := events.ShouldPersist(AssetEvent{
		Asset: "analytics.customers", Status: "running", UnitPosition: 0, HasUnitPosition: true,
	})
	require.ErrorContains(t, err, "belongs to analytics.orders")
}
