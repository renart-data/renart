package service

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistExecutableAssetKeepsOnlyAuthoredConnectionSecrets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		header      string
		wantSecrets bool
	}{
		{name: "injected at parse time", header: "connection: warehouse\n"},
		{name: "authored", header: "connection: warehouse\nsecrets:\n  - key: warehouse\n", wantSecrets: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := afero.NewMemMapFs()
			path := "/analytics/assets/analytics/orders.sql"
			require.NoError(t, afero.WriteFile(fs, path, []byte("/* @bruin\ntype: duckdb.sql\n"+tc.header+"@bruin */\n\nselect 1 as id\n"), 0o644))
			asset, err := pipeline.CreateTaskFromFileComments(fs)(path)
			require.NoError(t, err)
			// What Builder.InjectConnectionAsSecret leaves on a resolved asset.
			if len(asset.Secrets) == 0 {
				asset.Secrets = append(asset.Secrets, pipeline.SecretMapping{SecretKey: "warehouse", InjectedKey: "warehouse"})
			}

			require.NoError(t, persistExecutableAsset(fs, asset))

			content, err := afero.ReadFile(fs, path)
			require.NoError(t, err)
			if tc.wantSecrets {
				assert.Contains(t, string(content), "secrets:")
			} else {
				assert.NotContains(t, string(content), "secrets:")
			}
			assert.Len(t, asset.Secrets, 1, "the resolved asset keeps its runtime mapping")
		})
	}
}
