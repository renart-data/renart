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
	"renart/internal/web/pyintelligence"
)

func TestAssetServiceFormatSQLUsesGolyglotAndPersistsResult(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetPath := filepath.Join("analytics", "assets", "customers.sql")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(absAssetPath, []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */

select old_value from source_table
`)+"\n"), 0o644))

	var suppressedPath string
	var pushedEvent string
	var pushedPath string
	var pushedIDs []string
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:  func(path string) { suppressedPath = path },
		PushWorkspaceUpdateImmediateWithChangedIDs: func(_ context.Context, event, path string, ids []string) {
			pushedEvent = event
			pushedPath = path
			pushedIDs = ids
		},
	})

	assetID := EncodeID(assetPath)
	response, apiErr := service.FormatSQL(context.Background(), assetID, FormatSQLAssetRequest{
		Content: "select a,b from t where x=1",
	})
	require.Nil(t, apiErr)

	expectedSQL := "SELECT\n  a,\n  b\nFROM t\nWHERE\n  x = 1"
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, assetID, response.AssetID)
	assert.Equal(t, expectedSQL, response.Content)
	assert.Empty(t, response.Error)

	fileBytes, err := os.ReadFile(absAssetPath)
	require.NoError(t, err)
	fileContent := string(fileBytes)
	assert.Contains(t, fileContent, "name: analytics.customers")
	assert.Contains(t, fileContent, "type: duckdb.sql")
	assert.Equal(t, expectedSQL, strings.TrimSpace(ExtractExecutableContent(fileContent)))
	assert.Equal(t, assetPath, suppressedPath)
	assert.Equal(t, "asset.updated", pushedEvent)
	assert.Equal(t, assetPath, pushedPath)
	assert.Equal(t, []string{assetID}, pushedIDs)
}

func TestAssetServiceFormatSQLUsesAssetContentWorkspaceUpdateWhenAvailable(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "customers.sql")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(absAssetPath, []byte("select old_value from source_table\n"), 0o644))

	var contentEvent string
	var updateContent string
	var syncCalled bool
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:   workspaceRoot,
		SuppressWatcher: func(string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {
			syncCalled = true
		},
		PushAssetContentUpdateImmediate: func(event, _ string, _ []string, content string) {
			contentEvent = event
			updateContent = content
		},
	})

	assetID := EncodeID(assetPath)
	response, apiErr := service.FormatSQL(context.Background(), assetID, FormatSQLAssetRequest{
		Content: "select a from t",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, "asset.updated", contentEvent)
	assert.Equal(t, response.Content, updateContent)
	assert.False(t, syncCalled)
}

func TestAssetServiceFormatSQLCanReturnFormattingWithoutPersisting(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("notebooks", "scratch", "cell.sql")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	original := "select old_value from source_table\n"
	require.NoError(t, os.WriteFile(absAssetPath, []byte(original), 0o644))

	pushCalled := false
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:   workspaceRoot,
		SuppressWatcher: func(string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {
			pushCalled = true
		},
	})

	persist := false
	response, apiErr := service.FormatSQL(context.Background(), EncodeID(assetPath), FormatSQLAssetRequest{
		Content: "select a,b from t",
		Persist: &persist,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, "SELECT\n  a,\n  b\nFROM t", response.Content)

	contents, err := os.ReadFile(absAssetPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(contents))
	assert.False(t, pushCalled)
}

func TestAssetServiceFormatSQLPersistsQuerySensorParameter(t *testing.T) {
	t.Parallel()

	service, _ := newSemanticAssetTestService(t, nil)
	created, createErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name: "analytics.orders_ready",
		Type: "duckdb.sensor.query",
		Parameters: map[string]string{
			"query":         "select true",
			"poke_interval": "15",
			"timeout":       "2h",
		},
	})
	require.Nil(t, createErr)

	response, apiErr := service.FormatSQL(context.Background(), created.AssetID, FormatSQLAssetRequest{
		Content: "select count(*)>0 from analytics.orders",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "ok", response.Status)
	assert.NotEqual(t, "select count(*)>0 from analytics.orders", response.Content)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), created.AssetID)
	require.NoError(t, err)
	assert.Equal(t, response.Content, asset.Parameters["query"])
	assert.Equal(t, "15", asset.Parameters["poke_interval"])
	assert.Equal(t, "2h", asset.Parameters["timeout"])
}

func TestAssetServiceFormatPythonUsesTyAndPersistsExecutableContent(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(absAssetPath, []byte(strings.TrimSpace(`
"""@bruin
name: analytics.task
type: python
image: python:3.11
@bruin"""

def add(left:int,right:int)->int:
 return left+right
`)+"\n"), 0o644))

	var suppressedPath string
	var pushedEvent string
	var pushedPath string
	var pushedIDs []string
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:  func(path string) { suppressedPath = path },
		PushWorkspaceUpdateImmediateWithChangedIDs: func(_ context.Context, event, path string, ids []string) {
			pushedEvent = event
			pushedPath = path
			pushedIDs = ids
		},
	})

	assetID := EncodeID(assetPath)
	response, apiErr := service.FormatPython(context.Background(), assetID, FormatPythonAssetRequest{
		Content: "def add(left:int,right:int)->int:\n return left+right\n",
	})
	require.Nil(t, apiErr)

	expectedPython := "def add(left: int, right: int) -> int:\n    return left + right"
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, assetID, response.AssetID)
	assert.Equal(t, expectedPython+"\n", response.Content)
	assert.Empty(t, response.Error)

	fileBytes, err := os.ReadFile(absAssetPath)
	require.NoError(t, err)
	fileContent := string(fileBytes)
	assert.Contains(t, fileContent, "name: analytics.task")
	assert.Contains(t, fileContent, "type: python")
	assert.Equal(t, expectedPython, strings.TrimSpace(ExtractExecutableContent(fileContent)))
	assert.Equal(t, assetPath, suppressedPath)
	assert.Equal(t, "asset.updated", pushedEvent)
	assert.Equal(t, assetPath, pushedPath)
	assert.Equal(t, []string{assetID}, pushedIDs)
}

func TestAssetServicePythonDiagnosticsResolvesInstalledRequirementPackage(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".venv", "lib", "python3.11", "site-packages", "pandas"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".venv", "lib", "python3.11", "site-packages", "pandas", "__init__.py"), []byte("from pandas.core.api import (\n    Series,\n    DataFrame,\n    Index,\n)\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(absAssetPath), "requirements.txt"), []byte("pandas\n"), 0o644))
	require.NoError(t, os.WriteFile(absAssetPath, []byte(strings.TrimSpace(`
"""@bruin
name: analytics.task
type: python
image: python:3.11
@bruin"""

import pandas as pd

df = pd.DataFrame({"a": [1]})
`)+"\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
	})

	response, apiErr := service.PythonDiagnostics(context.Background(), EncodeID(assetPath), PythonDiagnosticsRequest{})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", response.Status)
	for _, diagnostic := range response.Diagnostics {
		assert.NotContains(t, diagnostic.Message, "Cannot resolve imported module `pandas`")
	}
}

func TestAssetServicePythonIntelligenceResolvesInjectedRenartSDK(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(absAssetPath, []byte("from renart import context, query\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
	})
	assetID := EncodeID(assetPath)

	diagnostics, apiErr := service.PythonDiagnostics(context.Background(), assetID, PythonDiagnosticsRequest{
		Content: "from renart import context, query\n\ndef materialize():\n    return query(\"select 1\")\n",
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", diagnostics.Status)
	for _, diagnostic := range diagnostics.Diagnostics {
		assert.NotContains(t, diagnostic.Message, "Cannot resolve imported module `renart`")
		assert.NotContains(t, diagnostic.Message, "Unknown import `query`")
	}

	completions, apiErr := service.PythonCompletions(context.Background(), assetID, PythonCompletionsRequest{
		Content:  "from renart import context\n\ncontext.",
		Line:     3,
		Column:   9,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", completions.Status)
	assert.Contains(t, pythonCompletionLabels(completions.Completions), "run_id")

	completions, apiErr = service.PythonCompletions(context.Background(), assetID, PythonCompletionsRequest{
		Content:  "from renart import query\n\nresult = query(\"select 1\")\nresult.",
		Line:     4,
		Column:   8,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", completions.Status)
	assert.Contains(t, pythonCompletionLabels(completions.Completions), "to_pandas")

	completions, apiErr = service.PythonCompletions(context.Background(), assetID, PythonCompletionsRequest{
		Content:  "from renart import query\n\nresult = query(\"select 1\", format=\"pandas\")\nresult.",
		Line:     4,
		Column:   8,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", completions.Status)
	assert.Contains(t, pythonCompletionLabels(completions.Completions), "head")
}

func TestAssetServicePythonIntelligencePrefersInstalledSDKDependencyStub(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	pandasPath := filepath.Join(
		workspaceRoot,
		".venv",
		"lib",
		"python3.11",
		"site-packages",
		"pandas",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(absAssetPath, []byte("from renart import query\nimport pandas\n"), 0o644))
	require.NoError(t, os.MkdirAll(pandasPath, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pandasPath, "__init__.pyi"),
		[]byte("class DataFrame:\n    def workspace_method(self) -> None: ...\n"),
		0o644,
	))

	service := NewAssetService(AssetDependencies{WorkspaceRoot: workspaceRoot})
	files, _ := service.installedPythonPackageStubs(
		assetPath,
		absAssetPath,
		"from renart import query\nimport pandas\n",
	)
	pandasFiles := make([]pyintelligence.VirtualFile, 0, 1)
	for _, file := range files {
		if file.Path == "/site-packages/pandas/__init__.pyi" {
			pandasFiles = append(pandasFiles, file)
		}
	}
	require.Len(t, pandasFiles, 1)
	assert.Contains(t, pandasFiles[0].Content, "workspace_method")
}

func TestAssetServicePythonCompletionsReturnsTySuggestions(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(absAssetPath, []byte(strings.TrimSpace(`
"""@bruin
name: analytics.task
type: python
image: python:3.11
@bruin"""

def local_value() -> int:
    return 1

local_val
`)+"\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
	})

	response, apiErr := service.PythonCompletions(context.Background(), EncodeID(assetPath), PythonCompletionsRequest{
		Content:  "def local_value() -> int:\n    return 1\n\nlocal_val",
		Line:     4,
		Column:   10,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", response.Status)
	assert.Contains(t, pythonCompletionLabels(response.Completions), "local_value")
}

func TestAssetServicePythonCompletionsUsesInstalledPackageExports(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	pandasPath := filepath.Join(workspaceRoot, ".venv", "lib", "python3.11", "site-packages", "pandas")
	require.NoError(t, os.MkdirAll(pandasPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pandasPath, "__init__.py"), []byte("from pandas.core.api import (\n    Series,\n    DataFrame,\n    Index,\n)\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(pandasPath, "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pandasPath, "core", "api.py"), []byte("from pandas.core.frame import DataFrame\nfrom pandas.core.indexes.base import Index\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(pandasPath, "core", "indexes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pandasPath, "core", "frame.py"), []byte("class DataFrame:\n    def __init__(self, data=None, index=None, columns=None, dtype=None, copy=None): ...\n    columns = properties.AxisProperty(\n        doc=\"\"\"\n        Returns\n        -------\n        pandas.Index\n            The column labels.\n        \"\"\"\n    )\n    def head(self): ...\n    def merge(self): ...\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pandasPath, "core", "indexes", "base.py"), []byte("class Index:\n    name = None\n    def to_list(self): ...\n    def unique(self): ...\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(absAssetPath), "requirements.txt"), []byte("pandas\n"), 0o644))
	require.NoError(t, os.WriteFile(absAssetPath, []byte(strings.TrimSpace(`
"""@bruin
name: analytics.task
type: python
image: python:3.11
@bruin"""

import pandas as pd

def materialize():
    pd.
`)+"\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: newAssetTestResolver(workspaceRoot).ResolveAssetByID,
	})

	response, apiErr := service.PythonCompletions(context.Background(), EncodeID(assetPath), PythonCompletionsRequest{
		Content:  "import pandas as pd\n\ndef materialize():\n    pd.",
		Line:     4,
		Column:   8,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", response.Status)
	assert.Contains(t, pythonCompletionLabels(response.Completions), "DataFrame")

	response, apiErr = service.PythonCompletions(context.Background(), EncodeID(assetPath), PythonCompletionsRequest{
		Content:  "import pandas as pd\n\nx = pd.DataFrame()\nx.",
		Line:     4,
		Column:   3,
		Snippets: true,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", response.Status)
	labels := pythonCompletionLabels(response.Completions)
	assert.NotEmpty(t, labels)
}

func TestAssetServicePythonPackageSearchRootsSupportsWindowsLayouts(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "task.py")
	venvSitePackages := filepath.Join(workspaceRoot, ".venv", "Lib", "site-packages")
	uvCache := filepath.Join(workspaceRoot, "uv-cache")
	uvArchive := filepath.Join(uvCache, "archive-v0", "package-hash")
	require.NoError(t, os.MkdirAll(venvSitePackages, 0o755))
	require.NoError(t, os.MkdirAll(uvArchive, 0o755))
	t.Setenv("UV_CACHE_DIR", uvCache)

	service := NewAssetService(AssetDependencies{WorkspaceRoot: workspaceRoot})
	roots := service.pythonPackageSearchRoots(assetPath)

	assert.Contains(t, roots, venvSitePackages)
	assert.Contains(t, roots, uvArchive)
}

func TestAssetServicePythonPackageMountCacheReusesUnchangedPackageFiles(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	packagePath := filepath.Join(workspaceRoot, ".venv", "lib", "python3.11", "site-packages", "examplepkg")
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.MkdirAll(packagePath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(packagePath, "__init__.py"), []byte("VALUE = 1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(absAssetPath), "requirements.txt"), []byte("examplepkg\n"), 0o644))

	service := NewAssetService(AssetDependencies{WorkspaceRoot: workspaceRoot})
	files, fingerprint := service.installedPythonPackageStubs(assetPath, absAssetPath, "import examplepkg\n")
	require.Len(t, files, 1)
	require.NotEmpty(t, fingerprint)
	assert.Equal(t, "VALUE = 1\n", files[0].Content)

	files, repeatedFingerprint := service.installedPythonPackageStubs(assetPath, absAssetPath, "import examplepkg\n")
	require.Len(t, files, 1)
	assert.Equal(t, fingerprint, repeatedFingerprint)
	assert.Len(t, service.pythonPackageMountCache, 1)
}

func TestAssetServicePythonPackageMountCacheInvalidatesWhenPackageFileChanges(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetPath := filepath.Join("analytics", "assets", "task.py")
	absAssetPath := filepath.Join(workspaceRoot, assetPath)
	packagePath := filepath.Join(workspaceRoot, ".venv", "lib", "python3.11", "site-packages", "examplepkg")
	packageFile := filepath.Join(packagePath, "__init__.py")
	require.NoError(t, os.MkdirAll(filepath.Dir(absAssetPath), 0o755))
	require.NoError(t, os.MkdirAll(packagePath, 0o755))
	require.NoError(t, os.WriteFile(packageFile, []byte("VALUE = 1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(absAssetPath), "requirements.txt"), []byte("examplepkg\n"), 0o644))

	service := NewAssetService(AssetDependencies{WorkspaceRoot: workspaceRoot})
	_, fingerprint := service.installedPythonPackageStubs(assetPath, absAssetPath, "import examplepkg\n")
	require.NotEmpty(t, fingerprint)

	require.NoError(t, os.WriteFile(packageFile, []byte("VALUE = 100\n"), 0o644))
	files, changedFingerprint := service.installedPythonPackageStubs(assetPath, absAssetPath, "import examplepkg\n")
	require.Len(t, files, 1)
	assert.Equal(t, "VALUE = 100\n", files[0].Content)
	assert.NotEqual(t, fingerprint, changedFingerprint)
	assert.Len(t, service.pythonPackageMountCache, 1)
}

func TestAssetServicePythonTySessionFilesOnlySentUntilSessionIsWarm(t *testing.T) {
	service := NewAssetService(AssetDependencies{})
	files := []pyintelligence.VirtualFile{{Path: "/site-packages/examplepkg/__init__.py", Content: "VALUE = 1\n"}}

	assert.Equal(t, files, service.pythonTySessionFilesForRequest("asset:test.py", "fingerprint-1", files))
	service.markPythonTySessionFilesReady("asset:test.py", "fingerprint-1")
	assert.Nil(t, service.pythonTySessionFilesForRequest("asset:test.py", "fingerprint-1", files))
	assert.Equal(t, files, service.pythonTySessionFilesForRequest("asset:test.py", "fingerprint-2", files))
}

func pythonCompletionLabels(completions []PythonCompletion) []string {
	labels := make([]string, 0, len(completions))
	for _, completion := range completions {
		labels = append(labels, completion.Label)
	}
	return labels
}

func TestSQLFormatDialectForAssetTypeUsesBruinDialect(t *testing.T) {
	assert.Equal(t, "postgresql", sqlFormatDialectForAssetType(pipeline.AssetTypePostgresQuery))
	assert.Equal(t, "databricks", sqlFormatDialectForAssetType(pipeline.AssetTypeDatabricksQuery))
	assert.Equal(t, "duckdb", sqlFormatDialectForAssetType(pipeline.AssetTypeMotherduckQuery))
	assert.Equal(t, "postgresql", sqlFormatDialectForAssetType(pipeline.AssetTypeVerticaQuery))
	assert.Equal(t, "generic", sqlFormatDialectForAssetType(pipeline.AssetTypePython))
}
