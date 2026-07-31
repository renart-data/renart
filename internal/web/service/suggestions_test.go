package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLPathSuggestionsHonorFilesystemAccessPolicy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "example.parquet"), []byte("PAR1"), 0o600))

	enabled, apiErr := NewSuggestionsService(SuggestionsDependencies{WorkspaceRoot: root}).SQLPath(
		t.Context(), "asset", "./exam", "",
	)
	require.Nil(t, apiErr)
	require.Len(t, enabled.Suggestions, 1)

	disabled, apiErr := NewSuggestionsService(SuggestionsDependencies{
		WorkspaceRoot:           root,
		DisableFilesystemAccess: true,
	}).SQLPath(t.Context(), "asset", "./exam", "")
	require.Nil(t, apiErr)
	require.Empty(t, disabled.Suggestions)
}
