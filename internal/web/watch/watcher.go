// Package watch provides filesystem watching functionality for workspace changes.
package watch

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bruinpath "github.com/bruin-data/bruin/pkg/path"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/afero"
)

// ChangeHandler is called when workspace changes are detected.
type ChangeHandler func(ctx context.Context, eventType, eventPath string)

// Config holds watcher configuration.
type Config struct {
	WorkspaceRoot string
	Mode          string        // "auto", "fsnotify", or "poll"
	PollInterval  time.Duration // Used when Mode is "poll" or "auto"
}

// Watcher monitors a workspace for file changes.
type Watcher struct {
	config  Config
	handler ChangeHandler

	// dedup prevents the same event from being fired twice when both
	// fsnotify and poll detect the same change in "auto" mode.
	dedupMu   sync.Mutex
	lastFired map[string]time.Time
}

// New creates a new Watcher with the given configuration.
func New(config Config, handler ChangeHandler) *Watcher {
	return &Watcher{
		config:    config,
		handler:   handler,
		lastFired: make(map[string]time.Time),
	}
}

// Start begins watching the workspace according to the configured mode.
// It blocks until the context is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	switch w.config.Mode {
	case "poll":
		w.watchPoll(ctx)
	case "fsnotify":
		if err := w.watchFSNotify(ctx); err != nil {
			fmt.Printf("warning: fsnotify watcher failed: %v\n", err)
		}
	default: // auto
		go w.watchPoll(ctx)
		if err := w.watchFSNotify(ctx); err != nil {
			fmt.Printf("warning: fsnotify watcher failed, continuing with polling: %v\n", err)
			<-ctx.Done()
		}
	}
}

// dedupWindow is the minimum interval between duplicate events for the same path.
const dedupWindow = 500 * time.Millisecond

// fire invokes the handler unless the same path was already handled recently.
// This prevents duplicate notifications when both fsnotify and poll detect
// the same change in "auto" mode.
func (w *Watcher) fire(ctx context.Context, eventType, eventPath string) {
	w.dedupMu.Lock()
	last, seen := w.lastFired[eventPath]
	now := time.Now()
	if seen && now.Sub(last) < dedupWindow {
		w.dedupMu.Unlock()
		return
	}
	w.lastFired[eventPath] = now

	// Periodically prune stale entries to avoid unbounded growth.
	if len(w.lastFired) > 500 {
		cutoff := now.Add(-dedupWindow)
		for k, t := range w.lastFired {
			if t.Before(cutoff) {
				delete(w.lastFired, k)
			}
		}
	}
	w.dedupMu.Unlock()

	w.handler(ctx, eventType, eventPath)
}

func (w *Watcher) watchFSNotify(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	addDir := func(path string) {
		if err := watcher.Add(path); err != nil {
			return
		}
	}

	fs := afero.NewOsFs()
	_ = afero.Walk(fs, w.config.WorkspaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if slicesContains(bruinpath.SkipDirs, info.Name()) {
			return filepath.SkipDir
		}
		addDir(path)
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Name == "" {
				continue
			}

			if event.Op&fsnotify.Create == fsnotify.Create {
				if fi, err := fs.Stat(event.Name); err == nil && fi.IsDir() {
					_ = afero.Walk(fs, event.Name, func(path string, info os.FileInfo, err error) error {
						if err != nil {
							return nil
						}
						if info.IsDir() {
							addDir(path)
						}
						return nil
					})
				}
			}

			if IsRelevantPath(event.Name) {
				relPath, _ := filepath.Rel(w.config.WorkspaceRoot, event.Name)
				w.fire(context.Background(), "workspace.updated", filepath.ToSlash(relPath))
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// Snapshot represents a point-in-time state of the workspace filesystem.
type Snapshot map[string]string

func (w *Watcher) watchPoll(ctx context.Context) {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	prevSnapshot, err := w.takeSnapshot()
	if err != nil {
		prevSnapshot = make(Snapshot)
	}
	prevHash := hashSnapshot(prevSnapshot)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentSnapshot, snapErr := w.takeSnapshot()
			if snapErr != nil {
				continue
			}

			currentHash := hashSnapshot(currentSnapshot)
			if currentHash == prevHash {
				continue
			}

			changedPath := firstChangedPath(prevSnapshot, currentSnapshot)
			if changedPath == "" {
				changedPath = "."
			}
			w.fire(context.Background(), "workspace.updated", changedPath)
			prevSnapshot = currentSnapshot
			prevHash = currentHash
		}
	}
}

func (w *Watcher) takeSnapshot() (Snapshot, error) {
	snapshot := make(Snapshot)

	err := afero.Walk(afero.NewOsFs(), w.config.WorkspaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() && slicesContains(bruinpath.SkipDirs, info.Name()) {
			return filepath.SkipDir
		}

		if !info.IsDir() && !IsRelevantPath(path) {
			return nil
		}

		relPath, relErr := filepath.Rel(w.config.WorkspaceRoot, path)
		if relErr != nil || relPath == "." {
			return nil
		}

		normalized := filepath.ToSlash(relPath)
		fingerprint := fmt.Sprintf("%t:%d:%d:%s", info.IsDir(), info.Size(), info.ModTime().UnixNano(), info.Mode().String())
		snapshot[normalized] = fingerprint
		return nil
	})

	return snapshot, err
}

func hashSnapshot(snapshot Snapshot) uint64 {
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	hasher := fnv.New64a()
	for _, k := range keys {
		_, _ = hasher.Write([]byte(k))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(snapshot[k]))
		_, _ = hasher.Write([]byte{0})
	}

	return hasher.Sum64()
}

func firstChangedPath(prev, current Snapshot) string {
	keys := make([]string, 0, len(prev)+len(current))
	seen := make(map[string]struct{}, len(prev)+len(current))

	for k := range prev {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range current {
		if _, exists := seen[k]; exists {
			continue
		}
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		before, hadBefore := prev[k]
		after, hasAfter := current[k]
		if !hadBefore || !hasAfter || before != after {
			return k
		}
	}
	return ""
}

// IsRelevantPath returns true if the given path should trigger workspace updates.
func IsRelevantPath(path string) bool {
	base := filepath.Base(path)
	if base == "pipeline.yml" || base == "pipeline.yaml" || base == "glossary.yml" || base == "glossary.yaml" || base == ".bruin.yml" {
		return true
	}

	normalized := filepath.ToSlash(path)
	if strings.Contains(normalized, "/assets/") || strings.Contains(normalized, "/tasks/") {
		return true
	}

	suffixes := []string{".sql", ".py", ".r", ".yml", ".yaml"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			return true
		}
	}

	return false
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
