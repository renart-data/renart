package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renart/internal/web/model"
)

func TestNotebookPromotionPlansAndAppliesLocalFileSourceAsSeed(t *testing.T) {
	root := promotionWorkspace(t)
	writeWorkspaceFile(t, root, "inputs/events.csv", "event_id,amount\n1,12.5\n")
	writeWorkspaceFile(t, root, "notebooks/source-promotion/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-000000000101
title: Source promotion
blocks:
  - cell: source_events
  - cell: child_events
`)
	writeWorkspaceFile(t, root, "notebooks/source-promotion/events.source.yml", `version: 1
id: source_events
kind: file
uri: inputs/events.csv
snapshot:
  mode: full
columns:
  - name: event_id
    type: bigint
  - name: amount
    type: decimal
`)
	writeWorkspaceFile(t, root, "notebooks/source-promotion/child.sql", `/* @bruin
id: child_events
class: notebook
type: duckdb.sql
@bruin */

select * from events
`)

	svc := promotionNotebookService(t, root)
	notebookID := EncodeID("notebooks/source-promotion")
	request := PromoteCellRequest{
		PipelineID: EncodeID("analytics"), TargetName: "marts.events",
	}
	plan, apiErr := svc.PlanPromoteCell(notebookID, "source_events", request)
	if apiErr != nil {
		t.Fatalf("plan promotion: %+v", apiErr)
	}
	if !plan.CanApply || len(plan.Assets) != 1 {
		t.Fatalf("unexpected promotion plan: %+v", plan)
	}
	asset := plan.Assets[0]
	if asset.AssetType != "duckdb.seed" || asset.Connection != "duckdb-default" || asset.Materialization != "table (create+replace)" {
		t.Fatalf("unexpected Seed consequence: %+v", asset)
	}
	if asset.Path != "analytics/assets/marts/events.asset.yml" {
		t.Fatalf("unexpected target path: %q", asset.Path)
	}

	request.BaseRevision = plan.BaseRevision
	result, apiErr := svc.PromoteCell(notebookID, "source_events", request)
	if apiErr != nil {
		t.Fatalf("apply promotion: %+v", apiErr)
	}
	if result.PromotedCount != 1 || len(result.Notebook.Cells) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	definition, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "events.asset.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(definition)
	for _, expected := range []string{
		"name: marts.events", "type: duckdb.seed", "connection: duckdb-default",
		"path: ../../../inputs/events.csv", "file_type: csv", "enforce_schema: true",
		"strategy: create+replace", "name: event_id", "type: bigint",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("promoted Seed is missing %q:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "snapshot:") || strings.Contains(content, "source_events") {
		t.Fatalf("notebook-only source metadata leaked into Seed asset:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "notebooks", "source-promotion", "events.source.yml")); !os.IsNotExist(err) {
		t.Fatalf("source file still exists: %v", err)
	}
	if !strings.Contains(result.Notebook.Cells[0].Content, "from marts.events") {
		t.Fatalf("remaining reference was not rewritten: %s", result.Notebook.Cells[0].Content)
	}
}

func TestNotebookPromotionMapsObjectAndHTTPSourceAssets(t *testing.T) {
	t.Run("object storage becomes Load", func(t *testing.T) {
		root := promotionWorkspace(t)
		writeWorkspaceFile(t, root, "notebooks/object-source/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-000000000102
blocks:
  - cell: source_archive
`)
		writeWorkspaceFile(t, root, "notebooks/object-source/archive.source.yml", `version: 1
id: source_archive
kind: file
connection: lake-files
uri: archive/events.parquet
snapshot:
  mode: full
columns:
  - name: event_id
    type: bigint
`)
		svc := promotionNotebookService(t, root)
		notebookID := EncodeID("notebooks/object-source")
		request := PromoteCellRequest{PipelineID: EncodeID("analytics"), TargetName: "marts.archive_events"}
		plan, apiErr := svc.PlanPromoteCell(notebookID, "source_archive", request)
		if apiErr != nil {
			t.Fatalf("plan: %+v", apiErr)
		}
		if plan.Assets[0].AssetType != "load" || plan.Assets[0].SourceConnection != "lake-files" {
			t.Fatalf("unexpected Load consequence: %+v", plan.Assets[0])
		}
		request.BaseRevision = plan.BaseRevision
		if _, apiErr := svc.PromoteCell(notebookID, "source_archive", request); apiErr != nil {
			t.Fatalf("promote: %+v", apiErr)
		}
		content, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "archive_events.asset.yml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{
			"name: marts.archive_events", "type: load", "connection: duckdb-default",
			"source_connection: lake-files", "source_table: archive/events.parquet",
			"strategy: create+replace", "name: event_id",
		} {
			if !strings.Contains(string(content), expected) {
				t.Fatalf("promoted Load is missing %q:\n%s", expected, content)
			}
		}
	})

	t.Run("HTTP becomes API and retains request body", func(t *testing.T) {
		root := promotionWorkspace(t)
		writeWorkspaceFile(t, root, "notebooks/http-source/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-000000000103
blocks:
  - cell: source_accounts
`)
		writeWorkspaceFile(t, root, "notebooks/http-source/accounts.source.yml", `version: 1
id: source_accounts
kind: http
request:
  url: https://example.test/accounts/search
  method: POST
  headers:
    Accept: application/json
  body:
    active: true
    regions: [eu, us]
auth:
  type: bearer
  token: "{{ var.api_token }}"
response:
  records_path: data.items
snapshot:
  mode: full
columns:
  - name: account_id
    type: bigint
`)
		svc := promotionNotebookService(t, root)
		notebookID := EncodeID("notebooks/http-source")
		request := PromoteCellRequest{PipelineID: EncodeID("analytics"), TargetName: "marts.accounts_api"}
		plan, apiErr := svc.PlanPromoteCell(notebookID, "source_accounts", request)
		if apiErr != nil {
			t.Fatalf("plan: %+v", apiErr)
		}
		if plan.Assets[0].AssetType != "api" || plan.Assets[0].Connection != "duckdb-default" {
			t.Fatalf("unexpected API consequence: %+v", plan.Assets[0])
		}
		request.BaseRevision = plan.BaseRevision
		if _, apiErr := svc.PromoteCell(notebookID, "source_accounts", request); apiErr != nil {
			t.Fatalf("promote: %+v", apiErr)
		}
		content, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "accounts_api.asset.yml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{
			"name: marts.accounts_api", "type: api", "connection: duckdb-default",
			"url: https://example.test/accounts/search", "method: POST", "active: true",
			"regions: [eu, us]", "type: bearer", `token: "{{ var.api_token }}"`,
			"records_path: data.items", "name: account_id",
		} {
			if !strings.Contains(string(content), expected) {
				t.Fatalf("promoted API is missing %q:\n%s", expected, content)
			}
		}
		if strings.Contains(string(content), "snapshot:") || strings.Contains(string(content), "kind: http") {
			t.Fatalf("notebook-only HTTP fields leaked into API asset:\n%s", content)
		}
	})
}

func TestNotebookPromotionRejectsSampleSource(t *testing.T) {
	root := promotionWorkspace(t)
	writeWorkspaceFile(t, root, "inputs/events.csv", "id\n1\n")
	writeWorkspaceFile(t, root, "notebooks/sample-source/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-000000000104
blocks:
  - cell: source_sample
`)
	writeWorkspaceFile(t, root, "notebooks/sample-source/sample.source.yml", `version: 1
id: source_sample
kind: file
uri: inputs/events.csv
snapshot:
  mode: sample
  row_limit: 10
`)
	svc := promotionNotebookService(t, root)
	_, apiErr := svc.PlanPromoteCell(EncodeID("notebooks/sample-source"), "source_sample", PromoteCellRequest{
		PipelineID: EncodeID("analytics"), TargetName: "marts.sample",
	})
	if apiErr == nil || apiErr.Code != "sample_source_promotion_requires_full" {
		t.Fatalf("sample source was not rejected: %+v", apiErr)
	}
	if _, err := os.Stat(filepath.Join(root, "analytics", "assets", "marts", "sample.asset.yml")); !os.IsNotExist(err) {
		t.Fatalf("sample promotion wrote a pipeline file: %v", err)
	}
}

func TestNotebookPromotionWritesPythonAsset(t *testing.T) {
	root := promotionWorkspace(t)
	writeWorkspaceFile(t, root, "notebooks/python-promotion/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-000000000105
blocks:
  - cell: python_score
`)
	writeWorkspaceFile(t, root, "notebooks/python-promotion/score.py", `""" @bruin
id: python_score
class: notebook
type: python
@bruin """

def materialize():
    return {"score": [1]}
`)
	svc := promotionNotebookService(t, root)
	result, apiErr := svc.PromoteCell(EncodeID("notebooks/python-promotion"), "python_score", PromoteCellRequest{
		PipelineID: EncodeID("analytics"), TargetName: "marts.score",
	})
	if apiErr != nil {
		t.Fatalf("promote Python: %+v", apiErr)
	}
	if result.AssetPath != "analytics/assets/marts/score.py" {
		t.Fatalf("unexpected Python path: %q", result.AssetPath)
	}
	content, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "score.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"\"\"\" @bruin", "type: python", "connection: duckdb-default", "def materialize():"} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("promoted Python is missing %q:\n%s", expected, content)
		}
	}
	if strings.Contains(string(content), "/* @bruin") {
		t.Fatalf("Python was rendered with SQL frontmatter:\n%s", content)
	}
}

func TestWorkspaceFileTransactionRollsBackNotebookAndPipelineFiles(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "notebooks/demo/notebook.yml", "before notebook\n")
	writeWorkspaceFile(t, root, "notebooks/demo/query.sql", "before query\n")
	before := map[string][]byte{
		"notebooks/demo/notebook.yml": []byte("before notebook\n"),
		"notebooks/demo/query.sql":    []byte("before query\n"),
	}
	after := map[string][]byte{
		"notebooks/demo/notebook.yml": []byte("after notebook\n"),
		"pipelines/demo/assets/x.sql": []byte("new asset\n"),
	}
	injected := errors.New("promotion transaction failed")
	err := applyWorkspaceFileTransaction(root, before, after, func(index int, _ string) error {
		if index == 2 {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("unexpected transaction error: %v", err)
	}
	manifest, _ := os.ReadFile(filepath.Join(root, "notebooks", "demo", "notebook.yml"))
	query, _ := os.ReadFile(filepath.Join(root, "notebooks", "demo", "query.sql"))
	if string(manifest) != "before notebook\n" || string(query) != "before query\n" {
		t.Fatalf("rollback did not restore notebook files: manifest=%q query=%q", manifest, query)
	}
	if _, err := os.Stat(filepath.Join(root, "pipelines", "demo", "assets", "x.sql")); !os.IsNotExist(err) {
		t.Fatalf("rollback kept the pipeline asset: %v", err)
	}
}

func promotionWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "analytics/pipeline.yml", `id: 0c8e3c93-0000-0000-0000-000000000100
name: analytics
default_connections:
  duckdb: duckdb-default
`)
	return root
}

func promotionNotebookService(t *testing.T, root string) *NotebookService {
	t.Helper()
	return NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return mustComputeState(t, root) },
	})
}
