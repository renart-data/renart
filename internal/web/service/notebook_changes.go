package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
)

const (
	NotebookOperationManifestUpgrade      = "manifest.upgrade"
	NotebookOperationCellCreate           = "cell.create"
	NotebookOperationCellUpdate           = "cell.update"
	NotebookOperationCellSQLRefactor      = "cell.sql.refactor"
	NotebookOperationCellRename           = "cell.rename"
	NotebookOperationCellDelete           = "cell.delete"
	NotebookOperationCellSourceConfigure  = "cell.source.configure"
	NotebookOperationSourceCreate         = "source.create"
	NotebookOperationSourceUpdate         = "source.update"
	NotebookOperationMarkdownCreate       = "markdown.create"
	NotebookOperationMarkdownUpdate       = "markdown.update"
	NotebookOperationVisualizationCreate  = "visualization.create"
	NotebookOperationVisualizationUpdate  = "visualization.update"
	NotebookOperationVisualizationMigrate = "visualization.migrate_legacy"
	NotebookOperationParametersReplace    = "parameters.replace"
	NotebookOperationControlCreate        = "control.create"
	NotebookOperationControlUpdate        = "control.update"
	NotebookOperationControlDelete        = "control.delete"
	NotebookOperationBlockMove            = "block.move"
	NotebookOperationBlockDelete          = "block.delete"
)

const (
	NotebookSQLRefactorRelationRename = "relation.rename"
	NotebookSQLRefactorColumnQualify  = "column.qualify"
	NotebookSQLRefactorRelationAlias  = "relation.alias"
)

// NotebookSQLRefactor describes a source-preserving semantic SQL edit. Each
// kind uses only the fields named in its comment and is normalized during
// Prepare so Apply commits the exact reviewed bytes.
type NotebookSQLRefactor struct {
	Kind      string `json:"kind"`
	Relation  string `json:"relation,omitempty"`
	NewName   string `json:"new_name,omitempty"`
	Column    string `json:"column,omitempty"`
	Qualifier string `json:"qualifier,omitempty"`
	Alias     string `json:"alias,omitempty"`
}

const (
	notebookChangePositionStart = "start"
	notebookChangePositionEnd   = "end"
	notebookChangePositionAfter = "after"
	maxNotebookChangeOperations = 100
	maxNotebookChangeContent    = 2 << 20
)

// NotebookOperation is one semantic notebook edit. Fields are intentionally
// addressed by durable cell/block ID rather than caller-provided filesystem
// paths. Create operations are normalized with generated IDs during Prepare;
// callers must apply that returned, normalized change set.
type NotebookOperation struct {
	Kind          string                          `json:"kind"`
	CellID        string                          `json:"cell_id,omitempty"`
	BlockID       string                          `json:"block_id,omitempty"`
	ControlID     string                          `json:"control_id,omitempty"`
	Name          string                          `json:"name,omitempty"`
	Language      string                          `json:"language,omitempty"`
	Connection    string                          `json:"connection,omitempty"`
	AssetType     string                          `json:"asset_type,omitempty"`
	SnapshotMode  string                          `json:"snapshot_mode,omitempty"`
	RowLimit      int64                           `json:"row_limit,omitempty"`
	Content       string                          `json:"content,omitempty"`
	SQLRefactor   *NotebookSQLRefactor            `json:"sql_refactor,omitempty"`
	Visualization *model.NotebookVisualization    `json:"visualization,omitempty"`
	Source        *model.NotebookSourceDefinition `json:"source,omitempty"`
	Parameter     *model.NotebookParameter        `json:"parameter,omitempty"`
	Parameters    []model.NotebookParameter       `json:"parameters,omitempty"`
	Position      string                          `json:"position,omitempty"`
	AfterBlockID  string                          `json:"after_block_id,omitempty"`
}

// NotebookChangeSet is a notebook-wide optimistic change. ExpectedRevision is
// empty on input to Prepare and populated in the normalized result. Apply
// requires it so reviewed bytes cannot differ from committed bytes.
type NotebookChangeSet struct {
	BaseRevision     string              `json:"base_revision"`
	ExpectedRevision string              `json:"expected_revision,omitempty"`
	Operations       []NotebookOperation `json:"operations"`
}

type NotebookChangeDiff struct {
	Path string `json:"path"`
	// Status is added, modified, or deleted.
	Status string `json:"status"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type NotebookChangePlan struct {
	Status           string               `json:"status"`
	ChangeSet        NotebookChangeSet    `json:"change_set"`
	Diff             []NotebookChangeDiff `json:"diff"`
	Problems         []string             `json:"problems,omitempty"`
	BlockingProblems []string             `json:"blocking_problems,omitempty"`
	CanApply         bool                 `json:"can_apply"`
}

type NotebookChangeApplyResult struct {
	Status   string               `json:"status"`
	Notebook model.Notebook       `json:"notebook"`
	Diff     []NotebookChangeDiff `json:"diff"`
}

type preparedNotebookChange struct {
	plan        NotebookChangePlan
	beforeFiles map[string][]byte
	afterFiles  map[string][]byte
	cleanup     func()
}

// PrepareChangeSet normalizes and validates a semantic change set against an
// isolated copy of the notebook and returns the exact authored-file diff.
func (s *NotebookService) PrepareChangeSet(notebookID string, changeSet NotebookChangeSet) (NotebookChangePlan, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	prepared, apiErr := s.prepareChangeSetLocked(notebookID, changeSet)
	if prepared != nil && prepared.cleanup != nil {
		defer prepared.cleanup()
	}
	if apiErr != nil {
		return NotebookChangePlan{}, apiErr
	}
	return prepared.plan, nil
}

// ApplyChangeSet re-prepares the normalized operations, verifies their result
// revision matches the reviewed revision, and commits the file set through a
// recoverable journal. It publishes one workspace update after all files land.
func (s *NotebookService) ApplyChangeSet(notebookID string, changeSet NotebookChangeSet) (NotebookChangeApplyResult, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	if strings.TrimSpace(changeSet.ExpectedRevision) == "" {
		return NotebookChangeApplyResult{}, &APIError{
			Status:  400,
			Code:    "notebook_change_not_prepared",
			Message: "prepare the notebook change set and apply the returned normalized change set",
		}
	}
	prepared, apiErr := s.prepareChangeSetLocked(notebookID, changeSet)
	if prepared != nil && prepared.cleanup != nil {
		defer prepared.cleanup()
	}
	if apiErr != nil {
		return NotebookChangeApplyResult{}, apiErr
	}
	if !prepared.plan.CanApply {
		return NotebookChangeApplyResult{}, &APIError{
			Status:  400,
			Code:    "notebook_change_invalid",
			Message: strings.Join(prepared.plan.BlockingProblems, "; "),
		}
	}
	if prepared.plan.ChangeSet.ExpectedRevision != changeSet.ExpectedRevision {
		return NotebookChangeApplyResult{}, &APIError{
			Status:  409,
			Code:    "notebook_change_drifted",
			Message: "The prepared notebook change no longer produces the reviewed result. Prepare it again before applying.",
		}
	}

	nb, loadErr := s.load(notebookID)
	if loadErr != nil {
		return NotebookChangeApplyResult{}, loadErr
	}
	currentFiles, err := readNotebookAuthoredFiles(nb.Dir)
	if err != nil {
		return NotebookChangeApplyResult{}, &APIError{Status: 500, Code: "notebook_change_read_failed", Message: err.Error()}
	}
	if !equalNotebookFiles(currentFiles, prepared.beforeFiles) {
		return NotebookChangeApplyResult{}, &APIError{
			Status:  409,
			Code:    "notebook_edit_conflict",
			Message: "This notebook changed while the prepared edit was being applied. Prepare it again.",
		}
	}
	if err := applyNotebookFileTransaction(s.deps.WorkspaceRoot, nb.Dir, prepared.beforeFiles, prepared.afterFiles, nil); err != nil {
		return NotebookChangeApplyResult{}, &APIError{Status: 500, Code: "notebook_change_apply_failed", Message: err.Error()}
	}

	updatedNotebook, apiErr := s.load(notebookID)
	if apiErr != nil {
		return NotebookChangeApplyResult{}, apiErr
	}
	if updatedNotebook.Revision != changeSet.ExpectedRevision {
		return NotebookChangeApplyResult{}, &APIError{
			Status:  500,
			Code:    "notebook_change_revision_mismatch",
			Message: "The notebook files were committed, but their revision did not match the prepared result.",
		}
	}

	for _, operation := range prepared.plan.ChangeSet.Operations {
		switch operation.Kind {
		case NotebookOperationCellCreate, NotebookOperationCellUpdate, NotebookOperationCellSQLRefactor, NotebookOperationCellSourceConfigure,
			NotebookOperationSourceCreate, NotebookOperationSourceUpdate:
			s.onCellChanged(notebookID, updatedNotebook, operation.CellID)
		case NotebookOperationParametersReplace, NotebookOperationControlCreate,
			NotebookOperationControlUpdate, NotebookOperationControlDelete:
			s.onNotebookParametersChanged(notebookID, updatedNotebook)
		case NotebookOperationCellDelete:
			_ = s.store.DropCellObjects(updatedNotebook.UUID, operation.CellID)
			s.forgetCell(notebookID, updatedNotebook.UUID, operation.CellID)
		}
	}
	s.pushUpdate(updatedNotebook.Dir)
	return NotebookChangeApplyResult{
		Status:   "ok",
		Notebook: s.toModel(updatedNotebook),
		Diff:     prepared.plan.Diff,
	}, nil
}

func (s *NotebookService) prepareChangeSetLocked(notebookID string, changeSet NotebookChangeSet) (*preparedNotebookChange, *APIError) {
	if strings.TrimSpace(changeSet.BaseRevision) == "" {
		return nil, &APIError{Status: 400, Code: "notebook_revision_required", Message: "a notebook base revision is required"}
	}
	if len(changeSet.Operations) == 0 {
		return nil, &APIError{Status: 400, Code: "notebook_change_empty", Message: "at least one notebook operation is required"}
	}
	if len(changeSet.Operations) > maxNotebookChangeOperations {
		return nil, &APIError{Status: 400, Code: "notebook_change_too_large", Message: fmt.Sprintf("a notebook change may contain at most %d operations", maxNotebookChangeOperations)}
	}
	for _, operation := range changeSet.Operations {
		if len(operation.Content) > maxNotebookChangeContent {
			return nil, &APIError{Status: 400, Code: "notebook_change_too_large", Message: fmt.Sprintf("operation content may not exceed %d bytes", maxNotebookChangeContent)}
		}
	}

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return nil, apiErr
	}
	if changeSet.BaseRevision != nb.Revision {
		return nil, &APIError{
			Status:  409,
			Code:    "notebook_edit_conflict",
			Message: "This notebook changed after the edit was prepared. Reload it and try again.",
		}
	}
	beforeFiles, err := readNotebookAuthoredFiles(nb.Dir)
	if err != nil {
		return nil, &APIError{Status: 500, Code: "notebook_change_read_failed", Message: err.Error()}
	}

	stagingRoot := filepath.Join(s.deps.WorkspaceRoot, ".renart", "notebook-change-staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, &APIError{Status: 500, Code: "notebook_change_stage_failed", Message: err.Error()}
	}
	tempRoot, err := os.MkdirTemp(stagingRoot, "change-")
	if err != nil {
		return nil, &APIError{Status: 500, Code: "notebook_change_stage_failed", Message: err.Error()}
	}
	cleanup := func() { _ = os.RemoveAll(tempRoot) }
	tempDir := filepath.Join(tempRoot, filepath.Base(nb.Dir))
	if err := writeNotebookFileSet(tempDir, beforeFiles); err != nil {
		cleanup()
		return nil, &APIError{Status: 500, Code: "notebook_change_stage_failed", Message: err.Error()}
	}

	loader, loaderCleanup := s.newLoader()
	defer loaderCleanup()
	draft, err := loader.Load(tempDir)
	if err != nil {
		cleanup()
		return nil, &APIError{Status: 400, Code: "notebook_change_invalid", Message: err.Error()}
	}
	normalized := cloneNotebookOperations(changeSet.Operations)
	for index := range normalized {
		if opErr := s.applyDraftOperation(draft, &normalized[index]); opErr != nil {
			cleanup()
			return nil, opErr
		}
		draft, err = loader.Load(tempDir)
		if err != nil {
			cleanup()
			return nil, &APIError{Status: 400, Code: "notebook_change_invalid", Message: err.Error()}
		}
	}

	afterFiles, err := readNotebookAuthoredFiles(tempDir)
	if err != nil {
		cleanup()
		return nil, &APIError{Status: 500, Code: "notebook_change_stage_failed", Message: err.Error()}
	}
	baseVisualizationProblems, _ := s.notebookVisualizationProblems(context.Background(), nb)
	visualizationProblems, visualizationBlocking := s.notebookVisualizationProblems(context.Background(), draft)
	allProblems := append(append([]string(nil), draft.Problems...), visualizationProblems...)
	baseProblems := append(append([]string(nil), nb.Problems...), baseVisualizationProblems...)
	blocking := newNotebookProblems(baseProblems, allProblems)
	for _, operation := range normalized {
		if operation.Kind != NotebookOperationVisualizationCreate && operation.Kind != NotebookOperationVisualizationUpdate && operation.Kind != NotebookOperationVisualizationMigrate {
			continue
		}
		prefix := fmt.Sprintf("visualization %q:", operation.BlockID)
		for _, problem := range visualizationBlocking {
			if strings.HasPrefix(problem, prefix) && !containsString(blocking, problem) {
				blocking = append(blocking, problem)
			}
		}
	}
	workspaceNotebookDir, relErr := filepath.Rel(s.deps.WorkspaceRoot, nb.Dir)
	if relErr != nil {
		workspaceNotebookDir = nb.Dir
	}
	normalizedChangeSet := NotebookChangeSet{
		BaseRevision:     changeSet.BaseRevision,
		ExpectedRevision: draft.Revision,
		Operations:       normalized,
	}
	plan := NotebookChangePlan{
		Status:           "ok",
		ChangeSet:        normalizedChangeSet,
		Diff:             buildNotebookChangeDiff(filepath.ToSlash(workspaceNotebookDir), beforeFiles, afterFiles),
		Problems:         allProblems,
		BlockingProblems: blocking,
		CanApply:         len(blocking) == 0,
	}
	return &preparedNotebookChange{
		plan:        plan,
		beforeFiles: beforeFiles,
		afterFiles:  afterFiles,
		cleanup:     cleanup,
	}, nil
}

func cloneNotebookOperations(operations []NotebookOperation) []NotebookOperation {
	result := make([]NotebookOperation, len(operations))
	copy(result, operations)
	for index := range result {
		if result[index].SQLRefactor != nil {
			refactor := *result[index].SQLRefactor
			result[index].SQLRefactor = &refactor
		}
		if result[index].Visualization != nil {
			visualization := *result[index].Visualization
			visualization.Definition = cloneStringAnyMap(visualization.Definition)
			result[index].Visualization = &visualization
		}
		if result[index].Parameter != nil {
			parameter := cloneNotebookParameters([]model.NotebookParameter{*result[index].Parameter})[0]
			result[index].Parameter = &parameter
		}
		result[index].Parameters = cloneNotebookParameters(result[index].Parameters)
	}
	return result
}

func (s *NotebookService) applyDraftOperation(nb *notebook.Notebook, operation *NotebookOperation) *APIError {
	operation.Kind = strings.ToLower(strings.TrimSpace(operation.Kind))
	switch operation.Kind {
	case NotebookOperationManifestUpgrade:
		_, err := notebook.UpgradeManifestV2(afero.NewOsFs(), nb)
		if err != nil {
			return badRequestError("notebook_upgrade_failed", err.Error())
		}
		return nil

	case NotebookOperationCellCreate:
		return s.applyDraftCellCreate(nb, operation)

	case NotebookOperationCellUpdate:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		operation.CellID = cell.ID
		content := notebook.NormalizeCellID(operation.Content, cell.ID, notebook.IsPythonCell(cell))
		if err := os.WriteFile(cell.Path, []byte(content), 0o644); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		operation.Content = content
		return nil

	case NotebookOperationCellSQLRefactor:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		if notebook.IsPythonCell(cell) || notebook.IsSourceCell(cell) {
			return badRequestError("invalid_sql_refactor", "semantic SQL refactors require a SQL cell")
		}
		if operation.SQLRefactor == nil {
			return badRequestError("invalid_sql_refactor", "sql_refactor is required")
		}
		operation.CellID = cell.ID
		refactor := *operation.SQLRefactor
		refactor.Kind = strings.ToLower(strings.TrimSpace(refactor.Kind))
		refactor.Relation = strings.TrimSpace(refactor.Relation)
		refactor.NewName = strings.TrimSpace(refactor.NewName)
		refactor.Column = strings.TrimSpace(refactor.Column)
		refactor.Qualifier = strings.TrimSpace(refactor.Qualifier)
		refactor.Alias = strings.TrimSpace(refactor.Alias)

		content, err := os.ReadFile(cell.Path)
		if err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		dialect, err := bruincompat.AssetTypeToDialect(pipeline.AssetType(cell.Asset.Type))
		if err != nil || strings.TrimSpace(dialect) == "" {
			return badRequestError("invalid_sql_refactor", fmt.Sprintf("cannot determine SQL dialect for cell type %q", cell.Asset.Type))
		}
		var rewritten string
		switch refactor.Kind {
		case NotebookSQLRefactorRelationRename:
			if refactor.Relation == "" || refactor.NewName == "" {
				return badRequestError("invalid_sql_refactor", "relation.rename requires relation and new_name")
			}
			rewritten, err = sqlintelligence.RenameTables(string(content), dialect, map[string]string{refactor.Relation: refactor.NewName})
		case NotebookSQLRefactorColumnQualify:
			if refactor.Column == "" || refactor.Qualifier == "" {
				return badRequestError("invalid_sql_refactor", "column.qualify requires column and qualifier")
			}
			rewritten, err = sqlintelligence.QualifyColumn(string(content), dialect, refactor.Column, refactor.Qualifier)
		case NotebookSQLRefactorRelationAlias:
			if refactor.Relation == "" || refactor.Alias == "" {
				return badRequestError("invalid_sql_refactor", "relation.alias requires relation and alias")
			}
			rewritten, err = sqlintelligence.AliasRelation(string(content), dialect, refactor.Relation, refactor.Alias)
		default:
			return badRequestError("invalid_sql_refactor", fmt.Sprintf("unknown SQL refactor kind %q", refactor.Kind))
		}
		if err != nil {
			return badRequestError("sql_refactor_failed", err.Error())
		}
		if err := os.WriteFile(cell.Path, []byte(rewritten), 0o644); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		operation.SQLRefactor = &refactor
		operation.Content = rewritten
		return nil

	case NotebookOperationCellSourceConfigure:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		if notebook.IsPythonCell(cell) {
			return badRequestError("invalid_notebook_source", "only SQL cells can use a warehouse source connection")
		}
		operation.CellID = cell.ID
		return s.configureDraftCellSource(cell, operation)

	case NotebookOperationSourceCreate:
		return s.applyDraftSourceCreate(nb, operation)

	case NotebookOperationSourceUpdate:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil || !notebook.IsSourceCell(cell) {
			return badRequestError("source_not_found", fmt.Sprintf("source %q was not found", operation.CellID))
		}
		operation.CellID = cell.ID
		definition, apiErr := s.notebookSourceDefinition(operation.Source, cell.ID)
		if apiErr != nil {
			return apiErr
		}
		content, err := notebook.MarshalSourceDefinition(*definition)
		if err != nil {
			return badRequestError("invalid_notebook_source", err.Error())
		}
		operation.Source = notebookSourceDefinitionToModel(definition)
		operation.Content = string(content)
		if err := os.WriteFile(cell.Path, content, 0o644); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationCellRename:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		operation.CellID = cell.ID
		operation.Name = strings.TrimSpace(operation.Name)
		if message := notebook.ValidateCellName(nb, operation.Name, cell.ID, s.pipelineAssetNameSet()); message != "" {
			return badRequestError("invalid_cell_name", message)
		}
		edits, err := notebook.PlanRename(nb, cell.ID, operation.Name)
		if err != nil {
			return badRequestError("rename_failed", err.Error())
		}
		for _, edit := range edits {
			if edit.NewContent != "" {
				if err := os.WriteFile(edit.Path, []byte(edit.NewContent), 0o644); err != nil {
					return internalError("notebook_change_stage_failed", err.Error())
				}
			}
		}
		for _, edit := range edits {
			if edit.NewPath != "" {
				if err := os.Rename(edit.Path, edit.NewPath); err != nil {
					return internalError("notebook_change_stage_failed", err.Error())
				}
			}
		}
		return nil

	case NotebookOperationCellDelete:
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		operation.CellID = cell.ID
		if err := os.Remove(cell.Path); err != nil && !os.IsNotExist(err) {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		next := make([]notebook.Block, 0, len(nb.Blocks))
		for _, block := range nb.Blocks {
			if block.Cell == cell.ID || (block.Visualization != nil && block.Visualization.Source == cell.ID) {
				continue
			}
			next = append(next, block)
		}
		nb.Blocks = next
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationMarkdownCreate:
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebookUpgradeRequiredError()
		}
		if operation.BlockID == "" {
			operation.BlockID = uniqueDraftBlockID(nb, "md")
		}
		if nbBlockByID(nb, operation.BlockID) != nil {
			return badRequestError("duplicate_notebook_block", fmt.Sprintf("block %q already exists", operation.BlockID))
		}
		block := notebook.Block{ID: operation.BlockID, Markdown: operation.Content}
		var err error
		nb.Blocks, err = insertNotebookBlock(nb.Blocks, block, operation.Position, operation.AfterBlockID)
		if err != nil {
			return badRequestError("invalid_block_position", err.Error())
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationMarkdownUpdate:
		block := nbBlockByID(nb, strings.TrimSpace(operation.BlockID))
		if block == nil || block.Cell != "" || block.Visualization != nil {
			return badRequestError("markdown_block_not_found", fmt.Sprintf("markdown block %q was not found", operation.BlockID))
		}
		operation.BlockID = block.ID
		block.Markdown = operation.Content
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationVisualizationCreate:
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebookUpgradeRequiredError()
		}
		if operation.BlockID == "" {
			operation.BlockID = uniqueDraftBlockID(nb, "viz")
		}
		if nbBlockByID(nb, operation.BlockID) != nil {
			return badRequestError("duplicate_notebook_block", fmt.Sprintf("block %q already exists", operation.BlockID))
		}
		visualization, apiErr := normalizeDraftVisualization(nb, operation.BlockID, operation.Visualization)
		if apiErr != nil {
			return apiErr
		}
		operation.Visualization = visualization
		block := notebook.Block{
			ID: operation.BlockID,
			Visualization: &notebook.VisualizationBlock{
				ID: operation.BlockID, Source: visualization.Source,
				Definition: cloneStringAnyMap(visualization.Definition),
			},
		}
		var err error
		nb.Blocks, err = insertNotebookBlock(nb.Blocks, block, operation.Position, operation.AfterBlockID)
		if err != nil {
			return badRequestError("invalid_block_position", err.Error())
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationVisualizationUpdate:
		block := nbBlockByID(nb, strings.TrimSpace(operation.BlockID))
		if block == nil || block.Visualization == nil {
			return badRequestError("visualization_not_found", fmt.Sprintf("visualization %q was not found", operation.BlockID))
		}
		operation.BlockID = block.ID
		visualization, apiErr := normalizeDraftVisualization(nb, block.ID, operation.Visualization)
		if apiErr != nil {
			return apiErr
		}
		operation.Visualization = visualization
		block.Visualization = &notebook.VisualizationBlock{
			ID: block.ID, Source: visualization.Source,
			Definition: cloneStringAnyMap(visualization.Definition),
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationVisualizationMigrate:
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebookUpgradeRequiredError()
		}
		cell := nb.CellByID(strings.TrimSpace(operation.CellID))
		if cell == nil {
			return badRequestError("cell_not_found", fmt.Sprintf("cell %q was not found", operation.CellID))
		}
		operation.CellID = cell.ID
		content, err := os.ReadFile(cell.Path)
		if err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		migratedContent, definition, migrated, err := notebook.MigrateLegacyVisualization(string(content))
		if err != nil {
			return badRequestError("visualization_migration_failed", err.Error())
		}
		if !migrated {
			return badRequestError("visualization_not_found", "this cell has no legacy @viz directive to migrate")
		}
		if operation.BlockID == "" {
			operation.BlockID = uniqueDraftBlockID(nb, "viz")
		}
		if nbBlockByID(nb, operation.BlockID) != nil {
			return badRequestError("duplicate_notebook_block", fmt.Sprintf("block %q already exists", operation.BlockID))
		}
		visualization, apiErr := normalizeDraftVisualization(nb, operation.BlockID, &model.NotebookVisualization{
			Source: cell.ID, Definition: definition,
		})
		if apiErr != nil {
			return apiErr
		}
		operation.Content = migratedContent
		operation.Visualization = visualization
		if err := os.WriteFile(cell.Path, []byte(migratedContent), 0o644); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		block := notebook.Block{
			ID: operation.BlockID,
			Visualization: &notebook.VisualizationBlock{
				ID: operation.BlockID, Source: cell.ID, Definition: cloneStringAnyMap(definition),
			},
		}
		nb.Blocks, err = insertNotebookBlock(nb.Blocks, block, notebookChangePositionAfter, cell.ID)
		if err != nil {
			return badRequestError("invalid_block_position", err.Error())
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationParametersReplace:
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebookUpgradeRequiredError()
		}
		if len(operation.Parameters) > 64 {
			return badRequestError("too_many_notebook_parameters", "a notebook may declare at most 64 parameters")
		}
		definitions := make([]presentation.ParameterDefinition, 0, len(operation.Parameters))
		for _, parameter := range operation.Parameters {
			definitions = append(definitions, notebookParameterDefinition(parameter))
		}
		if findings := presentation.CheckParameterDefinitions(definitions); len(findings) > 0 {
			return badRequestError("invalid_notebook_parameters", findings[0].Message)
		}
		nb.Parameters = definitions
		knownParameters := make(map[string]bool, len(definitions))
		for _, definition := range definitions {
			knownParameters[definition.ID] = true
		}
		blocks := make([]notebook.Block, 0, len(nb.Blocks))
		for _, block := range nb.Blocks {
			if block.Control == "" || knownParameters[block.Control] {
				blocks = append(blocks, block)
			}
		}
		nb.Blocks = blocks
		operation.Parameters = notebookParametersToModel(definitions)
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationControlCreate:
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebookUpgradeRequiredError()
		}
		if operation.Parameter == nil {
			return badRequestError("invalid_notebook_control", "a control parameter definition is required")
		}
		if len(nb.Parameters) >= 64 {
			return badRequestError("too_many_notebook_parameters", "a notebook may declare at most 64 parameters")
		}
		definition := notebookParameterDefinition(*operation.Parameter)
		definitions := append(append([]presentation.ParameterDefinition(nil), nb.Parameters...), definition)
		if findings := presentation.CheckParameterDefinitions(definitions); len(findings) > 0 {
			return badRequestError("invalid_notebook_control", findings[0].Message)
		}
		nb.Parameters = definitions
		parameter := notebookParametersToModel([]presentation.ParameterDefinition{definition})[0]
		operation.Parameter = &parameter
		operation.ControlID = definition.ID
		var err error
		nb.Blocks, err = insertNotebookBlock(nb.Blocks, notebook.Block{Control: definition.ID}, operation.Position, operation.AfterBlockID)
		if err != nil {
			return badRequestError("invalid_block_position", err.Error())
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationControlUpdate:
		controlID := strings.TrimSpace(operation.ControlID)
		if operation.Parameter == nil {
			return badRequestError("invalid_notebook_control", "a control parameter definition is required")
		}
		parameterIndex := -1
		for index, parameter := range nb.Parameters {
			if parameter.ID == controlID {
				parameterIndex = index
				break
			}
		}
		if parameterIndex < 0 {
			return badRequestError("notebook_control_not_found", fmt.Sprintf("control %q was not found", operation.ControlID))
		}
		definition := notebookParameterDefinition(*operation.Parameter)
		definitions := append([]presentation.ParameterDefinition(nil), nb.Parameters...)
		definitions[parameterIndex] = definition
		if findings := presentation.CheckParameterDefinitions(definitions); len(findings) > 0 {
			return badRequestError("invalid_notebook_control", findings[0].Message)
		}
		nb.Parameters = definitions
		for index := range nb.Blocks {
			if nb.Blocks[index].Control == controlID {
				nb.Blocks[index].Control = definition.ID
			}
		}
		operation.ControlID = controlID
		parameter := notebookParametersToModel([]presentation.ParameterDefinition{definition})[0]
		operation.Parameter = &parameter
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationControlDelete:
		controlID := strings.TrimSpace(operation.ControlID)
		found := false
		parameters := make([]presentation.ParameterDefinition, 0, len(nb.Parameters))
		for _, parameter := range nb.Parameters {
			if parameter.ID == controlID {
				found = true
				continue
			}
			parameters = append(parameters, parameter)
		}
		if !found {
			return badRequestError("notebook_control_not_found", fmt.Sprintf("control %q was not found", operation.ControlID))
		}
		nb.Parameters = parameters
		blocks := make([]notebook.Block, 0, len(nb.Blocks)-1)
		for _, block := range nb.Blocks {
			if block.Control != controlID {
				blocks = append(blocks, block)
			}
		}
		nb.Blocks = blocks
		operation.ControlID = controlID
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationBlockMove:
		block := nbBlockByID(nb, strings.TrimSpace(operation.BlockID))
		if block == nil {
			return badRequestError("notebook_block_not_found", fmt.Sprintf("block %q was not found", operation.BlockID))
		}
		operation.BlockID = block.StableID()
		without := make([]notebook.Block, 0, len(nb.Blocks)-1)
		for _, candidate := range nb.Blocks {
			if candidate.StableID() != operation.BlockID {
				without = append(without, candidate)
			}
		}
		var err error
		nb.Blocks, err = insertNotebookBlock(without, *block, operation.Position, operation.AfterBlockID)
		if err != nil {
			return badRequestError("invalid_block_position", err.Error())
		}
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil

	case NotebookOperationBlockDelete:
		block := nbBlockByID(nb, strings.TrimSpace(operation.BlockID))
		if block == nil {
			return badRequestError("notebook_block_not_found", fmt.Sprintf("block %q was not found", operation.BlockID))
		}
		if block.Cell != "" {
			return badRequestError("cell_delete_required", "delete a cell with cell.delete so its file and runtime object are removed too")
		}
		if block.Control != "" {
			return badRequestError("control_delete_required", "delete a control with control.delete so its parameter definition is removed too")
		}
		operation.BlockID = block.StableID()
		next := make([]notebook.Block, 0, len(nb.Blocks)-1)
		for _, candidate := range nb.Blocks {
			if candidate.StableID() != operation.BlockID {
				next = append(next, candidate)
			}
		}
		nb.Blocks = next
		if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
			return internalError("notebook_change_stage_failed", err.Error())
		}
		return nil
	default:
		return badRequestError("unsupported_notebook_operation", fmt.Sprintf("unsupported notebook operation %q", operation.Kind))
	}
}

func notebookParametersToModel(definitions []presentation.ParameterDefinition) []model.NotebookParameter {
	result := make([]model.NotebookParameter, 0, len(definitions))
	for _, definition := range definitions {
		parameter := model.NotebookParameter{
			ID: definition.ID, Label: definition.Label, Type: string(definition.Type),
			Default: cloneJSONValue(definition.Default), Min: definition.Min, Max: definition.Max, Step: definition.Step,
		}
		if definition.Options != nil {
			parameter.Options = &model.NotebookParameterOptions{
				Values: cloneJSONValues(definition.Options.Values), Dataset: definition.Options.Dataset,
				ValueField: definition.Options.ValueField, LabelField: definition.Options.LabelField,
			}
		}
		result = append(result, parameter)
	}
	return result
}

func notebookParameterDefinition(parameter model.NotebookParameter) presentation.ParameterDefinition {
	definition := presentation.ParameterDefinition{
		ID: strings.TrimSpace(parameter.ID), Label: strings.TrimSpace(parameter.Label),
		Type:    presentation.ParameterType(strings.ToLower(strings.TrimSpace(parameter.Type))),
		Default: cloneJSONValue(parameter.Default),
		Min:     parameter.Min, Max: parameter.Max, Step: parameter.Step,
	}
	if parameter.Options != nil {
		definition.Options = &presentation.ParameterOptions{
			Values:     cloneJSONValues(parameter.Options.Values),
			Dataset:    strings.TrimSpace(parameter.Options.Dataset),
			ValueField: strings.TrimSpace(parameter.Options.ValueField),
			LabelField: strings.TrimSpace(parameter.Options.LabelField),
		}
	}
	return definition
}

func cloneNotebookParameters(parameters []model.NotebookParameter) []model.NotebookParameter {
	if parameters == nil {
		return nil
	}
	result := make([]model.NotebookParameter, len(parameters))
	for index, parameter := range parameters {
		result[index] = parameter
		result[index].Default = cloneJSONValue(parameter.Default)
		if parameter.Options != nil {
			options := *parameter.Options
			options.Values = cloneJSONValues(parameter.Options.Values)
			result[index].Options = &options
		}
	}
	return result
}

func cloneJSONValues(values []any) []any {
	if values == nil {
		return nil
	}
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return value
	}
	return result
}

func (s *NotebookService) applyDraftCellCreate(nb *notebook.Notebook, operation *NotebookOperation) *APIError {
	operation.Language = strings.ToLower(strings.TrimSpace(operation.Language))
	if operation.Language == "" {
		operation.Language = "sql"
	}
	if operation.Language != "sql" && operation.Language != "python" {
		return badRequestError("invalid_cell_language", "cell language must be sql or python")
	}
	if operation.CellID == "" {
		operation.CellID = uniqueDraftCellID(nb)
	}
	if nb.CellByID(operation.CellID) != nil {
		return badRequestError("duplicate_cell_id", fmt.Sprintf("cell id %q already exists", operation.CellID))
	}
	operation.Name = strings.TrimSpace(operation.Name)
	if operation.Name == "" {
		operation.Name = nextCellAutoname(nb, s.pipelineAssetNameSet())
	}
	if message := notebook.ValidateCellName(nb, operation.Name, "", s.pipelineAssetNameSet()); message != "" {
		return badRequestError("invalid_cell_name", message)
	}
	python := operation.Language == "python"
	extension := ".sql"
	content := notebook.CellFileTemplate(operation.CellID)
	if python {
		extension = ".py"
		content = notebook.PythonCellFileTemplate(operation.CellID)
	}
	if strings.TrimSpace(operation.Content) != "" {
		content = notebook.NormalizeCellID(operation.Content, operation.CellID, python)
	}
	if strings.TrimSpace(operation.Connection) != "" {
		if python {
			return badRequestError("invalid_notebook_source", "warehouse source cells must use SQL")
		}
		assetType, apiErr := s.resolveNotebookSourceAssetType(operation.Connection)
		if apiErr != nil {
			return apiErr
		}
		operation.AssetType = assetType
		configured, err := notebook.ConfigureSQLSource(content, operation.CellID, notebook.SQLSourceConfig{
			Connection: operation.Connection, AssetType: assetType,
			SnapshotMode: operation.SnapshotMode, RowLimit: operation.RowLimit,
		})
		if err != nil {
			return badRequestError("invalid_notebook_source", err.Error())
		}
		content = configured
	}
	operation.Content = content
	path := filepath.Join(nb.Dir, operation.Name+extension)
	if _, err := os.Stat(path); err == nil {
		return badRequestError("cell_exists", fmt.Sprintf("a file for cell %q already exists", operation.Name))
	} else if !os.IsNotExist(err) {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	var err error
	nb.Blocks, err = insertNotebookBlock(nb.Blocks, notebook.Block{Cell: operation.CellID}, operation.Position, operation.AfterBlockID)
	if err != nil {
		return badRequestError("invalid_block_position", err.Error())
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	return nil
}

func (s *NotebookService) applyDraftSourceCreate(nb *notebook.Notebook, operation *NotebookOperation) *APIError {
	if nb.Version < notebook.ManifestVersionCurrent {
		return notebookUpgradeRequiredError()
	}
	if operation.CellID == "" {
		operation.CellID = uniqueDraftSourceID(nb)
	}
	if nb.CellByID(operation.CellID) != nil {
		return badRequestError("duplicate_cell_id", fmt.Sprintf("cell id %q already exists", operation.CellID))
	}
	operation.Name = strings.TrimSpace(operation.Name)
	if operation.Name == "" {
		operation.Name = nextCellAutoname(nb, s.pipelineAssetNameSet())
	}
	if message := notebook.ValidateCellName(nb, operation.Name, "", s.pipelineAssetNameSet()); message != "" {
		return badRequestError("invalid_cell_name", message)
	}
	definition, apiErr := s.notebookSourceDefinition(operation.Source, operation.CellID)
	if apiErr != nil {
		return apiErr
	}
	content, err := notebook.MarshalSourceDefinition(*definition)
	if err != nil {
		return badRequestError("invalid_notebook_source", err.Error())
	}
	operation.Source = notebookSourceDefinitionToModel(definition)
	operation.Content = string(content)
	path := filepath.Join(nb.Dir, operation.Name+".source.yml")
	if _, err := os.Stat(path); err == nil {
		return badRequestError("cell_exists", fmt.Sprintf("a file for source %q already exists", operation.Name))
	} else if !os.IsNotExist(err) {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	nb.Blocks, err = insertNotebookBlock(nb.Blocks, notebook.Block{Cell: operation.CellID}, operation.Position, operation.AfterBlockID)
	if err != nil {
		return badRequestError("invalid_block_position", err.Error())
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	return nil
}

func (s *NotebookService) notebookSourceDefinition(input *model.NotebookSourceDefinition, sourceID string) (*notebook.SourceDefinition, *APIError) {
	if input == nil {
		return nil, badRequestError("invalid_notebook_source", "a notebook source definition is required")
	}
	definition := &notebook.SourceDefinition{
		Version: input.Version, ID: strings.TrimSpace(sourceID), Kind: input.Kind,
		Connection: input.Connection, URI: input.URI, Format: input.Format,
		Request: notebook.SourceHTTPRequest{
			URL: input.Request.URL, Method: input.Request.Method, Headers: input.Request.Headers,
			Params: input.Request.Params, Body: input.Request.Body,
		},
		Response: notebook.SourceHTTPResponse{
			RecordsPath: input.Response.RecordsPath, Fields: input.Response.Fields,
		},
		Snapshot: notebook.SourceSnapshotConfig{
			Mode: input.Snapshot.Mode, RowLimit: input.Snapshot.RowLimit,
		},
	}
	if definition.Version == 0 {
		definition.Version = notebook.SourceDefinitionVersionCurrent
	}
	definition.Kind = strings.ToLower(strings.TrimSpace(definition.Kind))
	definition.Connection = strings.TrimSpace(definition.Connection)
	if definition.Kind == notebook.SourceKindFile && definition.Connection != "" {
		if apiErr := s.validateNotebookStorageConnection(definition.Connection); apiErr != nil {
			return nil, apiErr
		}
	}
	if err := notebook.ValidateSourceDefinition(definition); err != nil {
		return nil, badRequestError("invalid_notebook_source", err.Error())
	}
	return definition, nil
}

func notebookSourceDefinitionToModel(definition *notebook.SourceDefinition) *model.NotebookSourceDefinition {
	if definition == nil {
		return nil
	}
	return &model.NotebookSourceDefinition{
		Version: definition.Version, ID: definition.ID, Kind: definition.Kind,
		Connection: definition.Connection, URI: definition.URI, Format: definition.Format,
		Request: model.NotebookSourceRequest{
			URL: definition.Request.URL, Method: definition.Request.Method,
			Headers: definition.Request.Headers, Params: definition.Request.Params,
			Body: definition.Request.Body,
		},
		Response: model.NotebookSourceResponse{
			RecordsPath: definition.Response.RecordsPath, Fields: definition.Response.Fields,
		},
		Snapshot: model.NotebookSourceSnapshot{
			Mode: definition.Snapshot.Mode, RowLimit: definition.Snapshot.RowLimit,
		},
	}
}

func (s *NotebookService) validateNotebookStorageConnection(connection string) *APIError {
	if s.deps.CurrentState == nil {
		return badRequestError("unknown_notebook_source_connection", fmt.Sprintf("storage connection %q is unavailable", connection))
	}
	connectionType := ""
	for name, candidateType := range s.deps.CurrentState().Connections {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(connection)) {
			connectionType = candidateType
			break
		}
	}
	if connectionType == "" {
		return badRequestError("unknown_notebook_source_connection", fmt.Sprintf("connection %q is not configured", connection))
	}
	if loadConnectionCategory(connectionType) != LoadCategoryStorage {
		return badRequestError("invalid_notebook_source_connection", fmt.Sprintf("connection %q is not an object-storage connection", connection))
	}
	return nil
}

func (s *NotebookService) configureDraftCellSource(cell *notebook.Cell, operation *NotebookOperation) *APIError {
	connection := strings.TrimSpace(operation.Connection)
	assetType := notebook.DefaultCellType
	if connection != "" {
		resolved, apiErr := s.resolveNotebookSourceAssetType(connection)
		if apiErr != nil {
			return apiErr
		}
		assetType = resolved
	}
	operation.Connection = connection
	operation.AssetType = assetType
	configured, err := notebook.ConfigureSQLSource(cell.Raw, cell.ID, notebook.SQLSourceConfig{
		Connection: connection, AssetType: assetType,
		SnapshotMode: operation.SnapshotMode, RowLimit: operation.RowLimit,
	})
	if err != nil {
		return badRequestError("invalid_notebook_source", err.Error())
	}
	operation.Content = configured
	if err := os.WriteFile(cell.Path, []byte(configured), 0o644); err != nil {
		return internalError("notebook_change_stage_failed", err.Error())
	}
	return nil
}

func (s *NotebookService) resolveNotebookSourceAssetType(connection string) (string, *APIError) {
	connection = strings.TrimSpace(connection)
	if connection == "" {
		return notebook.DefaultCellType, nil
	}
	if s.deps.CurrentState == nil {
		return "", badRequestError("unknown_notebook_source_connection", fmt.Sprintf("query connection %q is unavailable", connection))
	}
	for _, candidate := range s.deps.CurrentState().QueryConnections {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), connection) {
			return strings.TrimSpace(candidate.AssetType), nil
		}
	}
	return "", badRequestError("unknown_notebook_source_connection", fmt.Sprintf("connection %q cannot execute notebook SQL", connection))
}

func normalizeDraftVisualization(nb *notebook.Notebook, blockID string, input *model.NotebookVisualization) (*model.NotebookVisualization, *APIError) {
	if input == nil {
		return nil, badRequestError("invalid_visualization", "a visualization definition is required")
	}
	source := strings.TrimSpace(input.Source)
	if nb.CellByID(source) == nil {
		return nil, badRequestError("unknown_visualization_source", fmt.Sprintf("visualization %q references unknown source cell %q", blockID, source))
	}
	if len(input.Definition) == 0 {
		return nil, badRequestError("invalid_visualization_definition", fmt.Sprintf("visualization %q has no definition", blockID))
	}
	definition, findings := presentation.DecodeVisualizationDefinition(input.Definition)
	if hasPresentationErrors(findings) {
		return nil, badRequestError("invalid_visualization_definition", findings[0].Message)
	}
	structural := (presentation.Checker{}).CheckVisualization(context.Background(), definition, presentation.ResolvedSchema{}, presentation.CheckOptions{})
	for _, finding := range structural {
		if finding.Code == "visualization-field-required" && finding.Severity == "error" {
			return nil, badRequestError("invalid_visualization_definition", finding.Message)
		}
	}
	return &model.NotebookVisualization{
		ID: blockID, Source: source, Definition: cloneStringAnyMap(input.Definition),
	}, nil
}

func notebookUpgradeRequiredError() *APIError {
	return &APIError{Status: 409, Code: "notebook_upgrade_required", Message: "upgrade this notebook before editing identity-bearing presentation blocks"}
}

func uniqueDraftCellID(nb *notebook.Notebook) string {
	for {
		id := notebook.NewCellID()
		if nb.CellByID(id) == nil {
			return id
		}
	}
}

func uniqueDraftSourceID(nb *notebook.Notebook) string {
	for {
		id := notebook.NewBlockID("source")
		if nb.CellByID(id) == nil {
			return id
		}
	}
}

func uniqueDraftBlockID(nb *notebook.Notebook, prefix string) string {
	for {
		id := notebook.NewBlockID(prefix)
		if nbBlockByID(nb, id) == nil {
			return id
		}
	}
}

func nbBlockByID(nb *notebook.Notebook, id string) *notebook.Block {
	for index := range nb.Blocks {
		if nb.Blocks[index].StableID() == id {
			return &nb.Blocks[index]
		}
	}
	return nil
}

func insertNotebookBlock(blocks []notebook.Block, block notebook.Block, position, afterID string) ([]notebook.Block, error) {
	position = strings.ToLower(strings.TrimSpace(position))
	if position == "" {
		if strings.TrimSpace(afterID) != "" {
			position = notebookChangePositionAfter
		} else {
			position = notebookChangePositionEnd
		}
	}
	switch position {
	case notebookChangePositionStart:
		return append([]notebook.Block{block}, blocks...), nil
	case notebookChangePositionEnd:
		return append(blocks, block), nil
	case notebookChangePositionAfter:
		afterID = strings.TrimSpace(afterID)
		if afterID == "" {
			return nil, fmt.Errorf("after_block_id is required for position after")
		}
		for index, candidate := range blocks {
			if candidate.StableID() != afterID {
				continue
			}
			result := make([]notebook.Block, 0, len(blocks)+1)
			result = append(result, blocks[:index+1]...)
			result = append(result, block)
			result = append(result, blocks[index+1:]...)
			return result, nil
		}
		return nil, fmt.Errorf("block %q was not found", afterID)
	default:
		return nil, fmt.Errorf("position must be start, end, or after")
	}
}

func newNotebookProblems(before, after []string) []string {
	existing := make(map[string]bool, len(before))
	for _, problem := range before {
		existing[problem] = true
	}
	result := make([]string, 0)
	for _, problem := range after {
		if !existing[problem] {
			result = append(result, problem)
		}
	}
	return result
}

func readNotebookAuthoredFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if name != notebook.ManifestFileName && lower != "pyproject.toml" &&
			!strings.HasSuffix(lower, ".sql") && !strings.HasSuffix(lower, ".py") &&
			!strings.HasSuffix(lower, ".source.yml") && !strings.HasSuffix(lower, ".source.yaml") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return nil, readErr
		}
		result[filepath.ToSlash(name)] = content
	}
	return result, nil
}

func writeNotebookFileSet(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func equalNotebookFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, content := range left {
		if !bytes.Equal(content, right[path]) {
			return false
		}
	}
	return true
}

func buildNotebookChangeDiff(notebookDir string, before, after map[string][]byte) []NotebookChangeDiff {
	paths := make(map[string]bool, len(before)+len(after))
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	result := make([]NotebookChangeDiff, 0, len(ordered))
	for _, rel := range ordered {
		beforeContent, beforeExists := before[rel]
		afterContent, afterExists := after[rel]
		if beforeExists && afterExists && bytes.Equal(beforeContent, afterContent) {
			continue
		}
		status := "modified"
		if !beforeExists {
			status = "added"
		} else if !afterExists {
			status = "deleted"
		}
		result = append(result, NotebookChangeDiff{
			Path:   filepath.ToSlash(filepath.Join(notebookDir, filepath.FromSlash(rel))),
			Status: status, Before: string(beforeContent), After: string(afterContent),
		})
	}
	return result
}

type notebookTransactionEntry struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	Backup       string `json:"backup,omitempty"`
}

type notebookTransactionState struct {
	Phase   string                     `json:"phase"`
	Entries []notebookTransactionEntry `json:"entries"`
}

type notebookTransactionHook func(index int, path string) error

func applyNotebookFileTransaction(workspaceRoot, notebookDir string, before, after map[string][]byte, hook notebookTransactionHook) error {
	current, err := readNotebookAuthoredFiles(notebookDir)
	if err != nil {
		return err
	}
	if !equalNotebookFiles(current, before) {
		return fmt.Errorf("notebook files changed before transaction commit")
	}

	notebookRel, err := filepath.Rel(workspaceRoot, notebookDir)
	if err != nil || strings.HasPrefix(notebookRel, "..") {
		return fmt.Errorf("notebook directory is outside the workspace")
	}
	return applyWorkspaceFileTransaction(
		workspaceRoot,
		prefixWorkspaceFileSet(notebookRel, before),
		prefixWorkspaceFileSet(notebookRel, after),
		hook,
	)
}

func prefixWorkspaceFileSet(prefix string, files map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for rel, content := range files {
		result[filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(rel)))] = content
	}
	return result
}

// applyWorkspaceFileTransaction commits an exact set of workspace-relative
// file mutations through the notebook recovery journal. Notebook promotion
// needs this wider boundary because it removes notebook blocks, rewrites
// sibling cells, and creates pipeline assets as one logical operation. The
// journal already stores workspace-relative targets, so startup recovery works
// for both notebook-only and cross-artifact transactions.
func applyWorkspaceFileTransaction(workspaceRoot string, before, after map[string][]byte, hook notebookTransactionHook) error {
	paths := make(map[string]bool, len(before)+len(after))
	for path := range before {
		paths[filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))] = true
	}
	for path := range after {
		paths[filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))] = true
	}
	ordered := make([]string, 0, len(paths))
	for rel := range paths {
		beforeContent, beforeExists := before[rel]
		afterContent, afterExists := after[rel]
		if beforeExists && afterExists && bytes.Equal(beforeContent, afterContent) {
			continue
		}
		targetPath, err := SafeJoin(workspaceRoot, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		current, readErr := os.ReadFile(targetPath)
		switch {
		case beforeExists && readErr != nil:
			return fmt.Errorf("workspace file %s changed before transaction commit", rel)
		case beforeExists && !bytes.Equal(current, beforeContent):
			return fmt.Errorf("workspace file %s changed before transaction commit", rel)
		case !beforeExists && readErr == nil:
			return fmt.Errorf("workspace file %s appeared before transaction commit", rel)
		case !beforeExists && !os.IsNotExist(readErr):
			return fmt.Errorf("inspect workspace file %s: %w", rel, readErr)
		}
		ordered = append(ordered, rel)
	}
	sort.Strings(ordered)
	if len(ordered) == 0 {
		return nil
	}

	journalRoot := filepath.Join(workspaceRoot, ".renart", "notebook-transactions")
	if err := os.MkdirAll(journalRoot, 0o700); err != nil {
		return err
	}
	journalDir, err := os.MkdirTemp(journalRoot, "transaction-")
	if err != nil {
		return err
	}
	state := notebookTransactionState{Phase: "prepared", Entries: make([]notebookTransactionEntry, 0, len(ordered))}
	for index, rel := range ordered {
		entry := notebookTransactionEntry{
			Path:         rel,
			BeforeExists: false,
		}
		if content, exists := before[rel]; exists {
			entry.BeforeExists = true
			entry.Backup = filepath.ToSlash(filepath.Join("backups", fmt.Sprintf("%03d", index)))
			backupPath := filepath.Join(journalDir, filepath.FromSlash(entry.Backup))
			if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
				_ = os.RemoveAll(journalDir)
				return err
			}
			if err := os.WriteFile(backupPath, content, 0o600); err != nil {
				_ = os.RemoveAll(journalDir)
				return err
			}
		}
		state.Entries = append(state.Entries, entry)
	}
	if err := writeNotebookTransactionState(journalDir, state); err != nil {
		_ = os.RemoveAll(journalDir)
		return err
	}
	state.Phase = "applying"
	if err := writeNotebookTransactionState(journalDir, state); err != nil {
		_ = os.RemoveAll(journalDir)
		return err
	}

	rollback := func() error { return rollbackNotebookTransaction(workspaceRoot, journalDir, state) }
	for index, rel := range ordered {
		targetPath, joinErr := SafeJoin(workspaceRoot, filepath.FromSlash(rel))
		if joinErr != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return fmt.Errorf("resolve %s: %v; rollback: %w", rel, joinErr, rollbackErr)
			}
			return joinErr
		}
		if hook != nil {
			if hookErr := hook(index, targetPath); hookErr != nil {
				if rollbackErr := rollback(); rollbackErr != nil {
					return fmt.Errorf("commit hook: %v; rollback: %w", hookErr, rollbackErr)
				}
				return hookErr
			}
		}
		content, exists := after[rel]
		if !exists {
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				if rollbackErr := rollback(); rollbackErr != nil {
					return fmt.Errorf("remove %s: %v; rollback: %w", rel, err, rollbackErr)
				}
				return err
			}
			continue
		}
		if err := writeFileAtomically(targetPath, content, 0o644); err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return fmt.Errorf("write %s: %v; rollback: %w", rel, err, rollbackErr)
			}
			return err
		}
	}
	state.Phase = "committed"
	if err := writeNotebookTransactionState(journalDir, state); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return fmt.Errorf("mark transaction committed: %v; rollback: %w", err, rollbackErr)
		}
		return err
	}
	return os.RemoveAll(journalDir)
}

func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".renart-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := temp.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func writeNotebookTransactionState(journalDir string, state notebookTransactionState) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return writeFileAtomically(filepath.Join(journalDir, "state.json"), content, 0o600)
}

func rollbackNotebookTransaction(workspaceRoot, journalDir string, state notebookTransactionState) error {
	var failures []string
	for index := len(state.Entries) - 1; index >= 0; index-- {
		entry := state.Entries[index]
		target, err := SafeJoin(workspaceRoot, filepath.FromSlash(entry.Path))
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !entry.BeforeExists {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err.Error())
			}
			continue
		}
		backup, err := os.ReadFile(filepath.Join(journalDir, filepath.FromSlash(entry.Backup)))
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if err := writeFileAtomically(target, backup, 0o644); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return os.RemoveAll(journalDir)
}

func recoverNotebookFileTransactions(workspaceRoot string) error {
	journalRoot := filepath.Join(workspaceRoot, ".renart", "notebook-transactions")
	entries, err := os.ReadDir(journalRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		journalDir := filepath.Join(journalRoot, entry.Name())
		content, readErr := os.ReadFile(filepath.Join(journalDir, "state.json"))
		if readErr != nil {
			failures = append(failures, readErr.Error())
			continue
		}
		var state notebookTransactionState
		if unmarshalErr := json.Unmarshal(content, &state); unmarshalErr != nil {
			failures = append(failures, unmarshalErr.Error())
			continue
		}
		if state.Phase == "committed" {
			if removeErr := os.RemoveAll(journalDir); removeErr != nil {
				failures = append(failures, removeErr.Error())
			}
			continue
		}
		if rollbackErr := rollbackNotebookTransaction(workspaceRoot, journalDir, state); rollbackErr != nil {
			failures = append(failures, rollbackErr.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("recover notebook transactions: %s", strings.Join(failures, "; "))
	}
	return nil
}
