package service

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"renart/internal/web/bus"
	"renart/internal/web/events"
	"renart/internal/web/identity"
	webmodel "renart/internal/web/model"
)

// renart:web
type WorkspaceEvent struct {
	Type            string         `json:"type"`
	Path            string         `json:"path,omitempty"`
	Workspace       WorkspaceState `json:"workspace"`
	Lite            bool           `json:"lite,omitempty"`
	ChangedAssetIDs []string       `json:"changed_asset_ids,omitempty"`
}

// The workspace DTOs are defined once in the model package; these aliases
// keep service call sites compiling without a parallel type set.
type (
	WorkspaceAsset         = webmodel.Asset
	WorkspaceColumnCheck   = webmodel.ColumnCheck
	WorkspaceColumn        = webmodel.Column
	ColumnInferencePreview = webmodel.ColumnInferencePreview
	ColumnSchemaSyncResult = webmodel.ColumnSchemaSyncResult
	ColumnSchemaResolution = webmodel.ColumnSchemaResolution
	WorkspacePipeline      = webmodel.Pipeline
	WorkspaceNotebook      = webmodel.Notebook
	WorkspaceState         = webmodel.WorkspaceState
)

type WorkspaceCoordinatorDependencies struct {
	WorkspaceService *WorkspaceService
	Hub              *events.Hub
	RefreshHook      func(context.Context) error
	Logger           *zap.Logger
	Events           *bus.Bus
}

type WorkspaceCoordinator struct {
	deps WorkspaceCoordinatorDependencies

	stateMu  sync.RWMutex
	state    WorkspaceState
	revision atomic.Int64

	refreshes           atomic.Uint64
	refreshFailures     atomic.Uint64
	lastRefreshNanos    atomic.Int64
	workspacePipelines  atomic.Int64
	workspaceAssetCount atomic.Int64
	workspaceNotebooks  atomic.Int64
	workspaceCellCount  atomic.Int64

	recentServerWritesMu sync.Mutex
	recentServerWrites   map[string]time.Time
}

// WorkspaceCoordinatorStats exposes bounded, content-free measurements for
// deciding whether full SSE snapshots remain within budget.
type WorkspaceCoordinatorStats struct {
	Revision            int64
	Refreshes           uint64
	RefreshFailures     uint64
	LastRefreshDuration time.Duration
	Pipelines           int
	Assets              int
	Notebooks           int
	NotebookCells       int
	Hub                 events.HubStats
}

func NewWorkspaceCoordinator(deps WorkspaceCoordinatorDependencies) *WorkspaceCoordinator {
	return &WorkspaceCoordinator{
		deps:               deps,
		recentServerWrites: make(map[string]time.Time),
	}
}

func (c *WorkspaceCoordinator) CurrentState() WorkspaceState {
	c.stateMu.RLock()
	state := c.state
	c.stateMu.RUnlock()
	return cloneWorkspaceState(state)
}

func (c *WorkspaceCoordinator) SetState(state WorkspaceState) {
	state = cloneWorkspaceState(state)
	pipelineCount, assetCount, notebookCount, cellCount := workspaceStateCounts(state)
	c.stateMu.Lock()
	c.state = state
	c.workspacePipelines.Store(int64(pipelineCount))
	c.workspaceAssetCount.Store(int64(assetCount))
	c.workspaceNotebooks.Store(int64(notebookCount))
	c.workspaceCellCount.Store(int64(cellCount))
	c.stateMu.Unlock()
}

func (c *WorkspaceCoordinator) Refresh(ctx context.Context) (err error) {
	started := time.Now()
	defer func() {
		duration := time.Since(started)
		c.refreshes.Add(1)
		c.lastRefreshNanos.Store(duration.Nanoseconds())
		if err != nil {
			c.refreshFailures.Add(1)
		}
		c.logRefresh(duration, err)
	}()

	if c.deps.RefreshHook != nil {
		return c.deps.RefreshHook(ctx)
	}
	if err = c.deps.WorkspaceService.Refresh(ctx); err != nil {
		return err
	}

	state := c.deps.WorkspaceService.GetState()
	state.Revision = c.revision.Add(1)
	c.SetState(state)
	return nil
}

func (c *WorkspaceCoordinator) Stats() WorkspaceCoordinatorStats {
	c.stateMu.RLock()
	revision := c.state.Revision
	c.stateMu.RUnlock()

	stats := WorkspaceCoordinatorStats{
		Revision:            revision,
		Refreshes:           c.refreshes.Load(),
		RefreshFailures:     c.refreshFailures.Load(),
		LastRefreshDuration: time.Duration(c.lastRefreshNanos.Load()),
		Pipelines:           int(c.workspacePipelines.Load()),
		Assets:              int(c.workspaceAssetCount.Load()),
		Notebooks:           int(c.workspaceNotebooks.Load()),
		NotebookCells:       int(c.workspaceCellCount.Load()),
	}
	if c.deps.Hub != nil {
		stats.Hub = c.deps.Hub.Stats()
	}
	return stats
}

func (c *WorkspaceCoordinator) logRefresh(duration time.Duration, refreshErr error) {
	if c.deps.Logger == nil {
		return
	}
	stats := c.Stats()
	c.deps.Logger.Debug("workspace refreshed",
		zap.Bool("success", refreshErr == nil),
		zap.Duration("duration", duration),
		zap.Int64("revision", stats.Revision),
		zap.Int("pipelines", stats.Pipelines),
		zap.Int("assets", stats.Assets),
		zap.Int("notebooks", stats.Notebooks),
		zap.Int("notebook_cells", stats.NotebookCells),
		zap.Uint64("refreshes", stats.Refreshes),
		zap.Uint64("refresh_failures", stats.RefreshFailures),
	)
}

func workspaceStateCounts(state WorkspaceState) (int, int, int, int) {
	assets := 0
	for _, pipeline := range state.Pipelines {
		assets += len(pipeline.Assets)
	}
	cells := 0
	for _, notebook := range state.Notebooks {
		cells += len(notebook.Cells)
	}
	return len(state.Pipelines), assets, len(state.Notebooks), cells
}

// refreshLogged refreshes the workspace state and logs failures; the
// previous state keeps being served when a re-parse fails mid-edit.
func (c *WorkspaceCoordinator) refreshLogged(ctx context.Context) {
	if err := c.Refresh(ctx); err != nil && c.deps.Logger != nil {
		c.deps.Logger.Warn("workspace refresh failed, serving previous state", zap.Error(err))
	}
}

func (c *WorkspaceCoordinator) SuppressWatcherFor(eventPath string) {
	normalized := filepath.ToSlash(eventPath)
	c.recentServerWritesMu.Lock()
	c.recentServerWrites[normalized] = time.Now()
	c.recentServerWritesMu.Unlock()
}

func (c *WorkspaceCoordinator) IsWatcherSuppressed(eventPath string) bool {
	normalized := filepath.ToSlash(eventPath)
	now := time.Now()

	c.recentServerWritesMu.Lock()
	defer c.recentServerWritesMu.Unlock()
	for path, ts := range c.recentServerWrites {
		if now.Sub(ts) > 3*time.Second {
			delete(c.recentServerWrites, path)
		}
	}

	ts, ok := c.recentServerWrites[normalized]
	return ok && now.Sub(ts) <= 3*time.Second
}

func (c *WorkspaceCoordinator) PushUpdate(ctx context.Context, eventType, eventPath string) {
	c.refreshLogged(ctx)
	state := c.CurrentState()
	changed := c.FindDirectlyChangedAssetIDs(filepath.ToSlash(eventPath))

	now := time.Now().UTC()
	c.emitAssetSaved(changed, now)

	c.deps.Hub.Publish(WorkspaceEvent{
		Type:            eventType,
		Path:            filepath.ToSlash(eventPath),
		Workspace:       StripAssetContent(state),
		Lite:            true,
		ChangedAssetIDs: changed,
	})
	c.logWorkspacePublish(false, eventType, eventPath)
}

func (c *WorkspaceCoordinator) PushUpdateImmediate(ctx context.Context, eventType, eventPath string) {
	c.PushUpdateImmediateWithChangedIDs(ctx, eventType, eventPath, nil)
}

func (c *WorkspaceCoordinator) PushUpdateImmediateWithChangedIDs(ctx context.Context, eventType, eventPath string, changedAssetIDs []string) {
	c.refreshLogged(ctx)
	state := c.CurrentState()
	changed := changedAssetIDs
	if len(changed) == 0 {
		changed = c.FindDirectlyChangedAssetIDs(filepath.ToSlash(eventPath))
	}

	now := time.Now().UTC()
	c.emitAssetSaved(changed, now)

	c.deps.Hub.PublishImmediate(WorkspaceEvent{
		Type:            eventType,
		Path:            filepath.ToSlash(eventPath),
		Workspace:       StripAssetContentKeepingIDs(state, changed),
		Lite:            true,
		ChangedAssetIDs: changed,
	})
	c.logWorkspacePublish(true, eventType, eventPath)
}

func (c *WorkspaceCoordinator) PushAssetContentUpdateImmediate(eventType, eventPath string, changedAssetIDs []string, content string) {
	changed := changedAssetIDs
	if len(changed) == 0 {
		changed = c.FindDirectlyChangedAssetIDs(filepath.ToSlash(eventPath))
	}
	now := time.Now().UTC()
	state := c.updateAssetContent(changed, content, now)

	c.emitAssetSaved(changed, now)

	c.deps.Hub.PublishImmediate(WorkspaceEvent{
		Type:            eventType,
		Path:            filepath.ToSlash(eventPath),
		Workspace:       StripAssetContentKeepingIDs(state, changed),
		Lite:            true,
		ChangedAssetIDs: changed,
	})
	c.logWorkspacePublish(true, eventType, eventPath)
}

func (c *WorkspaceCoordinator) logWorkspacePublish(immediate bool, eventType, eventPath string) {
	if c.deps.Logger == nil || c.deps.Hub == nil {
		return
	}
	stats := c.deps.Hub.Stats()
	c.deps.Logger.Debug("workspace event published",
		zap.String("event_type", eventType),
		zap.String("path", filepath.ToSlash(eventPath)),
		zap.Bool("immediate", immediate),
		zap.Uint64("payload_bytes", stats.LastPayloadBytes),
		zap.Int("clients", stats.Clients),
		zap.Bool("pending", stats.Pending),
		zap.Uint64("published", stats.Published),
		zap.Uint64("coalesced", stats.Coalesced),
		zap.Uint64("delivered", stats.Delivered),
		zap.Uint64("dropped", stats.Dropped),
	)
}

func (c *WorkspaceCoordinator) updateAssetContent(assetIDs []string, content string, updatedAt time.Time) WorkspaceState {
	if len(assetIDs) == 0 {
		return c.CurrentState()
	}
	changed := make(map[string]struct{}, len(assetIDs))
	for _, id := range assetIDs {
		changed[id] = struct{}{}
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	state := cloneWorkspaceState(c.state)
	state.UpdatedAt = updatedAt
	state.Revision = c.revision.Add(1)
	for i, pipeline := range state.Pipelines {
		for j, asset := range pipeline.Assets {
			if _, ok := changed[asset.ID]; ok {
				asset.Content = content
			}
			state.Pipelines[i].Assets[j] = asset
		}
	}
	for i, notebook := range state.Notebooks {
		for j, cell := range notebook.Cells {
			if _, ok := changed[cell.ID]; ok {
				cell.Content = content
			}
			state.Notebooks[i].Cells[j] = cell
		}
	}
	c.state = state
	return cloneWorkspaceState(state)
}

func (c *WorkspaceCoordinator) Subscribe() chan []byte {
	return c.deps.Hub.Subscribe()
}

func (c *WorkspaceCoordinator) Unsubscribe(ch chan []byte) {
	c.deps.Hub.Unsubscribe(ch)
}

func (c *WorkspaceCoordinator) CurrentStateLiteEvent() WorkspaceEvent {
	return WorkspaceEvent{
		Type:      "workspace.updated",
		Workspace: StripAssetContent(c.CurrentState()),
		Lite:      true,
	}
}

type workspaceAssetEntry struct {
	id           string
	name         string
	path         string
	upstreams    []string
	pipelineUUID string
}

func (c *WorkspaceCoordinator) buildAssetIndex() ([]workspaceAssetEntry, map[string]string) {
	state := c.CurrentState()
	var all []workspaceAssetEntry
	nameToID := make(map[string]string)
	for _, p := range state.Pipelines {
		for _, a := range p.Assets {
			all = append(all, workspaceAssetEntry{id: a.ID, name: a.Name, path: a.Path, upstreams: a.Upstreams, pipelineUUID: p.UUID})
			nameToID[a.Name] = a.ID
		}
	}
	return all, nameToID
}

// emitAssetSaved publishes AssetSaved bus events for the changed encoded
// asset IDs. Both API saves and watcher-detected external edits funnel
// through the coordinator, so this is the single seam for save observation.
func (c *WorkspaceCoordinator) emitAssetSaved(changedAssetIDs []string, at time.Time) {
	if c.deps.Events == nil || len(changedAssetIDs) == 0 {
		return
	}
	changed := make(map[string]struct{}, len(changedAssetIDs))
	for _, id := range changedAssetIDs {
		changed[id] = struct{}{}
	}
	assets, _ := c.buildAssetIndex()
	for _, asset := range assets {
		if _, ok := changed[asset.id]; !ok {
			continue
		}
		if asset.pipelineUUID == "" || asset.name == "" {
			continue
		}
		c.deps.Events.EmitAssetSaved(bus.AssetSaved{
			PipelineUUID: asset.pipelineUUID,
			AssetID:      identity.AssetID(asset.pipelineUUID, asset.name),
			AssetName:    asset.name,
			Path:         asset.path,
			SavedAt:      at,
		})
	}
}

func buildDownstreamIndex(assets []workspaceAssetEntry, nameToID map[string]string) map[string][]string {
	downstream := make(map[string][]string)
	for _, a := range assets {
		for _, upName := range a.upstreams {
			if upID, ok := nameToID[upName]; ok {
				downstream[upID] = append(downstream[upID], a.id)
			}
		}
	}
	return downstream
}

func (c *WorkspaceCoordinator) FindDirectlyChangedAssetIDs(eventPath string) []string {
	assets, _ := c.buildAssetIndex()
	normalizedEvent := filepath.ToSlash(eventPath)

	var result []string
	for _, a := range assets {
		if PathContains(normalizedEvent, a.path) {
			result = append(result, a.id)
		}
	}
	sort.Strings(result)
	return result
}

func (c *WorkspaceCoordinator) FindMaterializationInspectIDs(assetIDs ...string) []string {
	assets, nameToID := c.buildAssetIndex()
	downstream := buildDownstreamIndex(assets, nameToID)

	seen := make(map[string]struct{})
	for _, id := range assetIDs {
		seen[id] = struct{}{}
		for _, child := range downstream[id] {
			seen[child] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (c *WorkspaceCoordinator) FindAssetNameByID(assetID string) string {
	state := c.CurrentState()
	for _, p := range state.Pipelines {
		for _, a := range p.Assets {
			if a.ID == assetID {
				return a.Name
			}
		}
	}
	return ""
}

func StripAssetContent(state WorkspaceState) WorkspaceState {
	lite := state
	lite.Pipelines = make([]WorkspacePipeline, len(state.Pipelines))
	for i, p := range state.Pipelines {
		litePipeline := p
		litePipeline.Assets = make([]WorkspaceAsset, len(p.Assets))
		for j, a := range p.Assets {
			a.Content = ""
			litePipeline.Assets[j] = a
		}
		lite.Pipelines[i] = litePipeline
	}
	return lite
}

func StripAssetContentKeepingIDs(state WorkspaceState, keepIDs []string) WorkspaceState {
	if len(keepIDs) == 0 {
		return StripAssetContent(state)
	}

	keep := make(map[string]struct{}, len(keepIDs))
	for _, id := range keepIDs {
		keep[id] = struct{}{}
	}

	lite := state
	lite.Pipelines = make([]WorkspacePipeline, len(state.Pipelines))
	for i, p := range state.Pipelines {
		litePipeline := p
		litePipeline.Assets = make([]WorkspaceAsset, len(p.Assets))
		for j, a := range p.Assets {
			if _, ok := keep[a.ID]; !ok {
				a.Content = ""
			}
			litePipeline.Assets[j] = a
		}
		lite.Pipelines[i] = litePipeline
	}

	return lite
}

func PathContains(eventPath, assetPath string) bool {
	eventPath = filepath.ToSlash(filepath.Clean(eventPath))
	assetPath = filepath.ToSlash(filepath.Clean(assetPath))

	if eventPath == assetPath {
		return true
	}
	if strings.HasPrefix(assetPath, eventPath+"/") {
		return true
	}

	base := filepath.Base(eventPath)
	if base == "pipeline.yml" || base == ".pipeline.yml" {
		assetsDir := filepath.ToSlash(filepath.Join(filepath.Dir(eventPath), "assets"))
		if strings.HasPrefix(assetPath, assetsDir+"/") {
			return true
		}
	}

	if base == "asset.yml" || base == ".asset.yml" ||
		base == "schema.yml" || base == "schema.yaml" ||
		base == "checks.yml" || base == "checks.yaml" ||
		base == "source.yml" || base == "source.yaml" {
		if filepath.Dir(eventPath) == filepath.Dir(assetPath) {
			return true
		}
	}

	return false
}
