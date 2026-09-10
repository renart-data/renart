package service

import (
	"reflect"
	"strings"

	"renart/internal/web/databrowser"
	"renart/internal/web/model"
)

// renart:web
type NotebookBrowserSourceRequest struct {
	ObjectID     string             `json:"object_id"`
	Environment  string             `json:"environment"`
	BaseRevision string             `json:"base_revision,omitempty"`
	Position     string             `json:"position,omitempty"`
	AfterBlockID string             `json:"after_block_id,omitempty"`
	Name         string             `json:"name,omitempty"`
	SnapshotMode string             `json:"snapshot_mode,omitempty"`
	RowLimit     int64              `json:"row_limit,omitempty"`
	ChangeSet    *NotebookChangeSet `json:"change_set,omitempty"`
}

func browserSourceOperation(source databrowser.NotebookSourceReference, req NotebookBrowserSourceRequest) (NotebookOperation, *APIError) {
	if strings.TrimSpace(req.Environment) == "" {
		return NotebookOperation{}, badRequestError("notebook_browser_environment_required", "Choose an environment before adding a notebook source.")
	}
	mode := req.SnapshotMode
	if mode == "" {
		mode = "full"
	}
	if (mode != "full" && mode != "sample") || (mode == "sample" && req.RowLimit <= 0) {
		return NotebookOperation{}, badRequestError("invalid_notebook_source", "Choose a full snapshot or a sample with a positive row limit.")
	}
	if mode == "full" {
		req.RowLimit = 0
	}
	if req.Position != "start" && req.Position != "after" {
		return NotebookOperation{}, badRequestError("invalid_block_position", "Choose a notebook insertion point.")
	}
	op := NotebookOperation{Name: req.Name, Position: req.Position, AfterBlockID: req.AfterBlockID, Environment: req.Environment}
	if source.Query != "" {
		op.Kind, op.Language, op.Connection, op.Content = NotebookOperationCellCreate, "sql", source.Connection, source.Query
		op.SnapshotMode, op.RowLimit = mode, req.RowLimit
	} else if source.URI != "" {
		op.Kind = NotebookOperationSourceCreate
		op.Source = &model.NotebookSourceDefinition{Version: 1, Kind: "file", Connection: source.Connection, URI: source.URI, Format: source.Format, Snapshot: model.NotebookSourceSnapshot{Mode: mode, RowLimit: req.RowLimit}}
	} else {
		return NotebookOperation{}, badRequestError("notebook_browser_source_invalid", "Choose a table or supported file.")
	}
	return op, nil
}

func (s *NotebookService) PrepareBrowserSource(notebookID string, source databrowser.NotebookSourceReference, req NotebookBrowserSourceRequest) (NotebookChangePlan, *APIError) {
	op, apiErr := browserSourceOperation(source, req)
	if apiErr != nil {
		return NotebookChangePlan{}, apiErr
	}
	return s.PrepareChangeSet(notebookID, NotebookChangeSet{BaseRevision: req.BaseRevision, Operations: []NotebookOperation{op}})
}

// Apply re-resolves the browser reference at the HTTP boundary, then rebuilds
// the proposed operation using that source. It cannot substitute a client SQL
// label, a changed environment, or a different file into the reviewed batch.
func (s *NotebookService) ApplyBrowserSource(notebookID string, source databrowser.NotebookSourceReference, req NotebookBrowserSourceRequest) (NotebookChangeApplyResult, *APIError) {
	if req.ChangeSet == nil || req.ChangeSet.ExpectedRevision == "" || len(req.ChangeSet.Operations) != 1 {
		return NotebookChangeApplyResult{}, badRequestError("notebook_change_not_prepared", "Review the notebook source before adding it.")
	}
	reviewed := req.ChangeSet.Operations[0]
	if reviewed.Environment != req.Environment {
		return NotebookChangeApplyResult{}, badRequestError("notebook_browser_environment_changed", "The environment changed. Review the source again.")
	}
	req.Name, req.Position, req.AfterBlockID = reviewed.Name, reviewed.Position, reviewed.AfterBlockID
	req.SnapshotMode, req.RowLimit = reviewed.SnapshotMode, reviewed.RowLimit
	if reviewed.Source != nil {
		req.SnapshotMode, req.RowLimit = reviewed.Source.Snapshot.Mode, reviewed.Source.Snapshot.RowLimit
	}
	op, apiErr := browserSourceOperation(source, req)
	if apiErr != nil {
		return NotebookChangeApplyResult{}, apiErr
	}
	op.CellID = reviewed.CellID
	plan, apiErr := s.PrepareChangeSet(notebookID, NotebookChangeSet{BaseRevision: req.ChangeSet.BaseRevision, Operations: []NotebookOperation{op}})
	if apiErr != nil {
		return NotebookChangeApplyResult{}, apiErr
	}
	if !plan.CanApply || !reflect.DeepEqual(plan.ChangeSet, *req.ChangeSet) {
		return NotebookChangeApplyResult{}, &APIError{Status: 409, Code: "notebook_browser_source_changed", Message: "This source or notebook changed. Review the insertion again."}
	}
	return s.ApplyChangeSet(notebookID, plan.ChangeSet)
}
