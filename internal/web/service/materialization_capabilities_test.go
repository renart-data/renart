package service

import (
	"context"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/model"
)

func TestWorkspaceExposesCompleteMaterializationContract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, ".git/keep", "")
	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "name: analytics\n")
	writeWorkspaceFile(t, root, "analytics/assets/events.sql", `/* @bruin
name: analytics.events
type: bq.sql
description: Retained customer event history.
materialization:
  type: table
  strategy: time_interval
  incremental_key: event_date
  time_granularity: date
  partition_by: event_date
  cluster_by:
    - customer_id
columns:
  - name: event_date
    type: date
  - name: customer_id
    type: string
@bruin */
select current_date() as event_date, 'customer' as customer_id
`)

	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	require.NoError(t, err)
	require.Empty(t, state.Errors)
	require.Len(t, state.Pipelines, 1)
	require.Len(t, state.Pipelines[0].Assets, 1)

	asset := state.Pipelines[0].Assets[0]
	assert.Equal(t, "Retained customer event history.", asset.Description)
	assert.Equal(t, "table", asset.MaterializationType)
	assert.Equal(t, "time_interval", asset.MaterializationStrategy)
	assert.Equal(t, "event_date", asset.IncrementalKey)
	assert.Equal(t, "date", asset.TimeGranularity)
	assert.Equal(t, "event_date", asset.PartitionBy)
	assert.Equal(t, []string{"customer_id"}, asset.ClusterBy)
	assert.True(t, asset.SupportsFullRefresh)

	var interval model.MaterializationCapability
	for _, capability := range asset.MaterializationCapabilities {
		if capability.Mode == materializationModeTimeInterval {
			interval = capability
			break
		}
	}
	require.Equal(t, materializationModeTimeInterval, interval.Mode)
	assert.True(t, interval.RequiresIncrementalKey)
	assert.True(t, interval.RequiresTimeGranularity)
	assert.True(t, interval.SupportsPartitionBy)
	assert.True(t, interval.SupportsClusterBy)
}

func TestMaterializationProfilesMatchExecutionPaths(t *testing.T) {
	t.Parallel()

	t.Run("duckdb SQL exposes every guided runtime mode", func(t *testing.T) {
		t.Parallel()
		profile := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, "")
		assert.ElementsMatch(t, []string{
			materializationModeNone,
			materializationModeView,
			materializationModeReplace,
			materializationModeTruncate,
			materializationModeAppend,
			materializationModeMerge,
			materializationModeDeleteInsert,
			materializationModeTimeInterval,
		}, materializationProfileModes(profile))
		assert.Contains(t, materializationCapabilityModes(editableMaterializationCapabilities(profile)), materializationModeTimeInterval)
		assert.True(t, profile.SupportsFullRefresh)
	})

	t.Run("warehouse differences are preserved", func(t *testing.T) {
		t.Parallel()

		fabric := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeFabricQuery}, "")
		assert.NotContains(t, materializationProfileModes(fabric), materializationModeTimeInterval)

		trino := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeTrinoQuery}, "")
		assert.NotContains(t, materializationProfileModes(trino), materializationModeMerge)

		clickHouse := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeClickHouse}, "")
		assert.NotContains(t, materializationProfileModes(clickHouse), materializationModeMerge)
		replace, ok := materializationCapabilityForMode(clickHouse, materializationModeReplace)
		require.True(t, ok)
		assert.True(t, replace.RequiresPrimaryKey)
		deleteInsert, ok := materializationCapabilityForMode(clickHouse, materializationModeDeleteInsert)
		require.True(t, ok)
		assert.True(t, deleteInsert.RequiresIncrementalKey)
		assert.True(t, deleteInsert.RequiresPrimaryKey)

		oracle := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeOracleQuery}, "")
		assert.Equal(t, []string{materializationModeNone}, materializationProfileModes(oracle))
	})

	t.Run("table layout fields follow warehouse renderers", func(t *testing.T) {
		t.Parallel()

		bigQuery := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeBigqueryQuery}, "")
		bigQueryInterval, ok := materializationCapabilityForMode(bigQuery, materializationModeTimeInterval)
		require.True(t, ok)
		assert.True(t, bigQueryInterval.SupportsPartitionBy)
		assert.True(t, bigQueryInterval.SupportsClusterBy)

		athena := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeAthenaQuery}, "")
		athenaReplace, ok := materializationCapabilityForMode(athena, materializationModeReplace)
		require.True(t, ok)
		assert.True(t, athenaReplace.SupportsPartitionBy)
		assert.False(t, athenaReplace.SupportsClusterBy)

		snowflake := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeSnowflakeQuery}, "")
		snowflakeAppend, ok := materializationCapabilityForMode(snowflake, materializationModeAppend)
		require.True(t, ok)
		assert.False(t, snowflakeAppend.SupportsPartitionBy)
		assert.True(t, snowflakeAppend.SupportsClusterBy)

		duckDB := materializationProfileFor(&pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, "")
		duckReplace, ok := materializationCapabilityForMode(duckDB, materializationModeReplace)
		require.True(t, ok)
		assert.False(t, duckReplace.SupportsPartitionBy)
		assert.False(t, duckReplace.SupportsClusterBy)
	})

	t.Run("loader assets only expose Sling modes", func(t *testing.T) {
		t.Parallel()
		for _, assetType := range []pipeline.AssetType{pipeline.AssetType("api"), pipeline.AssetType("load")} {
			profile := materializationProfileFor(&pipeline.Asset{Type: assetType}, "duckdb")
			assert.Equal(t, []string{
				materializationModeReplace,
				materializationModeTruncate,
				materializationModeAppend,
				materializationModeMerge,
			}, materializationProfileModes(profile))
			assert.True(t, profile.SupportsFullRefresh)
		}
	})

	t.Run("python follows its concrete load leg", func(t *testing.T) {
		t.Parallel()
		asset := &pipeline.Asset{Type: pipeline.AssetTypePython}

		duckDB := materializationProfileFor(asset, "duckdb")
		assert.Contains(t, materializationProfileModes(duckDB), materializationModeDeleteInsert)
		duckMerge, ok := materializationCapabilityForMode(duckDB, materializationModeMerge)
		require.True(t, ok)
		assert.False(t, duckMerge.SupportsIncrementalKey)

		postgres := materializationProfileFor(asset, "postgres")
		assert.NotContains(t, materializationProfileModes(postgres), materializationModeDeleteInsert)
		postgresMerge, ok := materializationCapabilityForMode(postgres, materializationModeMerge)
		require.True(t, ok)
		assert.True(t, postgresMerge.SupportsIncrementalKey)
		assert.True(t, postgres.SupportsFullRefresh)
	})

	t.Run("dedicated non-query runtimes do not get a generic editor", func(t *testing.T) {
		t.Parallel()
		for _, assetType := range []pipeline.AssetType{
			pipeline.AssetTypeIngestr,
			pipeline.AssetTypeR,
			pipeline.AssetTypeDuckDBSeed,
			pipeline.AssetTypeDuckDBQuerySensor,
		} {
			assert.Empty(t, materializationProfileFor(&pipeline.Asset{Type: assetType}, "duckdb").Modes)
		}
	})

	t.Run("refresh restricted suppresses the action without changing modes", func(t *testing.T) {
		t.Parallel()
		restricted := true
		profile := materializationProfileFor(&pipeline.Asset{
			Type:              pipeline.AssetType("load"),
			RefreshRestricted: &restricted,
		}, "duckdb")
		assert.NotEmpty(t, profile.Modes)
		assert.False(t, profile.SupportsFullRefresh)
	})
}

func TestValidateMaterializationCapability(t *testing.T) {
	t.Parallel()

	clickHouseMerge := &pipeline.Asset{
		Type: pipeline.AssetTypeClickHouse,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategyMerge,
		},
	}
	assert.ErrorContains(t, validateMaterializationCapability(clickHouseMerge, ""), "not supported")

	advancedDuckDB := &pipeline.Asset{
		Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategySCD2ByTime,
		},
	}
	assert.NoError(t, validateMaterializationCapability(advancedDuckDB, ""), "supported hand-authored advanced SQL remains a passthrough")

	advancedTrino := &pipeline.Asset{
		Type: pipeline.AssetTypeTrinoQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategySCD2ByColumn,
		},
	}
	assert.NoError(t, validateMaterializationCapability(advancedTrino, ""))

	unsupportedAdvancedMSSQL := &pipeline.Asset{
		Type: pipeline.AssetTypeMsSQLQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategySCD2ByTime,
		},
	}
	assert.ErrorContains(t, validateMaterializationCapability(unsupportedAdvancedMSSQL, ""), "not supported")

	unknownDuckDB := &pipeline.Asset{
		Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategy("future_strategy"),
		},
	}
	assert.ErrorContains(t, validateMaterializationCapability(unknownDuckDB, ""), "not supported")

	advancedOracle := &pipeline.Asset{
		Type: pipeline.AssetTypeOracleQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategySCD2ByTime,
		},
	}
	assert.ErrorContains(t, validateMaterializationCapability(advancedOracle, ""), "not supported")

	viewWithTableStrategy := &pipeline.Asset{
		Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeView,
			Strategy: pipeline.MaterializationStrategyMerge,
		},
	}
	assert.ErrorContains(t, validateMaterializationCapability(viewWithTableStrategy, ""), "does not support strategy")
}

func TestSupportsFullRefreshForCurrentMaterialization(t *testing.T) {
	t.Parallel()
	view := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery, Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeView}}
	assert.False(t, supportsFullRefreshForAsset(view, materializationProfileFor(view, "")))

	table := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery, Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable}}
	assert.True(t, supportsFullRefreshForAsset(table, materializationProfileFor(table, "")))

	ddl := &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery, Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable, Strategy: pipeline.MaterializationStrategyDDL}}
	assert.False(t, supportsFullRefreshForAsset(ddl, materializationProfileFor(ddl, "")))

	loaderDefault := &pipeline.Asset{Type: pipeline.AssetType("api")}
	assert.True(t, supportsFullRefreshForAsset(loaderDefault, materializationProfileFor(loaderDefault, "duckdb")))

	restricted := true
	loaderDefault.RefreshRestricted = &restricted
	assert.False(t, supportsFullRefreshForAsset(loaderDefault, materializationProfileFor(loaderDefault, "duckdb")))
}

func TestMaterializationDestinationType(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "postgres", materializationDestinationType(
		&pipeline.Asset{Type: pipeline.AssetTypePython, Connection: "warehouse"},
		&pipeline.Pipeline{},
		map[string]string{"Warehouse": "pg"},
	))
	assert.Equal(t, "duckdb", materializationDestinationType(
		&pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery},
		nil,
		nil,
	))
	assert.Empty(t, materializationDestinationType(
		&pipeline.Asset{Type: pipeline.AssetTypePython, Connection: "unclassified"},
		&pipeline.Pipeline{Assets: []*pipeline.Asset{{Type: pipeline.AssetTypeDuckDBQuery}}},
		nil,
	), "an explicit connection with unknown type must not inherit the pipeline majority")
}

func materializationProfileModes(profile materializationProfile) []string {
	modes := make([]string, 0, len(profile.Modes))
	for _, capability := range profile.Modes {
		modes = append(modes, capability.Mode)
	}
	return modes
}

func materializationCapabilityModes(capabilities []model.MaterializationCapability) []string {
	modes := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		modes = append(modes, capability.Mode)
	}
	return modes
}
