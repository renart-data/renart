package service

import (
	"fmt"
	"strings"

	"renart/internal/web/notebook"
)

// validateNotebookStorageConnection adapts the live workspace connection
// catalog to the authored notebook domain without making notebookdoc depend on
// the broad workspace state model.
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

// resolveNotebookSourceAssetType translates a selected query connection into
// the Bruin asset type persisted in a notebook cell header.
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
