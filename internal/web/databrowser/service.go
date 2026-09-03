package databrowser

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"renart/internal/web/apperror"
	"renart/internal/web/model"
)

const (
	localFilesConnectionName = "Project files"
	maxChildren              = 500
	maxPreviewRows           = 200
)

var supportedLocalExtensions = map[string]string{
	".csv":     "csv",
	".json":    "json",
	".jsonl":   "jsonl",
	".ndjson":  "jsonl",
	".parquet": "parquet",
}

var excludedLocalDirectories = map[string]struct{}{
	".git":         {},
	".renart":      {},
	".venv":        {},
	"__pycache__":  {},
	"dist":         {},
	"node_modules": {},
}

type ConnectionConfig struct {
	Name      string
	Type      string
	Queryable bool
}

type Table struct {
	Name         string
	ShortName    string
	SchemaName   string
	DatabaseName string
}

type QueryResult struct {
	Columns   []string
	Rows      []map[string]any
	Truncated bool
}

type Dependencies struct {
	WorkspaceRoot   string
	ListConnections func(context.Context, string) (string, []ConnectionConfig, int64, error)
	ListDatabases   func(context.Context, string, string) ([]string, error)
	ListTables      func(context.Context, string, string, string) ([]Table, error)
	ListColumns     func(context.Context, string, string, string) ([]model.SQLColumn, error)
	RunQuery        func(context.Context, string, string, string, int) (QueryResult, error)
	Now             func() time.Time
}

type Service struct {
	deps Dependencies
}

type objectRef struct {
	Kind           string `json:"k"`
	SourceKind     string `json:"s"`
	Connection     string `json:"c,omitempty"`
	ConnectionType string `json:"t,omitempty"`
	Environment    string `json:"e"`
	Revision       string `json:"r"`
	Database       string `json:"d,omitempty"`
	Schema         string `json:"h,omitempty"`
	Name           string `json:"n,omitempty"`
	Path           string `json:"p,omitempty"`
}

type resolvedScope struct {
	ref         objectRef
	connections []ConnectionConfig
	revision    string
}

func New(deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}
}

func (s *Service) Connections(ctx context.Context, environment string) (ConnectionsResponse, *apperror.Error) {
	resolvedEnvironment, configs, stateRevision, err := s.listConnections(ctx, environment)
	if err != nil {
		return ConnectionsResponse{}, internalError("data_browser_connections_failed", err)
	}
	revision := revisionToken(resolvedEnvironment, stateRevision, configs)
	duckDBAvailable := hasDuckDBConnection(configs)
	connections := make([]Connection, 0, len(configs)+1)
	for _, config := range configs {
		if !config.Queryable {
			continue
		}
		ref := objectRef{
			Kind:           "connection",
			SourceKind:     "warehouse",
			Connection:     config.Name,
			ConnectionType: config.Type,
			Environment:    resolvedEnvironment,
			Revision:       revision,
		}
		connections = append(connections, Connection{
			ID:              encodeRef(ref),
			Name:            config.Name,
			Type:            config.Type,
			Environment:     resolvedEnvironment,
			Revision:        revision,
			SourceKind:      "warehouse",
			DiscoveryStatus: "idle",
			Capabilities: Capabilities{
				ListNamespaces:  true,
				ListObjects:     true,
				DescribeColumns: s.deps.ListColumns != nil,
				PreviewRows:     s.deps.RunQuery != nil,
				Query:           true,
			},
		})
	}
	if strings.TrimSpace(s.deps.WorkspaceRoot) != "" {
		ref := objectRef{
			Kind:        "connection",
			SourceKind:  "local_files",
			Environment: resolvedEnvironment,
			Revision:    revision,
		}
		connections = append(connections, Connection{
			ID:              encodeRef(ref),
			Name:            localFilesConnectionName,
			Type:            "file",
			Environment:     resolvedEnvironment,
			Revision:        revision,
			SourceKind:      "local_files",
			DiscoveryStatus: "ready",
			Capabilities: Capabilities{
				ListNamespaces:  true,
				ListObjects:     true,
				DescribeColumns: duckDBAvailable && s.deps.RunQuery != nil,
				PreviewRows:     duckDBAvailable && s.deps.RunQuery != nil,
			},
		})
	}

	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].SourceKind != connections[j].SourceKind {
			return connections[i].SourceKind == "warehouse"
		}
		return strings.ToLower(connections[i].Name) < strings.ToLower(connections[j].Name)
	})
	return ConnectionsResponse{
		Status:      "ok",
		Environment: resolvedEnvironment,
		Revision:    revision,
		Connections: connections,
	}, nil
}

func (s *Service) Children(ctx context.Context, connectionID, parentID, environment string) (ChildrenResponse, *apperror.Error) {
	scope, apiErr := s.resolveScope(ctx, connectionID, environment)
	if apiErr != nil {
		return ChildrenResponse{}, apiErr
	}
	parent := scope.ref
	if strings.TrimSpace(parentID) != "" {
		decoded, err := decodeRef(parentID)
		if err != nil {
			return ChildrenResponse{}, badRequest("data_browser_parent_invalid", "The selected data-browser parent is invalid.")
		}
		if !sameScope(scope.ref, decoded) {
			return ChildrenResponse{}, badRequest("data_browser_parent_scope_mismatch", "The selected parent belongs to another data source.")
		}
		parent = decoded
	}

	var nodes []Node
	var truncated bool
	var err error
	if scope.ref.SourceKind == "local_files" {
		nodes, truncated, err = s.localChildren(scope.ref, parent, parentID)
	} else {
		nodes, truncated, err = s.warehouseChildren(ctx, scope.ref, parent, parentID)
	}
	if err != nil {
		return ChildrenResponse{}, internalError("data_browser_discovery_failed", err)
	}
	return ChildrenResponse{
		Status:       "ok",
		ConnectionID: connectionID,
		ParentID:     parentID,
		Revision:     scope.revision,
		Nodes:        nodes,
		Truncated:    truncated,
	}, nil
}

func (s *Service) Object(ctx context.Context, objectID, environment string) (ObjectResponse, *apperror.Error) {
	ref, err := decodeRef(objectID)
	if err != nil || (ref.Kind != "table" && ref.Kind != "file") {
		return ObjectResponse{}, badRequest("data_browser_object_invalid", "The selected data object is invalid.")
	}
	connectionRef := ref
	connectionRef.Kind = "connection"
	connectionRef.Database = ""
	connectionRef.Schema = ""
	connectionRef.Name = ""
	connectionRef.Path = ""
	scope, apiErr := s.resolveScope(ctx, encodeRef(connectionRef), environment)
	if apiErr != nil {
		return ObjectResponse{}, apiErr
	}
	if !sameScope(scope.ref, ref) {
		return ObjectResponse{}, badRequest("data_browser_object_scope_mismatch", "The selected object belongs to another data source.")
	}

	object := Object{
		ID:             objectID,
		ConnectionID:   encodeRef(connectionRef),
		ConnectionName: ref.Connection,
		ConnectionType: ref.ConnectionType,
		Environment:    ref.Environment,
		Revision:       ref.Revision,
		Columns:        []model.SQLColumn{},
	}
	if ref.SourceKind == "local_files" {
		return s.localObject(ctx, scope, ref, object)
	}
	if ref.Kind != "table" || strings.TrimSpace(ref.Name) == "" {
		return ObjectResponse{}, badRequest("data_browser_object_invalid", "The selected warehouse object is invalid.")
	}
	object.Name = shortObjectName(ref.Name)
	object.Kind = "table"
	object.ReferenceText = ref.Name
	object.Namespace = compactStrings([]string{ref.Database, ref.Schema})
	object.Capabilities = Capabilities{
		DescribeColumns: s.deps.ListColumns != nil,
		PreviewRows:     s.deps.RunQuery != nil,
		Query:           true,
	}
	if s.deps.ListColumns != nil {
		columns, columnErr := s.deps.ListColumns(ctx, ref.Connection, ref.Name, ref.Environment)
		if columnErr != nil {
			return ObjectResponse{}, internalError("data_browser_describe_failed", columnErr)
		}
		object.Columns = nonNilColumns(columns)
	}
	return ObjectResponse{Status: "ok", Object: object}, nil
}

func (s *Service) Preview(ctx context.Context, request PreviewRequest) (PreviewResponse, *apperror.Error) {
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > maxPreviewRows {
		limit = maxPreviewRows
	}
	ref, err := decodeRef(request.ObjectID)
	if err != nil || (ref.Kind != "table" && ref.Kind != "file") {
		return PreviewResponse{}, badRequest("data_browser_object_invalid", "The selected data object is invalid.")
	}
	connectionRef := ref
	connectionRef.Kind = "connection"
	connectionRef.Database = ""
	connectionRef.Schema = ""
	connectionRef.Name = ""
	connectionRef.Path = ""
	scope, apiErr := s.resolveScope(ctx, encodeRef(connectionRef), request.Environment)
	if apiErr != nil {
		return PreviewResponse{}, apiErr
	}
	if s.deps.RunQuery == nil {
		return PreviewResponse{}, badRequest("data_browser_preview_unavailable", "This data source does not support preview queries.")
	}

	queryConnection := ref.Connection
	relation := ""
	if ref.SourceKind == "local_files" {
		queryConnection = duckDBConnection(scope.connections)
		if queryConnection == "" {
			return PreviewResponse{}, badRequest("data_browser_preview_unavailable", "Add a DuckDB connection to preview local tabular files.")
		}
		path, pathErr := s.resolveLocalFile(ref.Path)
		if pathErr != nil {
			return PreviewResponse{}, badRequest("data_browser_file_invalid", pathErr.Error())
		}
		relation, pathErr = localFileRelation(path)
		if pathErr != nil {
			return PreviewResponse{}, badRequest("data_browser_file_unsupported", pathErr.Error())
		}
	} else {
		relation = quoteQualifiedIdentifier(ref.Name)
	}

	started := s.deps.Now()
	result, queryErr := s.deps.RunQuery(
		ctx,
		queryConnection,
		ref.Environment,
		fmt.Sprintf("select * from %s limit %d", relation, limit+1),
		limit+1,
	)
	if queryErr != nil {
		return PreviewResponse{}, internalError("data_browser_preview_failed", queryErr)
	}
	truncated := result.Truncated || len(result.Rows) > limit
	if len(result.Rows) > limit {
		result.Rows = result.Rows[:limit]
	}
	if result.Columns == nil {
		result.Columns = []string{}
	}
	if result.Rows == nil {
		result.Rows = []map[string]any{}
	}
	return PreviewResponse{
		Status:    "ok",
		ObjectID:  request.ObjectID,
		Columns:   result.Columns,
		Rows:      result.Rows,
		Truncated: truncated,
		ElapsedMS: max(0, s.deps.Now().Sub(started).Milliseconds()),
	}, nil
}

func (s *Service) listConnections(ctx context.Context, environment string) (string, []ConnectionConfig, int64, error) {
	if s.deps.ListConnections == nil {
		return "", nil, 0, fmt.Errorf("connection discovery is unavailable")
	}
	resolvedEnvironment, connections, revision, err := s.deps.ListConnections(ctx, strings.TrimSpace(environment))
	if err != nil {
		return "", nil, 0, err
	}
	sort.Slice(connections, func(i, j int) bool {
		return strings.ToLower(connections[i].Name) < strings.ToLower(connections[j].Name)
	})
	return resolvedEnvironment, connections, revision, nil
}

func (s *Service) resolveScope(ctx context.Context, connectionID, environment string) (resolvedScope, *apperror.Error) {
	ref, err := decodeRef(connectionID)
	if err != nil || ref.Kind != "connection" {
		return resolvedScope{}, badRequest("data_browser_connection_invalid", "The selected data source is invalid.")
	}
	requestedEnvironment := strings.TrimSpace(environment)
	if requestedEnvironment == "" {
		requestedEnvironment = ref.Environment
	}
	resolvedEnvironment, configs, stateRevision, listErr := s.listConnections(ctx, requestedEnvironment)
	if listErr != nil {
		return resolvedScope{}, internalError("data_browser_connections_failed", listErr)
	}
	revision := revisionToken(resolvedEnvironment, stateRevision, configs)
	if ref.Environment != resolvedEnvironment || ref.Revision != revision {
		return resolvedScope{}, &apperror.Error{
			Status:  http.StatusConflict,
			Code:    "data_browser_revision_stale",
			Message: "The data source changed. Refresh the Data Browser and try again.",
		}
	}
	if ref.SourceKind == "local_files" {
		return resolvedScope{ref: ref, connections: configs, revision: revision}, nil
	}
	for _, config := range configs {
		if config.Queryable && config.Name == ref.Connection && config.Type == ref.ConnectionType {
			return resolvedScope{ref: ref, connections: configs, revision: revision}, nil
		}
	}
	return resolvedScope{}, &apperror.Error{
		Status:  http.StatusNotFound,
		Code:    "data_browser_connection_not_found",
		Message: "The selected data source is no longer available in this environment.",
	}
}

func (s *Service) warehouseChildren(ctx context.Context, connection objectRef, parent objectRef, parentID string) ([]Node, bool, error) {
	if parent.Kind == "connection" {
		if s.deps.ListDatabases == nil {
			return nil, false, fmt.Errorf("database discovery is unavailable")
		}
		databases, err := s.deps.ListDatabases(ctx, connection.Connection, connection.Environment)
		if err != nil {
			return nil, false, err
		}
		databases = compactSortedStrings(databases)
		truncated := len(databases) > maxChildren
		if truncated {
			databases = databases[:maxChildren]
		}
		nodes := make([]Node, 0, len(databases))
		for _, database := range databases {
			ref := connection
			ref.Kind = "database"
			ref.Database = database
			nodes = append(nodes, Node{
				ID:            encodeRef(ref),
				ParentID:      parentID,
				NodeType:      "namespace",
				Label:         database,
				NamespaceKind: "database",
				HasChildren:   true,
			})
		}
		return nodes, truncated, nil
	}
	if parent.Kind != "database" && parent.Kind != "schema" {
		return []Node{}, false, nil
	}
	if s.deps.ListTables == nil {
		return nil, false, fmt.Errorf("table discovery is unavailable")
	}
	tables, err := s.deps.ListTables(ctx, connection.Connection, parent.Database, connection.Environment)
	if err != nil {
		return nil, false, err
	}
	if parent.Kind == "database" {
		schemas := make([]string, 0)
		seen := map[string]struct{}{}
		for _, table := range tables {
			schema := strings.TrimSpace(table.SchemaName)
			if schema == "" {
				continue
			}
			if _, exists := seen[schema]; exists {
				continue
			}
			seen[schema] = struct{}{}
			schemas = append(schemas, schema)
		}
		if len(schemas) > 0 {
			sort.Strings(schemas)
			truncated := len(schemas) > maxChildren
			if truncated {
				schemas = schemas[:maxChildren]
			}
			nodes := make([]Node, 0, len(schemas))
			for _, schema := range schemas {
				ref := connection
				ref.Kind = "schema"
				ref.Database = parent.Database
				ref.Schema = schema
				nodes = append(nodes, Node{
					ID:            encodeRef(ref),
					ParentID:      parentID,
					NodeType:      "namespace",
					Label:         schema,
					NamespaceKind: "schema",
					HasChildren:   true,
				})
			}
			return nodes, truncated, nil
		}
	}

	filtered := make([]Table, 0, len(tables))
	for _, table := range tables {
		if parent.Kind == "schema" && table.SchemaName != parent.Schema {
			continue
		}
		filtered = append(filtered, table)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
	})
	truncated := len(filtered) > maxChildren
	if truncated {
		filtered = filtered[:maxChildren]
	}
	nodes := make([]Node, 0, len(filtered))
	for _, table := range filtered {
		ref := connection
		ref.Kind = "table"
		ref.Database = table.DatabaseName
		if ref.Database == "" {
			ref.Database = parent.Database
		}
		ref.Schema = table.SchemaName
		ref.Name = table.Name
		label := strings.TrimSpace(table.ShortName)
		if label == "" {
			label = shortObjectName(table.Name)
		}
		nodes = append(nodes, Node{
			ID:            encodeRef(ref),
			ParentID:      parentID,
			NodeType:      "object",
			Label:         label,
			ObjectKind:    "table",
			HasChildren:   false,
			ReferenceText: table.Name,
		})
	}
	return nodes, truncated, nil
}

func (s *Service) localChildren(connection objectRef, parent objectRef, parentID string) ([]Node, bool, error) {
	relPath := ""
	if parent.Kind == "folder" {
		relPath = parent.Path
	} else if parent.Kind != "connection" {
		return []Node{}, false, nil
	}
	directory, err := s.resolveLocalDirectory(relPath)
	if err != nil {
		return nil, false, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, false, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	nodes := make([]Node, 0, min(len(entries), maxChildren))
	truncated := false
	for _, entry := range entries {
		if len(nodes) >= maxChildren {
			truncated = true
			break
		}
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(name, ".") {
			continue
		}
		childPath := filepath.ToSlash(filepath.Join(relPath, name))
		if entry.IsDir() {
			if _, excluded := excludedLocalDirectories[name]; excluded {
				continue
			}
			ref := connection
			ref.Kind = "folder"
			ref.Path = childPath
			nodes = append(nodes, Node{
				ID:            encodeRef(ref),
				ParentID:      parentID,
				NodeType:      "namespace",
				Label:         name,
				NamespaceKind: "folder",
				HasChildren:   true,
				ReferenceText: childPath,
			})
			continue
		}
		format, supported := supportedLocalExtensions[strings.ToLower(filepath.Ext(name))]
		if !supported {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		ref := connection
		ref.Kind = "file"
		ref.Path = childPath
		ref.Name = name
		nodes = append(nodes, Node{
			ID:            encodeRef(ref),
			ParentID:      parentID,
			NodeType:      "object",
			Label:         name,
			ObjectKind:    "file",
			HasChildren:   false,
			ReferenceText: childPath,
			Format:        format,
			SizeBytes:     info.Size(),
			ModifiedAt:    info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return nodes, truncated, nil
}

func (s *Service) localObject(ctx context.Context, scope resolvedScope, ref objectRef, object Object) (ObjectResponse, *apperror.Error) {
	path, err := s.resolveLocalFile(ref.Path)
	if err != nil {
		return ObjectResponse{}, badRequest("data_browser_file_invalid", err.Error())
	}
	info, err := os.Stat(path)
	if err != nil {
		return ObjectResponse{}, internalError("data_browser_file_stat_failed", err)
	}
	format, supported := supportedLocalExtensions[strings.ToLower(filepath.Ext(path))]
	if !supported {
		return ObjectResponse{}, badRequest("data_browser_file_unsupported", "This file format is not supported for tabular browsing.")
	}
	queryConnection := duckDBConnection(scope.connections)
	object.ConnectionName = localFilesConnectionName
	object.ConnectionType = "file"
	object.Namespace = localFileNamespace(ref.Path)
	object.Name = filepath.Base(ref.Path)
	object.Kind = "file"
	object.ReferenceText = filepath.ToSlash(ref.Path)
	object.Format = format
	object.SizeBytes = info.Size()
	object.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	object.Capabilities = Capabilities{
		DescribeColumns: queryConnection != "" && s.deps.RunQuery != nil,
		PreviewRows:     queryConnection != "" && s.deps.RunQuery != nil,
	}
	if object.Capabilities.DescribeColumns {
		relation, relationErr := localFileRelation(path)
		if relationErr == nil {
			result, queryErr := s.deps.RunQuery(ctx, queryConnection, ref.Environment, "describe select * from "+relation, 500)
			if queryErr != nil {
				object.Warning = "Column discovery failed: " + queryErr.Error()
			} else {
				object.Columns = describeColumns(result.Rows)
			}
		}
	}
	return ObjectResponse{Status: "ok", Object: object}, nil
}

func (s *Service) resolveLocalDirectory(relPath string) (string, error) {
	path, err := s.resolveLocalPath(relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("the selected local path is not a folder")
	}
	return path, nil
}

func (s *Service) resolveLocalFile(relPath string) (string, error) {
	path, err := s.resolveLocalPath(relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the selected local path is not a regular file")
	}
	return path, nil
}

func (s *Service) resolveLocalPath(relPath string) (string, error) {
	root := filepath.Clean(s.deps.WorkspaceRoot)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project-local file browsing is unavailable")
	}
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "." {
		clean = ""
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the selected path is outside the project")
	}
	if err := validateBrowsableLocalPath(clean); err != nil {
		return "", err
	}
	target := filepath.Join(root, clean)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the selected path is outside the project")
	}
	return realTarget, nil
}

func validateBrowsableLocalPath(path string) error {
	if path == "" {
		return nil
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("hidden project paths are not available in the Data Browser")
		}
		if _, excluded := excludedLocalDirectories[part]; excluded {
			return fmt.Errorf("this project path is not available in the Data Browser")
		}
	}
	return nil
}

func localFileRelation(path string) (string, error) {
	literal := "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "''") + "'"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return "read_csv_auto(" + literal + ", sample_size = 20480)", nil
	case ".parquet":
		return "read_parquet(" + literal + ")", nil
	case ".json", ".jsonl", ".ndjson":
		return "read_json_auto(" + literal + ")", nil
	default:
		return "", fmt.Errorf("this file format is not supported for tabular browsing")
	}
}

func describeColumns(rows []map[string]any) []model.SQLColumn {
	columns := make([]model.SQLColumn, 0, len(rows))
	for _, row := range rows {
		name := mapStringValue(row, "column_name", "name")
		if name == "" {
			continue
		}
		columns = append(columns, model.SQLColumn{
			Name: name,
			Type: mapStringValue(row, "column_type", "type"),
		})
	}
	return columns
}

func mapStringValue(row map[string]any, keys ...string) string {
	for key, value := range row {
		for _, candidate := range keys {
			if strings.EqualFold(key, candidate) {
				return strings.TrimSpace(fmt.Sprint(value))
			}
		}
	}
	return ""
}

func revisionToken(environment string, stateRevision int64, connections []ConnectionConfig) string {
	parts := []string{environment, strconv.FormatInt(stateRevision, 10)}
	for _, connection := range connections {
		parts = append(parts, connection.Name+"\x00"+connection.Type+"\x00"+strconv.FormatBool(connection.Queryable))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:8])
}

func encodeRef(ref objectRef) string {
	payload, _ := json.Marshal(ref)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRef(value string) (objectRef, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return objectRef{}, err
	}
	var ref objectRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return objectRef{}, err
	}
	if ref.Kind == "" || ref.SourceKind == "" || ref.Environment == "" || ref.Revision == "" {
		return objectRef{}, fmt.Errorf("incomplete data-browser identity")
	}
	return ref, nil
}

func sameScope(left, right objectRef) bool {
	return left.SourceKind == right.SourceKind &&
		left.Connection == right.Connection &&
		left.ConnectionType == right.ConnectionType &&
		left.Environment == right.Environment &&
		left.Revision == right.Revision
}

func hasDuckDBConnection(connections []ConnectionConfig) bool {
	return duckDBConnection(connections) != ""
}

func duckDBConnection(connections []ConnectionConfig) string {
	for _, connection := range connections {
		if connection.Queryable && strings.EqualFold(strings.TrimSpace(connection.Type), "duckdb") {
			return connection.Name
		}
	}
	return ""
}

func quoteQualifiedIdentifier(value string) string {
	parts := strings.Split(value, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.Trim(part, "`\"[]"))
		quoted = append(quoted, `"`+strings.ReplaceAll(trimmed, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, ".")
}

func shortObjectName(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) == 0 {
		return value
	}
	return strings.Trim(parts[len(parts)-1], "`\"[]")
}

func compactSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func localFileNamespace(path string) []string {
	directory := filepath.ToSlash(filepath.Dir(path))
	if directory == "." || directory == "" {
		return []string{}
	}
	return strings.Split(directory, "/")
}

func nonNilColumns(columns []model.SQLColumn) []model.SQLColumn {
	if columns == nil {
		return []model.SQLColumn{}
	}
	return columns
}

func badRequest(code, message string) *apperror.Error {
	return &apperror.Error{Status: http.StatusBadRequest, Code: code, Message: message}
}

func internalError(code string, err error) *apperror.Error {
	return &apperror.Error{Status: http.StatusBadRequest, Code: code, Message: err.Error()}
}
