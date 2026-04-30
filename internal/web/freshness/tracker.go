package freshness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/spf13/afero"
)

// AssetTimestamps stores freshness metadata for a single asset.
// It distinguishes between two types of staleness:
//   - Materialization: when was `bruin run <asset>` last executed (table contents)
//   - Content change: when was the asset's source file last edited (SQL/Python code)
type AssetTimestamps struct {
	MaterializedAt     *time.Time `json:"materialized_at,omitempty"`
	MaterializedStatus string     `json:"materialized_status,omitempty"` // "succeeded" / "failed"
	ContentChangedAt   *time.Time `json:"content_changed_at,omitempty"`
}

// Tracker maintains in-memory per-asset freshness information.
// It is safe for concurrent use.
type Tracker struct {
	mu   sync.RWMutex
	data map[string]*AssetTimestamps // asset name → timestamps
}

// New creates a fresh Tracker.
func New() *Tracker {
	return &Tracker{data: make(map[string]*AssetTimestamps)}
}

// RecordMaterialization updates the materialization timestamp for an asset.
func (t *Tracker) RecordMaterialization(assetName string, ts time.Time, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.data[assetName]
	if !ok {
		entry = &AssetTimestamps{}
		t.data[assetName] = entry
	}
	if status == "succeeded" {
		entry.MaterializedAt = &ts
	}
	entry.MaterializedStatus = status
}

// RecordContentChange updates the content-changed timestamp for an asset.
// This is called when the file watcher detects a source file edit.
func (t *Tracker) RecordContentChange(assetName string, ts time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.data[assetName]
	if !ok {
		entry = &AssetTimestamps{}
		t.data[assetName] = entry
	}
	entry.ContentChangedAt = &ts
}

// GetAll returns a snapshot of all tracked asset timestamps.
func (t *Tracker) GetAll() map[string]AssetTimestamps {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make(map[string]AssetTimestamps, len(t.data))
	for k, v := range t.data {
		out[k] = *v
	}
	return out
}

// Get returns the freshness data for a single asset, or nil if not tracked.
func (t *Tracker) Get(assetName string) *AssetTimestamps {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entry, ok := t.data[assetName]
	if !ok {
		return nil
	}
	cp := *entry
	return &cp
}

// runLogEntry mirrors the subset of the run log JSON we need.
type runLogEntry struct {
	State []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"state"`
	Timestamp time.Time `json:"timestamp"`
}

// LoadFromRunLogs scans <logsDir>/runs/<pipeline>/*.json and bootstraps
// materialization timestamps from the latest run of each pipeline.
func (t *Tracker) LoadFromRunLogs(logsDir string) error {
	fs := afero.NewOsFs()
	runsDir := filepath.Join(logsDir, "runs")
	entries, err := afero.ReadDir(fs, runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.loadLatestRun(fs, filepath.Join(runsDir, entry.Name()))
	}
	return nil
}

// loadLatestRun reads the most-recent run log in a pipeline directory.
func (t *Tracker) loadLatestRun(fs afero.Fs, dir string) {
	entries, err := afero.ReadDir(fs, dir)
	if err != nil || len(entries) == 0 {
		return
	}

	// File names are timestamp-based so alphabetical order = chronological.
	// Walk backwards to find the latest .json file.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name()
		if filepath.Ext(name) != ".json" {
			continue
		}

		data, readErr := afero.ReadFile(fs, filepath.Join(dir, name))
		if readErr != nil {
			continue
		}

		var entry runLogEntry
		if json.Unmarshal(data, &entry) != nil {
			continue
		}

		t.ingest(entry)
		return
	}
}

// ingest merges a single run log into the tracker, keeping the latest timestamp per asset.
func (t *Tracker) ingest(entry runLogEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, asset := range entry.State {
		existing, ok := t.data[asset.Name]
		if asset.Status != "succeeded" {
			if !ok {
				existing = &AssetTimestamps{}
				t.data[asset.Name] = existing
			}
			existing.MaterializedStatus = asset.Status
			continue
		}

		if ok && existing.MaterializedAt != nil && !entry.Timestamp.After(*existing.MaterializedAt) {
			continue
		}
		if !ok {
			existing = &AssetTimestamps{}
			t.data[asset.Name] = existing
		}
		ts := entry.Timestamp
		existing.MaterializedAt = &ts
		existing.MaterializedStatus = asset.Status
	}
}
