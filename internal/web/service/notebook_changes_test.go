package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renart/internal/web/model"
)

func TestNotebookChangeSetPrepareAndApply(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	updates := 0
	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		PushWorkspaceUpdate: func(_ context.Context, _, _ string) {
			updates++
		},
	})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Reviewed changes"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	updates = 0
	seedCellID := created.Cells[0].CellID

	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{
			{
				Kind:     NotebookOperationCellCreate,
				Name:     "warehouse_snapshot",
				Language: "sql",
				Content:  "select 42 as answer\n",
			},
			{
				Kind:    NotebookOperationMarkdownCreate,
				Content: "## Findings",
			},
			{
				Kind: NotebookOperationVisualizationCreate,
				Visualization: &model.NotebookVisualization{
					Source: seedCellID,
					Definition: map[string]any{
						"version": 1,
						"type":    "table",
					},
				},
			},
		},
	})
	if apiErr != nil {
		t.Fatalf("prepare change set: %+v", apiErr)
	}
	if !plan.CanApply || len(plan.BlockingProblems) != 0 {
		t.Fatalf("prepared plan is not applicable: %+v", plan)
	}
	if plan.ChangeSet.ExpectedRevision == "" || plan.ChangeSet.ExpectedRevision == created.Revision {
		t.Fatalf("prepare did not calculate a new reviewed revision: %+v", plan.ChangeSet)
	}
	if plan.ChangeSet.Operations[0].CellID == "" {
		t.Fatalf("cell create was not normalized with a durable id: %+v", plan.ChangeSet.Operations[0])
	}
	if !strings.HasPrefix(plan.ChangeSet.Operations[1].BlockID, "md_") ||
		!strings.HasPrefix(plan.ChangeSet.Operations[2].BlockID, "viz_") {
		t.Fatalf("presentation creates were not normalized with durable ids: %+v", plan.ChangeSet.Operations)
	}
	if len(plan.Diff) != 2 {
		t.Fatalf("expected manifest and cell diff, got %+v", plan.Diff)
	}
	if updates != 0 {
		t.Fatalf("prepare emitted %d workspace updates", updates)
	}
	unmodified, apiErr := svc.Get(created.ID)
	if apiErr != nil {
		t.Fatalf("reload after prepare: %+v", apiErr)
	}
	if unmodified.Revision != created.Revision || len(unmodified.Cells) != 1 {
		t.Fatalf("prepare mutated authoritative files: before=%+v after=%+v", created, unmodified)
	}

	result, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply change set: %+v", apiErr)
	}
	if result.Notebook.Revision != plan.ChangeSet.ExpectedRevision || len(result.Notebook.Cells) != 2 {
		t.Fatalf("unexpected applied notebook: %+v", result.Notebook)
	}
	if updates != 1 {
		t.Fatalf("apply emitted %d workspace updates, want one", updates)
	}
	foundMarkdown, foundVisualization := false, false
	for _, block := range result.Notebook.Blocks {
		foundMarkdown = foundMarkdown || block.ID == plan.ChangeSet.Operations[1].BlockID
		foundVisualization = foundVisualization || block.ID == plan.ChangeSet.Operations[2].BlockID
	}
	if !foundMarkdown || !foundVisualization {
		t.Fatalf("applied blocks do not match normalized change set: %+v", result.Notebook.Blocks)
	}
}

func TestNotebookChangeSetReplacesTypedParametersAtomically(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Parameterized"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}

	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationParametersReplace,
			Parameters: []model.NotebookParameter{{
				ID: "region", Label: "Region", Type: "select", Default: "eu",
				Options: &model.NotebookParameterOptions{Values: []any{"eu", "us"}},
			}},
		}},
	})
	if apiErr != nil || !plan.CanApply {
		t.Fatalf("prepare parameters: plan=%+v error=%+v", plan, apiErr)
	}
	if len(plan.Diff) != 1 || !strings.Contains(plan.Diff[0].After, "parameters:") {
		t.Fatalf("parameter change did not produce a reviewable manifest diff: %+v", plan.Diff)
	}
	result, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply parameters: %+v", apiErr)
	}
	if len(result.Notebook.Parameters) != 1 || result.Notebook.Parameters[0].ID != "region" {
		t.Fatalf("typed parameters were not returned: %+v", result.Notebook.Parameters)
	}

	_, apiErr = svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: result.Notebook.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationParametersReplace,
			Parameters: []model.NotebookParameter{{
				ID: "region", Type: "select", Default: "apac",
				Options: &model.NotebookParameterOptions{Values: []any{"eu", "us"}},
			}},
		}},
	})
	if apiErr == nil || apiErr.Code != "invalid_notebook_parameters" {
		t.Fatalf("invalid parameter definition was accepted: %+v", apiErr)
	}
}

func TestNotebookChangeSetRejectsUnpreparedAndStaleApply(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Conflicting changes"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	changeSet := NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{{
			Kind:    NotebookOperationMarkdownCreate,
			Content: "prepared",
		}},
	}
	if _, apiErr = svc.ApplyChangeSet(created.ID, changeSet); apiErr == nil || apiErr.Code != "notebook_change_not_prepared" {
		t.Fatalf("unprepared apply was not rejected: %+v", apiErr)
	}
	plan, apiErr := svc.PrepareChangeSet(created.ID, changeSet)
	if apiErr != nil {
		t.Fatalf("prepare: %+v", apiErr)
	}
	cell := created.Cells[0]
	if _, apiErr = svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
		Content:      strings.Replace(cell.Content, "select", "select 2 as changed,", 1),
		BaseRevision: cell.ContentRevision,
	}); apiErr != nil {
		t.Fatalf("concurrent edit: %+v", apiErr)
	}
	if _, apiErr = svc.ApplyChangeSet(created.ID, plan.ChangeSet); apiErr == nil || apiErr.Code != "notebook_edit_conflict" {
		t.Fatalf("stale apply was not rejected: %+v", apiErr)
	}
}

func TestNotebookChangeSetConfiguresWarehouseSourceFromBackendConnectionMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState: func() model.WorkspaceState {
			return model.WorkspaceState{QueryConnections: []model.WorkspaceQueryConnection{{
				Name: "postgres-other", ConnectionType: "postgres", AssetType: "pg.sql", Dialect: "postgres",
			}}}
		},
	})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Source configuration"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	cellID := created.Cells[0].CellID
	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationCellSourceConfigure, CellID: cellID,
			Connection: "postgres-other", SnapshotMode: "sample", RowLimit: 250,
		}},
	})
	if apiErr != nil {
		t.Fatalf("prepare source config: %+v", apiErr)
	}
	operation := plan.ChangeSet.Operations[0]
	if operation.AssetType != "pg.sql" || !strings.Contains(operation.Content, "connection: postgres-other") {
		t.Fatalf("source operation was not server-normalized: %+v", operation)
	}
	result, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply source config: %+v", apiErr)
	}
	cell := result.Notebook.Cells[0]
	if cell.Connection != "postgres-other" || cell.Type != "pg.sql" || cell.Meta["renart_notebook_snapshot_mode"] != "sample" {
		t.Fatalf("source execution identity did not round trip: %+v", cell)
	}

	_, apiErr = svc.PrepareChangeSet(result.Notebook.ID, NotebookChangeSet{
		BaseRevision: result.Notebook.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationCellSourceConfigure, CellID: cellID, Connection: "missing",
		}},
	})
	if apiErr == nil || apiErr.Code != "unknown_notebook_source_connection" {
		t.Fatalf("unknown source connection was not rejected: %+v", apiErr)
	}
}

func TestNotebookChangeSetCreatesTypedObjectAndHTTPSourceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState: func() model.WorkspaceState {
			return model.WorkspaceState{Connections: map[string]string{"lake-files": "s3"}}
		},
	})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Non SQL sources"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{
			{
				Kind: NotebookOperationSourceCreate, Name: "events",
				Source: &model.NotebookSourceDefinition{
					Kind: "file", Connection: "lake-files", URI: "archive/events.parquet",
					Snapshot: model.NotebookSourceSnapshot{Mode: "full"},
				},
			},
			{
				Kind: NotebookOperationSourceCreate, Name: "accounts_api",
				Source: &model.NotebookSourceDefinition{
					Kind: "http",
					Request: model.NotebookSourceRequest{
						URL: "https://example.test/accounts", Method: "POST",
						Body: map[string]any{"active": true},
					},
					Response: model.NotebookSourceResponse{RecordsPath: "data.items"},
					Snapshot: model.NotebookSourceSnapshot{Mode: "sample", RowLimit: 100},
				},
			},
		},
	})
	if apiErr != nil || !plan.CanApply {
		t.Fatalf("prepare source changes: plan=%+v err=%+v", plan, apiErr)
	}
	if !strings.HasPrefix(plan.ChangeSet.Operations[0].CellID, "source_") ||
		plan.ChangeSet.Operations[0].Source.ID != plan.ChangeSet.Operations[0].CellID {
		t.Fatalf("source create was not normalized with one durable id: %+v", plan.ChangeSet.Operations[0])
	}
	result, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply source changes: %+v", apiErr)
	}
	if len(result.Notebook.Cells) != 3 {
		t.Fatalf("expected seed plus two sources, got %+v", result.Notebook.Cells)
	}
	for _, name := range []string{"events.source.yml", "accounts_api.source.yml"} {
		content, err := os.ReadFile(filepath.Join(root, "notebooks", "non-sql-sources", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(content), "version: 1") || !strings.Contains(string(content), "snapshot:") {
			t.Fatalf("source file is not versioned/canonical: %s", content)
		}
	}
	var httpSource *model.NotebookSourceDefinition
	for _, cell := range result.Notebook.Cells {
		if cell.Name == "accounts_api" {
			httpSource = cell.NotebookSource
		}
	}
	if httpSource == nil || httpSource.Request.Method != "POST" || httpSource.Snapshot.RowLimit != 100 {
		t.Fatalf("HTTP source summary did not round trip: %+v", httpSource)
	}
}

func TestNotebookChangeSetMigratesLegacyVisualizationAtomically(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Legacy chart"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	cell := created.Cells[0]
	legacyContent := strings.Replace(cell.Content, "select 'hello'", "-- @viz(table)\nselect 'hello'", 1)
	withLegacy, apiErr := svc.UpdateCell(created.ID, cell.CellID, UpdateCellRequest{
		Content: legacyContent, BaseRevision: cell.ContentRevision,
	})
	if apiErr != nil {
		t.Fatalf("add legacy directive: %+v", apiErr)
	}

	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: withLegacy.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationVisualizationMigrate, CellID: cell.CellID,
		}},
	})
	if apiErr != nil || !plan.CanApply {
		t.Fatalf("prepare migration: plan=%+v err=%+v", plan, apiErr)
	}
	if !strings.HasPrefix(plan.ChangeSet.Operations[0].BlockID, "viz_") || len(plan.Diff) != 2 {
		t.Fatalf("migration was not normalized as one cell+manifest change: %+v", plan)
	}
	result, apiErr := svc.ApplyChangeSet(created.ID, plan.ChangeSet)
	if apiErr != nil {
		t.Fatalf("apply migration: %+v", apiErr)
	}
	if strings.Contains(result.Notebook.Cells[0].Content, "@viz") {
		t.Fatalf("legacy directive remained after migration: %s", result.Notebook.Cells[0].Content)
	}
	found := false
	for _, block := range result.Notebook.Blocks {
		if block.ID == plan.ChangeSet.Operations[0].BlockID && block.Visualization != nil {
			found = block.Visualization.Source == cell.CellID && block.Visualization.Definition["type"] == "table"
		}
	}
	if !found {
		t.Fatalf("structured visualization block was not created: %+v", result.Notebook.Blocks)
	}
}

func TestNotebookFileTransactionRollsBackExactSnapshot(t *testing.T) {
	root := t.TempDir()
	notebookDir := filepath.Join(root, "notebooks", "rollback")
	writeWorkspaceFile(t, root, "notebooks/rollback/notebook.yml", "version: 2\n")
	writeWorkspaceFile(t, root, "notebooks/rollback/query.sql", "before\n")
	if err := os.WriteFile(filepath.Join(notebookDir, "empty.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := readNotebookAuthoredFiles(notebookDir)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string][]byte{
		"notebook.yml": []byte("version: 2\ntitle: changed\n"),
		"new.sql":      []byte("new\n"),
		"query.sql":    []byte("after\n"),
	}
	injected := errors.New("injected transaction failure")
	err = applyNotebookFileTransaction(root, notebookDir, before, after, func(index int, _ string) error {
		if index == 2 {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("unexpected transaction error: %v", err)
	}
	restored, err := readNotebookAuthoredFiles(notebookDir)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNotebookFiles(restored, before) {
		t.Fatalf("rollback did not restore exact files: before=%q restored=%q", before, restored)
	}
	if info, err := os.Stat(filepath.Join(notebookDir, "empty.py")); err != nil || info.Size() != 0 {
		t.Fatalf("zero-byte file was not restored: info=%v err=%v", info, err)
	}
}

func TestRecoverNotebookFileTransactionsRollsBackInterruptedApply(t *testing.T) {
	root := t.TempDir()
	targetRel := filepath.Join("notebooks", "recovery", "query.sql")
	target := filepath.Join(root, targetRel)
	writeWorkspaceFile(t, root, targetRel, "partial\n")
	journalDir := filepath.Join(root, ".renart", "notebook-transactions", "transaction-test")
	backupRel := filepath.Join("backups", "000")
	if err := os.MkdirAll(filepath.Join(journalDir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, backupRel), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := notebookTransactionState{
		Phase: "applying",
		Entries: []notebookTransactionEntry{{
			Path:         filepath.ToSlash(targetRel),
			BeforeExists: true,
			Backup:       filepath.ToSlash(backupRel),
		}},
	}
	if err := writeNotebookTransactionState(journalDir, state); err != nil {
		t.Fatal(err)
	}
	if err := recoverNotebookFileTransactions(root); err != nil {
		t.Fatalf("recover transactions: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original\n" {
		t.Fatalf("recovery kept partial content: %q", content)
	}
	if _, err := os.Stat(journalDir); !os.IsNotExist(err) {
		t.Fatalf("recovered journal still exists: %v", err)
	}
}
