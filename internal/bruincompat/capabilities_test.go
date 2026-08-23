package bruincompat

import (
	"sort"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBruinConnectionMappingsHaveExplicitCapabilityDecision(t *testing.T) {
	t.Parallel()

	assetTypes := make([]pipeline.AssetType, 0, len(pipeline.AssetTypeConnectionMapping))
	for assetType := range pipeline.AssetTypeConnectionMapping {
		assetTypes = append(assetTypes, assetType)
	}
	sort.Slice(assetTypes, func(i, j int) bool { return assetTypes[i] < assetTypes[j] })

	for _, assetType := range assetTypes {
		assetType := assetType
		t.Run(string(assetType), func(t *testing.T) {
			connectionType := pipeline.AssetTypeConnectionMapping[assetType]
			supported, reason, known := ConnectionTypeDecision(connectionType)
			require.Truef(t, known, "Bruin connection type %q needs an explicit supported/unsupported decision", connectionType)
			if supported {
				profile, ok := ConnectionProfileForAssetType(assetType)
				require.True(t, ok)
				assert.Equal(t, NormalizeConnectionType(connectionType), profile.CanonicalType)
				return
			}
			assert.NotEmpty(t, reason)
		})
	}
}

func TestConnectionProfilesDerivePreferredBruinAssetTypes(t *testing.T) {
	t.Parallel()

	queryType, ok := QueryAssetTypeForConnectionType("gcp")
	require.True(t, ok)
	assert.Equal(t, pipeline.AssetTypeBigqueryQuery, queryType)

	queryType, ok = QueryAssetTypeForConnectionType("fabric")
	require.True(t, ok)
	assert.Equal(t, pipeline.AssetTypeFabricQuery, queryType)

	sourceType, ok := SourceAssetTypeForConnectionType("pg")
	require.True(t, ok)
	assert.Equal(t, pipeline.AssetTypePostgresSource, sourceType)
}

func TestConnectionProfilesKeepConsumerDialectDecisionsExplicit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assetType   pipeline.AssetType
		parser      string
		analyzer    string
		formatter   string
		fingerprint string
	}{
		{pipeline.AssetTypeRedshiftQuery, "redshift", "postgres", "redshift", "redshift"},
		{pipeline.AssetTypeStarRocksQuery, "starrocks", "mysql", "generic", "generic"},
		{pipeline.AssetTypeDremioQuerySensor, "trino", "trino", "generic", "generic"},
		{pipeline.AssetTypeMotherduckQuery, "duckdb", "duckdb", "duckdb", "generic"},
		{pipeline.AssetTypeVerticaQuery, "postgres", "postgres", "postgresql", "generic"},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.assetType), func(t *testing.T) {
			parser, ok := ParserDialectForAssetType(test.assetType)
			require.True(t, ok)
			analyzer, ok := AnalyzerDialectForAssetType(test.assetType)
			require.True(t, ok)
			formatter, ok := FormatterDialectForAssetType(test.assetType)
			require.True(t, ok)
			fingerprint, ok := FingerprintDialectForAssetType(test.assetType)
			require.True(t, ok)
			assert.Equal(t, test.parser, parser)
			assert.Equal(t, test.analyzer, analyzer)
			assert.Equal(t, test.formatter, formatter)
			assert.Equal(t, test.fingerprint, fingerprint)
		})
	}
}

func TestConnectionProfileAliasesAreCallerOwned(t *testing.T) {
	t.Parallel()

	profile, ok := ConnectionProfileForType("postgres")
	require.True(t, ok)
	require.NotEmpty(t, profile.Aliases)
	profile.Aliases[0] = "changed"

	again, ok := ConnectionProfileForType("postgres")
	require.True(t, ok)
	assert.NotEqual(t, "changed", again.Aliases[0])
}
