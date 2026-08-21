package service

import (
	"errors"

	ath "github.com/bruin-data/bruin/pkg/athena"
	bq "github.com/bruin-data/bruin/pkg/bigquery"
	ch "github.com/bruin-data/bruin/pkg/clickhouse"
	dbsql "github.com/bruin-data/bruin/pkg/databricks"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	fw "github.com/bruin-data/bruin/pkg/fabric"
	"github.com/bruin-data/bruin/pkg/jinja"
	ms "github.com/bruin-data/bruin/pkg/mssql"
	my "github.com/bruin-data/bruin/pkg/mysql"
	"github.com/bruin-data/bruin/pkg/pipeline"
	pg "github.com/bruin-data/bruin/pkg/postgres"
	"github.com/bruin-data/bruin/pkg/query"
	sf "github.com/bruin-data/bruin/pkg/snowflake"
	syn "github.com/bruin-data/bruin/pkg/synapse"
	tri "github.com/bruin-data/bruin/pkg/trino"
	vert "github.com/bruin-data/bruin/pkg/vertica"
	"github.com/spf13/afero"

	"renart/internal/bruincompat"
)

// newDirectStringExecutionMaterializer is the single construction seam for
// hook-aware, string-returning materializers shared by direct execution and
// read-only rendering. Keeping the concrete Bruin materializer and DECLARE
// hoister here prevents the renderer from growing a second materializer
// registry that can drift from execution.
func newDirectStringExecutionMaterializer(assetType pipeline.AssetType, fullRefresh bool) (pipeline.HookWrapperMaterializer, bool, error) {
	var materializer interface {
		Render(*pipeline.Asset, string) (string, error)
	}
	switch assetType {
	case pipeline.AssetTypeDuckDBQuery, pipeline.AssetTypeMotherduckQuery:
		materializer = duck.NewMaterializer(fullRefresh)
	case pipeline.AssetTypePostgresQuery, pipeline.AssetTypeRedshiftQuery:
		materializer = pg.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeBigqueryQuery:
		materializer = bq.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeMySQLQuery:
		materializer = my.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeSnowflakeQuery:
		materializer = sf.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeMsSQLQuery:
		materializer = ms.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeTrinoQuery:
		materializer = tri.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeVerticaQuery:
		materializer = vert.NewMaterializer(fullRefresh)
	case pipeline.AssetTypeFabricQuery, pipeline.AssetTypeFabricQueryLegacy:
		materializer = fw.NewMaterializer(fullRefresh)
	default:
		return pipeline.HookWrapperMaterializer{}, false, nil
	}

	// Bruin passes the same DECLARE hoister to every hook-aware string
	// materializer. Renart preserves that behavior with its shared Golyglot
	// parser so previews and direct runs cannot drift when a multi-statement
	// script contains a top-level DECLARE.
	hoister := bruincompat.NewDeclareHoister()
	return pipeline.HookWrapperMaterializer{
		Mat:     materializer,
		Hoister: hoister,
	}, true, nil
}

// newDirectQueryBatchExecutionMaterializer is the shared construction seam for
// SQL operators whose runtime submits an ordered list of statements. The hook
// wrapper must stay outside the refresh-restriction selector so pre/post hooks
// are retained regardless of which per-asset materializer is selected.
func newDirectQueryBatchExecutionMaterializer(assetType pipeline.AssetType, fullRefresh bool) (*directQueryBatchExecutionMaterializer, bool, error) {
	materializer, supported := newDirectQueryBatchBaseMaterializer(assetType, fullRefresh)
	if !supported {
		return nil, false, nil
	}

	hoister := bruincompat.NewDeclareHoister()
	return &directQueryBatchExecutionMaterializer{
		materializer: materializer,
		hoister:      hoister,
	}, true, nil
}

func newDirectQueryBatchBaseMaterializer(assetType pipeline.AssetType, fullRefresh bool) (queryBatchMaterializer, bool) {
	var materializer queryBatchMaterializer
	switch assetType {
	case pipeline.AssetTypeDatabricksQuery:
		materializer = refreshRestrictedQueryBatchMaterializer{
			configured: dbsql.NewMaterializer(false),
			full:       dbsql.NewMaterializer(fullRefresh),
		}
	case pipeline.AssetTypeClickHouse:
		materializer = refreshRestrictedQueryBatchMaterializer{
			configured: ch.NewMaterializer(false),
			full:       ch.NewMaterializer(fullRefresh),
		}
	case pipeline.AssetTypeSynapseQuery:
		materializer = refreshRestrictedQueryBatchMaterializer{
			configured: syn.NewMaterializer(false),
			full:       syn.NewMaterializer(fullRefresh),
		}
	default:
		return nil, false
	}
	return materializer, true
}

func newDirectAthenaExecutionMaterializer(fullRefresh bool) (athenaBatchMaterializer, error) {
	hoister := bruincompat.NewDeclareHoister()
	return pipeline.HookWrapperMaterializerListWithLocation{
		Mat: refreshRestrictedAthenaMaterializer{
			configured: ath.NewMaterializer(false),
			full:       ath.NewMaterializer(fullRefresh),
		},
		Hoister: hoister,
	}, nil
}

func newDirectSQLQueryExtractor(fs afero.Fs, renderer jinja.RendererInterface, assetType pipeline.AssetType) query.QueryExtractor {
	if assetType == pipeline.AssetTypeTrinoQuery {
		return &query.FileQuerySplitterExtractor{Fs: fs, Renderer: renderer}
	}
	return &query.WholeFileExtractor{Fs: fs, Renderer: renderer}
}

func supportsDirectStringExecutionRender(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	switch asset.Type {
	case pipeline.AssetTypeDuckDBQuery,
		pipeline.AssetTypeMotherduckQuery,
		pipeline.AssetTypePostgresQuery,
		pipeline.AssetTypeRedshiftQuery,
		pipeline.AssetTypeMySQLQuery,
		pipeline.AssetTypeMsSQLQuery,
		pipeline.AssetTypeTrinoQuery,
		pipeline.AssetTypeVerticaQuery,
		pipeline.AssetTypeFabricQuery,
		pipeline.AssetTypeFabricQueryLegacy:
		return true
	default:
		return false
	}
}

func supportsDirectExecutionHooks(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	switch asset.Type {
	case pipeline.AssetTypeDuckDBQuery,
		pipeline.AssetTypeMotherduckQuery,
		pipeline.AssetTypePostgresQuery,
		pipeline.AssetTypeRedshiftQuery,
		pipeline.AssetTypeBigqueryQuery,
		pipeline.AssetTypeAthenaQuery,
		pipeline.AssetTypeDatabricksQuery,
		pipeline.AssetTypeFabricQuery,
		pipeline.AssetTypeFabricQueryLegacy,
		pipeline.AssetTypeMySQLQuery,
		pipeline.AssetTypeSnowflakeQuery,
		pipeline.AssetTypeMsSQLQuery,
		pipeline.AssetTypeSynapseQuery,
		pipeline.AssetTypeClickHouse,
		pipeline.AssetTypeTrinoQuery,
		pipeline.AssetTypeVerticaQuery:
		return true
	default:
		return false
	}
}

// Some Bruin multi-statement materializers predate the shared pipeline
// materializer's per-asset refresh restriction. Keep both run-scoped variants
// and select at Render time so a pipeline can full-refresh unrestricted assets
// while preserving the configured strategy for restricted siblings.
type queryBatchMaterializer interface {
	Render(*pipeline.Asset, string) ([]string, error)
	LogIfFullRefreshAndDDL(interface{}, *pipeline.Asset) error
}

type queryBatchRenderer interface {
	Render(*pipeline.Asset, string) ([]string, error)
}

// directQueryBatchExecutionMaterializer owns the exact base materializer and
// DECLARE hoister used by direct execution. Rendering can observe the wrapper's
// final ordered slice through this same object without building a parallel
// materializer stack or joining elements that the warehouse receives as
// separate batches.
type directQueryBatchExecutionMaterializer struct {
	materializer queryBatchMaterializer
	hoister      pipeline.DeclareHoister
}

func (m *directQueryBatchExecutionMaterializer) Render(asset *pipeline.Asset, query string) ([]string, error) {
	if m == nil || m.materializer == nil {
		return nil, errors.New("direct query batch materializer is not configured")
	}
	return pipeline.HookWrapperMaterializerList{
		Mat:     m.materializer,
		Hoister: m.hoister,
	}.Render(asset, query)
}

func (m *directQueryBatchExecutionMaterializer) LogIfFullRefreshAndDDL(writer interface{}, asset *pipeline.Asset) error {
	if m == nil || m.materializer == nil {
		return errors.New("direct query batch materializer is not configured")
	}
	return m.materializer.LogIfFullRefreshAndDDL(writer, asset)
}

type refreshRestrictedQueryBatchMaterializer struct {
	configured queryBatchRenderer
	full       queryBatchRenderer
}

func (m refreshRestrictedQueryBatchMaterializer) selected(asset *pipeline.Asset) queryBatchRenderer {
	if assetRefreshRestricted(asset) {
		return m.configured
	}
	return m.full
}

func (m refreshRestrictedQueryBatchMaterializer) Render(asset *pipeline.Asset, query string) ([]string, error) {
	return m.selected(asset).Render(asset, query)
}

func (m refreshRestrictedQueryBatchMaterializer) LogIfFullRefreshAndDDL(writer interface{}, asset *pipeline.Asset) error {
	logger, ok := m.selected(asset).(interface {
		LogIfFullRefreshAndDDL(interface{}, *pipeline.Asset) error
	})
	if !ok {
		return nil
	}
	return logger.LogIfFullRefreshAndDDL(writer, asset)
}

type athenaBatchMaterializer interface {
	Render(*pipeline.Asset, string, string) ([]string, error)
	LogIfFullRefreshAndDDL(interface{}, *pipeline.Asset) error
}

type refreshRestrictedAthenaMaterializer struct {
	configured athenaBatchMaterializer
	full       athenaBatchMaterializer
}

func (m refreshRestrictedAthenaMaterializer) selected(asset *pipeline.Asset) athenaBatchMaterializer {
	if assetRefreshRestricted(asset) {
		return m.configured
	}
	return m.full
}

func (m refreshRestrictedAthenaMaterializer) Render(asset *pipeline.Asset, query, location string) ([]string, error) {
	return m.selected(asset).Render(asset, query, location)
}

func (m refreshRestrictedAthenaMaterializer) LogIfFullRefreshAndDDL(writer interface{}, asset *pipeline.Asset) error {
	return m.selected(asset).LogIfFullRefreshAndDDL(writer, asset)
}

func assetRefreshRestricted(asset *pipeline.Asset) bool {
	return asset != nil && asset.RefreshRestricted != nil && *asset.RefreshRestricted
}
