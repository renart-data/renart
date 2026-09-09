package databrowser

import (
	"context"
	"net/http"
	"strings"

	"renart/internal/web/apperror"
	"renart/internal/web/dataaddress"
	"renart/internal/web/sqlnamespace"
)

// Resolve discovers the exact current object and mints a fresh operation token.
// It never runs a row preview or constructs SQL from the supplied address.
func (s *Service) Resolve(ctx context.Context, request ResolveRequest) (ObjectResponse, *apperror.Error) {
	a := request.Address
	if a.SourceKind != "warehouse" && a.Catalog != "" {
		return ObjectResponse{}, badRequest("data_browser_address_invalid", "Only warehouse objects have catalogs.")
	}
	for _, value := range []string{request.Environment, a.Connection, a.ConnectionType, a.Catalog, a.Database, a.Schema, a.Name, a.Path} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return ObjectResponse{}, badRequest("data_browser_address_invalid", "This data address is invalid.")
		}
	}
	if request.Environment == "" || (a.SourceKind != "warehouse" && a.SourceKind != "local_files" && a.SourceKind != "storage") ||
		(a.SourceKind == "storage" && (a.Connection == "" || a.ConnectionType == "" || a.Path == "" || a.Database != "" || a.Schema != "" || a.Name != "")) ||
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
	if a.SourceKind == "storage" {
		ref.Kind, ref.Path = "storage_object", a.Path
		if strings.HasSuffix(a.Path, "/") {
			ref.Kind = "storage_prefix"
		}
		return s.Object(ctx, encodeRef(ref), request.Environment)
	}
	if a.SourceKind == "local_files" {
		ref.Kind, ref.Path = "file", a.Path
		return s.Object(ctx, encodeRef(ref), request.Environment)
	}
	if s.deps.ListWarehouse != nil && sqlnamespace.Supported(ref.ConnectionType) {
		return s.resolveCatalogAddress(ctx, ref, a, request.Environment)
	}
	if a.Catalog != "" {
		return ObjectResponse{}, badRequest("data_browser_address_invalid", "This connection does not support catalog addresses.")
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
	connection.Catalog = table.CatalogName
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
	if ref.SourceKind == "storage" && (ref.Kind == "storage_object" || ref.Kind == "storage_prefix") {
		return &dataaddress.Address{SourceKind: "storage", Connection: ref.Connection, ConnectionType: ref.ConnectionType, Path: ref.Path}
	}
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
	return &dataaddress.Address{SourceKind: "warehouse", Connection: ref.Connection, ConnectionType: ref.ConnectionType, Catalog: ref.Catalog, Database: ref.Database, Schema: ref.Schema, Name: name}
}

func (s *Service) resolveCatalogAddress(ctx context.Context, ref objectRef, a dataaddress.Address, environment string) (ObjectResponse, *apperror.Error) {
	// Old Trino addresses stored the catalog in Database; DuckDB/Databricks
	// stored a schema there. Only catalogless legacy addresses are normalized.
	legacy := a.Catalog == ""
	if legacy {
		switch ref.ConnectionType {
		case "trino":
			a.Catalog, a.Database = a.Database, ""
		case "duckdb", "motherduck", "databricks":
			if a.Schema == "" {
				a.Schema = a.Database
			}
			a.Database = ""
		}
	}
	entries, err := s.deps.ListWarehouse(ctx, ref.Connection, sqlnamespace.Scope{Catalog: a.Catalog, Database: a.Database, Schema: a.Schema}, environment)
	if err != nil {
		return ObjectResponse{}, internalError("data_browser_discovery_failed", err)
	}
	var matches []objectRef
	for _, entry := range entries {
		if entry.Kind != "table" {
			continue
		}
		candidate := ref
		candidate.Kind, candidate.Name, candidate.LeafName = "table", entry.Reference, entry.Name
		candidate.Catalog, candidate.Database, candidate.Schema = entry.Scope.Catalog, entry.Scope.Database, entry.Scope.Schema
		address := *addressForRef(candidate)
		if legacy && a.Catalog == "" {
			address.Catalog = ""
		}
		if address == a {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return ObjectResponse{}, &apperror.Error{Status: http.StatusNotFound, Code: "data_browser_object_not_found", Message: "The linked object was removed, renamed, or is ambiguous. No other object has been selected."}
	}
	return s.Object(ctx, encodeRef(matches[0]), environment)
}
