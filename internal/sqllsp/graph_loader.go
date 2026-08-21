package sqllsp

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	bruinBlockPattern = regexp.MustCompile(`(?is)/\*\s*@bruin\s*(.*?)\s*@bruin\s*\*/`)
	dbtRefPattern     = regexp.MustCompile(`(?is)\{\{\s*ref\s*\(\s*['"]([^'"]+)['"]`)
	dbtSourcePattern  = regexp.MustCompile(`(?is)\{\{\s*source\s*\(\s*['"]([^'"]+)['"]\s*,\s*['"]([^'"]+)['"]`)
)

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func LoadGraphFromDir(ctx context.Context, root string) (CanonicalGraph, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return CanonicalGraph{}, err
	}
	graph := CanonicalGraph{Version: 1, WorkspaceURI: FileURI(absRoot)}
	if fileExists(filepath.Join(absRoot, "dbt_project.yml")) || fileExists(filepath.Join(absRoot, "dbt_project.yaml")) {
		if err := loadDBTGraph(ctx, absRoot, &graph); err != nil {
			return graph, err
		}
		graph = normalizeGraph(graph)
		return InferSchemaSnapshot(ctx, graph, nil), nil
	}
	if err := loadBruinGraph(ctx, absRoot, &graph, "bruin"); err != nil {
		return graph, err
	}
	graph = normalizeGraph(graph)
	return InferSchemaSnapshot(ctx, graph, nil), nil
}

func GraphFromRenartAssets(workspaceURI URI, assets []AssetNode, columns map[string][]ColumnInfo) CanonicalGraph {
	graph := CanonicalGraph{Version: 1, WorkspaceURI: workspaceURI}
	for _, asset := range assets {
		relationID := relationID("renart", asset.Name)
		asset.OutputRelations = appendMissing(asset.OutputRelations, relationID)
		asset.Provenance = append(asset.Provenance, Provenance{Provider: "renart", ProviderID: asset.ID, URI: asset.URI, Confidence: "high"})
		graph.Assets = append(graph.Assets, asset)
		graph.Relations = append(graph.Relations, RelationNode{ID: relationID, Name: asset.Name, AssetID: asset.ID, Provenance: asset.Provenance})
		cols := columns[asset.ID]
		layer := SchemaLayer{RelationID: relationID, SourceKind: "unknown", Completeness: "unknown", Confidence: "low", Columns: []ColumnInfo{}, Provenance: asset.Provenance}
		if len(cols) > 0 {
			layer = SchemaLayer{RelationID: relationID, SourceKind: "declared", Completeness: "complete", Confidence: "high", Columns: cols, Provenance: asset.Provenance}
		}
		graph.Schemas = append(graph.Schemas, layer)
	}
	return normalizeGraph(graph)
}

func loadBruinGraph(ctx context.Context, root string, graph *CanonicalGraph, provider string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "target" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".sql" && ext != ".yml" && ext != ".yaml" && ext != ".csv" {
			return nil
		}
		if ext == ".yml" || ext == ".yaml" {
			return loadBruinYAMLAsset(root, path, graph, provider)
		}
		if ext == ".csv" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		meta := parseBruinSQLMeta(content)
		if len(meta) == 0 && !strings.Contains(filepath.ToSlash(path), "/assets/") {
			return nil
		}
		name := bruinAssetName(root, path, meta)
		dialect := DialectFromAssetType(fmt.Sprint(meta["type"]))
		addSQLAsset(graph, provider, name, path, string(content), dialect, meta)
		return nil
	})
}

func loadBruinYAMLAsset(root, path string, graph *CanonicalGraph, provider string) error {
	if !strings.HasSuffix(path, ".asset.yml") && !strings.HasSuffix(path, ".asset.yaml") {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var meta map[string]any
	if err := yaml.Unmarshal(content, &meta); err != nil {
		return nil
	}
	name := strings.TrimSpace(fmt.Sprint(meta["name"]))
	if name == "" {
		name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".asset.yml"), ".asset.yaml")
	}
	assetID := assetID(provider, name)
	relationID := relationID(provider, name)
	provenance := []Provenance{{Provider: provider, ProviderID: assetID, URI: FileURI(path), Confidence: "medium"}}
	graph.Assets = append(graph.Assets, AssetNode{ID: assetID, Name: name, Kind: "seed", Dialect: DialectFromAssetType(fmt.Sprint(meta["type"])), Connection: strings.TrimSpace(fmt.Sprint(meta["connection"])), URI: FileURI(path), OutputRelations: []string{relationID}, Provenance: provenance})
	graph.Relations = append(graph.Relations, RelationNode{ID: relationID, Name: name, AssetID: assetID, Provenance: provenance})
	columns := columnsFromYAML(meta["columns"])
	layer := SchemaLayer{RelationID: relationID, SourceKind: "unknown", Completeness: "unknown", Confidence: "low", Columns: []ColumnInfo{}, Provenance: provenance}
	if len(columns) > 0 {
		layer = SchemaLayer{RelationID: relationID, SourceKind: "declared", Completeness: "complete", Confidence: "high", Columns: columns, Provenance: provenance}
	}
	graph.Schemas = append(graph.Schemas, layer)
	return nil
}

func loadDBTGraph(ctx context.Context, root string, graph *CanonicalGraph) error {
	declaredColumns := map[string][]ColumnInfo{}
	sources := map[string][]ColumnInfo{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "target" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yml" || ext == ".yaml" {
			loadDBTSchemaYAML(path, declaredColumns, sources)
		}
		return nil
	}); err != nil {
		return err
	}
	for sourceName, columns := range sources {
		relationID := relationID("dbt", sourceName)
		provenance := []Provenance{{Provider: "dbt", ProviderID: "source:" + sourceName, Confidence: "medium"}}
		graph.Relations = append(graph.Relations, RelationNode{ID: relationID, Name: sourceName, Provenance: provenance})
		graph.Schemas = append(graph.Schemas, SchemaLayer{RelationID: relationID, SourceKind: "declared", Completeness: "complete", Confidence: "high", Columns: columns, Provenance: provenance})
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "target" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".sql" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		addSQLAsset(graph, "dbt", name, path, string(content), "generic", map[string]any{"declared_columns": declaredColumns[name]})
		return nil
	})
}

func addSQLAsset(graph *CanonicalGraph, provider, name, filePath, sql, dialect string, meta map[string]any) {
	assetID := assetID(provider, name)
	relationID := relationID(provider, name)
	provenance := []Provenance{{Provider: provider, ProviderID: assetID, URI: FileURI(filePath), Confidence: "high"}}
	inputs := logicalInputs(provider, assetID, sql, meta)
	graph.Assets = append(graph.Assets, AssetNode{
		ID:              assetID,
		Name:            name,
		Kind:            "sql_model",
		Dialect:         dialect,
		Connection:      strings.TrimSpace(fmt.Sprint(meta["connection"])),
		URI:             FileURI(filePath),
		OutputRelations: []string{relationID},
		InputRelations:  inputs,
		Provenance:      provenance,
	})
	renderEngine := NewEngine(*graph)
	renderDoc := TextDocumentItem{URI: FileURI(filePath), LanguageID: "sql", Text: sql}
	rendered := renderTemplateSQL(renderDoc, renderEngine)
	rendered.ID = assetID + ":rendered"
	rendered.AssetID = assetID
	rendered.Dialect = dialect
	rendered.Provenance = provenance
	graph.Renderings = append(graph.Renderings, rendered)
	graph.Relations = append(graph.Relations, RelationNode{ID: relationID, Name: name, AssetID: assetID, Provenance: provenance})
	columns := columnsFromYAML(meta["columns"])
	if declared, ok := meta["declared_columns"].([]ColumnInfo); ok && len(declared) > 0 {
		columns = mergeColumns(declared, columns)
	}
	layer := SchemaLayer{RelationID: relationID, SourceKind: "unknown", Completeness: "unknown", Confidence: "low", Columns: []ColumnInfo{}, Provenance: provenance}
	if len(columns) > 0 {
		layer = SchemaLayer{RelationID: relationID, SourceKind: "declared", Completeness: "complete", Confidence: "high", Columns: columns, Provenance: provenance}
	}
	graph.Schemas = append(graph.Schemas, layer)
	for _, ref := range referencesFromSQL(provider, assetID, sql) {
		graph.References = append(graph.References, ref)
	}
	for _, input := range inputs {
		graph.Lineage = append(graph.Lineage, LineageEdge{From: input, To: relationID, Kind: "depends"})
	}
}

func parseBruinSQLMeta(content []byte) map[string]any {
	match := bruinBlockPattern.FindSubmatch(content)
	if len(match) < 2 {
		return nil
	}
	var meta map[string]any
	if err := yaml.Unmarshal(bytes.TrimSpace(match[1]), &meta); err != nil {
		return nil
	}
	return meta
}

func bruinAssetName(root, path string, meta map[string]any) string {
	if name := strings.TrimSpace(fmt.Sprint(meta["name"])); name != "" && name != "<nil>" {
		return name
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		if part == "assets" || part == "tasks" {
			parts = parts[i+1:]
			break
		}
	}
	if len(parts) > 0 {
		parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(parts[len(parts)-1]))
	}
	return strings.Join(parts, ".")
}

func logicalInputs(provider, assetID, sql string, meta map[string]any) []string {
	var inputs []string
	if depends, ok := meta["depends"]; ok {
		for _, dep := range stringSlice(depends) {
			inputs = appendMissing(inputs, relationID(provider, dep))
		}
	}
	for _, ref := range referencesFromSQL(provider, assetID, sql) {
		if ref.TargetRelation != "" {
			inputs = appendMissing(inputs, ref.TargetRelation)
		}
	}
	for _, relation := range analyzeSQL(stripTemplates(sql)).relations {
		inputs = appendMissing(inputs, relationID(provider, relation.name))
	}
	return inputs
}

func referencesFromSQL(provider, assetID, sql string) []LogicalReference {
	var refs []LogicalReference
	for _, match := range dbtRefPattern.FindAllStringSubmatchIndex(sql, -1) {
		target := sql[match[2]:match[3]]
		refs = append(refs, LogicalReference{
			ID:             fmt.Sprintf("%s:ref:%d", assetID, match[0]),
			Kind:           "ModelRef",
			SourceAssetID:  assetID,
			TargetAssetID:  assetIDForProvider(provider, target),
			TargetRelation: relationID(provider, target),
			TemplateRange:  ptr(RangeFromOffsets(sql, match[0], match[1])),
			OriginalSyntax: sql[match[0]:match[1]],
			Provenance:     []Provenance{{Provider: provider, ProviderID: assetID, Confidence: "medium"}},
		})
	}
	for _, match := range dbtSourcePattern.FindAllStringSubmatchIndex(sql, -1) {
		source := sql[match[2]:match[3]] + "." + sql[match[4]:match[5]]
		refs = append(refs, LogicalReference{
			ID:             fmt.Sprintf("%s:source:%d", assetID, match[0]),
			Kind:           "SourceRef",
			SourceAssetID:  assetID,
			TargetRelation: relationID(provider, source),
			TemplateRange:  ptr(RangeFromOffsets(sql, match[0], match[1])),
			OriginalSyntax: sql[match[0]:match[1]],
			Provenance:     []Provenance{{Provider: provider, ProviderID: assetID, Confidence: "medium"}},
		})
	}
	return refs
}

func loadDBTSchemaYAML(path string, models map[string][]ColumnInfo, sources map[string][]ColumnInfo) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return
	}
	for _, model := range mapSlice(doc["models"]) {
		name := strings.TrimSpace(fmt.Sprint(model["name"]))
		if name != "" {
			models[name] = columnsFromYAML(model["columns"])
		}
	}
	for _, source := range mapSlice(doc["sources"]) {
		sourceName := strings.TrimSpace(fmt.Sprint(source["name"]))
		for _, table := range mapSlice(source["tables"]) {
			tableName := strings.TrimSpace(fmt.Sprint(table["name"]))
			if sourceName != "" && tableName != "" {
				sources[sourceName+"."+tableName] = columnsFromYAML(table["columns"])
			}
		}
	}
}

func columnsFromYAML(value any) []ColumnInfo {
	var columns []ColumnInfo
	for _, item := range mapSlice(value) {
		name := strings.TrimSpace(fmt.Sprint(item["name"]))
		if name == "" {
			continue
		}
		column := ColumnInfo{
			Name:       name,
			Type:       strings.TrimSpace(fmt.Sprint(item["type"])),
			PrimaryKey: boolFromYAML(item["primary_key"]),
		}
		if nullable, ok := item["nullable"].(bool); ok {
			column.Nullable = &nullable
		}
		if foreignKey, ok := item["foreign_key"].(map[string]any); ok {
			table := strings.TrimSpace(fmt.Sprint(foreignKey["table"]))
			targetColumn := strings.TrimSpace(fmt.Sprint(foreignKey["column"]))
			if table != "" && table != "<nil>" && targetColumn != "" && targetColumn != "<nil>" {
				column.ForeignKey = &ColumnReference{Table: table, Column: targetColumn}
			}
		}
		columns = append(columns, column)
	}
	return columns
}

func boolFromYAML(value any) bool {
	result, _ := value.(bool)
	return result
}

func stripTemplates(sql string) string {
	// Delegate to the shared renderer, which matches the whole `{{ … }}` call
	// (including the closing `) }}`). The previous regexes only consumed up to
	// the ref/source name's closing quote, leaving a dangling `) }}` fragment
	// that leaked into relation/column extraction.
	return sqlForScopeAnalysis(sql)
}

func normalizeGraph(graph CanonicalGraph) CanonicalGraph {
	sort.Slice(graph.Assets, func(i, j int) bool { return graph.Assets[i].ID < graph.Assets[j].ID })
	sort.Slice(graph.Relations, func(i, j int) bool { return graph.Relations[i].ID < graph.Relations[j].ID })
	sort.Slice(graph.Schemas, func(i, j int) bool { return graph.Schemas[i].RelationID < graph.Schemas[j].RelationID })
	return graph
}

// DialectFromAssetType maps a Bruin/dbt asset type (e.g. "duckdb.sql") to the
// SQL dialect name used by the analyzer and validator.
func DialectFromAssetType(assetType string) string {
	switch strings.ToLower(strings.TrimSpace(assetType)) {
	case "duckdb.sql", "duckdb.sensor.query":
		return "duckdb"
	case "bq.sql", "bq.sensor.query":
		return "bigquery"
	case "sf.sql", "sf.sensor.query":
		return "snowflake"
	case "pg.sql", "pg.sensor.query", "rs.sql", "rs.sensor.query":
		return "postgres"
	case "athena.sql", "athena.sensor.query":
		return "athena"
	case "databricks.sql", "databricks.sensor.query":
		return "databricks"
	case "ms.sql", "ms.sensor.query", "synapse.sql", "synapse.sensor.query":
		return "tsql"
	case "ch.sql", "clickhouse.sql", "clickhouse.sensor.query":
		return "clickhouse"
	case "trino.sql", "trino.sensor.query":
		return "trino"
	case "my.sql", "my.sensor.query":
		return "mysql"
	case "starrocks.sql", "starrocks.sensor.query":
		return "mysql"
	case "oracle.sql":
		return "oracle"
	default:
		return "generic"
	}
}

func relationID(provider, name string) string {
	return "relation:" + provider + ":" + strings.ToLower(strings.TrimSpace(name))
}

func assetID(provider, name string) string {
	return assetIDForProvider(provider, name)
}

func assetIDForProvider(provider, name string) string {
	return "asset:" + provider + ":" + strings.ToLower(strings.TrimSpace(name))
}

func mapSlice(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			switch v := item.(type) {
			case string:
				result = append(result, v)
			case map[string]any:
				if asset := strings.TrimSpace(fmt.Sprint(v["asset"])); asset != "" {
					result = append(result, asset)
				}
			}
		}
		return result
	case []string:
		return typed
	case string:
		return []string{typed}
	default:
		return nil
	}
}

func mergeColumns(left, right []ColumnInfo) []ColumnInfo {
	seen := map[string]bool{}
	result := make([]ColumnInfo, 0, len(left)+len(right))
	// Avoid append(left, right...): when left has spare capacity that append
	// writes into left's backing array, mutating a caller-owned slice.
	add := func(columns []ColumnInfo) {
		for _, column := range columns {
			if strings.TrimSpace(column.Name) == "" || seen[strings.ToLower(column.Name)] {
				continue
			}
			seen[strings.ToLower(column.Name)] = true
			result = append(result, column)
		}
	}
	add(left)
	add(right)
	return result
}

func appendMissing(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
