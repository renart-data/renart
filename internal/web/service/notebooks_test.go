package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"renart/internal/web/model"
	"renart/internal/web/notebook"
)

func writeWorkspaceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComputeStateLoadsNotebooks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "id: 0c8e3c93-0000-0000-0000-000000000001\nname: analytics\n")
	writeWorkspaceFile(t, root, "analytics/assets/orders.sql", "/* @bruin\nname: marts.orders\ntype: duckdb.sql\n@bruin */\nselect 1 as id\n")

	writeWorkspaceFile(t, root, "notebooks/revenue/notebook.yml", "id: 0c8e3c93-0000-0000-0000-0000000000aa\ntitle: Revenue\nblocks:\n  - cell: aaaa1111\n  - cell: bbbb2222\n")
	writeWorkspaceFile(t, root, "notebooks/revenue/clean_sales.sql", "/* @bruin\nid: aaaa1111\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect * from marts.orders\n")
	writeWorkspaceFile(t, root, "notebooks/revenue/by_month.sql", "/* @bruin\nid: bbbb2222\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect count(*) from clean_sales\n")

	service := NewWorkspaceService(root, "")
	state, err := service.ComputeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(state.Notebooks) != 1 {
		t.Fatalf("expected 1 notebook, got %d (errors: %v)", len(state.Notebooks), state.Errors)
	}
	nb := state.Notebooks[0]
	if nb.Title != "Revenue" || nb.UUID == "" {
		t.Fatalf("unexpected notebook: %+v", nb)
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d (problems: %v)", len(nb.Cells), nb.Problems)
	}
	for _, cell := range nb.Cells {
		if cell.Class != "notebook" {
			t.Fatalf("cell %q missing notebook class: %q", cell.Name, cell.Class)
		}
		if cell.CellID == "" {
			t.Fatalf("cell %q missing cell id", cell.Name)
		}
	}

	byMonth := nb.Cells[1]
	if byMonth.Name != "by_month" {
		t.Fatalf("expected by_month as second cell, got %q", byMonth.Name)
	}
	if len(byMonth.Upstreams) != 1 || byMonth.Upstreams[0] != "clean_sales" {
		t.Fatalf("expected by_month → clean_sales, got %v", byMonth.Upstreams)
	}

	cleanSales := nb.Cells[0]
	if len(cleanSales.ExternalRefs) != 1 || cleanSales.ExternalRefs[0] != "marts.orders" {
		t.Fatalf("expected external ref marts.orders, got %v", cleanSales.ExternalRefs)
	}

	// Pipeline assets are class-tagged too.
	if len(state.Pipelines) != 1 || len(state.Pipelines[0].Assets) != 1 {
		t.Fatalf("unexpected pipelines: %+v", state.Pipelines)
	}
	if state.Pipelines[0].Assets[0].Class != "pipeline" {
		t.Fatalf("pipeline asset missing class: %+v", state.Pipelines[0].Assets[0])
	}
}

func TestComputeStateRejectsPipelineToNotebookDeps(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "id: 0c8e3c93-0000-0000-0000-000000000002\nname: analytics\n")
	writeWorkspaceFile(t, root, "analytics/assets/report.sql", "/* @bruin\nname: marts.report\ntype: duckdb.sql\ndepends:\n  - by_month\n@bruin */\nselect * from by_month\n")

	writeWorkspaceFile(t, root, "notebooks/revenue/notebook.yml", "id: 0c8e3c93-0000-0000-0000-0000000000ab\ntitle: Revenue\nblocks:\n  - cell: bbbb2222\n")
	writeWorkspaceFile(t, root, "notebooks/revenue/by_month.sql", "/* @bruin\nid: bbbb2222\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect 1\n")

	service := NewWorkspaceService(root, "")
	state, err := service.ComputeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, stateErr := range state.Errors {
		if strings.Contains(stateErr, "pipeline assets cannot depend on notebook cells") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected direction-rule error, got %v", state.Errors)
	}
}

func TestNotebookServiceLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})

	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Revenue Exploration"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	if created.Path != "notebooks/revenue-exploration" {
		t.Fatalf("unexpected path: %q", created.Path)
	}
	if created.ManifestVersion != notebook.ManifestVersionCurrent || created.Revision == "" {
		t.Fatalf("new notebook did not use the revisioned v2 format: %+v", created)
	}
	notebookID := created.ID

	// A new notebook seeds one runnable cell with a concise two-word name.
	if len(created.Cells) != 1 || !regexp.MustCompile(`^[a-z]+_[a-z]+$`).MatchString(created.Cells[0].Name) {
		t.Fatalf("expected one seeded two-word cell, got %+v", created.Cells)
	}
	if _, apiErr := svc.DeleteCell(notebookID, created.Cells[0].CellID); apiErr != nil {
		t.Fatalf("delete example failed: %+v", apiErr)
	}

	withCell, apiErr := svc.CreateCell(notebookID, CreateCellRequest{Name: "base"})
	if apiErr != nil {
		t.Fatalf("create cell failed: %+v", apiErr)
	}
	if len(withCell.Cells) != 1 || withCell.Cells[0].Name != "base" {
		t.Fatalf("unexpected cells: %+v", withCell.Cells)
	}
	baseCellID := withCell.Cells[0].CellID

	// Update content; the durable id survives even though the payload
	// omits it.
	updated, apiErr := svc.UpdateCell(notebookID, baseCellID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 21 as half\n"})
	if apiErr != nil {
		t.Fatalf("update cell failed: %+v", apiErr)
	}
	if updated.Cells[0].CellID != baseCellID {
		t.Fatalf("cell id changed on update: %q → %q", baseCellID, updated.Cells[0].CellID)
	}

	withSecond, apiErr := svc.CreateCell(notebookID, CreateCellRequest{Name: "doubled"})
	if apiErr != nil {
		t.Fatalf("create second cell failed: %+v", apiErr)
	}
	doubledCellID := withSecond.Cells[1].CellID
	if _, apiErr = svc.UpdateCell(notebookID, doubledCellID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect half * 2 as answer from base\n"}); apiErr != nil {
		t.Fatalf("update second cell failed: %+v", apiErr)
	}

	result, apiErr := svc.Run(context.Background(), notebookID, RunNotebookRequest{All: true})
	if apiErr != nil {
		t.Fatalf("run failed: %+v", apiErr)
	}
	if result.Status != "ok" || len(result.Results) != 2 {
		t.Fatalf("unexpected run result: %+v", result)
	}
	answer := result.Results[1]
	if answer.Name != "doubled" || len(answer.Rows) != 1 {
		t.Fatalf("unexpected answer result: %+v", answer)
	}
	if fmt.Sprintf("%v", answer.Rows[0][0]) != "42" {
		t.Fatalf("expected 42, got %v", answer.Rows[0][0])
	}
	if answer.Performance == nil || answer.Performance.RequestSetupMS == nil ||
		answer.Performance.RuntimeSyncMS == nil || answer.Performance.RequestTotalMS == nil {
		t.Fatalf("request phase telemetry is incomplete: %+v", answer.Performance)
	}
	if *answer.Performance.RequestTotalMS < *answer.Performance.RequestSetupMS {
		t.Fatalf("request total %.3fms is smaller than setup %.3fms", *answer.Performance.RequestTotalMS, *answer.Performance.RequestSetupMS)
	}

	// Running just the downstream cell on a fresh session pulls in the
	// missing ancestor automatically.
	if apiErr := svc.CloseSession(notebookID); apiErr != nil {
		t.Fatalf("close session failed: %+v", apiErr)
	}
	partial, apiErr := svc.Run(context.Background(), notebookID, RunNotebookRequest{Cells: []string{doubledCellID}})
	if apiErr != nil {
		t.Fatalf("partial run failed: %+v", apiErr)
	}
	if len(partial.Results) != 2 {
		t.Fatalf("expected ancestor to be pulled in, got %+v", partial.Results)
	}

	// Delete a cell: file, block, and session object go away.
	afterDelete, apiErr := svc.DeleteCell(notebookID, doubledCellID)
	if apiErr != nil {
		t.Fatalf("delete cell failed: %+v", apiErr)
	}
	if len(afterDelete.Cells) != 1 {
		t.Fatalf("expected 1 cell after delete, got %+v", afterDelete.Cells)
	}

	// Delete the notebook: folder and session file removed.
	uuid := afterDelete.UUID
	if apiErr := svc.Delete(notebookID); apiErr != nil {
		t.Fatalf("delete notebook failed: %+v", apiErr)
	}
	if _, err := os.Stat(filepath.Join(root, "notebooks", "revenue-exploration")); !os.IsNotExist(err) {
		t.Fatal("notebook folder still exists")
	}
	if _, err := os.Stat(svc.SessionStore().DBPath(uuid)); !os.IsNotExist(err) {
		t.Fatal("session db still exists")
	}
}

func TestNotebookParametersRenderAndPersistOnlyInRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Parameterized"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	if apiErr := svc.SetAutoRecompute(created.ID, false, "", nil); apiErr != nil {
		t.Fatalf("disable auto-recompute: %+v", apiErr)
	}

	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationParametersReplace,
			Parameters: []model.NotebookParameter{
				{ID: "customer", Label: "Customer", Type: "text", Default: "default"},
				{ID: "minimum", Type: "number", Default: float64(3)},
				{ID: "active", Type: "boolean", Default: true},
			},
		}},
	})
	if apiErr != nil || !plan.CanApply {
		t.Fatalf("prepare parameters failed: plan=%+v err=%+v", plan, apiErr)
	}
	withParameters, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply parameters failed: %+v", apiErr)
	}
	cell := withParameters.Notebook.Cells[0]
	content := "/* @bruin\ntype: duckdb.sql\n@bruin */\n" +
		"select {{ parameter.customer }} as customer, {{ parameter.minimum }} as minimum, {{ parameter.active }} as active\n"
	if _, apiErr := svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{Content: content}); apiErr != nil {
		t.Fatalf("update cell failed: %+v", apiErr)
	}

	result, apiErr := svc.Run(context.Background(), created.ID, RunNotebookRequest{
		All: true,
		Parameters: map[string]any{
			"customer": "O'Reilly",
			"minimum":  float64(7),
		},
	})
	if apiErr != nil || len(result.Results) != 1 || result.Results[0].Status != notebook.CellRunOK {
		t.Fatalf("parameterized run failed: result=%+v err=%+v", result, apiErr)
	}
	row := result.Results[0].Rows[0]
	if fmt.Sprint(row[0]) != "O'Reilly" || fmt.Sprint(row[1]) != "7" || fmt.Sprint(row[2]) != "true" {
		t.Fatalf("unexpected parameterized row: %+v", row)
	}
	runtime, runtimeErr := svc.Runtime(created.ID)
	if runtimeErr != nil {
		t.Fatalf("runtime failed: %+v", runtimeErr)
	}
	if runtime.ParameterValues["customer"] != "O'Reilly" || fmt.Sprint(runtime.ParameterValues["minimum"]) != "7" {
		t.Fatalf("runtime values were not retained: %+v", runtime.ParameterValues)
	}

	if _, apiErr := svc.Run(context.Background(), created.ID, RunNotebookRequest{
		All: true, Parameters: map[string]any{"missing": "value"},
	}); apiErr == nil || apiErr.Code != "invalid_notebook_parameter_values" {
		t.Fatalf("unknown parameter override was not rejected: %+v", apiErr)
	}
	loaded, loadErr := svc.Get(created.ID)
	if loadErr != nil || loaded.Parameters[0].Default != "default" {
		t.Fatalf("runtime override leaked into notebook.yml: notebook=%+v err=%+v", loaded.Parameters, loadErr)
	}
}

func TestNotebookServiceStructuredBlocksUseStableIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Presentation"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	cellID := created.Cells[0].CellID

	updated, apiErr := svc.UpdateBlocks(created.ID, []model.NotebookBlock{
		{Cell: cellID},
		{Markdown: "## Findings"},
		{Visualization: &model.NotebookVisualization{
			Source: cellID,
			Definition: map[string]any{
				"version": 1,
				"type":    "line",
				"encoding": map[string]any{
					"x": map[string]any{"field": "greeting"},
					"y": []any{map[string]any{"field": "answer"}},
				},
			},
		}},
	})
	if apiErr != nil {
		t.Fatalf("update blocks failed: %+v", apiErr)
	}
	if updated.Revision == created.Revision {
		t.Fatal("block update did not advance notebook revision")
	}
	if len(updated.Blocks) != 3 || !strings.HasPrefix(updated.Blocks[1].ID, "md_") {
		t.Fatalf("markdown block did not receive identity: %+v", updated.Blocks)
	}
	visualization := updated.Blocks[2]
	if !strings.HasPrefix(visualization.ID, "viz_") || visualization.Visualization == nil || visualization.Visualization.ID != visualization.ID {
		t.Fatalf("visualization block did not receive consistent identity: %+v", visualization)
	}

	reloaded, apiErr := svc.Get(created.ID)
	if apiErr != nil {
		t.Fatalf("reload failed: %+v", apiErr)
	}
	if reloaded.Blocks[1].ID != updated.Blocks[1].ID || reloaded.Blocks[2].ID != updated.Blocks[2].ID {
		t.Fatalf("block identities changed across reload: before=%+v after=%+v", updated.Blocks, reloaded.Blocks)
	}
}

func TestNotebookRuntimeSnapshotIncludesActiveCells(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Running state"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	cellID := created.Cells[0].CellID
	rt := svc.runtimes.get(created.UUID)
	_, finishRun := rt.beginManualRun(context.Background(), []string{cellID})

	runtime, runtimeErr := svc.Runtime(created.ID)
	if runtimeErr != nil {
		t.Fatalf("runtime failed: %+v", runtimeErr)
	}
	if len(runtime.Running) != 1 || runtime.Running[0] != cellID {
		t.Fatalf("active cell missing from runtime snapshot: %v", runtime.Running)
	}

	finishRun()
	runtime, runtimeErr = svc.Runtime(created.ID)
	if runtimeErr != nil {
		t.Fatalf("runtime after finish failed: %+v", runtimeErr)
	}
	if len(runtime.Running) != 0 {
		t.Fatalf("finished cell remained in runtime snapshot: %v", runtime.Running)
	}
}

func TestNotebookServiceExplicitlyUpgradesLegacyManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "notebooks/legacy/notebook.yml", "id: 0c8e3c93-0000-0000-0000-0000000000dd\ntitle: Legacy\nblocks:\n  - markdown: Notes\n  - cell: aaaa1111\n")
	writeWorkspaceFile(t, root, "notebooks/legacy/query.sql", "/* @bruin\nid: aaaa1111\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect 1\n")
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	notebookID := EncodeID("notebooks/legacy")
	legacy, apiErr := svc.Get(notebookID)
	if apiErr != nil {
		t.Fatalf("get legacy failed: %+v", apiErr)
	}
	if legacy.ManifestVersion != notebook.ManifestVersionLegacy {
		t.Fatalf("legacy notebook loaded as version %d", legacy.ManifestVersion)
	}

	upgraded, apiErr := svc.UpgradeManifest(notebookID, legacy.Revision)
	if apiErr != nil {
		t.Fatalf("upgrade failed: %+v", apiErr)
	}
	if upgraded.ManifestVersion != notebook.ManifestVersionCurrent || !strings.HasPrefix(upgraded.Blocks[0].ID, "md_") {
		t.Fatalf("unexpected upgraded notebook: %+v", upgraded)
	}
	_, apiErr = svc.UpgradeManifest(notebookID, legacy.Revision)
	if apiErr == nil || apiErr.Status != http.StatusConflict || apiErr.Code != "notebook_edit_conflict" {
		t.Fatalf("stale notebook revision was not rejected: %+v", apiErr)
	}
}

func TestGeneratedNotebookCellNamesAreConciseAndCollisionSafe(t *testing.T) {
	nb := &notebook.Notebook{}
	first := cellAutonameFromSeed(nb, nil, 42)
	if !regexp.MustCompile(`^[a-z]+_[a-z]+$`).MatchString(first) {
		t.Fatalf("generated name is not a two-word identifier: %q", first)
	}

	second := cellAutonameFromSeed(nb, map[string]bool{first: true}, 42)
	if second == first {
		t.Fatalf("pipeline asset collision was not avoided: %q", second)
	}
	if !regexp.MustCompile(`^[a-z]+_[a-z]+$`).MatchString(second) {
		t.Fatalf("collision fallback lost the two-word form: %q", second)
	}
}

func TestNotebookCellUpdateRejectsStaleRevision(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Concurrent Editing"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	cell := created.Cells[0]
	if cell.ContentRevision == "" {
		t.Fatal("new notebook cell did not include a content revision")
	}

	latestContent := "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 2 as latest\n"
	updated, apiErr := svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
		Content:      latestContent,
		BaseRevision: cell.ContentRevision,
	})
	if apiErr != nil {
		t.Fatalf("first revision-checked update failed: %+v", apiErr)
	}
	if updated.Cells[0].ContentRevision == "" || updated.Cells[0].ContentRevision == cell.ContentRevision {
		t.Fatalf("content revision did not advance: before=%q after=%q", cell.ContentRevision, updated.Cells[0].ContentRevision)
	}

	_, apiErr = svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
		Content:      "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 1 as stale\n",
		BaseRevision: cell.ContentRevision,
	})
	if apiErr == nil {
		t.Fatal("expected stale update to be rejected")
	}
	if apiErr.Status != 409 || apiErr.Code != "cell_edit_conflict" {
		t.Fatalf("unexpected stale-update error: %+v", apiErr)
	}

	fresh, apiErr := svc.Get(created.ID)
	if apiErr != nil {
		t.Fatalf("get after conflict failed: %+v", apiErr)
	}
	if !strings.Contains(fresh.Cells[0].Content, "select 2 as latest") {
		t.Fatalf("stale update replaced newer content: %q", fresh.Cells[0].Content)
	}

	// Two clients that branch from the same acknowledged snapshot must not both
	// win, even when they reach the service concurrently.
	start := make(chan struct{})
	type updateResult struct {
		content string
		err     *APIError
	}
	results := make(chan updateResult, 2)
	for _, content := range []string{
		"/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 3 as peer_a\n",
		"/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 4 as peer_b\n",
	} {
		go func(content string) {
			<-start
			_, updateErr := svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
				Content:      content,
				BaseRevision: updated.Cells[0].ContentRevision,
			})
			results <- updateResult{content: content, err: updateErr}
		}(content)
	}
	close(start)
	first := <-results
	second := <-results
	winners := 0
	conflicts := 0
	winningContent := ""
	for _, result := range []updateResult{first, second} {
		if result.err == nil {
			winners++
			winningContent = result.content
		} else if result.err.Status == http.StatusConflict && result.err.Code == "cell_edit_conflict" {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent-update result: %+v", result.err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent updates: got %d winner(s), %d conflict(s)", winners, conflicts)
	}
	concurrentFresh, apiErr := svc.Get(created.ID)
	if apiErr != nil {
		t.Fatalf("get after concurrent updates failed: %+v", apiErr)
	}
	statementStart := strings.Index(winningContent, "select ")
	if statementStart < 0 || !strings.Contains(concurrentFresh.Cells[0].Content, strings.TrimSpace(winningContent[statementStart:])) {
		t.Fatalf("disk does not contain the winning update: %q", concurrentFresh.Cells[0].Content)
	}
}

func TestNotebookCellIdenticalUpdateDoesNotBecomeStale(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	events := 0
	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		PublishEvent: func(any) {
			events++
		},
	})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Stable Focus"})
	if apiErr != nil {
		t.Fatalf("create failed: %+v", apiErr)
	}
	cell := created.Cells[0]

	updated, apiErr := svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
		Content:      cell.Content,
		BaseRevision: cell.ContentRevision,
	})
	if apiErr != nil {
		t.Fatalf("identical update failed: %+v", apiErr)
	}
	if updated.Cells[0].ContentRevision != cell.ContentRevision {
		t.Fatalf("identical update changed the revision: before=%q after=%q", cell.ContentRevision, updated.Cells[0].ContentRevision)
	}
	runtime, apiErr := svc.Runtime(created.ID)
	if apiErr != nil {
		t.Fatalf("runtime failed: %+v", apiErr)
	}
	if len(runtime.Stale) != 0 {
		t.Fatalf("identical update marked the cell stale: %v", runtime.Stale)
	}
	if events != 0 {
		t.Fatalf("identical update emitted %d runtime events", events)
	}
}

func TestNotebookServicePromoteCell(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "id: 0c8e3c93-0000-0000-0000-0000000000c1\nname: analytics\n")
	writeWorkspaceFile(t, root, "analytics/assets/orders.sql", "/* @bruin\nname: marts.orders\ntype: duckdb.sql\n@bruin */\nselect 1 as id\n")

	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() WorkspaceState { return mustComputeState(t, root) },
	})

	nb, apiErr := svc.Create(CreateNotebookRequest{Title: "Promote Me"})
	if apiErr != nil {
		t.Fatalf("create: %+v", apiErr)
	}
	// Drop the seeded example cell so this test controls the DAG.
	if _, apiErr := svc.DeleteCell(nb.ID, nb.Cells[0].CellID); apiErr != nil {
		t.Fatalf("delete example: %+v", apiErr)
	}
	withBase, apiErr := svc.CreateCell(nb.ID, CreateCellRequest{Name: "base"})
	if apiErr != nil {
		t.Fatalf("create base: %+v", apiErr)
	}
	baseID := withBase.Cells[0].CellID
	if _, apiErr = svc.UpdateCell(nb.ID, baseID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 1 as id, 10 as amount\n"}); apiErr != nil {
		t.Fatalf("update base: %+v", apiErr)
	}
	withChild, apiErr := svc.CreateCell(nb.ID, CreateCellRequest{Name: "child"})
	if apiErr != nil {
		t.Fatalf("create child: %+v", apiErr)
	}
	childID := withChild.Cells[1].CellID
	if _, apiErr = svc.UpdateCell(nb.ID, childID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect sum(amount) from base\n"}); apiErr != nil {
		t.Fatalf("update child: %+v", apiErr)
	}

	// Promote base → marts.revenue in the analytics pipeline.
	pipelineID := EncodeID("analytics")
	result, apiErr := svc.PromoteCell(nb.ID, baseID, PromoteCellRequest{PipelineID: pipelineID, TargetName: "marts.revenue"})
	if apiErr != nil {
		t.Fatalf("promote: %+v", apiErr)
	}
	if result.DialectWarning != "" {
		t.Fatalf("unexpected dialect warning for duckdb→duckdb: %q", result.DialectWarning)
	}

	// The pipeline asset file exists with the right name and class.
	assetPath := filepath.Join(root, "analytics", "assets", "marts", "revenue.sql")
	content, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("promoted asset missing: %v", err)
	}
	if !strings.Contains(string(content), "name: marts.revenue") || !strings.Contains(string(content), "materialization") {
		t.Fatalf("unexpected promoted content: %s", content)
	}

	// The cell is gone from the notebook; the child now references the
	// pipeline asset name.
	if len(result.Notebook.Cells) != 1 || result.Notebook.Cells[0].Name != "child" {
		t.Fatalf("unexpected remaining cells: %+v", result.Notebook.Cells)
	}
	if !strings.Contains(result.Notebook.Cells[0].Content, "from marts.revenue") {
		t.Fatalf("child reference not rewritten: %q", result.Notebook.Cells[0].Content)
	}

	// The original cell file is gone.
	if _, err := os.Stat(filepath.Join(root, "notebooks", "promote-me", "base.sql")); !os.IsNotExist(err) {
		t.Fatal("original cell file still present")
	}
}

func TestNotebookServicePromoteCellWithUpstream(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "id: 0c8e3c93-0000-0000-0000-0000000000d1\nname: analytics\n")

	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() WorkspaceState { return mustComputeState(t, root) },
	})

	nb, apiErr := svc.Create(CreateNotebookRequest{Title: "Chain"})
	if apiErr != nil {
		t.Fatalf("create: %+v", apiErr)
	}
	if _, apiErr := svc.DeleteCell(nb.ID, nb.Cells[0].CellID); apiErr != nil {
		t.Fatalf("delete example: %+v", apiErr)
	}
	withBase, apiErr := svc.CreateCell(nb.ID, CreateCellRequest{Name: "base"})
	if apiErr != nil {
		t.Fatalf("create base: %+v", apiErr)
	}
	baseID := withBase.Cells[0].CellID
	if _, apiErr = svc.UpdateCell(nb.ID, baseID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 1 as id, 10 as amount\n"}); apiErr != nil {
		t.Fatalf("update base: %+v", apiErr)
	}
	withChild, apiErr := svc.CreateCell(nb.ID, CreateCellRequest{Name: "child"})
	if apiErr != nil {
		t.Fatalf("create child: %+v", apiErr)
	}
	childID := withChild.Cells[1].CellID
	if _, apiErr = svc.UpdateCell(nb.ID, childID, UpdateCellRequest{Content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect sum(amount) from base\n"}); apiErr != nil {
		t.Fatalf("update child: %+v", apiErr)
	}

	// Promote child + its upstream into the pipeline together.
	pipelineID := EncodeID("analytics")
	result, apiErr := svc.PromoteCell(nb.ID, childID, PromoteCellRequest{
		PipelineID:      pipelineID,
		TargetName:      "marts.child",
		IncludeUpstream: true,
	})
	if apiErr != nil {
		t.Fatalf("promote: %+v", apiErr)
	}
	if result.PromotedCount != 2 {
		t.Fatalf("expected 2 promoted assets, got %d", result.PromotedCount)
	}

	// Both assets exist; the child reads the promoted upstream by its new name.
	baseAsset, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "base.sql"))
	if err != nil {
		t.Fatalf("base asset missing: %v", err)
	}
	if !strings.Contains(string(baseAsset), "name: marts.base") {
		t.Fatalf("unexpected base content: %s", baseAsset)
	}
	childAsset, err := os.ReadFile(filepath.Join(root, "analytics", "assets", "marts", "child.sql"))
	if err != nil {
		t.Fatalf("child asset missing: %v", err)
	}
	if !strings.Contains(string(childAsset), "from marts.base") {
		t.Fatalf("child upstream reference not rewritten: %s", childAsset)
	}

	// Both cells left the notebook.
	if len(result.Notebook.Cells) != 0 {
		t.Fatalf("expected no cells left, got %+v", result.Notebook.Cells)
	}
}

func mustComputeState(t *testing.T, root string) WorkspaceState {
	t.Helper()
	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestResolveNotebookCellByID(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "notebooks/revenue/notebook.yml", "id: 0c8e3c93-0000-0000-0000-0000000000f1\ntitle: Revenue\nblocks:\n  - cell: aaaa1111\n")
	writeWorkspaceFile(t, root, "notebooks/revenue/clean.sql", "/* @bruin\nid: aaaa1111\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect 1 as id\n")

	svc := NewWorkspaceService(root, "")
	assetID := EncodeID("notebooks/revenue/clean.sql")
	rel, parsed, asset, err := svc.ResolveAssetByID(context.Background(), assetID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if rel != "notebooks/revenue/clean.sql" {
		t.Fatalf("unexpected rel path: %q", rel)
	}
	if asset == nil || string(asset.Type) != "duckdb.sql" {
		t.Fatalf("unexpected asset: %+v", asset)
	}
	if parsed == nil || len(parsed.Assets) != 1 {
		t.Fatalf("expected synthetic pipeline with 1 cell, got %+v", parsed)
	}
}

func TestNotebookJinjaContextForAssetUsesRuntimeValues(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "notebooks/revenue/notebook.yml", `version: 2
id: 0c8e3c93-0000-0000-0000-0000000000f2
title: Revenue
parameters:
  - id: region
    type: text
    default: eu
blocks:
  - cell: aaaa1111
`)
	writeWorkspaceFile(t, root, "notebooks/revenue/clean.sql", "/* @bruin\nid: aaaa1111\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect {{ parameter.region }} as region\n")

	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	notebookID := EncodeID("notebooks/revenue")
	nb, apiErr := svc.load(notebookID)
	if apiErr != nil {
		t.Fatalf("load notebook: %+v", apiErr)
	}
	if _, apiErr := svc.updateNotebookParameterValues(notebookID, nb, map[string]any{"region": "us"}, false); apiErr != nil {
		t.Fatalf("set runtime parameter: %+v", apiErr)
	}

	resolved, found, err := svc.JinjaContextForAsset(context.Background(), EncodeID("notebooks/revenue/clean.sql"))
	if err != nil || !found {
		t.Fatalf("resolve notebook Jinja context: found=%v err=%v", found, err)
	}
	if len(resolved.Definitions) != 1 || resolved.Values["region"] != "us" {
		t.Fatalf("unexpected notebook Jinja context: %+v", resolved)
	}

	_, found, err = svc.JinjaContextForAsset(context.Background(), EncodeID("analytics/assets/orders.sql"))
	if err != nil || found {
		t.Fatalf("ordinary asset resolved as notebook: found=%v err=%v", found, err)
	}
}
