package databrowser

import (
	"context"
	"strings"

	"renart/internal/web/apperror"
	"renart/internal/web/sqlnamespace"
)

// NotebookSourceReference is an authoring handoff, not a snapshot or a preview.
// Only identifiers discovered on the server may become executable selectors.
type NotebookSourceReference struct {
	Connection string
	Query      string
	URI        string
	Format     string
}

func (s *Service) NotebookSource(ctx context.Context, objectID, environment string) (NotebookSourceReference, *apperror.Error) {
	ref, err := decodeRef(objectID)
	if err != nil || environment == "" || addressForRef(ref) == nil {
		return NotebookSourceReference{}, badRequest("notebook_browser_source_invalid", "Choose a table or supported file from the Data Browser.")
	}
	connection := ref
	connection.Kind, connection.Catalog, connection.Database, connection.Schema, connection.Name, connection.LeafName, connection.Path = "connection", "", "", "", "", "", ""
	scope, apiErr := s.resolveScope(ctx, encodeRef(connection), environment)
	if apiErr != nil {
		return NotebookSourceReference{}, apiErr
	}
	if ref.SourceKind == "warehouse" {
		supported := false
		for _, c := range scope.connections {
			if c.Name == ref.Connection && c.Type == ref.ConnectionType {
				supported = c.NotebookSource
			}
		}
		if !supported {
			return NotebookSourceReference{}, badRequest("notebook_browser_source_unsupported", "This warehouse does not support typed notebook snapshots yet.")
		}
	}
	if ref.SourceKind == "storage" && (ref.ConnectionType != "s3" || ref.Kind != "storage_object") {
		return NotebookSourceReference{}, badRequest("notebook_browser_source_unsupported", "Choose an individual S3 file. Prefix imports and SFTP notebook transfers are not supported yet.")
	}
	// Reuse address discovery and path containment without describing files,
	// reading view definitions, previewing rows or opening a notebook session.
	metadata := *s
	metadata.deps.ListColumns = nil
	metadata.deps.LookupViewDefinition = nil
	metadata.deps.RunQuery = nil
	resolved, apiErr := metadata.Resolve(ctx, ResolveRequest{Environment: environment, Address: *addressForRef(ref)})
	if apiErr != nil {
		return NotebookSourceReference{}, apiErr
	}
	object := resolved.Object
	if object.Revision != ref.Revision {
		return NotebookSourceReference{}, badRequest("notebook_browser_source_changed", "This source changed. Refresh the Data Browser and review it again.")
	}
	source := NotebookSourceReference{Connection: object.ConnectionName}
	if ref.SourceKind == "warehouse" {
		query, err := sqlnamespace.QuoteReference(ref.ConnectionType, object.ReferenceText)
		if err != nil {
			return NotebookSourceReference{}, badRequest("notebook_browser_source_invalid", "This table has an invalid qualified name.")
		}
		// SQL source cells render Jinja. Literal template delimiters in identifiers
		// must not be silently promoted into executable templates.
		if strings.Contains(query, "{{") || strings.Contains(query, "{%") {
			return NotebookSourceReference{}, badRequest("notebook_browser_source_invalid", "Table names containing template delimiters cannot be inserted as notebook sources.")
		}
		source.Query = "select * from " + query
	} else {
		if object.Format == "" || strings.ContainsAny(object.ReferenceText, "*?[]") || strings.Contains(object.ReferenceText, "{{") || strings.Contains(object.ReferenceText, "{%") {
			return NotebookSourceReference{}, badRequest("notebook_browser_source_unsupported", "Choose a CSV, JSON, JSONL or Parquet file with a literal path (no wildcards or templates).")
		}
		source.URI, source.Format = object.ReferenceText, object.Format
		if ref.SourceKind == "local_files" {
			source.Connection = ""
		}
	}
	return source, nil
}
