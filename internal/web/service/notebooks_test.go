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
