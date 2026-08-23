package service

import (
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/notebookdoc"
)

// Compatibility aliases keep the HTTP and MCP contracts stable while the
// authored notebook lifecycle is owned by notebookdoc.Service.
type CreateNotebookRequest = notebookdoc.CreateRequest
type CreateCellRequest = notebookdoc.CreateCellRequest
type UpdateCellRequest = notebookdoc.UpdateCellRequest
type NotebookSQLRefactor = notebookdoc.NotebookSQLRefactor
type NotebookOperation = notebookdoc.NotebookOperation
type NotebookChangeSet = notebookdoc.NotebookChangeSet
type NotebookChangeDiff = notebookdoc.NotebookChangeDiff
type NotebookChangePlan = notebookdoc.NotebookChangePlan
type NotebookChangeApplyResult = notebookdoc.NotebookChangeApplyResult

const (
	notebookChangePositionStart = "start"

	NotebookOperationManifestUpgrade      = notebookdoc.NotebookOperationManifestUpgrade
	NotebookOperationCellCreate           = notebookdoc.NotebookOperationCellCreate
	NotebookOperationCellUpdate           = notebookdoc.NotebookOperationCellUpdate
	NotebookOperationCellSQLRefactor      = notebookdoc.NotebookOperationCellSQLRefactor
	NotebookOperationCellRename           = notebookdoc.NotebookOperationCellRename
	NotebookOperationCellDelete           = notebookdoc.NotebookOperationCellDelete
	NotebookOperationCellSourceConfigure  = notebookdoc.NotebookOperationCellSourceConfigure
	NotebookOperationSourceCreate         = notebookdoc.NotebookOperationSourceCreate
	NotebookOperationSourceUpdate         = notebookdoc.NotebookOperationSourceUpdate
	NotebookOperationMarkdownCreate       = notebookdoc.NotebookOperationMarkdownCreate
	NotebookOperationMarkdownUpdate       = notebookdoc.NotebookOperationMarkdownUpdate
	NotebookOperationVisualizationCreate  = notebookdoc.NotebookOperationVisualizationCreate
	NotebookOperationVisualizationUpdate  = notebookdoc.NotebookOperationVisualizationUpdate
	NotebookOperationVisualizationMigrate = notebookdoc.NotebookOperationVisualizationMigrate
	NotebookOperationParametersReplace    = notebookdoc.NotebookOperationParametersReplace
	NotebookOperationControlCreate        = notebookdoc.NotebookOperationControlCreate
	NotebookOperationControlUpdate        = notebookdoc.NotebookOperationControlUpdate
	NotebookOperationControlDelete        = notebookdoc.NotebookOperationControlDelete
	NotebookOperationBlockMove            = notebookdoc.NotebookOperationBlockMove
	NotebookOperationBlockDelete          = notebookdoc.NotebookOperationBlockDelete

	NotebookSQLRefactorRelationRename = notebookdoc.NotebookSQLRefactorRelationRename
	NotebookSQLRefactorColumnQualify  = notebookdoc.NotebookSQLRefactorColumnQualify
	NotebookSQLRefactorRelationAlias  = notebookdoc.NotebookSQLRefactorRelationAlias
)

func (s *NotebookService) Create(req CreateNotebookRequest) (model.Notebook, *APIError) {
	return s.documents.Create(req)
}

func (s *NotebookService) Delete(notebookID string) *APIError {
	return s.documents.Delete(notebookID)
}

func (s *NotebookService) CloseSession(notebookID string) *APIError {
	return s.documents.CloseSession(notebookID)
}

func (s *NotebookService) CreateCell(notebookID string, req CreateCellRequest) (model.Notebook, *APIError) {
	return s.documents.CreateCell(notebookID, req)
}

func (s *NotebookService) RenameCell(notebookID, cellID, newName string) (model.Notebook, *APIError) {
	return s.documents.RenameCell(notebookID, cellID, newName)
}

func (s *NotebookService) UpdateCell(notebookID, cellID string, req UpdateCellRequest) (model.Notebook, *APIError) {
	return s.documents.UpdateCell(notebookID, cellID, req)
}

func (s *NotebookService) DeleteCell(notebookID, cellID string) (model.Notebook, *APIError) {
	return s.documents.DeleteCell(notebookID, cellID)
}

func (s *NotebookService) UpdateBlocks(notebookID string, blocks []model.NotebookBlock) (model.Notebook, *APIError) {
	return s.documents.UpdateBlocks(notebookID, blocks)
}

func (s *NotebookService) UpgradeManifest(notebookID, baseRevision string) (model.Notebook, *APIError) {
	return s.documents.UpgradeManifest(notebookID, baseRevision)
}

func (s *NotebookService) PrepareChangeSet(notebookID string, changeSet NotebookChangeSet) (NotebookChangePlan, *APIError) {
	return s.documents.PrepareChangeSet(notebookID, changeSet)
}

func (s *NotebookService) ApplyChangeSet(notebookID string, changeSet NotebookChangeSet) (NotebookChangeApplyResult, *APIError) {
	return s.documents.ApplyChangeSet(notebookID, changeSet)
}

func nextCellAutoname(nb *notebook.Notebook, pipelineAssetNames map[string]bool) string {
	return notebookdoc.NextCellAutoname(nb, pipelineAssetNames)
}

func cellAutonameFromSeed(nb *notebook.Notebook, pipelineAssetNames map[string]bool, seed uint64) string {
	return notebookdoc.CellAutonameFromSeed(nb, pipelineAssetNames, seed)
}
