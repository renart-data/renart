package notebook

import (
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestEnsureCellIDPreservesNumericLookingHexScalar(t *testing.T) {
	t.Parallel()

	filesystem := afero.NewMemMapFs()
	path := filepath.Join("notebooks", "example", "cell.sql")
	require.NoError(t, filesystem.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, afero.WriteFile(filesystem, path, []byte("/* @bruin\nid: 4101e095\ntype: duckdb.sql\n@bruin */\nselect 1\n"), 0o644))

	id, generated, err := EnsureCellID(filesystem, path)
	require.NoError(t, err)
	require.False(t, generated)
	require.Equal(t, "4101e095", id)
}
