package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"renart/internal/web/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathContains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		eventPath string
		assetPath string
		expected  bool
	}{
		{name: "exact match", eventPath: "assets/a.sql", assetPath: "assets/a.sql", expected: true},
		{name: "directory contains asset", eventPath: "assets", assetPath: "assets/a.sql", expected: true},
		{name: "pipeline manifest affects assets dir", eventPath: "pipelines/orders/pipeline.yml", assetPath: "pipelines/orders/assets/a.sql", expected: true},
		{name: "schema file affects sibling asset", eventPath: "pipelines/orders/assets/schema.yml", assetPath: "pipelines/orders/assets/a.sql", expected: true},
		{name: "unrelated file", eventPath: "other/x.sql", assetPath: "assets/a.sql", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, PathContains(tt.eventPath, tt.assetPath))
		})
	}
}

func TestStripAssetContent(t *testing.T) {
	t.Parallel()

	state := WorkspaceState{
		Pipelines: []WorkspacePipeline{{
			ID:   "p1",
			Name: "orders",
			Assets: []WorkspaceAsset{{
				ID:      "a1",
				Name:    "marts.orders",
				Path:    "assets/orders.sql",
				Content: "select * from raw.orders",
			}},
		}},
	}

	stripped := StripAssetContent(state)

	assert.Equal(t, "", stripped.Pipelines[0].Assets[0].Content)
	assert.Equal(t, "select * from raw.orders", state.Pipelines[0].Assets[0].Content)
}

func TestStripAssetContentKeepingIDs(t *testing.T) {
	t.Parallel()

	state := WorkspaceState{
		Pipelines: []WorkspacePipeline{{
			Assets: []WorkspaceAsset{
				{ID: "keep", Content: "select 1"},
				{ID: "drop", Content: "select 2"},
			},
		}},
	}

	stripped := StripAssetContentKeepingIDs(state, []string{"keep"})

	assert.Equal(t, "select 1", stripped.Pipelines[0].Assets[0].Content)
	assert.Equal(t, "", stripped.Pipelines[0].Assets[1].Content)
}

func TestWorkspaceCoordinatorWatcherSuppression(t *testing.T) {
	t.Parallel()

	coord := NewWorkspaceCoordinator(WorkspaceCoordinatorDependencies{})

	assert.False(t, coord.IsWatcherSuppressed("assets/orders.sql"))
	coord.SuppressWatcherFor("assets/orders.sql")
	assert.True(t, coord.IsWatcherSuppressed("assets/orders.sql"))

	coord.recentServerWritesMu.Lock()
	coord.recentServerWrites["assets/orders.sql"] = time.Now().Add(-4 * time.Second)
	coord.recentServerWritesMu.Unlock()

	assert.False(t, coord.IsWatcherSuppressed("assets/orders.sql"))
}

func TestWorkspaceCoordinatorFindInspectIDs(t *testing.T) {
	t.Parallel()

	coord := NewWorkspaceCoordinator(WorkspaceCoordinatorDependencies{})
	coord.SetState(WorkspaceState{
		Pipelines: []WorkspacePipeline{{
			Assets: []WorkspaceAsset{
				{ID: "a", Name: "a", Path: "assets/a.sql"},
				{ID: "b", Name: "b", Path: "assets/b.sql", Upstreams: []string{"a"}},
				{ID: "c", Name: "c", Path: "assets/c.sql", Upstreams: []string{"b"}},
			},
		}},
	})

	assert.Equal(t, []string{"a", "b"}, coord.FindMaterializationInspectIDs("a"))
	assert.Equal(t, []string{"a"}, coord.FindDirectlyChangedAssetIDs("assets/a.sql"))
	assert.Equal(t, "b", coord.FindAssetNameByID("b"))
	assert.Equal(t, "", coord.FindAssetNameByID("missing"))
}

func TestWorkspaceCoordinatorPushAssetContentUpdateImmediateSkipsRefresh(t *testing.T) {
	hub := events.NewHub()
	refreshCalled := false
	coord := NewWorkspaceCoordinator(WorkspaceCoordinatorDependencies{
		Hub: hub,
		RefreshHook: func(context.Context) error {
			refreshCalled = true
			return nil
		},
	})
	coord.SetState(WorkspaceState{
		Pipelines: []WorkspacePipeline{{
			Assets: []WorkspaceAsset{
				{ID: "keep", Content: "select 1"},
				{ID: "drop", Content: "select 2"},
			},
		}},
		Notebooks: []WorkspaceNotebook{{
			Cells: []WorkspaceAsset{{ID: "cell", Content: "select 3"}},
		}},
	})
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	coord.PushAssetContentUpdateImmediate("asset.updated", "assets/keep.sql", []string{"keep"}, "SELECT 1")

	assert.False(t, refreshCalled)
	assert.Equal(t, "SELECT 1", coord.CurrentState().Pipelines[0].Assets[0].Content)
	assert.Equal(t, "select 2", coord.CurrentState().Pipelines[0].Assets[1].Content)
	assert.Equal(t, "select 3", coord.CurrentState().Notebooks[0].Cells[0].Content)

	select {
	case payload := <-ch:
		var event WorkspaceEvent
		require.NoError(t, json.Unmarshal(payload, &event))
		assert.Equal(t, "asset.updated", event.Type)
		assert.True(t, event.Lite)
		assert.Equal(t, []string{"keep"}, event.ChangedAssetIDs)
		assert.Equal(t, "SELECT 1", event.Workspace.Pipelines[0].Assets[0].Content)
		assert.Equal(t, "", event.Workspace.Pipelines[0].Assets[1].Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for workspace event")
	}
}

func TestWorkspaceCoordinatorStatsRecordRefreshOutcomes(t *testing.T) {
	t.Parallel()

	fail := false
	coord := NewWorkspaceCoordinator(WorkspaceCoordinatorDependencies{
		Hub: events.NewHub(),
		RefreshHook: func(context.Context) error {
			time.Sleep(time.Millisecond)
			if fail {
				return errors.New("refresh failed")
			}
			return nil
		},
	})
	coord.SetState(WorkspaceState{
		Revision: 7,
		Pipelines: []WorkspacePipeline{{
			Assets: []WorkspaceAsset{{ID: "one"}, {ID: "two"}},
		}},
		Notebooks: []WorkspaceNotebook{{
			Cells: []WorkspaceAsset{{ID: "cell"}},
		}},
	})

	require.NoError(t, coord.Refresh(t.Context()))
	fail = true
	require.EqualError(t, coord.Refresh(t.Context()), "refresh failed")

	stats := coord.Stats()
	assert.Equal(t, int64(7), stats.Revision)
	assert.Equal(t, uint64(2), stats.Refreshes)
	assert.Equal(t, uint64(1), stats.RefreshFailures)
	assert.GreaterOrEqual(t, stats.LastRefreshDuration, time.Millisecond)
	assert.Equal(t, 1, stats.Pipelines)
	assert.Equal(t, 2, stats.Assets)
	assert.Equal(t, 1, stats.Notebooks)
	assert.Equal(t, 1, stats.NotebookCells)
}
