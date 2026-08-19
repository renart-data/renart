package service

import (
	"fmt"
	"net/http"
	"strings"

	"renart/internal/web/model"
)

// ResolveNotebookAgentReferences turns browser-supplied opaque addresses into
// canonical, credential-free labels from the current workspace snapshot. It
// deliberately does not include file paths, SQL content, request configuration,
// or credentials; the agent must use its scoped semantic tools for details.
func ResolveNotebookAgentReferences(
	notebook model.Notebook,
	state WorkspaceState,
	inputs []NotebookAgentReferenceRequest,
) ([]NotebookAgentReference, *APIError) {
	cells := make(map[string]model.Asset, len(notebook.Cells))
	for _, cell := range notebook.Cells {
		cells[strings.TrimSpace(cell.CellID)] = cell
	}
	assets := make(map[string]model.Asset)
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			assets[strings.TrimSpace(asset.ID)] = asset
		}
	}

	resolved := make([]NotebookAgentReference, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		kind := strings.ToLower(strings.TrimSpace(input.Kind))
		id := strings.TrimSpace(input.ID)
		if id == "" {
			return nil, badRequestError(
				"notebook_agent_reference_id_required",
				"every notebook agent reference must include an id",
			)
		}
		key := kind + "\x00" + id
		if _, exists := seen[key]; exists {
			continue
		}

		var reference NotebookAgentReference
		switch kind {
		case "cell":
			cell, exists := cells[id]
			if !exists {
				return nil, &APIError{
					Status:  http.StatusBadRequest,
					Code:    "notebook_agent_cell_reference_not_found",
					Message: fmt.Sprintf("cell reference %q does not belong to the selected notebook", id),
				}
			}
			reference = NotebookAgentReference{
				Kind: "cell", ID: id, Label: notebookAgentReferenceLabel(cell.Name, id),
				Detail: notebookAgentCellReferenceDetail(cell),
			}
		case "asset":
			asset, exists := assets[id]
			if !exists {
				return nil, &APIError{
					Status:  http.StatusBadRequest,
					Code:    "notebook_agent_asset_reference_not_found",
					Message: fmt.Sprintf("asset reference %q does not exist in the current workspace", id),
				}
			}
			reference = NotebookAgentReference{
				Kind: "asset", ID: id, Label: notebookAgentReferenceLabel(asset.Name, id),
				Detail: notebookAgentAssetReferenceDetail(asset),
			}
		default:
			return nil, badRequestError(
				"notebook_agent_reference_kind_invalid",
				"notebook agent references must be cells or assets",
			)
		}
		seen[key] = struct{}{}
		resolved = append(resolved, reference)
	}
	return resolved, nil
}

func notebookAgentReferenceLabel(label, fallback string) string {
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	return fallback
}

func notebookAgentCellReferenceDetail(cell model.Asset) string {
	parts := []string{strings.TrimSpace(cell.Type)}
	if connection := strings.TrimSpace(cell.Connection); connection != "" {
		parts = append(parts, "connection "+connection)
	}
	return strings.Join(compactNotebookAgentReferenceParts(parts), ", ")
}

func notebookAgentAssetReferenceDetail(asset model.Asset) string {
	parts := []string{strings.TrimSpace(asset.Type)}
	if connection := strings.TrimSpace(asset.Connection); connection != "" {
		parts = append(parts, "connection "+connection)
	}
	return strings.Join(compactNotebookAgentReferenceParts(parts), ", ")
}

func compactNotebookAgentReferenceParts(parts []string) []string {
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
