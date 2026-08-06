package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"renart/internal/sqlformat"
	"renart/internal/web/secretstore"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/mssql"
	"github.com/bruin-data/bruin/pkg/oracle"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/postgres"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"
)

func (e *HybridBruinExecutor) ImportDatabase(ctx context.Context, req ImportDatabaseRequest) ([]byte, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeInspect)
	if e.newConnectionManager == nil || e.newPipelineBuilder == nil {
		return nil, fmt.Errorf("direct database import requires a connection manager and pipeline builder")
	}

	manager, err := e.newConnectionManager(ctx, req.Environment)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection manager: %w", err)
	}

	conn, err := resolveRuntimeConnection(manager, req.ConnectionName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve connection %q: %w", req.ConnectionName, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %q not found", req.ConnectionName)
	}

	pipelinePath := resolveDirectPath(e.workspaceRoot, resolveDirectPipelinePath(req.PipelinePath))
	foundPipeline, err := e.newPipelineBuilder().CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline from path: %w", err)
	}

	var summary *ansisql.DBDatabase
	schemaList := append([]string{}, req.Schemas...)
	if strings.TrimSpace(req.Schema) != "" {
		schemaList = []string{req.Schema}
	}

	if len(schemaList) > 0 {
		if schemaSummarizer, ok := conn.(interface {
			GetDatabaseSummaryForSchemas(context.Context, []string) (*ansisql.DBDatabase, error)
		}); ok {
			summary, err = schemaSummarizer.GetDatabaseSummaryForSchemas(ctx, schemaList)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve database summary for specified schemas: %w", err)
			}
		}
	}

	if summary == nil {
		summarizer, ok := conn.(interface {
			GetDatabaseSummary(context.Context) (*ansisql.DBDatabase, error)
		})
		if !ok {
			return nil, fmt.Errorf("connection %q does not support database summary", req.ConnectionName)
		}
		summary, err = summarizer.GetDatabaseSummary(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve database summary: %w", err)
		}
	}

	existingAssets := make(map[string]*pipeline.Asset, len(foundPipeline.Assets))
	for _, asset := range foundPipeline.Assets {
		if asset == nil {
			continue
		}
		existingAssets[strings.ToLower(asset.Name)] = asset
	}

	assetsPath := filepath.Join(pipelinePath, "assets")
	assetType, ok := sourceAssetTypeForConnectionType(manager.GetConnectionType(req.ConnectionName))
	if !ok {
		assetType = pipeline.AssetTypeEmpty
	}
	selectedTables := make(map[string]bool, len(req.Tables))
	for _, tableName := range req.Tables {
		trimmed := strings.ToLower(strings.TrimSpace(tableName))
		if trimmed != "" {
			selectedTables[trimmed] = true
		}
	}

	fs := afero.NewOsFs()
	warnings := make([]directImportWarning, 0)
	type importCandidate struct {
		asset     *pipeline.Asset
		assetName string
		fullName  string
		existing  *pipeline.Asset
	}
	candidates := make([]importCandidate, 0)

	for _, schemaObj := range summary.Schemas {
		if req.Schema != "" && !strings.EqualFold(schemaObj.Name, req.Schema) {
			continue
		}
		for _, table := range schemaObj.Tables {
			fullName := fmt.Sprintf("%s.%s", schemaObj.Name, table.Name)
			if len(selectedTables) > 0 && !matchesDirectImportedTable(selectedTables, summary.Name, schemaObj.Name, table.Name) {
				continue
			}

			createdAsset, warning := createDirectImportedAsset(ctx, assetsPath, schemaObj.Name, table.Name, assetType, conn, !req.DisableColumns, table)
			if warning != "" {
				warnings = append(warnings, directImportWarning{Table: fullName, Warning: warning})
			}
			if createdAsset == nil {
				continue
			}

			assetName := fmt.Sprintf("%s.%s", strings.ToLower(schemaObj.Name), strings.ToLower(table.Name))
			candidates = append(candidates, importCandidate{
				asset:     createdAsset,
				assetName: assetName,
				fullName:  fullName,
				existing:  existingAssets[assetName],
			})
		}
	}
	if len(selectedTables) > 0 && len(candidates) == 0 {
		return nil, fmt.Errorf("none of the selected tables were found in the database summary")
	}

	if req.RejectExisting {
		for _, candidate := range candidates {
			if candidate.existing != nil {
				return nil, fmt.Errorf("asset %q already exists", candidate.assetName)
			}
			exists, existsErr := afero.Exists(fs, candidate.asset.ExecutableFile.Path)
			if existsErr != nil {
				return nil, fmt.Errorf("check imported asset path: %w", existsErr)
			}
			if exists {
				return nil, fmt.Errorf("asset path %q already exists", directImportAssetPath(e.workspaceRoot, candidate.asset.ExecutableFile.Path))
			}
		}
	}

	assetPreviews := make([]directImportAsset, 0, len(candidates))
	for _, candidate := range candidates {
		columns := make([]SQLColumn, 0, len(candidate.asset.Columns))
		for _, column := range candidate.asset.Columns {
			columns = append(columns, SQLColumn{Name: column.Name, Type: column.Type})
		}
		assetPreviews = append(assetPreviews, directImportAsset{
			Name:    candidate.asset.Name,
			Path:    directImportAssetPath(e.workspaceRoot, candidate.asset.ExecutableFile.Path),
			Type:    string(candidate.asset.Type),
			Columns: columns,
		})
	}

	totalTables := 0
	mergedTableCount := 0
	if !req.PreviewOnly {
		for _, candidate := range candidates {
			if candidate.existing == nil {
				assetFolder := filepath.Dir(candidate.asset.ExecutableFile.Path)
				if err := fs.MkdirAll(assetFolder, 0o755); err != nil {
					return nil, fmt.Errorf("failed to create asset directory %s: %w", assetFolder, err)
				}
				if err := candidate.asset.Persist(fs); err != nil {
					return nil, err
				}
				existingAssets[candidate.assetName] = candidate.asset
				totalTables++
				continue
			}

			existingColumns := make(map[string]pipeline.Column, len(candidate.existing.Columns))
			for _, column := range candidate.existing.Columns {
				existingColumns[column.Name] = column
			}
			for _, column := range candidate.asset.Columns {
				if _, ok := existingColumns[column.Name]; !ok {
					candidate.existing.Columns = append(candidate.existing.Columns, column)
				}
			}
			if err := candidate.existing.Persist(fs); err != nil {
				return nil, err
			}
			mergedTableCount++
		}
	} else {
		for _, candidate := range candidates {
			if candidate.existing == nil {
				totalTables++
			} else {
				mergedTableCount++
			}
		}
	}

	response := directImportDatabaseResponse{
		Status:         "ok",
		Preview:        req.PreviewOnly,
		ImportedTables: totalTables,
		MergedTables:   mergedTableCount,
		Database:       summary.Name,
		PipelinePath:   pipelinePath,
		Assets:         assetPreviews,
		Warnings:       warnings,
	}
	return json.Marshal(response)
}

func directImportAssetPath(workspaceRoot, assetPath string) string {
	relative, err := filepath.Rel(workspaceRoot, assetPath)
	if err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(assetPath)
}

// formatImportedViewDefinition cleans up engine-extracted view SQL (e.g.
// pg_get_viewdef output with its odd indentation and trailing semicolon) so
// imported assets read like hand-written ones. Falls back to the trimmed
// original if the formatter rejects the statement.
func formatImportedViewDefinition(ctx context.Context, definition string, assetType pipeline.AssetType) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(definition), ";")
	formatted, err := sqlformat.Format(ctx, trimmed, sqlFormatDialectForAssetType(assetType))
	if err != nil || strings.TrimSpace(formatted) == "" {
		return trimmed
	}
	return strings.TrimSpace(formatted)
}

func createDirectImportedAsset(ctx context.Context, assetsPath, schemaName, tableName string, assetType pipeline.AssetType, conn interface{}, fillColumns bool, table *ansisql.DBTable) (*pipeline.Asset, string) {
	schemaFolder := filepath.Join(assetsPath, strings.ToLower(schemaName))
	isView := table.Type == ansisql.DBTableTypeView && table.ViewDefinition != ""

	actualAssetType := assetType
	if isView {
		actualAssetType = convertDirectSourceTypeToQueryType(assetType)
	}

	var fileName, filePath string
	var materializationType pipeline.MaterializationType
	var content string

	if isView {
		fileName = strings.ToLower(tableName) + ".sql"
		filePath = filepath.Join(schemaFolder, fileName)
		content = formatImportedViewDefinition(ctx, table.ViewDefinition, actualAssetType)
		materializationType = pipeline.MaterializationTypeView
	} else {
		fileName = strings.ToLower(tableName) + ".asset.yml"
		filePath = filepath.Join(schemaFolder, fileName)
	}

	assetName := fmt.Sprintf("%s.%s", strings.ToLower(schemaName), strings.ToLower(tableName))
	asset := &pipeline.Asset{
		Name: assetName,
		Type: actualAssetType,
		ExecutableFile: pipeline.ExecutableFile{
			Name:    fileName,
			Path:    filePath,
			Content: content,
		},
		Description: buildDirectEnhancedDescription(table, schemaName, tableName),
	}

	if isView {
		asset.Materialization = pipeline.Materialization{Type: materializationType}
	}

	if !fillColumns {
		return asset, ""
	}

	if len(table.Columns) > 0 {
		columns := make([]pipeline.Column, 0, len(table.Columns))
		for _, col := range table.Columns {
			columns = append(columns, pipeline.Column{
				Name:        col.Name,
				Type:        col.Type,
				Description: col.Description,
				Checks:      []pipeline.ColumnCheck{},
				Upstreams:   []*pipeline.UpstreamColumn{},
			})
		}
		asset.Columns = columns
		return asset, ""
	}

	if err := fillDirectAssetColumnsFromDB(ctx, asset, conn, schemaName, tableName); err != nil {
		return asset, fmt.Sprintf("Could not fill columns: %v", err)
	}

	return asset, ""
}

func fillDirectAssetColumnsFromDB(ctx context.Context, asset *pipeline.Asset, conn interface{}, schemaName, tableName string) error {
	querier, ok := conn.(interface {
		SelectWithSchema(context.Context, *query.Query) (*query.QueryResult, error)
	})
	if !ok {
		return fmt.Errorf("connection does not support schema introspection")
	}

	fullTableName := schemaName + "." + tableName
	if _, ok := conn.(*postgres.Client); ok {
		fullTableName = postgres.QuoteIdentifier(fullTableName)
	}
	if _, ok := conn.(*mssql.DB); ok {
		fullTableName = mssql.QuoteIdentifier(fullTableName)
	}

	queryStr := fmt.Sprintf("SELECT * FROM %s WHERE 1=0 LIMIT 0", fullTableName)
	if _, ok := conn.(*mssql.DB); ok {
		queryStr = "SELECT TOP 0 * FROM " + fullTableName
	} else if _, ok := conn.(*oracle.Client); ok {
		queryStr = "SELECT * FROM " + fullTableName + " WHERE 1=0"
	}

	result, err := querier.SelectWithSchema(ctx, &query.Query{Query: queryStr})
	if err != nil {
		return err
	}
	if len(result.Columns) == 0 {
		return fmt.Errorf("no columns found for table %s.%s", schemaName, tableName)
	}

	descriptions := fetchDirectColumnDescriptions(ctx, conn, schemaName, tableName)
	skipColumns := map[string]bool{"_IS_CURRENT": true, "_VALID_UNTIL": true, "_VALID_FROM": true}
	columns := make([]pipeline.Column, 0, len(result.Columns))
	for i, colName := range result.Columns {
		if skipColumns[colName] {
			continue
		}
		colType := ""
		if i < len(result.ColumnTypes) {
			colType = result.ColumnTypes[i]
		}
		columns = append(columns, pipeline.Column{
			Name:        colName,
			Type:        colType,
			Description: descriptions[colName],
			Checks:      []pipeline.ColumnCheck{},
			Upstreams:   []*pipeline.UpstreamColumn{},
		})
	}
	asset.Columns = columns
	return nil
}

func fetchDirectColumnDescriptions(ctx context.Context, conn interface{}, schemaName, tableName string) map[string]string {
	descriptions := make(map[string]string)
	selector, ok := conn.(interface {
		Select(context.Context, *query.Query) ([][]interface{}, error)
	})
	if !ok {
		return descriptions
	}

	var queryStr string
	switch conn.(type) {
	case *postgres.Client:
		queryStr = fmt.Sprintf(`
SELECT a.attname as column_name, pg_catalog.col_description(a.attrelid, a.attnum) as column_description
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON a.attrelid = c.oid
JOIN pg_catalog.pg_namespace n ON c.relnamespace = n.oid
WHERE n.nspname = '%s' AND c.relname = '%s' AND a.attnum > 0 AND NOT a.attisdropped
AND pg_catalog.col_description(a.attrelid, a.attnum) IS NOT NULL
`, schemaName, tableName)
	case *mssql.DB:
		queryStr = fmt.Sprintf(`
SELECT c.name AS column_name, CAST(ep.value AS NVARCHAR(MAX)) AS column_description
FROM sys.columns c
JOIN sys.tables t ON c.object_id = t.object_id
JOIN sys.schemas s ON t.schema_id = s.schema_id
LEFT JOIN sys.extended_properties ep ON c.object_id = ep.major_id AND c.column_id = ep.minor_id AND ep.name = 'MS_Description'
WHERE s.name = '%s' AND t.name = '%s' AND ep.value IS NOT NULL
`, schemaName, tableName)
	default:
		return descriptions
	}

	rows, err := selector.Select(ctx, &query.Query{Query: queryStr})
	if err != nil {
		return descriptions
	}
	for _, row := range rows {
		if len(row) >= 2 {
			colName, ok1 := row[0].(string)
			desc, ok2 := row[1].(string)
			if ok1 && ok2 {
				descriptions[colName] = desc
			}
		}
	}
	return descriptions
}

func buildDirectEnhancedDescription(table *ansisql.DBTable, schemaName, tableName string) string {
	var parts []string
	if table.Description != "" {
		parts = append(parts, table.Description, "")
	}
	parts = append(parts, "Imported "+directTableTypeDescription(table.Type)+": "+schemaName+"."+tableName)
	parts = append(parts, "Extracted at: "+time.Now().UTC().Format(time.RFC3339))
	if table.CreatedAt != nil {
		parts = append(parts, "Created at: "+table.CreatedAt.UTC().Format(time.RFC3339))
	}
	if table.LastModified != nil {
		parts = append(parts, "Last modified: "+table.LastModified.UTC().Format(time.RFC3339))
	}
	if table.RowCount != nil {
		parts = append(parts, "Row count: "+formatDirectNumber(*table.RowCount))
	}
	if table.SizeBytes != nil {
		parts = append(parts, "Size: "+formatDirectBytes(*table.SizeBytes))
	}
	if table.Owner != "" {
		parts = append(parts, "Owner: "+table.Owner)
	}
	return strings.Join(parts, "\n")
}

func directTableTypeDescription(tableType ansisql.DBTableType) string {
	if tableType == ansisql.DBTableTypeView {
		return "view"
	}
	return "table"
}

func formatDirectNumber(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	s := strconv.FormatInt(n, 10)
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(c)
	}
	return result.String()
}

func formatDirectBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

const (
	fillStatusUpdated = "updated"
	fillStatusSkipped = "skipped"
	fillStatusFailed  = "failed"
)

func matchesDirectImportedTable(selectedTables map[string]bool, databaseName, schemaName, tableName string) bool {
	if len(selectedTables) == 0 {
		return true
	}

	candidates := []string{
		strings.ToLower(strings.TrimSpace(fmt.Sprintf("%s.%s", schemaName, tableName))),
		strings.ToLower(strings.TrimSpace(fmt.Sprintf("%s.%s.%s", databaseName, schemaName, tableName))),
		strings.ToLower(strings.TrimSpace(tableName)),
	}

	for _, candidate := range candidates {
		if candidate != "" && selectedTables[candidate] {
			return true
		}
	}

	return false
}
