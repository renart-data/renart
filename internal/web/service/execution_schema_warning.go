package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"

	webmodel "renart/internal/web/model"
)

// warnOnDeclaredSchemaDrift observes the relation produced by a successful
// main task and compares it with the asset's committed schema contract. The
// observation is deliberately best-effort: an unsupported connection or a
// transient metadata query must not turn a successful materialization into a
// failed run.
func (e *HybridBruinExecutor) warnOnDeclaredSchemaDrift(
	ctx context.Context,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	manager config.ConnectionAndDetailsGetter,
	output io.Writer,
) {
	if ctx == nil || ctx.Err() != nil || pl == nil || asset == nil || manager == nil || len(asset.Columns) == 0 || !assetProducesSchemaContract(asset) {
		return
	}
	if collector, _ := ctx.Value(executionWarningsKey{}).(*executionWarnings); collector == nil {
		// Every user-facing materialization enters through ExecutionService,
		// which installs the collector. Keep the lower-level executor usable by
		// render/parity callers without adding an unexpected metadata query.
		return
	}

	actual, err := e.observeMaterializedSchema(ctx, pl, asset, manager, output)
	if err != nil || len(actual) == 0 {
		return
	}
	actual = withoutRuntimeManagedColumns(asset, actual)
	connectionName, _ := targetConnectionNameForAsset(asset, pl)
	scope := SchemaEvidenceScope{Connection: connectionName, Relation: asset.Name}
	runtimeEvidence := SchemaEvidence{
		Source: webmodel.ColumnInferenceSource{
			ID: "runtime_output", Label: "Successful runtime output", Category: "observed",
			Description: "The schema reported by the relation produced by this execution.",
		},
		Stage: SchemaStageRuntime, Scope: scope, Completeness: SchemaComplete, Confidence: SchemaConfidenceHigh,
		AssetRevision: schemaAssetRevision(asset),
		ObservedAt:    timeNowUTC(), Columns: actual,
	}
	warning := formatDeclaredSchemaDriftWarning(asset, compareContractWithEvidence(asset.Columns, runtimeEvidence))
	if warning == "" {
		return
	}

	addExecutionWarning(ctx, warning)
	_, _ = fmt.Fprintf(output, "WARNING: %s\n", warning)
}

func (e *HybridBruinExecutor) observeMaterializedSchema(
	ctx context.Context,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	manager config.ConnectionAndDetailsGetter,
	output io.Writer,
) ([]WorkspaceColumn, error) {
	connectionName, err := targetConnectionNameForAsset(asset, pl)
	if err != nil {
		return nil, err
	}
	connection, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return nil, err
	}
	querier, ok := connection.(directSchemaQuerier)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support schema introspection", connectionName)
	}

	lease, err := e.acquireDuckDBConnections(
		ctx,
		manager,
		[]string{connectionName},
		directTaskLeaseOwner(ctx, pl, asset),
		output,
	)
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	connectionType := normalizeConnectionType(manager.GetConnectionType(connectionName))
	queryString := fmt.Sprintf(
		"SELECT * FROM %s WHERE 1 = 0",
		quoteRuntimeRelation(asset.Name, connectionType),
	)
	var result *query.QueryResult
	if connectionType == "duckdb" {
		result, err = selectDuckDBLogicalSchema(ctx, querier, queryString)
	} else {
		result, err = querier.SelectWithSchema(ctx, &query.Query{Query: queryString})
	}
	if err != nil || result == nil {
		return nil, err
	}

	columns := make([]WorkspaceColumn, 0, len(result.Columns))
	for index, name := range result.Columns {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		columnType := ""
		if index < len(result.ColumnTypes) {
			columnType = strings.TrimSpace(result.ColumnTypes[index])
		}
		columns = append(columns, WorkspaceColumn{Name: name, Type: columnType})
	}
	return columns, nil
}

func quoteRuntimeRelation(name, connectionType string) string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	quoteParts := func(open, close string) string {
		quoted := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(strings.Trim(part, "`\"[]"))
			part = strings.ReplaceAll(part, close, close+close)
			quoted = append(quoted, open+part+close)
		}
		return strings.Join(quoted, ".")
	}

	switch connectionType {
	case "google_cloud_platform":
		return "`" + strings.ReplaceAll(strings.Trim(strings.TrimSpace(name), "`"), "`", "\\`") + "`"
	case "databricks", "mysql", "doris", "vitess", "planetscale_mysql", "clickhouse", "starrocks":
		return quoteParts("`", "`")
	case "mssql", "synapse", "fabric":
		return quoteParts("[", "]")
	default:
		return quoteParts("\"", "\"")
	}
}

func withoutRuntimeManagedColumns(asset *pipeline.Asset, columns []WorkspaceColumn) []WorkspaceColumn {
	filtered := make([]WorkspaceColumn, 0, len(columns))
	for _, column := range columns {
		name := strings.ToLower(strings.TrimSpace(column.Name))
		if name == "_sling_loaded_at" && isSlingBackedAsset(asset) {
			continue
		}
		if asset.Materialization.IsSCD2() && (name == "_is_current" || name == "_valid_from" || name == "_valid_until") {
			continue
		}
		filtered = append(filtered, column)
	}
	return filtered
}

func isSlingBackedAsset(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	assetType := strings.ToLower(strings.TrimSpace(string(asset.Type)))
	return isAPIAsset(asset) || isLoadAsset(asset) || asset.Type == pipeline.AssetTypeIngestr || strings.HasSuffix(assetType, ".seed")
}

func formatDeclaredSchemaDriftWarning(asset *pipeline.Asset, drift webmodel.ColumnSchemaDrift) string {
	if asset == nil || drift.Added+drift.Removed+drift.TypeChanged == 0 {
		return ""
	}

	added := make([]string, 0, drift.Added)
	removed := make([]string, 0, drift.Removed)
	changed := make([]string, 0, drift.TypeChanged)
	for _, item := range drift.Items {
		switch item.Kind {
		case "added":
			added = append(added, formatRuntimeColumn(item.Column, item.InferredType))
		case "removed":
			removed = append(removed, formatRuntimeColumn(item.Column, item.CurrentType))
		case "type_changed":
			// A name-only declaration makes no type claim. Likewise, an empty
			// driver type is insufficient evidence of drift.
			if strings.TrimSpace(item.CurrentType) == "" || strings.TrimSpace(item.InferredType) == "" {
				continue
			}
			changed = append(changed, fmt.Sprintf("%s (%s -> %s)", item.Column, emptyColumnType(item.CurrentType), emptyColumnType(item.InferredType)))
		}
	}

	details := make([]string, 0, 3)
	if len(added) > 0 {
		details = append(details, "undeclared result columns: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		details = append(details, "missing result columns: "+strings.Join(removed, ", "))
	}
	if len(changed) > 0 {
		details = append(details, "type changes: "+strings.Join(changed, ", "))
	}
	if len(details) == 0 {
		return ""
	}
	return fmt.Sprintf("Result schema for %s does not match its declaration: %s.", asset.Name, strings.Join(details, "; "))
}

func formatRuntimeColumn(name, columnType string) string {
	if strings.TrimSpace(columnType) == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, columnType)
}

func emptyColumnType(columnType string) string {
	if strings.TrimSpace(columnType) == "" {
		return "unspecified"
	}
	return columnType
}
