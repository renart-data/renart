package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/tablename"

	"renart/internal/web/duckcoord"
	"renart/internal/web/identity"
	"renart/internal/web/runcontext"
)

const (
	assetRenderTargetKindNone     = "none"
	assetRenderTargetKindUnknown  = "unknown"
	assetRenderTargetKindRelation = "relation"
	assetRenderTargetKindFile     = "file"
	assetWriteResourceNone        = "none"
	assetWriteResourcePipeline    = "pipeline"
	assetWriteResourceLocalFile   = "local_file"
	assetWriteResourceDuckDB      = "duckdb_database"
	assetWriteResourceWarehouse   = "warehouse_relation"
)

// SelectedPhysicalTarget is the value-only physical output selected for an
// asset in one environment. Identity is safe to persist and compare only when
// Fidelity is exact.
type SelectedPhysicalTarget struct {
	AssetID  string
	Identity string
	Fidelity AssetRenderFidelity
}

// ResolvePipelinePhysicalTargets shares execution's configuration and target
// resolver with staleness without opening a warehouse or mutating project
// files. Results are keyed by canonical asset ID.
func ResolvePipelinePhysicalTargets(
	workspaceRoot string,
	configPath string,
	environment string,
	pl *pipeline.Pipeline,
) (map[string]SelectedPhysicalTarget, error) {
	if pl == nil {
		return nil, fmt.Errorf("pipeline is required")
	}
	pipelineUUID := strings.TrimSpace(pl.LegacyID)
	if pipelineUUID == "" {
		return nil, fmt.Errorf("pipeline %q has no stable id", pl.Name)
	}
	cfg, err := loadSelectedConfigReadOnlyFS(nil, configPath, environment)
	if err != nil {
		return nil, err
	}
	result := make(map[string]SelectedPhysicalTarget, len(pl.Assets))
	for _, asset := range pl.Assets {
		if asset == nil || strings.TrimSpace(asset.Name) == "" {
			continue
		}
		target := resolveAssetPhysicalTarget(workspaceRoot, &directPipelineInfo{
			Pipeline: pl,
			Asset:    asset,
			Config:   cfg,
		})
		assetID := identity.AssetID(pipelineUUID, asset.Name)
		result[assetID] = SelectedPhysicalTarget{
			AssetID: assetID, Identity: target.Identity, Fidelity: target.Fidelity,
		}
	}
	return result, nil
}

// resolveAssetPhysicalTarget is intentionally independent from render stage
// construction. A later plan/freshness tranche can reuse the same resolver,
// while renderPath only needs to attach its result to the response.
func resolveAssetPhysicalTarget(workspaceRoot string, info *directPipelineInfo) AssetRenderTarget {
	if info == nil || info.Asset == nil || info.Pipeline == nil {
		return runtimeOnlyAssetTarget(assetRenderTargetKindNone, "", "the asset target context is incomplete")
	}
	asset := info.Asset

	// Sensors observe state but do not own a mutable output. This is exact even
	// when their condition uses a warehouse connection.
	if isSensorAssetType(asset.Type) {
		return applyAssetWriteResourceSafety(asset, AssetRenderTarget{
			Kind: assetRenderTargetKindNone, Fidelity: AssetRenderFidelityExact,
			WriteResource: AssetRenderWriteResource{
				Kind: assetWriteResourceNone, Fidelity: AssetRenderFidelityExact,
			},
		})
	}

	targetKind, displayObject := assetTargetIntent(asset, info.Pipeline)
	if asset.Type == pipeline.AssetTypePython && asset.Materialization.Type != pipeline.MaterializationTypeTable {
		return runtimeOnlyAssetTarget(targetKind, displayObject, "Python code and SDK calls do not declare a Renart-managed table output")
	}
	knownWriter := isKnownImplicitAssetWriter(asset)
	if asset.Materialization.Type == pipeline.MaterializationTypeNone && !knownWriter {
		return runtimeOnlyAssetTarget(targetKind, displayObject, "the asset does not declare an exact materialized output")
	}
	if !knownWriter && asset.Materialization.Type != pipeline.MaterializationTypeTable && asset.Materialization.Type != pipeline.MaterializationTypeView {
		return runtimeOnlyAssetTarget(targetKind, displayObject, "the declared materialization does not have a supported physical-target contract")
	}
	if info.Config != nil && info.Config.SelectedEnvironment != nil && strings.TrimSpace(info.Config.SelectedEnvironment.SchemaPrefix) != "" {
		return runtimeOnlyAssetTarget(targetKind, displayObject, "a schema prefix can change the physical target at runtime")
	}

	if isLoadAsset(asset) {
		params, err := resolvedLoadParams(asset, info.Pipeline)
		if err != nil {
			return runtimeOnlyAssetTarget(targetKind, displayObject, "the Load target connection could not be resolved")
		}
		if isLocalLoadConnection(params.DestinationConnection) {
			return applyAssetWriteResourceSafety(asset, resolveLocalFileAssetTarget(workspaceRoot, params.DestinationObject))
		}
	}

	connectionName, err := targetConnectionNameForAsset(asset, info.Pipeline)
	if err != nil || strings.TrimSpace(connectionName) == "" {
		return runtimeOnlyAssetTarget(assetRenderTargetKindRelation, strings.TrimSpace(asset.Name), "the target connection could not be resolved")
	}
	environment := selectedTargetEnvironment(info.Config)
	if environment == nil || environment.Connections == nil {
		return runtimeOnlyAssetTarget(assetRenderTargetKindRelation, strings.TrimSpace(asset.Name), "the selected configuration has no target connections")
	}
	connectionType := environment.Connections.ConnectionsSummaryList()[connectionName]
	connection := environment.Connections.GetConnection(connectionName)
	if strings.TrimSpace(connectionType) == "" || connection == nil {
		return runtimeOnlyAssetTarget(assetRenderTargetKindRelation, strings.TrimSpace(asset.Name), "the target connection is unavailable in the selected configuration")
	}

	if isLoadAsset(asset) {
		switch loadConnectionCategory(connectionType) {
		case LoadCategoryDatabase:
			// Database Load targets use the asset's canonical relation name.
		case LoadCategoryStorage, LoadCategoryFile:
			params := loadParamsFromAsset(asset)
			return runtimeOnlyAssetTarget(assetRenderTargetKindFile, safeFileObject(params.DestinationObject), "object and remote file targets do not yet have an exact physical identity")
		default:
			return runtimeOnlyAssetTarget(assetRenderTargetKindNone, "", "the Load target family does not have a physical-target contract")
		}
	}
	if len(asset.Hooks.Pre) > 0 && !explicitPhysicalRelation(connectionType, asset.Name) {
		return runtimeOnlyAssetTarget(assetRenderTargetKindRelation, strings.TrimSpace(asset.Name), "pre-hooks can change an unqualified target relation at runtime")
	}

	coordinates, object, message, ok := resolveRelationTargetCoordinates(workspaceRoot, connectionType, connection, asset.Name)
	if !ok {
		return runtimeOnlyAssetTarget(assetRenderTargetKindRelation, firstNonEmpty(object, strings.TrimSpace(asset.Name)), message)
	}
	target := exactAssetTarget(assetRenderTargetKindRelation, object, coordinates)
	target = applyExactWarehouseWriteResource(info.Config, asset, connection, coordinates, target)
	return applyAssetWriteResourceSafety(asset, target)
}

// applyExactWarehouseWriteResource promotes only native SQL operator families
// whose shared clients and materializers have been audited for concurrent
// execution. The claim uses the already-secret-free physical target digest, so
// aliases that reach the same relation still serialize without persisting
// endpoint or credential material.
//
// Audited local DuckDB SQL uses a relation claim backed by its shared database
// session. Other DuckDB operators retain their whole-file claim. Seeds,
// Load/API assets, Python, hooks, schema-prefixed environments, and every
// unaudited network warehouse family remain pipeline-exclusive.
func applyExactWarehouseWriteResource(
	cfg *config.Config,
	asset *pipeline.Asset,
	connection any,
	coordinates runcontext.PhysicalTargetCoordinates,
	target AssetRenderTarget,
) AssetRenderTarget {
	if asset == nil ||
		target.Fidelity != AssetRenderFidelityExact ||
		target.Identity == "" {
		return target
	}
	if coordinates.Platform == "duckdb_file" {
		if !isConcurrentNativeDuckDBTarget(cfg, asset, connection, coordinates) {
			return target
		}
		resource := exactAssetWriteResource(assetWriteResourceDuckDB, coordinates.FilePath, target.Identity)
		if resource.Fidelity == AssetRenderFidelityExact {
			target.WriteResource = resource
		}
		return target
	}
	if !auditedConcurrentWarehouseAsset(asset.Type, coordinates.Platform) {
		return target
	}
	resource := exactAssetWriteResource(assetWriteResourceWarehouse, "", target.Identity)
	if resource.Fidelity != AssetRenderFidelityExact {
		return target
	}
	target.WriteResource = resource
	return target
}

func isConcurrentNativeDuckDBTarget(
	cfg *config.Config,
	asset *pipeline.Asset,
	connection any,
	coordinates runcontext.PhysicalTargetCoordinates,
) bool {
	if asset == nil ||
		asset.Type != pipeline.AssetTypeDuckDBQuery ||
		coordinates.Platform != "duckdb_file" ||
		strings.TrimSpace(coordinates.FilePath) == "" ||
		(asset.Materialization.Type != pipeline.MaterializationTypeTable &&
			asset.Materialization.Type != pipeline.MaterializationTypeView) ||
		asset.Materialization.Strategy == pipeline.MaterializationStrategyDDL ||
		len(asset.Hooks.Pre) > 0 ||
		len(asset.Hooks.Post) > 0 {
		return false
	}
	if cfg != nil && cfg.SelectedEnvironment != nil &&
		strings.TrimSpace(cfg.SelectedEnvironment.SchemaPrefix) != "" {
		return false
	}

	var duckDBConnection *config.DuckDBConnection
	switch typed := connection.(type) {
	case *config.DuckDBConnection:
		duckDBConnection = typed
	case config.DuckDBConnection:
		clone := typed
		duckDBConnection = &clone
	}
	if duckDBConnection == nil ||
		duckDBConnection.ReadOnly ||
		duckDBConnection.Lakehouse != nil {
		return false
	}
	path := strings.TrimSpace(duckDBConnection.Path)
	return path != "" && !strings.ContainsAny(path, "?#")
}

func auditedConcurrentWarehouseAsset(assetType pipeline.AssetType, platform string) bool {
	switch platform {
	case "postgres":
		return assetType == pipeline.AssetTypePostgresQuery
	case "trino":
		return assetType == pipeline.AssetTypeTrinoQuery
	case "clickhouse":
		return assetType == pipeline.AssetTypeClickHouse
	case "starrocks":
		return assetType == pipeline.AssetTypeStarRocksQuery
	default:
		return false
	}
}

func applyAssetWriteResourceSafety(asset *pipeline.Asset, target AssetRenderTarget) AssetRenderTarget {
	if asset == nil {
		return target
	}
	if asset.Type == pipeline.AssetTypePython {
		target.WriteResource = conservativeAssetWriteResource("Python execution can mutate resources beyond its declared table output")
		return target
	}
	if len(asset.Hooks.Pre) == 0 && len(asset.Hooks.Post) == 0 {
		return target
	}
	target.WriteResource = conservativeAssetWriteResource("asset hooks can mutate resources beyond the declared output")
	return target
}

func selectedTargetEnvironment(cfg *config.Config) *config.Environment {
	if cfg == nil {
		return nil
	}
	return cfg.SelectedEnvironment
}

func assetTargetIntent(asset *pipeline.Asset, pl *pipeline.Pipeline) (string, string) {
	if asset == nil {
		return assetRenderTargetKindNone, ""
	}
	if isLoadAsset(asset) {
		params, err := resolvedLoadParams(asset, pl)
		if err == nil && isLocalLoadConnection(params.DestinationConnection) {
			return assetRenderTargetKindFile, safeFileObject(params.DestinationObject)
		}
	}
	if asset.Materialization.Type == pipeline.MaterializationTypeNone && !isKnownImplicitAssetWriter(asset) {
		return assetRenderTargetKindUnknown, ""
	}
	return assetRenderTargetKindRelation, strings.TrimSpace(asset.Name)
}

func isKnownImplicitAssetWriter(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	return isLoadAsset(asset) || isAPIAsset(asset) || asset.Type == pipeline.AssetTypeIngestr || strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".seed")
}

func exactAssetTarget(kind, object string, coordinates runcontext.PhysicalTargetCoordinates) AssetRenderTarget {
	identity := runcontext.PhysicalTargetIdentity(coordinates)
	if identity.Fidelity != runcontext.IdentityFidelityExact {
		return runtimeOnlyAssetTarget(kind, object, identity.Message)
	}
	resource := conservativeAssetWriteResource("this operator does not yet have a proven isolated write-resource contract")
	switch coordinates.Platform {
	case "local_file":
		resource = exactAssetWriteResource(assetWriteResourceLocalFile, coordinates.FilePath, "")
	case "duckdb_file":
		// DuckDB defaults to a whole-file claim. Audited native SQL assets are
		// promoted to relation-scoped claims by applyExactWarehouseWriteResource.
		resource = exactAssetWriteResource(assetWriteResourceDuckDB, coordinates.FilePath, "")
	}
	return AssetRenderTarget{
		Kind:          kind,
		Object:        object,
		Identity:      identity.Digest,
		Fidelity:      AssetRenderFidelityExact,
		WriteResource: resource,
	}
}

func runtimeOnlyAssetTarget(kind, object, message string) AssetRenderTarget {
	return AssetRenderTarget{
		Kind:          kind,
		Object:        object,
		Fidelity:      AssetRenderFidelityRuntimeOnly,
		Message:       message,
		WriteResource: conservativeAssetWriteResource(message),
	}
}

func exactAssetWriteResource(kind, filePath, targetIdentity string) AssetRenderWriteResource {
	identity := runcontext.WriteResourceIdentity(runcontext.WriteResourceCoordinates{
		Kind: kind, FilePath: filePath, TargetIdentity: targetIdentity,
	})
	if identity.Fidelity != runcontext.IdentityFidelityExact || identity.Digest == "" {
		return conservativeAssetWriteResource(identity.Message)
	}
	return AssetRenderWriteResource{
		Kind: kind, Identity: identity.Digest, Fidelity: AssetRenderFidelityExact,
	}
}

func conservativeAssetWriteResource(message string) AssetRenderWriteResource {
	if strings.TrimSpace(message) == "" {
		message = "the write resource is only available at runtime"
	}
	return AssetRenderWriteResource{
		Kind: assetWriteResourcePipeline, Fidelity: AssetRenderFidelityRuntimeOnly,
		Message: message,
	}
}

func resolveLocalFileAssetTarget(workspaceRoot, rawPath string) AssetRenderTarget {
	display := safeFileObject(rawPath)
	path := strings.TrimSpace(rawPath)
	if path == "" || strings.ContainsAny(path, "?#") {
		return runtimeOnlyAssetTarget(assetRenderTargetKindFile, display, "the local target file path is missing or ambiguous")
	}
	lower := strings.ToLower(path)
	if strings.Contains(path, "://") && !strings.HasPrefix(lower, "file://") {
		return runtimeOnlyAssetTarget(assetRenderTargetKindFile, display, "the local target must use a filesystem path")
	}
	canonical, err := duckcoord.CanonicalPath(workspaceRoot, path)
	if err != nil || canonical == "" {
		return runtimeOnlyAssetTarget(assetRenderTargetKindFile, display, "the local target file path could not be canonicalized")
	}
	return exactAssetTarget(assetRenderTargetKindFile, localFileDisplay(workspaceRoot, canonical), runcontext.PhysicalTargetCoordinates{
		Kind:     assetRenderTargetKindFile,
		Platform: "local_file",
		FilePath: canonical,
	})
}

func resolveRelationTargetCoordinates(workspaceRoot, rawConnectionType string, connection any, rawObject string) (runcontext.PhysicalTargetCoordinates, string, string, bool) {
	connectionType := normalizeConnectionType(rawConnectionType)
	switch connectionType {
	case "duckdb":
		conn, ok := connection.(*config.DuckDBConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the DuckDB target configuration has an unexpected shape")
		}
		if conn.Lakehouse != nil {
			return unresolvedRelation("lakehouse DuckDB targets do not yet have an exact physical identity")
		}
		path := strings.TrimSpace(conn.Path)
		lower := strings.ToLower(path)
		if path == "" || lower == ":memory:" || strings.HasPrefix(lower, "md:") || strings.HasPrefix(lower, "motherduck:") || strings.HasPrefix(lower, "ducklake:") || strings.HasPrefix(lower, "lakehouse:") {
			return unresolvedRelation("only local file-backed DuckDB targets have an exact physical identity")
		}
		if strings.Contains(path, "://") && !strings.HasPrefix(lower, "duckdb://") && !strings.HasPrefix(lower, "file://") {
			return unresolvedRelation("only local file-backed DuckDB targets have an exact physical identity")
		}
		canonical, err := duckcoord.CanonicalPath(workspaceRoot, path)
		if err != nil || canonical == "" {
			return unresolvedRelation("the DuckDB target path could not be canonicalized")
		}
		relation, object, ok := resolvePhysicalRelation("duckdb", rawObject, tablename.Defaults{Schema: "main"}, false, true)
		if !ok || relation.Catalog != "" {
			return unresolvedRelationWithObject(object, "the DuckDB target relation depends on an attached catalog or an ambiguous default")
		}
		return runcontext.PhysicalTargetCoordinates{
			Kind:     assetRenderTargetKindRelation,
			Platform: "duckdb_file",
			Catalog:  duckDBIdentifierIdentity(relation.Catalog),
			Schema:   duckDBIdentifierIdentity(relation.Schema),
			Object:   duckDBIdentifierIdentity(relation.Table),
			FilePath: canonical,
		}, object, "", true

	case "postgres":
		conn, ok := connection.(*config.PostgresConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the PostgreSQL target configuration has an unexpected shape")
		}
		return networkRelationTarget("postgres", "postgres", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Database, Schema: conn.Schema}, true, true)

	case "redshift":
		conn, ok := connection.(*config.RedshiftConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Redshift target configuration has an unexpected shape")
		}
		return networkRelationTarget("redshift", "redshift", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Database, Schema: conn.Schema}, true, true)

	case "mysql":
		conn, ok := connection.(*config.MySQLConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the MySQL target configuration has an unexpected shape")
		}
		return networkRelationTarget("mysql", "mysql", rawObject, conn.Host, conn.Port, 3306, tablename.Defaults{Schema: conn.Database}, false, true)

	case "mssql":
		conn, ok := connection.(*config.MsSQLConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the MSSQL target configuration has an unexpected shape")
		}
		if strings.TrimSpace(conn.Options) != "" {
			return unresolvedRelation("raw TDS connection options can change target routing at runtime")
		}
		return networkRelationTarget("tds", "mssql", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Database}, true, true)

	case "fabric":
		conn, ok := connection.(*config.FabricConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Fabric target configuration has an unexpected shape")
		}
		if strings.TrimSpace(conn.Options) != "" {
			return unresolvedRelation("raw TDS connection options can change target routing at runtime")
		}
		return networkRelationTarget("tds", "fabric", rawObject, conn.Host, conn.Port, 1433, tablename.Defaults{Catalog: conn.Database}, true, true)

	case "synapse":
		conn, ok := connection.(*config.SynapseConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Synapse target configuration has an unexpected shape")
		}
		if strings.TrimSpace(conn.Options) != "" {
			return unresolvedRelation("raw TDS connection options can change target routing at runtime")
		}
		return networkRelationTarget("tds", "synapse", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Database}, true, true)

	case "vertica":
		conn, ok := connection.(*config.VerticaConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Vertica target configuration has an unexpected shape")
		}
		return networkRelationTarget("vertica", "vertica", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Database, Schema: conn.Schema}, true, true)

	case "clickhouse":
		conn, ok := connection.(*config.ClickHouseConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the ClickHouse target configuration has an unexpected shape")
		}
		return networkRelationTarget("clickhouse", "clickhouse", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Schema: conn.Database}, false, true)

	case "trino":
		conn, ok := connection.(*config.TrinoConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Trino target configuration has an unexpected shape")
		}
		return networkRelationTarget("trino", "trino", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Catalog, Schema: conn.Schema}, true, true)

	case "starrocks":
		conn, ok := connection.(*config.StarRocksConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the StarRocks target configuration has an unexpected shape")
		}
		// Native StarRocks execution uses the MySQL protocol and selects the
		// configured database and catalog. Native connection setup and fully
		// qualified browser references use the same catalog default.
		catalog := conn.Catalog
		if catalog == "" {
			catalog = "default_catalog"
		}
		return networkRelationTarget("starrocks", "starrocks", rawObject, conn.Host, conn.Port, 9030, tablename.Defaults{Catalog: catalog, Schema: conn.Database}, true, true)

	case "databricks":
		conn, ok := connection.(*config.DatabricksConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Databricks target configuration has an unexpected shape")
		}
		coordinates, object, message, ok := networkRelationTarget("databricks", "databricks", rawObject, conn.Host, conn.Port, 0, tablename.Defaults{Catalog: conn.Catalog, Schema: conn.Schema}, true, true)
		if !ok {
			return coordinates, object, message, false
		}
		if strings.TrimSpace(conn.Path) == "" || strings.ContainsAny(conn.Path, "?#") {
			return unresolvedRelationWithObject(object, "the Databricks SQL routing path is missing or ambiguous")
		}
		coordinates.RoutingPath = strings.TrimSpace(conn.Path)
		return coordinates, object, "", true

	case "snowflake":
		conn, ok := connection.(*config.SnowflakeConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the Snowflake target configuration has an unexpected shape")
		}
		account := strings.ToLower(strings.TrimSpace(conn.Account))
		region := strings.ToLower(strings.TrimSpace(conn.Region))
		if account == "" || region == "" || containsRawRoutingMaterial(account) || containsRawRoutingMaterial(region) {
			return unresolvedRelation("the Snowflake account and region must be explicit")
		}
		relation, object, ok := resolvePhysicalRelation("snowflake", rawObject, tablename.Defaults{Catalog: conn.Database, Schema: conn.Schema}, true, true)
		if !ok {
			return unresolvedRelationWithObject(object, "the Snowflake target relation is not fully resolved")
		}
		return runcontext.PhysicalTargetCoordinates{
			Kind:     assetRenderTargetKindRelation,
			Platform: "snowflake",
			Account:  account,
			Region:   region,
			Catalog:  relation.Catalog,
			Schema:   relation.Schema,
			Object:   relation.Table,
		}, object, "", true

	case "google_cloud_platform":
		conn, ok := connection.(*config.GoogleCloudPlatformConnection)
		if !ok || conn == nil {
			return unresolvedRelation("the BigQuery target configuration has an unexpected shape")
		}
		relation, object, ok := resolvePhysicalRelation("google_cloud_platform", rawObject, tablename.Defaults{Catalog: conn.ProjectID}, true, true)
		if !ok {
			return unresolvedRelationWithObject(object, "the BigQuery project and dataset must be explicit")
		}
		return runcontext.PhysicalTargetCoordinates{
			Kind:     assetRenderTargetKindRelation,
			Platform: "bigquery",
			Catalog:  relation.Catalog,
			Schema:   relation.Schema,
			Object:   relation.Table,
		}, object, "", true

	default:
		return unresolvedRelation("this target family does not yet have an exact physical identity")
	}
}

func networkRelationTarget(identityPlatform, tablePlatform, rawObject, rawHost string, port, defaultPort int, defaults tablename.Defaults, requireCatalog, requireSchema bool) (runcontext.PhysicalTargetCoordinates, string, string, bool) {
	host, ok := canonicalTargetHost(rawHost)
	if !ok {
		return unresolvedRelation("the target host is missing or contains opaque routing material")
	}
	if port <= 0 {
		port = defaultPort
	}
	if port <= 0 {
		return unresolvedRelation("the target port must be explicit")
	}
	relation, object, ok := resolvePhysicalRelation(tablePlatform, rawObject, defaults, requireCatalog, requireSchema)
	if !ok {
		return unresolvedRelationWithObject(object, "the target relation is not fully resolved by the asset and connection defaults")
	}
	return runcontext.PhysicalTargetCoordinates{
		Kind:     assetRenderTargetKindRelation,
		Platform: identityPlatform,
		Host:     host,
		Port:     port,
		Catalog:  relation.Catalog,
		Schema:   relation.Schema,
		Object:   relation.Table,
	}, object, "", true
}

func canonicalTargetHost(raw string) (string, bool) {
	host := strings.TrimSpace(raw)
	if host == "" || containsRawRoutingMaterial(host) {
		return "", false
	}
	return strings.ToLower(strings.TrimSuffix(host, ".")), true
}

func containsRawRoutingMaterial(value string) bool {
	return strings.Contains(value, "://") || strings.ContainsAny(value, "/?#@{}$")
}

// duckDBIdentifierIdentity matches DuckDB's ASCII-based case-insensitive
// identifier comparison without changing the user-facing relation spelling.
// Non-ASCII bytes remain untouched because DuckDB does not case-fold them.
func duckDBIdentifierIdentity(value string) string {
	bytes := []byte(strings.TrimSpace(value))
	for index, character := range bytes {
		if character >= 'A' && character <= 'Z' {
			bytes[index] = character + ('a' - 'A')
		}
	}
	return string(bytes)
}

func resolvePhysicalRelation(platform, rawObject string, defaults tablename.Defaults, requireCatalog, requireSchema bool) (tablename.TableName, string, bool) {
	name := strings.TrimSpace(rawObject)
	if name == "" || strings.ContainsAny(name, "\"`[]{}$") {
		return tablename.TableName{}, "", false
	}
	capability, ok := warehouseTableCapability(platform)
	if !ok || capability.Unbounded {
		return tablename.TableName{}, name, false
	}
	defaults.Catalog = strings.TrimSpace(defaults.Catalog)
	defaults.Schema = strings.TrimSpace(defaults.Schema)
	relation, err := capability.Parse(name, defaults)
	if err != nil {
		return tablename.TableName{}, name, false
	}
	object := relation.String(".")
	if strings.TrimSpace(relation.Table) == "" || (requireCatalog && strings.TrimSpace(relation.Catalog) == "") || (requireSchema && strings.TrimSpace(relation.Schema) == "") {
		return relation, object, false
	}
	return tablename.TableName{
		Catalog: strings.TrimSpace(relation.Catalog),
		Schema:  strings.TrimSpace(relation.Schema),
		Table:   strings.TrimSpace(relation.Table),
	}, object, true
}

func unresolvedRelation(message string) (runcontext.PhysicalTargetCoordinates, string, string, bool) {
	return runcontext.PhysicalTargetCoordinates{}, "", message, false
}

func unresolvedRelationWithObject(object, message string) (runcontext.PhysicalTargetCoordinates, string, string, bool) {
	return runcontext.PhysicalTargetCoordinates{}, object, message, false
}

func safeFileObject(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if scheme := strings.Index(path, "://"); scheme >= 0 {
		path = strings.TrimRight(path[scheme+3:], "/")
		if path == "" {
			return "remote object"
		}
		return filepath.Base(path)
	}
	path = filepath.Clean(path)
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return filepath.Base(path)
	}
	return filepath.ToSlash(path)
}

func explicitPhysicalRelation(connectionType, rawObject string) bool {
	name := strings.TrimSpace(rawObject)
	if name == "" || strings.ContainsAny(name, "\"`[]{}$") {
		return false
	}
	components := strings.Split(name, ".")
	for _, component := range components {
		if strings.TrimSpace(component) == "" {
			return false
		}
	}
	switch normalizeConnectionType(connectionType) {
	case "duckdb", "postgres", "redshift", "mysql", "synapse", "vertica", "clickhouse":
		return len(components) == 2
	case "mssql", "fabric", "trino", "databricks", "snowflake", "google_cloud_platform":
		return len(components) == 3
	default:
		return false
	}
}

func localFileDisplay(workspaceRoot, canonical string) string {
	root := strings.TrimSpace(workspaceRoot)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if root != "" {
		if relative, err := filepath.Rel(root, canonical); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.Base(canonical)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
