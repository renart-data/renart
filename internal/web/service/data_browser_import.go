package service

import (
	"context"
	"strings"

	"renart/internal/web/databrowser"
)

// DataBrowserSourceRequest carries a revision-bound object reference, never
// credentials, executable SQL, a client-derived asset type, or a file path.
// renart:web
type DataBrowserSourceRequest struct {
	ObjectID       string `json:"object_id"`
	Environment    string `json:"environment"`
	IncludeColumns *bool  `json:"include_columns,omitempty"`
}

// ImportDataBrowserSource accepts only objects freshly resolved by the Data
// Browser service at the HTTP boundary, on both preview and confirmation.
func (s *PipelineService) ImportDataBrowserSource(ctx context.Context, pipelineID string, object databrowser.Object, preview, includeColumns bool) (ExternalRelationImportResult, *APIError) {
	if object.Kind != "table" || object.Address == nil || object.Address.SourceKind != "warehouse" ||
		strings.TrimSpace(object.ConnectionName) == "" || strings.TrimSpace(object.Environment) == "" ||
		strings.TrimSpace(object.ReferenceText) == "" {
		return ExternalRelationImportResult{}, newAPIError(400, "source_table_required", "Choose an existing warehouse table in the Data Browser.")
	}
	if _, ok := sourceAssetTypeForConnectionType(object.ConnectionType); !ok {
		return ExternalRelationImportResult{}, newAPIError(400, "source_connection_unsupported", "This connection does not support Source assets.")
	}
	relation := TypeCheckExternalRelation{
		ID: object.ID, Connection: object.ConnectionName, Environment: object.Environment,
		QualifiedName: object.ReferenceText, SchemaName: object.Address.Schema,
		Name: object.Name, Columns: object.Columns, ColumnsKnown: true,
	}
	return s.importObservedRelation(ctx, pipelineID, relation, &includeColumns, preview, false)
}
