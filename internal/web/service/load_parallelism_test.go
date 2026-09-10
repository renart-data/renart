package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadParallelismValidation(t *testing.T) {
	value, err := loadParallelism(&pipeline.Asset{Parameters: pipeline.ParameterMap{loadParamParallelism: 4}})
	require.NoError(t, err)
	assert.Equal(t, 4, value)
	_, err = loadParallelism(&pipeline.Asset{Parameters: pipeline.ParameterMap{loadParamParallelism: []any{4}}})
	require.Error(t, err)
	for _, tc := range []struct {
		value   string
		want    int
		invalid bool
	}{
		{"", 0, false}, {"1", 1, false}, {"4", 4, false}, {"32", 32, false}, {" 8 ", 8, false},
		{"0", 0, true}, {"-1", 0, true}, {"33", 0, true}, {"1.5", 0, true}, {"fast", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			asset := &pipeline.Asset{Type: "load", Parameters: pipeline.ParameterMap{loadParamParallelism: tc.value}}
			value, err := loadParallelism(asset)
			if tc.invalid {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "between 1 and 32")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, value)
			}
			if tc.invalid {
				require.Error(t, validateLoaderMaterialization(asset))
			}
		})
	}
}

func TestLoadParallelismReachesTransferWorkersWithoutChangingProcessDefaults(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "fake-transfer")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nprintf 'workers=%s\\nargs=%s\\n' \"$CONCURRENCY\" \"$*\"\n"), 0o700))
	t.Setenv("RENART_SLING_BINARY", launcher)
	t.Setenv("CONCURRENCY", "7")
	for _, explicit := range []string{"", "4"} {
		t.Run("workers="+explicit, func(t *testing.T) {
			executor := NewHybridBruinExecutor(root, "bruin", nil, nil)
			asset := &pipeline.Asset{Type: "load", Name: "analytics.orders", Connection: "databricks-default", Parameters: pipeline.ParameterMap{
				loadParamSourceConnection: "databricks-default", loadParamSourceTable: "main.analytics.orders_source", loadParamParallelism: explicit,
			}}
			output, err := executor.runLoadAsset(context.Background(), &pipeline.Pipeline{}, asset, databricksPATTestManager(), nil)
			require.NoError(t, err)
			lines := strings.Split(string(output), "\n")
			if explicit == "" {
				assert.Equal(t, "workers=7", lines[0])
				assert.Contains(t, lines[1], `--tgt-options {"use_bulk":false}`)
			} else {
				assert.Equal(t, "workers=4", lines[0])
				assert.Contains(t, lines[1], `--tgt-options {"concurrency":4,"use_bulk":false}`)
			}
			assert.NotContains(t, lines[1], "test-token")
			assert.Equal(t, "7", os.Getenv("CONCURRENCY"))
		})
	}
}

func TestLoadParallelismIsPreservedInSemanticDefinitionAndRender(t *testing.T) {
	content, err := renderLoadAssetContentWithParallelism("target", "source", "public.orders", "", nil, "4")
	require.NoError(t, err)
	assert.Contains(t, content, `parallelism: "4"`)
	_, err = renderLoadAssetContentWithParallelism("target", "source", "public.orders", "", nil, "-1")
	require.Error(t, err)
	outcome := renderLoadSemanticAsset(&pipeline.Asset{Type: "load", Name: "analytics.orders", Parameters: pipeline.ParameterMap{
		loadParamSourceConnection: "source", loadParamSourceTable: "public.orders", loadParamParallelism: "4",
	}}, nil, context.Background(), "target")
	require.Len(t, outcome.stages, 1)
	assert.Contains(t, outcome.stages[0].Content, `"parallelism": 4`)
}
