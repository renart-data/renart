package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingUVEnsurer struct {
	path  string
	calls int
}

func (s *countingUVEnsurer) EnsureUvInstalled(context.Context) (string, error) {
	s.calls++
	return s.path, nil
}

func TestUVPathCacheSkipsRepeatedVersionChecks(t *testing.T) {
	uvPath := filepath.Join(t.TempDir(), "uv")
	if err := os.WriteFile(uvPath, []byte("uv"), 0o700); err != nil {
		t.Fatal(err)
	}
	checker := &countingUVEnsurer{path: uvPath}
	cache := &uvPathCache{}

	for range 2 {
		got, err := cache.ensure(context.Background(), checker)
		if err != nil {
			t.Fatal(err)
		}
		if got != uvPath {
			t.Fatalf("unexpected uv path %q", got)
		}
	}
	if checker.calls != 1 {
		t.Fatalf("expected one version check, got %d", checker.calls)
	}

	if err := os.Remove(uvPath); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ensure(context.Background(), checker); err != nil {
		t.Fatal(err)
	}
	if checker.calls != 2 {
		t.Fatalf("expected a missing binary to invalidate the cache, got %d checks", checker.calls)
	}
}

func TestPythonMaterializationPassesDatabricksPayloadToSling(t *testing.T) {
	workspaceRoot := t.TempDir()
	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte("#!/bin/sh\nprintf 'target=%s\\nargs=%s\\n' \"$RENART_PY_TARGET\" \"$*\"\n"), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)

	var output bytes.Buffer
	operator := &renartPythonOperator{
		manager:       databricksPATTestManager(),
		workspaceRoot: workspaceRoot,
	}
	run := pythonRun{
		repo:   &git.Repo{Path: workspaceRoot},
		asset:  &pipeline.Asset{Name: "analytics.python_result"},
		output: &output,
	}

	err := operator.loadParquetViaSling(
		context.Background(),
		run,
		"databricks-default",
		filepath.Join(workspaceRoot, "result.parquet"),
	)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 2)
	parsed := requireDatabricksSlingPayload(t, strings.TrimPrefix(lines[0], "target="))
	assert.Equal(t, "main", parsed.Query().Get("catalog"))
	assert.Contains(t, lines[1], "--tgt-conn RENART_PY_TARGET")
	assert.Contains(t, lines[1], `--tgt-options {"use_bulk":false}`)
	assert.NotContains(t, lines[1], "test-token")
}
