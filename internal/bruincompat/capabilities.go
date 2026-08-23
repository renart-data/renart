package bruincompat

import (
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// ConnectionFamily is the stable semantic family exposed by Renart. It is
// deliberately independent from Bruin's connection keys and from visual
// choices such as icons or colours in the web app.
type ConnectionFamily string

// ConnectionProfile is Renart's single semantic description of a warehouse
// connection. Parser, analyzer and formatter dialects are separate because
// compatibility sometimes intentionally uses a nearby dialect (for example,
// Redshift analysis uses PostgreSQL semantics).
type ConnectionProfile struct {
	Family             ConnectionFamily
	CanonicalType      string
	Aliases            []string
	QueryAssetType     pipeline.AssetType
	SourceAssetType    pipeline.AssetType
	ParserDialect      string
	AnalyzerDialect    string
	FormatterDialect   string
	FingerprintDialect string
}

const genericDialect = "generic"

// connectionSpecs contains semantic decisions Renart owns. Query/source asset
// types are filled from Bruin's authoritative AssetTypeConnectionMapping below.
// FingerprintDialect temporarily remains distinct for the two historical cases
// where format-on-save and v3 fingerprinting disagree; aligning those requires
// an explicit fingerprint-version migration rather than an incidental refactor.
var connectionSpecs = []ConnectionProfile{
	{Family: "bigquery", CanonicalType: "google_cloud_platform", Aliases: []string{"bigquery", "gcp", "bq"}, ParserDialect: "bigquery", AnalyzerDialect: "bigquery", FormatterDialect: "bigquery", FingerprintDialect: "bigquery"},
	{Family: "snowflake", CanonicalType: "snowflake", Aliases: []string{"sf"}, ParserDialect: "snowflake", AnalyzerDialect: "snowflake", FormatterDialect: "snowflake", FingerprintDialect: "snowflake"},
	{Family: "postgres", CanonicalType: "postgres", Aliases: []string{"pg"}, ParserDialect: "postgres", AnalyzerDialect: "postgres", FormatterDialect: "postgresql", FingerprintDialect: "postgresql"},
	{Family: "mysql", CanonicalType: "mysql", ParserDialect: "mysql", AnalyzerDialect: "mysql", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "doris", CanonicalType: "doris", ParserDialect: "doris", AnalyzerDialect: "doris", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "starrocks", CanonicalType: "starrocks", ParserDialect: "starrocks", AnalyzerDialect: "mysql", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "redshift", CanonicalType: "redshift", Aliases: []string{"rs"}, ParserDialect: "redshift", AnalyzerDialect: "postgres", FormatterDialect: "redshift", FingerprintDialect: "redshift"},
	{Family: "athena", CanonicalType: "athena", ParserDialect: "athena", AnalyzerDialect: "athena", FormatterDialect: "athena", FingerprintDialect: "athena"},
	{Family: "trino", CanonicalType: "trino", ParserDialect: "trino", AnalyzerDialect: "trino", FormatterDialect: "trino", FingerprintDialect: "trino"},
	{Family: "dremio", CanonicalType: "dremio", ParserDialect: "trino", AnalyzerDialect: "trino", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "sail", CanonicalType: "sail", ParserDialect: "trino", AnalyzerDialect: "trino", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "clickhouse", CanonicalType: "clickhouse", ParserDialect: "clickhouse", AnalyzerDialect: "clickhouse", FormatterDialect: "clickhouse", FingerprintDialect: "clickhouse"},
	{Family: "databricks", CanonicalType: "databricks", ParserDialect: "databricks", AnalyzerDialect: "databricks", FormatterDialect: "databricks", FingerprintDialect: "databricks"},
	{Family: "mssql", CanonicalType: "mssql", Aliases: []string{"sqlserver", "ms"}, ParserDialect: "tsql", AnalyzerDialect: "tsql", FormatterDialect: "tsql", FingerprintDialect: "tsql"},
	{Family: "synapse", CanonicalType: "synapse", ParserDialect: "tsql", AnalyzerDialect: "tsql", FormatterDialect: "tsql", FingerprintDialect: "tsql"},
	{Family: "duckdb", CanonicalType: "duckdb", ParserDialect: "duckdb", AnalyzerDialect: "duckdb", FormatterDialect: "duckdb", FingerprintDialect: "duckdb"},
	{Family: "motherduck", CanonicalType: "motherduck", ParserDialect: "duckdb", AnalyzerDialect: "duckdb", FormatterDialect: "duckdb", FingerprintDialect: genericDialect},
	{Family: "oracle", CanonicalType: "oracle", ParserDialect: "oracle", AnalyzerDialect: "oracle", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "fabric", CanonicalType: "fabric", ParserDialect: "fabric", AnalyzerDialect: "fabric", FormatterDialect: genericDialect, FingerprintDialect: genericDialect},
	{Family: "vertica", CanonicalType: "vertica", ParserDialect: "postgres", AnalyzerDialect: "postgres", FormatterDialect: "postgresql", FingerprintDialect: genericDialect},
}

// These Bruin connection families are deliberately outside Renart's SQL
// warehouse profile. Keeping the decision explicit makes a newly added Bruin
// mapping fail the parity test instead of silently falling back to generic SQL.
var unsupportedConnectionTypes = map[string]string{
	"aws":                 "object-storage sensor connection",
	"dataproc_serverless": "serverless Python execution connection",
	"dynamodb":            "non-SQL source connection",
	"elasticsearch":       "non-SQL source connection",
	"emr_serverless":      "serverless Python execution connection",
	"google_sheets":       "non-SQL source connection",
	"iceberg":             "table-format asset without a Renart SQL connection profile",
	"mongo":               "non-SQL source connection",
	"quicksight":          "presentation asset connection",
}

var (
	profilesByCanonicalType map[string]ConnectionProfile
	canonicalTypeByAlias    map[string]string
)

func init() {
	profilesByCanonicalType = make(map[string]ConnectionProfile, len(connectionSpecs))
	canonicalTypeByAlias = make(map[string]string, len(connectionSpecs)*2)
	for _, spec := range connectionSpecs {
		canonical := strings.ToLower(strings.TrimSpace(spec.CanonicalType))
		spec.CanonicalType = canonical
		profilesByCanonicalType[canonical] = spec
		canonicalTypeByAlias[canonical] = canonical
		for _, alias := range spec.Aliases {
			canonicalTypeByAlias[strings.ToLower(strings.TrimSpace(alias))] = canonical
		}
	}

	assetTypes := make([]pipeline.AssetType, 0, len(pipeline.AssetTypeConnectionMapping))
	for assetType := range pipeline.AssetTypeConnectionMapping {
		assetTypes = append(assetTypes, assetType)
	}
	sort.Slice(assetTypes, func(i, j int) bool { return assetTypes[i] < assetTypes[j] })
	for _, assetType := range assetTypes {
		canonical := NormalizeConnectionType(pipeline.AssetTypeConnectionMapping[assetType])
		profile, ok := profilesByCanonicalType[canonical]
		if !ok {
			continue
		}
		switch {
		case IsQueryAssetType(assetType) && !isLegacyAssetType(assetType):
			if profile.QueryAssetType == "" {
				profile.QueryAssetType = assetType
			}
		case IsSourceAssetType(assetType):
			if profile.SourceAssetType == "" {
				profile.SourceAssetType = assetType
			}
		}
		profilesByCanonicalType[canonical] = profile
	}
}

// NormalizeConnectionType canonicalizes Bruin connection keys and accepted UI
// aliases. Unknown values are still normalized so callers can compare them.
func NormalizeConnectionType(connectionType string) string {
	normalized := strings.ToLower(strings.TrimSpace(connectionType))
	if canonical, ok := canonicalTypeByAlias[normalized]; ok {
		return canonical
	}
	return normalized
}

// ConnectionProfileForType resolves a connection key or alias. The returned
// profile owns its Aliases slice and is safe for callers to modify.
func ConnectionProfileForType(connectionType string) (ConnectionProfile, bool) {
	profile, ok := profilesByCanonicalType[NormalizeConnectionType(connectionType)]
	if !ok {
		return ConnectionProfile{}, false
	}
	profile.Aliases = append([]string(nil), profile.Aliases...)
	return profile, true
}

// ConnectionProfileForAssetType resolves an asset through Bruin's authoritative
// asset-to-connection mapping.
func ConnectionProfileForAssetType(assetType pipeline.AssetType) (ConnectionProfile, bool) {
	connectionType, ok := pipeline.AssetTypeConnectionMapping[assetType]
	if !ok {
		return ConnectionProfile{}, false
	}
	return ConnectionProfileForType(connectionType)
}

func QueryAssetTypeForConnectionType(connectionType string) (pipeline.AssetType, bool) {
	profile, ok := ConnectionProfileForType(connectionType)
	return profile.QueryAssetType, ok && profile.QueryAssetType != ""
}

func SourceAssetTypeForConnectionType(connectionType string) (pipeline.AssetType, bool) {
	profile, ok := ConnectionProfileForType(connectionType)
	return profile.SourceAssetType, ok && profile.SourceAssetType != ""
}

func ParserDialectForAssetType(assetType pipeline.AssetType) (string, bool) {
	profile, ok := sqlConnectionProfileForAssetType(assetType)
	return profile.ParserDialect, ok && profile.ParserDialect != ""
}

func AnalyzerDialectForAssetType(assetType pipeline.AssetType) (string, bool) {
	profile, ok := sqlConnectionProfileForAssetType(assetType)
	return profile.AnalyzerDialect, ok && profile.AnalyzerDialect != ""
}

func AnalyzerDialectForConnectionType(connectionType string) (string, bool) {
	profile, ok := ConnectionProfileForType(connectionType)
	return profile.AnalyzerDialect, ok && profile.AnalyzerDialect != ""
}

func FormatterDialectForAssetType(assetType pipeline.AssetType) (string, bool) {
	profile, ok := sqlConnectionProfileForAssetType(assetType)
	return profile.FormatterDialect, ok && profile.FormatterDialect != ""
}

func FingerprintDialectForAssetType(assetType pipeline.AssetType) (string, bool) {
	profile, ok := sqlConnectionProfileForAssetType(assetType)
	return profile.FingerprintDialect, ok && profile.FingerprintDialect != ""
}

// ConnectionTypeDecision reports whether a Bruin connection family is either
// supported by the SQL profile registry or explicitly unsupported.
func ConnectionTypeDecision(connectionType string) (supported bool, reason string, known bool) {
	canonical := NormalizeConnectionType(connectionType)
	if _, ok := profilesByCanonicalType[canonical]; ok {
		return true, "", true
	}
	reason, ok := unsupportedConnectionTypes[canonical]
	return false, reason, ok
}

func IsQueryAssetType(assetType pipeline.AssetType) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(assetType))), ".sql")
}

func IsQuerySensorAssetType(assetType pipeline.AssetType) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(assetType))), ".sensor.query")
}

func IsSourceAssetType(assetType pipeline.AssetType) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(assetType))), ".source")
}

func sqlConnectionProfileForAssetType(assetType pipeline.AssetType) (ConnectionProfile, bool) {
	if !IsQueryAssetType(assetType) && !IsQuerySensorAssetType(assetType) {
		return ConnectionProfile{}, false
	}
	return ConnectionProfileForAssetType(assetType)
}

func isLegacyAssetType(assetType pipeline.AssetType) bool {
	return assetType == pipeline.AssetTypeFabricQueryLegacy
}
