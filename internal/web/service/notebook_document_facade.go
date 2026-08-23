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

func nextCellAutoname(nb *notebook.Notebook, pipelineAssetNames map[string]bool) string {
	return notebookdoc.NextCellAutoname(nb, pipelineAssetNames)
}

func cellAutonameFromSeed(nb *notebook.Notebook, pipelineAssetNames map[string]bool, seed uint64) string {
	return notebookdoc.CellAutonameFromSeed(nb, pipelineAssetNames, seed)
}
