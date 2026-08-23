package notebookdoc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/notebook"
	"renart/internal/web/workspacefs"
)

func TestResolveDirRejectsNotebookOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	service := New(Dependencies{WorkspaceRoot: root})

	_, apiErr := service.ResolveDir(workspacefs.EncodePathID("../outside"))
	require.NotNil(t, apiErr)
	assert.Equal(t, "invalid_notebook_id", apiErr.Code)
}

func TestResolveDirFindsAuthoredNotebook(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "notebooks", "orders")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, notebook.ManifestFileName), []byte("blocks: []\n"), 0o644))
	service := New(Dependencies{WorkspaceRoot: root})

	resolved, apiErr := service.ResolveDir(workspacefs.EncodePathID("notebooks/orders"))
	require.Nil(t, apiErr)
	assert.Equal(t, dir, resolved)
}

func TestNotebookLockHasOneSerializationAuthority(t *testing.T) {
	service := New(Dependencies{})
	releaseFirst := service.LockNotebook("orders")
	attempted := make(chan struct{})
	acquired := make(chan func(), 1)
	go func() {
		close(attempted)
		acquired <- service.LockNotebook("orders")
	}()
	<-attempted
	select {
	case release := <-acquired:
		release()
		t.Fatal("second writer acquired the notebook lock before the first released it")
	default:
	}

	releaseFirst()
	releaseSecond := <-acquired
	releaseSecond()
}
