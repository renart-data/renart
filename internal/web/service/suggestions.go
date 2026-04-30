package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/spf13/afero"
)

type SuggestionItem struct {
	Value  string `json:"value"`
	Kind   string `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type SuggestionAPIError struct {
	Status  int
	Code    string
	Message string
}

type IngestrSuggestionsResult struct {
	Status         string           `json:"status"`
	ConnectionType string           `json:"connection_type,omitempty"`
	Suggestions    []SuggestionItem `json:"suggestions"`
	Error          string           `json:"error,omitempty"`
}

type SQLPathSuggestionsResult struct {
	Status      string           `json:"status"`
	Suggestions []SuggestionItem `json:"suggestions"`
	Error       string           `json:"error,omitempty"`
}

type SuggestionsDependencies struct {
	WorkspaceRoot        string
	ConfigPath           string
	ResolveAssetByID     func(context.Context, string) (string, any, any, error)
	NewConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
}

type SuggestionsService struct {
	deps SuggestionsDependencies
}

func NewSuggestionsService(deps SuggestionsDependencies) *SuggestionsService {
	return &SuggestionsService{deps: deps}
}

func (s *SuggestionsService) Ingestr(ctx context.Context, connectionName, prefix, environment string) (IngestrSuggestionsResult, *SuggestionAPIError) {
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 500, Code: "connection_manager_failed", Message: err.Error()}
	}

	conn := manager.GetConnection(connectionName)
	if conn == nil {
		return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "connection_not_found", Message: fmt.Sprintf("connection '%s' not found", connectionName)}
	}

	result := IngestrSuggestionsResult{
		Status:         "ok",
		ConnectionType: strings.TrimSpace(manager.GetConnectionType(connectionName)),
		Suggestions:    []SuggestionItem{},
	}

	if s3Conn, ok := conn.(interface {
		ListBuckets(ctx context.Context) ([]string, error)
		ListEntries(ctx context.Context, bucketName, prefix string) ([]string, error)
	}); ok {
		items, itemErr := BuildS3SuggestionItems(ctx, s3Conn, prefix, manager.GetConnectionDetails(connectionName))
		if itemErr != nil {
			return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "ingestr_s3_suggestions_failed", Message: itemErr.Error()}
		}
		result.Suggestions = items
		return result, nil
	}

	if fetcherWithSchemas, ok := conn.(interface {
		GetTablesWithSchemas(ctx context.Context, databaseName string) (map[string][]string, error)
	}); ok {
		databaseName := DatabaseNameForConnectionDetails(manager.GetConnectionDetails(connectionName))
		if databaseName == "" {
			return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "database_name_missing", Message: fmt.Sprintf("connection '%s' has no database configured", connectionName)}
		}

		tables, tableErr := fetcherWithSchemas.GetTablesWithSchemas(ctx, databaseName)
		if tableErr != nil {
			return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "ingestr_table_suggestions_failed", Message: tableErr.Error()}
		}

		result.Suggestions = BuildSchemaTableSuggestionItems(tables, prefix)
		return result, nil
	}

	if fetcher, ok := conn.(interface {
		GetDatabases(ctx context.Context) ([]string, error)
		GetTables(ctx context.Context, databaseName string) ([]string, error)
	}); ok {
		suggestions, tableErr := BuildDuckDBSuggestionItems(ctx, fetcher, prefix)
		if tableErr != nil {
			return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "ingestr_table_suggestions_failed", Message: tableErr.Error()}
		}

		result.Suggestions = suggestions
		return result, nil
	}

	return IngestrSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "connection_type_not_supported", Message: fmt.Sprintf("connection '%s' does not support ingestr suggestions", connectionName)}
}

func (s *SuggestionsService) SQLPath(ctx context.Context, assetID, prefix, environment string) (SQLPathSuggestionsResult, *SuggestionAPIError) {
	if strings.TrimSpace(prefix) == "" {
		return SQLPathSuggestionsResult{Status: "ok", Suggestions: []SuggestionItem{}}, nil
	}

	if s.deps.ResolveAssetByID != nil {
		if _, _, _, err := s.deps.ResolveAssetByID(ctx, assetID); err != nil {
			return SQLPathSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "asset_not_found", Message: err.Error()}
		}
	}

	result := SQLPathSuggestionsResult{Status: "ok", Suggestions: []SuggestionItem{}}

	switch {
	case strings.HasPrefix(prefix, "s3://"):
		items, err := s.BuildSQLS3PathSuggestionItems(ctx, prefix, environment)
		if err != nil {
			return SQLPathSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "sql_path_suggestions_failed", Message: err.Error()}
		}
		result.Suggestions = items
	case strings.HasPrefix(prefix, "./"):
		items, err := BuildWorkspacePathSuggestionItems(s.deps.WorkspaceRoot, prefix)
		if err != nil {
			return SQLPathSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "sql_path_suggestions_failed", Message: err.Error()}
		}
		result.Suggestions = items
	case strings.HasPrefix(prefix, "/"):
		items, err := BuildAbsolutePathSuggestionItems(prefix)
		if err != nil {
			return SQLPathSuggestionsResult{}, &SuggestionAPIError{Status: 400, Code: "sql_path_suggestions_failed", Message: err.Error()}
		}
		result.Suggestions = items
	}

	return result, nil
}

func (s *SuggestionsService) BuildSQLS3PathSuggestionItems(ctx context.Context, prefix, environment string) ([]SuggestionItem, error) {
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return nil, err
	}

	cfg, err := config.LoadOrCreate(afero.NewOsFs(), s.deps.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if environment != "" {
		if err := cfg.SelectEnvironment(environment); err != nil {
			return nil, fmt.Errorf("failed to select environment '%s': %w", environment, err)
		}
	}

	if cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return []SuggestionItem{}, nil
	}

	lookupPrefix := strings.TrimPrefix(prefix, "s3://")
	items := make([]SuggestionItem, 0)
	seen := make(map[string]struct{})
	var firstErr error

	for _, connConfig := range cfg.SelectedEnvironment.Connections.S3 {
		conn := manager.GetConnection(connConfig.Name)
		if conn == nil {
			continue
		}

		listableConn, ok := conn.(interface {
			ListBuckets(ctx context.Context) ([]string, error)
			ListEntries(ctx context.Context, bucketName, prefix string) ([]string, error)
		})
		if !ok {
			continue
		}

		connectionDetails := manager.GetConnectionDetails(connConfig.Name)
		if connectionDetails == nil {
			connectionDetails = &connConfig
		}

		connItems, itemErr := BuildS3SuggestionItems(ctx, listableConn, lookupPrefix, connectionDetails)
		if itemErr != nil {
			if firstErr == nil {
				firstErr = itemErr
			}
			continue
		}

		for _, item := range connItems {
			item.Value = "s3://" + strings.TrimPrefix(item.Value, "s3://")
			if item.Detail != "" {
				item.Detail = fmt.Sprintf("%s (%s)", item.Detail, connConfig.Name)
			} else {
				item.Detail = connConfig.Name
			}

			key := strings.ToLower(item.Value) + "::" + item.Kind
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, item)
		}
	}

	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})

	return LimitSuggestionItems(items, 200), nil
}

func DatabaseNameForConnectionDetails(details any) string {
	switch connectionDetails := details.(type) {
	case *config.PostgresConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.MySQLConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.MsSQLConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.ClickHouseConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.AthenaConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.SnowflakeConnection:
		return strings.TrimSpace(connectionDetails.Database)
	case *config.DatabricksConnection:
		return strings.TrimSpace(connectionDetails.Catalog)
	case *config.VerticaConnection:
		return strings.TrimSpace(connectionDetails.Database)
	default:
		return ""
	}
}

func BuildSchemaTableSuggestionItems(tables map[string][]string, prefix string) []SuggestionItem {
	normalizedPrefix := strings.ToLower(strings.TrimSpace(prefix))
	items := make([]SuggestionItem, 0)

	schemas := make([]string, 0, len(tables))
	for schema := range tables {
		schemas = append(schemas, schema)
	}
	sort.Strings(schemas)

	for _, schema := range schemas {
		schemaTables := append([]string{}, tables[schema]...)
		sort.Strings(schemaTables)
		for _, table := range schemaTables {
			value := fmt.Sprintf("%s.%s", schema, table)
			if normalizedPrefix != "" && !strings.HasPrefix(strings.ToLower(value), normalizedPrefix) {
				continue
			}
			items = append(items, SuggestionItem{Value: value, Kind: "table", Detail: schema})
		}
	}

	return LimitSuggestionItems(items, 200)
}

func BuildDuckDBSuggestionItems(
	ctx context.Context,
	fetcher interface {
		GetDatabases(ctx context.Context) ([]string, error)
		GetTables(ctx context.Context, databaseName string) ([]string, error)
	},
	prefix string,
) ([]SuggestionItem, error) {
	schemas, err := fetcher.GetDatabases(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]SuggestionItem, 0)
	normalizedPrefix := strings.ToLower(strings.TrimSpace(prefix))
	sort.Strings(schemas)

	for _, schema := range schemas {
		tables, tableErr := fetcher.GetTables(ctx, schema)
		if tableErr != nil {
			return nil, tableErr
		}
		sort.Strings(tables)
		for _, table := range tables {
			fullName := fmt.Sprintf("%s.%s", schema, table)
			if normalizedPrefix != "" && !strings.HasPrefix(strings.ToLower(fullName), normalizedPrefix) && !strings.HasPrefix(strings.ToLower(table), normalizedPrefix) {
				continue
			}

			insertValue := fullName
			if strings.EqualFold(schema, "main") && normalizedPrefix != "" && !strings.Contains(prefix, ".") {
				insertValue = table
			}

			items = append(items, SuggestionItem{Value: insertValue, Kind: "table", Detail: schema})
		}
	}

	return LimitSuggestionItems(items, 200), nil
}

func BuildS3SuggestionItems(
	ctx context.Context,
	conn interface {
		ListBuckets(ctx context.Context) ([]string, error)
		ListEntries(ctx context.Context, bucketName, prefix string) ([]string, error)
	},
	prefix string,
	connectionDetails any,
) ([]SuggestionItem, error) {
	normalizedPrefix := strings.TrimSpace(prefix)
	configuredBucket, configuredPrefix := S3SuggestionContext(connectionDetails)
	if configuredBucket != "" {
		lookupPrefix := normalizedPrefix
		if lookupPrefix == "" {
			lookupPrefix = configuredPrefix
		}

		items, err := conn.ListEntries(ctx, configuredBucket, lookupPrefix)
		if err != nil {
			return nil, err
		}

		return BuildS3EntrySuggestionItems(items), nil
	}

	if normalizedPrefix == "" || !strings.Contains(normalizedPrefix, "/") {
		buckets, err := conn.ListBuckets(ctx)
		if err != nil {
			return nil, err
		}

		items := make([]SuggestionItem, 0, len(buckets))
		filter := strings.ToLower(normalizedPrefix)
		for _, bucket := range buckets {
			if filter != "" && !strings.HasPrefix(strings.ToLower(bucket), filter) {
				continue
			}
			items = append(items, SuggestionItem{Value: bucket + "/", Kind: "bucket", Detail: "S3 bucket"})
		}

		return LimitSuggestionItems(items, 200), nil
	}

	bucketName, keyPrefix, _ := strings.Cut(normalizedPrefix, "/")
	items, err := conn.ListEntries(ctx, bucketName, keyPrefix)
	if err != nil {
		return nil, err
	}

	return BuildS3EntrySuggestionItemsWithBucket(bucketName, items), nil
}

func S3SuggestionContext(connectionDetails any) (string, string) {
	s3Connection, ok := connectionDetails.(*config.S3Connection)
	if !ok || s3Connection == nil {
		return "", ""
	}

	bucketName := strings.TrimSpace(s3Connection.BucketName)
	pathPrefix := strings.TrimSpace(s3Connection.PathToFile)
	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	return bucketName, pathPrefix
}

func BuildS3EntrySuggestionItems(items []string) []SuggestionItem {
	suggestions := make([]SuggestionItem, 0, len(items))
	for _, item := range items {
		kind := "file"
		detail := "S3 object"
		if strings.HasSuffix(item, "/") {
			kind = "prefix"
			detail = "S3 prefix"
		}
		suggestions = append(suggestions, SuggestionItem{Value: item, Kind: kind, Detail: detail})
	}
	return LimitSuggestionItems(suggestions, 200)
}

func BuildWorkspacePathSuggestionItems(workspaceRoot, prefix string) ([]SuggestionItem, error) {
	relativePrefix := strings.TrimPrefix(prefix, "./")
	searchDir, typedDirectory, fragment := splitRelativePathLookup(workspaceRoot, relativePrefix, prefix)

	entries, err := afero.ReadDir(afero.NewOsFs(), searchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SuggestionItem{}, nil
		}
		return nil, err
	}

	items := make([]SuggestionItem, 0, len(entries))
	filter := strings.ToLower(fragment)
	for _, entry := range entries {
		name := entry.Name()
		if filter != "" && !strings.HasPrefix(strings.ToLower(name), filter) {
			continue
		}

		displayPath := "./" + name
		if typedDirectory != "" {
			displayPath = "./" + filepath.ToSlash(filepath.Join(typedDirectory, name))
		}

		kind := "file"
		detail := "Workspace file"
		if entry.IsDir() {
			displayPath += "/"
			kind = "directory"
			detail = "Workspace directory"
		}

		items = append(items, SuggestionItem{Value: displayPath, Kind: kind, Detail: detail})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})

	return LimitSuggestionItems(items, 200), nil
}

func BuildAbsolutePathSuggestionItems(prefix string) ([]SuggestionItem, error) {
	searchDir, displayDirectory, fragment := splitAbsolutePathLookup(prefix)

	entries, err := afero.ReadDir(afero.NewOsFs(), searchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SuggestionItem{}, nil
		}
		return nil, err
	}

	items := make([]SuggestionItem, 0, len(entries))
	filter := strings.ToLower(fragment)
	for _, entry := range entries {
		name := entry.Name()
		if filter != "" && !strings.HasPrefix(strings.ToLower(name), filter) {
			continue
		}

		displayPath := filepath.ToSlash(filepath.Join(displayDirectory, name))
		if displayDirectory == string(filepath.Separator) {
			displayPath = string(filepath.Separator) + name
		}

		kind := "file"
		detail := "Local file"
		if entry.IsDir() {
			displayPath += "/"
			kind = "directory"
			detail = "Local directory"
		}

		items = append(items, SuggestionItem{Value: displayPath, Kind: kind, Detail: detail})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})

	return LimitSuggestionItems(items, 200), nil
}

func BuildS3EntrySuggestionItemsWithBucket(bucketName string, items []string) []SuggestionItem {
	suggestions := make([]SuggestionItem, 0, len(items))
	for _, item := range items {
		kind := "file"
		detail := "S3 object"
		if strings.HasSuffix(item, "/") {
			kind = "prefix"
			detail = "S3 prefix"
		}
		suggestions = append(suggestions, SuggestionItem{Value: bucketName + "/" + item, Kind: kind, Detail: detail})
	}

	return LimitSuggestionItems(suggestions, 200)
}

func LimitSuggestionItems(items []SuggestionItem, max int) []SuggestionItem {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[:max]
}

func splitRelativePathLookup(workspaceRoot string, relativePrefix string, rawPrefix string) (string, string, string) {
	trimmed := relativePrefix
	if strings.HasSuffix(rawPrefix, "/") {
		typedDirectory := strings.TrimSuffix(trimmed, "/")
		if typedDirectory == "." {
			typedDirectory = ""
		}
		return filepath.Join(workspaceRoot, typedDirectory), typedDirectory, ""
	}

	fragment := filepath.Base(trimmed)
	typedDirectory := filepath.Dir(trimmed)
	if typedDirectory == "." {
		typedDirectory = ""
	}

	return filepath.Join(workspaceRoot, typedDirectory), typedDirectory, fragment
}

func splitAbsolutePathLookup(prefix string) (string, string, string) {
	if strings.HasSuffix(prefix, "/") {
		directory := filepath.Clean(prefix)
		if directory == "." {
			directory = string(filepath.Separator)
		}
		return directory, directory, ""
	}

	searchDir := filepath.Dir(prefix)
	displayDirectory := searchDir
	if displayDirectory == "." {
		displayDirectory = string(filepath.Separator)
	}

	return searchDir, displayDirectory, filepath.Base(prefix)
}
