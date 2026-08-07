package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
)

const materializationTargetInspectionTimeout = 8 * time.Second

type TargetExistence string

const (
	TargetExistencePresent TargetExistence = "present"
	TargetExistenceAbsent  TargetExistence = "absent"
	TargetExistenceUnknown TargetExistence = "unknown"
)

type TargetRelationKind string

const (
	TargetRelationKindTable   TargetRelationKind = "table"
	TargetRelationKindView    TargetRelationKind = "view"
	TargetRelationKindOther   TargetRelationKind = "other"
	TargetRelationKindUnknown TargetRelationKind = "unknown"
)

// TargetState is the provider-neutral result of a fresh materialization-target
// probe. Unknown is intentionally distinct from absent: a failed, unsupported,
// or ambiguous lookup must never authorize bootstrap or destructive DDL.
type TargetState struct {
	Existence     TargetExistence
	Kind          TargetRelationKind
	QualifiedName string
	Connection    string
	Message       string
}

type materializationTargetLookup struct {
	catalog string
	schema  string
	table   string
}

type materializationTargetDecision struct {
	State              TargetState
	UseFullRefresh     bool
	DropOppositeKind   bool
	LifecycleSupported bool
}

type materializationTargetQueryRunner interface {
	RunQueryWithoutResult(context.Context, *query.Query) error
}

func (e *HybridBruinExecutor) prepareMaterializationTarget(
	ctx context.Context,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	manager config.ConnectionAndDetailsGetter,
	fullRefreshSeq *bruinexecutor.Sequential,
	output io.Writer,
) (materializationTargetDecision, error) {
	decision := materializationTargetDecision{}
	if !materializationTargetLifecycleApplies(asset) || manager == nil {
		return decision, nil
	}

	connectionName, err := targetConnectionNameForAsset(asset, pl)
	if err != nil {
		return decision, fmt.Errorf("resolve materialization target connection for %q: %w", asset.Name, err)
	}
	connectionType := normalizeConnectionType(manager.GetConnectionType(connectionName))
	if !materializationTargetAdapterSupported(connectionType) {
		state := TargetState{
			Existence: TargetExistenceUnknown, Kind: TargetRelationKindUnknown,
			QualifiedName: asset.Name, Connection: connectionName,
			Message: fmt.Sprintf("target lifecycle inspection is not implemented for %s connections", displayConnectionType(connectionType)),
		}
		decision.State = state
		emitMaterializationTargetWarning(ctx, output, asset, state.Message)
		return decision, nil
	}
	decision.LifecycleSupported = true

	inspectionCtx, cancel := context.WithTimeout(ctx, materializationTargetInspectionTimeout)
	defer cancel()
	state := inspectMaterializationTarget(inspectionCtx, manager, connectionName, connectionType, asset.Name)
	decision.State = state
	if state.Existence == TargetExistenceUnknown {
		emitMaterializationTargetWarning(ctx, output, asset, state.Message)
		return decision, nil
	}

	requestedFullRefresh, _ := ctx.Value(pipeline.RunConfigFullRefresh).(bool)
	effectiveFullRefresh := requestedFullRefresh && !assetRefreshRestricted(asset)
	expectedKind := targetRelationKindForMaterialization(asset.Materialization.Type)

	if state.Existence == TargetExistenceAbsent {
		if materializationRequiresExistingTarget(asset.Materialization) && !effectiveFullRefresh {
			if assetRefreshRestricted(asset) {
				return decision, fmt.Errorf(
					"materialization target %q does not exist, but %q uses %s and refresh_restricted prevents the full-refresh initialization it requires",
					asset.Name, asset.Name, asset.Materialization.Strategy,
				)
			}
			if fullRefreshSeq == nil {
				return decision, fmt.Errorf(
					"materialization target %q does not exist; run a full refresh once before using %s",
					asset.Name, asset.Materialization.Strategy,
				)
			}
			decision.UseFullRefresh = true
			message := fmt.Sprintf(
				"Target %q is absent; initializing it with Bruin's full-refresh materializer before future %s runs.",
				asset.Name, asset.Materialization.Strategy,
			)
			_, _ = fmt.Fprintf(output, "INFO: %s\n", message)
		}
		return decision, nil
	}

	if state.Kind == TargetRelationKindUnknown || state.Kind == expectedKind {
		return decision, nil
	}
	if state.Kind == TargetRelationKindOther {
		return decision, fmt.Errorf(
			"materialization target %q exists as an unsupported relation kind; remove or rename it before materializing %q as a %s",
			asset.Name, asset.Name, expectedKind,
		)
	}
	if !effectiveFullRefresh {
		return decision, fmt.Errorf(
			"materialization target %q exists as a %s but the asset now declares a %s; review and run a full refresh to replace the object kind",
			asset.Name, state.Kind, expectedKind,
		)
	}

	// Snowflake and BigQuery already perform their own type-aware replacement
	// inside Bruin's full-refresh materializers. Running a second drop here would
	// duplicate upstream warehouse-specific safety logic.
	if connectionType == "snowflake" || connectionType == "google_cloud_platform" {
		return decision, nil
	}

	if connectionType != "duckdb" && connectionType != "postgres" {
		return decision, fmt.Errorf(
			"full refresh cannot safely replace materialization target %q from %s to %s on %s yet",
			asset.Name, state.Kind, expectedKind, displayConnectionType(connectionType),
		)
	}
	decision.DropOppositeKind = true
	return decision, nil
}

func inspectMaterializationTarget(
	ctx context.Context,
	manager config.ConnectionAndDetailsGetter,
	connectionName string,
	connectionType string,
	qualifiedName string,
) TargetState {
	state := TargetState{
		Existence: TargetExistenceUnknown, Kind: TargetRelationKindUnknown,
		QualifiedName: qualifiedName, Connection: connectionName,
	}
	lookup, err := materializationTargetLookupFor(manager.GetConnectionDetails(connectionName), connectionType, qualifiedName)
	if err != nil {
		state.Message = err.Error()
		return state
	}
	connection, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		state.Message = fmt.Sprintf("target lifecycle inspection could not resolve connection %q: %v", connectionName, err)
		return state
	}
	querier, ok := connection.(directSchemaQuerier)
	if !ok {
		state.Message = fmt.Sprintf("connection %q does not expose targeted schema queries", connectionName)
		return state
	}

	queryString, err := materializationTargetInspectionSQL(connectionType, lookup)
	if err != nil {
		state.Message = err.Error()
		return state
	}
	result, err := querier.SelectWithSchema(ctx, &query.Query{Query: queryString})
	if err != nil {
		state.Message = fmt.Sprintf("target lifecycle inspection failed for %q: %v", qualifiedName, err)
		return state
	}
	if result == nil {
		state.Message = fmt.Sprintf("target lifecycle inspection returned no result for %q", qualifiedName)
		return state
	}
	if len(result.Rows) == 0 {
		state.Existence = TargetExistenceAbsent
		return state
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) == 0 {
		state.Message = fmt.Sprintf("target lifecycle inspection was ambiguous for %q", qualifiedName)
		return state
	}

	state.Existence = TargetExistencePresent
	state.Kind = targetRelationKindFromDatabaseValue(result.Rows[0][0])
	return state
}

func materializationTargetAdapterSupported(connectionType string) bool {
	switch connectionType {
	case "duckdb", "postgres", "snowflake", "google_cloud_platform":
		return true
	default:
		return false
	}
}

func materializationTargetLifecycleApplies(asset *pipeline.Asset) bool {
	if asset == nil || !isQueryAssetType(asset.Type) || !isDirectRunAssetTypeSupported(asset.Type) {
		return false
	}
	if asset.Type == pipeline.AssetTypeOracleQuery {
		return false
	}
	return asset.Materialization.Type == pipeline.MaterializationTypeTable ||
		asset.Materialization.Type == pipeline.MaterializationTypeView
}

func materializationRequiresExistingTarget(materialization pipeline.Materialization) bool {
	if materialization.Type != pipeline.MaterializationTypeTable {
		return false
	}
	switch materialization.Strategy {
	case pipeline.MaterializationStrategyAppend,
		pipeline.MaterializationStrategyMerge,
		pipeline.MaterializationStrategyDeleteInsert,
		pipeline.MaterializationStrategyTruncateInsert,
		pipeline.MaterializationStrategyTimeInterval,
		pipeline.MaterializationStrategySCD2ByTime,
		pipeline.MaterializationStrategySCD2ByColumn:
		return true
	default:
		return false
	}
}

func targetRelationKindForMaterialization(materializationType pipeline.MaterializationType) TargetRelationKind {
	if materializationType == pipeline.MaterializationTypeView {
		return TargetRelationKindView
	}
	return TargetRelationKindTable
}

func targetRelationKindFromDatabaseValue(value any) TargetRelationKind {
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		raw = fmt.Sprint(typed)
	}
	raw = strings.ToUpper(strings.TrimSpace(raw))
	switch raw {
	case "R", "P", "BASE TABLE", "TABLE":
		return TargetRelationKindTable
	case "V", "VIEW":
		return TargetRelationKindView
	case "M", "F", "OTHER", "EXTERNAL", "EXTERNAL TABLE", "MATERIALIZED VIEW", "SNAPSHOT", "CLONE":
		return TargetRelationKindOther
	default:
		return TargetRelationKindUnknown
	}
}

func materializationTargetLookupFor(details any, connectionType, qualifiedName string) (materializationTargetLookup, error) {
	parts := splitMaterializationTargetName(qualifiedName)
	if len(parts) == 0 || len(parts) > 3 {
		return materializationTargetLookup{}, fmt.Errorf("target lifecycle inspection cannot qualify %q", qualifiedName)
	}
	lookup := materializationTargetLookup{table: parts[len(parts)-1]}
	if len(parts) >= 2 {
		lookup.schema = parts[len(parts)-2]
	}
	if len(parts) == 3 {
		lookup.catalog = parts[0]
	}

	switch connectionType {
	case "duckdb":
		if lookup.schema == "" {
			lookup.schema = "main"
		}
	case "postgres":
		defaultCatalog, defaultSchema := postgresTargetDefaults(details)
		if lookup.catalog == "" {
			lookup.catalog = defaultCatalog
		}
		if lookup.schema == "" {
			lookup.schema = defaultSchema
		}
		if lookup.schema == "" {
			lookup.schema = "public"
		}
	case "snowflake":
		defaultCatalog, defaultSchema := snowflakeTargetDefaults(details)
		if lookup.catalog == "" {
			lookup.catalog = defaultCatalog
		}
		if lookup.schema == "" {
			lookup.schema = defaultSchema
		}
		if lookup.schema == "" {
			lookup.schema = "PUBLIC"
		}
		if lookup.catalog == "" {
			return materializationTargetLookup{}, fmt.Errorf("target lifecycle inspection cannot determine the Snowflake database for %q", qualifiedName)
		}
	case "google_cloud_platform":
		if lookup.catalog == "" {
			lookup.catalog = bigQueryTargetProject(details)
		}
		if lookup.catalog == "" || lookup.schema == "" {
			return materializationTargetLookup{}, fmt.Errorf("target lifecycle inspection requires a BigQuery project and dataset for %q", qualifiedName)
		}
	}
	if lookup.schema == "" || lookup.table == "" {
		return materializationTargetLookup{}, fmt.Errorf("target lifecycle inspection cannot determine the schema and table for %q", qualifiedName)
	}
	return lookup, nil
}

func materializationTargetInspectionSQL(connectionType string, lookup materializationTargetLookup) (string, error) {
	schemaLiteral := quoteSQLStringLiteral(lookup.schema)
	tableLiteral := quoteSQLStringLiteral(lookup.table)
	switch connectionType {
	case "duckdb":
		catalogPredicate := "table_catalog = current_database()"
		if lookup.catalog != "" {
			catalogPredicate = "table_catalog = " + quoteSQLStringLiteral(lookup.catalog)
		}
		return fmt.Sprintf(
			"SELECT table_type FROM information_schema.tables WHERE %s AND table_schema = %s AND table_name = %s",
			catalogPredicate, schemaLiteral, tableLiteral,
		), nil
	case "postgres":
		catalogPredicate := "current_database()"
		if lookup.catalog != "" {
			catalogPredicate = quoteSQLStringLiteral(lookup.catalog)
		}
		return fmt.Sprintf(
			"SELECT c.relkind FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE current_database() = %s AND n.nspname = %s AND c.relname = %s AND c.relkind IN ('r', 'p', 'v', 'm', 'f')",
			catalogPredicate, schemaLiteral, tableLiteral,
		), nil
	case "snowflake":
		return fmt.Sprintf(
			"SELECT table_type FROM %s.INFORMATION_SCHEMA.TABLES WHERE table_schema = %s AND table_name = %s",
			quoteANSIIdentifier(lookup.catalog), schemaLiteral, tableLiteral,
		), nil
	case "google_cloud_platform":
		informationSchema := strings.Join([]string{lookup.catalog, lookup.schema, "INFORMATION_SCHEMA", "TABLES"}, ".")
		return fmt.Sprintf(
			"SELECT table_type FROM `%s` WHERE table_name = %s",
			strings.ReplaceAll(informationSchema, "`", "\\`"), tableLiteral,
		), nil
	default:
		return "", fmt.Errorf("target lifecycle inspection is not implemented for %s connections", displayConnectionType(connectionType))
	}
}

func dropOppositeMaterializationTarget(
	ctx context.Context,
	manager config.ConnectionAndDetailsGetter,
	state TargetState,
	output io.Writer,
) error {
	if state.Kind != TargetRelationKindTable && state.Kind != TargetRelationKindView {
		return fmt.Errorf("cannot drop materialization target %q with unknown relation kind", state.QualifiedName)
	}
	connection, err := resolveRuntimeConnection(manager, state.Connection)
	if err != nil {
		return fmt.Errorf("resolve connection %q for target replacement: %w", state.Connection, err)
	}
	runner, ok := connection.(materializationTargetQueryRunner)
	if !ok {
		return fmt.Errorf("connection %q cannot replace an opposite-kind materialization target", state.Connection)
	}
	connectionType := normalizeConnectionType(manager.GetConnectionType(state.Connection))
	objectKind := strings.ToUpper(string(state.Kind))
	dropSQL := fmt.Sprintf("DROP %s %s", objectKind, quoteRuntimeRelation(state.QualifiedName, connectionType))
	if err := runner.RunQueryWithoutResult(ctx, &query.Query{Query: dropSQL}); err != nil {
		return fmt.Errorf("drop existing %s target %q before full refresh: %w", state.Kind, state.QualifiedName, err)
	}
	_, _ = fmt.Fprintf(output, "INFO: Replaced existing %s target %q before full refresh.\n", state.Kind, state.QualifiedName)
	return nil
}

func emitMaterializationTargetWarning(ctx context.Context, output io.Writer, asset *pipeline.Asset, detail string) {
	if collector, _ := ctx.Value(executionWarningsKey{}).(*executionWarnings); collector == nil {
		return
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "target lifecycle state could not be verified"
	}
	warning := fmt.Sprintf(
		"Could not verify materialization target %q before execution: %s. Renart kept the configured materialization strategy.",
		asset.Name, detail,
	)
	addExecutionWarning(ctx, warning)
	_, _ = fmt.Fprintf(output, "WARNING: %s\n", warning)
}

func renderMaterializationTargetLifecycleStage(asset *pipeline.Asset, targetName string, requestedFullRefresh bool) (AssetRenderStage, bool) {
	if !materializationTargetLifecycleApplies(asset) {
		return AssetRenderStage{}, false
	}
	effectiveFullRefresh := requestedFullRefresh && !assetRefreshRestricted(asset)
	operation := map[string]any{
		"operation":           "inspect_materialization_target",
		"target":              targetName,
		"declared_kind":       targetRelationKindForMaterialization(asset.Materialization.Type),
		"requested_refresh":   effectiveFullRefresh,
		"unknown_state":       "keep_configured_strategy_and_warn",
		"kind_mismatch":       "require_full_refresh",
		"inspection_fidelity": "fresh_runtime_lookup",
	}
	message := "fresh target inspection runs immediately before execution; unknown state preserves the configured materialization strategy"
	if materializationRequiresExistingTarget(asset.Materialization) && !effectiveFullRefresh {
		operation["absent_target"] = "initialize_with_full_refresh_materializer"
		if assetRefreshRestricted(asset) {
			operation["absent_target"] = "block_refresh_restricted_asset"
		}
		message += "; a positively absent incremental target is initialized before incremental SQL"
	}
	content, _ := json.MarshalIndent(operation, "", "  ")
	return AssetRenderStage{
		Kind: "condition", Label: "Inspect materialization target", Language: "json",
		Content: string(content), Status: AssetRenderStageStatusOK,
		Fidelity: AssetRenderFidelitySemantic, Conditional: true, Message: message,
	}, true
}

func splitMaterializationTargetName(name string) []string {
	raw := strings.Split(strings.TrimSpace(name), ".")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(strings.Trim(part, "`\"[]"))
		if part == "" {
			return nil
		}
		parts = append(parts, part)
	}
	return parts
}

func postgresTargetDefaults(details any) (string, string) {
	switch value := details.(type) {
	case config.PostgresConnection:
		return value.Database, value.Schema
	case *config.PostgresConnection:
		if value != nil {
			return value.Database, value.Schema
		}
	}
	return "", ""
}

func snowflakeTargetDefaults(details any) (string, string) {
	switch value := details.(type) {
	case config.SnowflakeConnection:
		return value.Database, value.Schema
	case *config.SnowflakeConnection:
		if value != nil {
			return value.Database, value.Schema
		}
	}
	return "", ""
}

func bigQueryTargetProject(details any) string {
	switch value := details.(type) {
	case config.GoogleCloudPlatformConnection:
		return value.ProjectID
	case *config.GoogleCloudPlatformConnection:
		if value != nil {
			return value.ProjectID
		}
	}
	return ""
}

func quoteSQLStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteANSIIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func displayConnectionType(connectionType string) string {
	if strings.TrimSpace(connectionType) == "" {
		return "unknown"
	}
	return connectionType
}
