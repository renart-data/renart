package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotReportsScheduleDeclarationRemoval(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ".renart", "schedules.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("version: 1\n"), 0o644))
	watcher := New(Config{WorkspaceRoot: root}, nil)

	before, err := watcher.takeSnapshot()
	require.NoError(t, err)
	require.Contains(t, before, ".renart/schedules.yml")
	require.NoError(t, os.Remove(path))
	after, err := watcher.takeSnapshot()
	require.NoError(t, err)

	assert.Equal(t, ".renart/schedules.yml", firstChangedPath(before, after))
	assert.NotEqual(t, hashSnapshot(before), hashSnapshot(after))
}

func TestRelevantPathIncludesScheduleDeclarations(t *testing.T) {
	t.Parallel()
	assert.True(t, IsRelevantPath("/workspace/.renart/schedules.yml"))
}

func TestSlingRuntimeBootstrapDoesNotChangeWorkspaceSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	watcher := New(Config{WorkspaceRoot: root}, nil)
	before, err := watcher.takeSnapshot()
	require.NoError(t, err)
	path := filepath.Join(root, ".renart", "config", ".sling", "env.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("connections: {}\n"), 0o600))
	after, err := watcher.takeSnapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.False(t, IsRelevantPath(path), "fsnotify must agree with polling")
	assert.False(t, IsRelevantPath(".renart/config/.sling/env.yaml"))
	for _, authored := range []string{".bruin.yml", ".renart/secrets.yml", ".renart/environments.yml", ".renart/config/settings.yaml", "assets/foo.sql"} {
		assert.True(t, IsRelevantPath(filepath.Join(root, authored)), authored)
	}
}
