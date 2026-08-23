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

	recentServerWritesMu sync.Mutex
	recentServerWrites   map[string]time.Time
}

func NewWorkspaceCoordinator(deps WorkspaceCoordinatorDependencies) *WorkspaceCoordinator {
	return &WorkspaceCoordinator{
		deps:               deps,
		recentServerWrites: make(map[string]time.Time),
	}
}

func (c *WorkspaceCoordinator) CurrentState() WorkspaceState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *WorkspaceCoordinator) SetState(state WorkspaceState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.state = state
}

func (c *WorkspaceCoordinator) Refresh(ctx context.Context) error {
	if c.deps.RefreshHook != nil {
		return c.deps.RefreshHook(ctx)
	}
	if err := c.deps.WorkspaceService.Refresh(ctx); err != nil {
		return err
	}

	state := c.deps.WorkspaceService.GetState()
	state.Revision = c.revision.Add(1)
	c.SetState(state)
	return nil
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

	state := c.state
	state.UpdatedAt = updatedAt
	state.Revision = c.revision.Add(1)
	state.Pipelines = make([]WorkspacePipeline, len(c.state.Pipelines))
	for i, pipeline := range c.state.Pipelines {
		nextPipeline := pipeline
		nextPipeline.Assets = make([]WorkspaceAsset, len(pipeline.Assets))
		for j, asset := range pipeline.Assets {
			if _, ok := changed[asset.ID]; ok {
				asset.Content = content
			}
			nextPipeline.Assets[j] = asset
		}
		state.Pipelines[i] = nextPipeline
	}
	state.Notebooks = make([]webmodel.Notebook, len(c.state.Notebooks))
	for i, notebook := range c.state.Notebooks {
		nextNotebook := notebook
		nextNotebook.Cells = make([]WorkspaceAsset, len(notebook.Cells))
		for j, cell := range notebook.Cells {
			if _, ok := changed[cell.ID]; ok {
				cell.Content = content
			}
			nextNotebook.Cells[j] = cell
		}
		state.Notebooks[i] = nextNotebook
	}
	c.state = state
	return state
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
