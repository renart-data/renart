package service

import (
	"fmt"
	"strings"

	"renart/internal/web/notebook"
)

// validateNotebookStorageConnection adapts the live workspace connection
// catalog to the authored notebook domain without making notebookdoc depend on
// the broad workspace state model.
func (s *NotebookService) validateNotebookStorageConnection(connection, environment string) *APIError {
	if environment != "" {
		connectionType, apiErr := s.notebookConnectionType(connection, environment)
		if apiErr != nil {
			return apiErr
		}
		if loadConnectionCategory(connectionType) != LoadCategoryStorage {
			return badRequestError("invalid_notebook_source_connection", "Choose an object-storage connection.")
		}
		return nil
	}
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
func (s *NotebookService) resolveNotebookSourceAssetType(connection, environment string) (string, *APIError) {
	connection = strings.TrimSpace(connection)
	if connection == "" {
		return notebook.DefaultCellType, nil
	}
	if environment != "" {
		connectionType, apiErr := s.notebookConnectionType(connection, environment)
		if apiErr != nil {
			return "", apiErr
		}
		if assetType, ok := queryAssetTypeForConnectionType(connectionType); ok {
			return string(assetType), nil
		}
		return "", badRequestError("invalid_notebook_source_connection", "This connection cannot execute notebook SQL.")
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

func (s *NotebookService) notebookConnectionType(connection, environment string) (string, *APIError) {
	resolved, connections, err := NewConfigService(s.deps.WorkspaceRoot, s.deps.ConfigPath).ConnectionSummaries(environment)
	if err != nil || resolved != environment || connections[connection] == "" {
		return "", badRequestError("unknown_notebook_source_connection", "This connection is unavailable in the selected environment. Refresh and review the source again.")
	}
	return connections[connection], nil
}
