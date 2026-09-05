package databrowser

import (
	"context"
	"net/http"
	"strings"

	"renart/internal/web/apperror"
	"renart/internal/web/dataaddress"
)

// Resolve discovers the exact current object and mints a fresh operation token.
// It never runs a row preview or constructs SQL from the supplied address.
func (s *Service) Resolve(ctx context.Context, request ResolveRequest) (ObjectResponse, *apperror.Error) {
	a := request.Address
	for _, value := range []string{request.Environment, a.Connection, a.ConnectionType, a.Database, a.Schema, a.Name, a.Path} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return ObjectResponse{}, badRequest("data_browser_address_invalid", "This data address is invalid.")
		}
	}
	if request.Environment == "" || (a.SourceKind != "warehouse" && a.SourceKind != "local_files") ||
		(a.SourceKind == "warehouse" && (a.Connection == "" || a.ConnectionType == "" || a.Name == "" || a.Path != "")) ||
		(a.SourceKind == "local_files" && (a.Path == "" || a.Connection != "" || a.ConnectionType != "" || a.Database != "" || a.Schema != "" || a.Name != "")) {
		return ObjectResponse{}, badRequest("data_browser_address_invalid", "This data address is incomplete or ambiguous.")
	}
	connections, apiErr := s.Connections(ctx, request.Environment)
	if apiErr != nil {
		return ObjectResponse{}, apiErr
	}
	if connections.Environment != request.Environment {
		return ObjectResponse{}, badRequest("data_browser_environment_invalid", "The linked environment is not available.")
	}
	var candidates []Connection
	for _, c := range connections.Connections {
		if c.SourceKind == a.SourceKind && (a.SourceKind == "local_files" || (c.Name == a.Connection && c.Type == a.ConnectionType)) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) != 1 {
		return ObjectResponse{}, &apperror.Error{Status: http.StatusNotFound, Code: "data_browser_connection_not_found", Message: "The linked data source is missing or ambiguous in this environment."}
	}
	ref, _ := decodeRef(candidates[0].ID)
	if a.SourceKind == "local_files" {
		ref.Kind, ref.Path = "file", a.Path
		return s.Object(ctx, encodeRef(ref), request.Environment)
	}
	if s.deps.ListTables == nil {
		return ObjectResponse{}, badRequest("data_browser_discovery_unavailable", "Table discovery is unavailable for this data source.")
	}
	tables, err := s.deps.ListTables(ctx, a.Connection, a.Database, request.Environment)
	if err != nil {
		return ObjectResponse{}, internalError("data_browser_discovery_failed", err)
	}
	var matches []objectRef
	for _, table := range tables {
		candidate := tableRef(ref, table, a.Database)
		if *addressForRef(candidate) == a {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return ObjectResponse{}, &apperror.Error{Status: http.StatusNotFound, Code: "data_browser_object_not_found", Message: "The linked object was removed, renamed, or is ambiguous. No other object has been selected."}
	}
	return s.Object(ctx, encodeRef(matches[0]), request.Environment)
}

func tableRef(connection objectRef, table Table, database string) objectRef {
	connection.Kind, connection.Name = "table", table.Name
	connection.Database, connection.Schema = table.DatabaseName, table.SchemaName
	if connection.Database == "" {
		connection.Database = database
	}
	connection.LeafName = table.ShortName
	if connection.LeafName == "" {
		connection.LeafName = shortObjectName(table.Name)
	}
	return connection
}

func addressForRef(ref objectRef) *dataaddress.Address {
	if ref.Kind == "file" {
		return &dataaddress.Address{SourceKind: "local_files", Path: ref.Path}
	}
	if ref.Kind != "table" {
		return nil
	}
	name := ref.LeafName
	if name == "" {
		name = shortObjectName(ref.Name)
	}
	return &dataaddress.Address{SourceKind: "warehouse", Connection: ref.Connection, ConnectionType: ref.ConnectionType, Database: ref.Database, Schema: ref.Schema, Name: name}
}
