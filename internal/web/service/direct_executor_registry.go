package service

import (
	"context"
	"fmt"
	"time"

	"github.com/bruin-data/bruin/pkg/ansisql"
	ath "github.com/bruin-data/bruin/pkg/athena"
	bq "github.com/bruin-data/bruin/pkg/bigquery"
	ch "github.com/bruin-data/bruin/pkg/clickhouse"
	"github.com/bruin-data/bruin/pkg/config"
	dbsql "github.com/bruin-data/bruin/pkg/databricks"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	fw "github.com/bruin-data/bruin/pkg/fabric"
	bruiningestr "github.com/bruin-data/bruin/pkg/ingestr"
	"github.com/bruin-data/bruin/pkg/jinja"
	ms "github.com/bruin-data/bruin/pkg/mssql"
	my "github.com/bruin-data/bruin/pkg/mysql"
	"github.com/bruin-data/bruin/pkg/pipeline"
	pg "github.com/bruin-data/bruin/pkg/postgres"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/redshift"
	"github.com/bruin-data/bruin/pkg/s3"
	"github.com/bruin-data/bruin/pkg/scheduler"
	sf "github.com/bruin-data/bruin/pkg/snowflake"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	sr "github.com/bruin-data/bruin/pkg/starrocks"
	syn "github.com/bruin-data/bruin/pkg/synapse"
	tri "github.com/bruin-data/bruin/pkg/trino"
	vert "github.com/bruin-data/bruin/pkg/vertica"
	"github.com/spf13/afero"

	"renart/internal/bruincompat"
	"renart/internal/web/duckcoord"
	"renart/internal/web/duckdbsession"
	"renart/internal/web/runstate"
)

func buildDirectMainExecutors(manager config.ConnectionAndDetailsGetter, renderer *jinja.Renderer, parser *sqlparser.SQLParser, pl *pipeline.Pipeline, cfg *config.Config, registry *runstate.Registry, coordinator *duckcoord.Coordinator, sessions *duckdbsession.Manager, workspaceRoot string, disableDuckDBFilesystemAccess bool, fullRefresh bool, sensorMode string) (map[pipeline.AssetType]bruinexecutor.Config, error) {
	executors := make(map[pipeline.AssetType]bruinexecutor.Config, len(bruinexecutor.DefaultExecutorsV2))
	for assetType, cfg := range bruinexecutor.DefaultExecutorsV2 {
		if cfg == nil {
			executors[assetType] = nil
			continue
		}
		cloned := make(bruinexecutor.Config, len(cfg))
		for instanceType, operator := range cfg {
			cloned[instanceType] = operator
		}
		executors[assetType] = cloned
	}
	for assetType := range executors {
		if isDirectRunAssetTypeSupported(assetType) {
			continue
		}
		if executors[assetType] == nil {
			executors[assetType] = bruinexecutor.Config{}
		}
		executors[assetType][scheduler.TaskInstanceTypeMain] = directUnsupportedOperator{assetType: assetType}
	}

	directFS := afero.NewOsFs()
	wholeFileExtractor := newDirectSQLQueryExtractor(directFS, renderer, pipeline.AssetTypeDuckDBQuery)
	trinoFileExtractor := newDirectSQLQueryExtractor(directFS, renderer, pipeline.AssetTypeTrinoQuery)
	seedOperator := newSlingSeedOperator(manager, renderer, workspaceRoot)
	ensureExecutorConfig := func(assetType pipeline.AssetType) {
		if executors[assetType] == nil {
			executors[assetType] = bruinexecutor.Config{}
		}
	}
	assignExecutor := func(assetType pipeline.AssetType, main bruinexecutor.Operator) {
		ensureExecutorConfig(assetType)
		executors[assetType][scheduler.TaskInstanceTypeMain] = main
	}
	assignSeedExecutor := func(assetType pipeline.AssetType) {
		assignExecutor(assetType, seedOperator)
	}
	assignSensorExecutor := func(assetType pipeline.AssetType, main bruinexecutor.Operator) {
		assignExecutor(assetType, main)
	}

	ensureExecutorConfig(pipeline.AssetTypeDuckDBQuery)
	duckDBMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeDuckDBQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	if coordinator == nil {
		coordinator = duckcoord.New(duckcoord.Options{})
	}
	if sessions == nil {
		sessions = duckdbsession.New(coordinator)
	}
	duckDBFallback := duck.NewBasicOperator(manager, wholeFileExtractor, duckDBMaterializer, parser)
	executors[pipeline.AssetTypeDuckDBQuery][scheduler.TaskInstanceTypeMain] = &directDuckDBOperator{
		manager: manager, extractor: wholeFileExtractor, materializer: duckDBMaterializer,
		fallback: duckDBFallback, sessions: sessions, coordinator: coordinator,
		cfg: cfg, workspaceRoot: workspaceRoot, disableFilesystemAccess: disableDuckDBFilesystemAccess,
	}
	assignSeedExecutor(pipeline.AssetTypeDuckDBSeed)
	ensureExecutorConfig(pipeline.AssetTypeMotherduckQuery)
	executors[pipeline.AssetTypeMotherduckQuery][scheduler.TaskInstanceTypeMain] = duck.NewBasicOperator(manager, wholeFileExtractor, duckDBMaterializer, parser)
	postgresMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypePostgresQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypePostgresQuery)
	executors[pipeline.AssetTypePostgresQuery][scheduler.TaskInstanceTypeMain] = pg.NewBasicOperator(manager, wholeFileExtractor, postgresMaterializer, parser)
	pgMetadataPushOperator := pg.NewMetadataPushOperator(manager)
	assignSeedExecutor(pipeline.AssetTypePostgresSeed)
	ensureExecutorConfig(pipeline.AssetTypeRedshiftQuery)
	executors[pipeline.AssetTypeRedshiftQuery][scheduler.TaskInstanceTypeMain] = pg.NewBasicOperator(manager, wholeFileExtractor, postgresMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeRedshiftSeed)
	bigQueryMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeBigqueryQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeBigqueryQuery)
	executors[pipeline.AssetTypeBigqueryQuery][scheduler.TaskInstanceTypeMain] = bq.NewBasicOperator(manager, wholeFileExtractor, bigQueryMaterializer, parser)
	bqMetadataPushOperator := bq.NewMetadataPushOperator(manager)
	assignSeedExecutor(pipeline.AssetTypeBigquerySeed)
	assignSensorExecutor(pipeline.AssetTypeBigqueryQuerySensor, bq.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeBigqueryTableSensor, bq.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	athenaMaterializer, err := newDirectAthenaExecutionMaterializer(fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeAthenaQuery)
	executors[pipeline.AssetTypeAthenaQuery][scheduler.TaskInstanceTypeMain] = ath.NewBasicOperator(manager, wholeFileExtractor, athenaMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeAthenaSeed)
	assignSensorExecutor(pipeline.AssetTypeAthenaSQLSensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeAthenaTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	databricksMaterializer, _, err := newDirectQueryBatchExecutionMaterializer(pipeline.AssetTypeDatabricksQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeDatabricksQuery)
	executors[pipeline.AssetTypeDatabricksQuery][scheduler.TaskInstanceTypeMain] = dbsql.NewBasicOperator(manager, wholeFileExtractor, databricksMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeDatabricksSeed)
	assignSensorExecutor(pipeline.AssetTypeDatabricksQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeDatabricksTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	assignSeedExecutor(pipeline.AssetTypeDorisSeed)
	assignSensorExecutor(pipeline.AssetTypeDorisQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeDorisTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	fabricMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeFabricQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeFabricQuery)
	executors[pipeline.AssetTypeFabricQuery][scheduler.TaskInstanceTypeMain] = fw.NewBasicOperator(manager, wholeFileExtractor, fabricMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeFabricSeed)
	assignSensorExecutor(pipeline.AssetTypeFabricQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeFabricTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	ensureExecutorConfig(pipeline.AssetTypeFabricQueryLegacy)
	executors[pipeline.AssetTypeFabricQueryLegacy][scheduler.TaskInstanceTypeMain] = fw.NewBasicOperator(manager, wholeFileExtractor, fabricMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeFabricSeedLegacy)
	assignSensorExecutor(pipeline.AssetTypeFabricQuerySensorLegacy, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeFabricTableSensorLegacy, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	mysqlMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeMySQLQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeMySQLQuery)
	executors[pipeline.AssetTypeMySQLQuery][scheduler.TaskInstanceTypeMain] = my.NewBasicOperator(manager, wholeFileExtractor, mysqlMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeMySQLSeed)
	assignSensorExecutor(pipeline.AssetTypeMySQLQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeMySQLTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	snowflakeMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeSnowflakeQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeSnowflakeQuery)
	executors[pipeline.AssetTypeSnowflakeQuery][scheduler.TaskInstanceTypeMain] = sf.NewBasicOperator(manager, wholeFileExtractor, snowflakeMaterializer, parser)
	snowflakeMetadataPushOperator := sf.NewMetadataPushOperator(manager)
	assignSeedExecutor(pipeline.AssetTypeSnowflakeSeed)
	assignSensorExecutor(pipeline.AssetTypeSnowflakeQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeSnowflakeTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	mssqlMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeMsSQLQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeMsSQLQuery)
	executors[pipeline.AssetTypeMsSQLQuery][scheduler.TaskInstanceTypeMain] = ms.NewBasicOperator(manager, wholeFileExtractor, mssqlMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeMsSQLSeed)
	assignSensorExecutor(pipeline.AssetTypeMsSQLQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeMsSQLTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	synapseMaterializer, _, err := newDirectQueryBatchExecutionMaterializer(pipeline.AssetTypeSynapseQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeSynapseQuery)
	executors[pipeline.AssetTypeSynapseQuery][scheduler.TaskInstanceTypeMain] = syn.NewBasicOperator(manager, wholeFileExtractor, synapseMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeSynapseSeed)
	assignSensorExecutor(pipeline.AssetTypeSynapseQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeSynapseTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	clickHouseMaterializer, _, err := newDirectQueryBatchExecutionMaterializer(pipeline.AssetTypeClickHouse, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeClickHouse)
	executors[pipeline.AssetTypeClickHouse][scheduler.TaskInstanceTypeMain] = ch.NewBasicOperator(manager, wholeFileExtractor, clickHouseMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeClickHouseSeed)
	assignSensorExecutor(pipeline.AssetTypeClickHouseQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeClickHouseTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	ensureExecutorConfig(pipeline.AssetTypeStarRocksQuery)
	executors[pipeline.AssetTypeStarRocksQuery][scheduler.TaskInstanceTypeMain] = sr.NewBasicOperator(manager, wholeFileExtractor, fullRefresh, bruincompat.NewDeclareHoister(), parser)
	assignSeedExecutor(pipeline.AssetTypeStarRocksSeed)
	assignSensorExecutor(pipeline.AssetTypeStarRocksQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeStarRocksTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	trinoMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeTrinoQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeTrinoQuery)
	executors[pipeline.AssetTypeTrinoQuery][scheduler.TaskInstanceTypeMain] = tri.NewBasicOperator(manager, trinoFileExtractor, trinoMaterializer, parser)
	assignSeedExecutor(assetTypeTrinoSeed)
	assignSensorExecutor(pipeline.AssetTypeTrinoQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeDremioQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeSailQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	verticaMaterializer, _, err := newDirectStringExecutionMaterializer(pipeline.AssetTypeVerticaQuery, fullRefresh)
	if err != nil {
		return nil, err
	}
	ensureExecutorConfig(pipeline.AssetTypeVerticaQuery)
	executors[pipeline.AssetTypeVerticaQuery][scheduler.TaskInstanceTypeMain] = vert.NewBasicOperator(manager, wholeFileExtractor, verticaMaterializer, parser)
	assignSeedExecutor(pipeline.AssetTypeVerticaSeed)
	assignSensorExecutor(pipeline.AssetTypeVerticaQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeVerticaTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	assignSensorExecutor(pipeline.AssetTypePostgresQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypePostgresTableSensor, ansisql.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	assignSensorExecutor(pipeline.AssetTypeRedshiftQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeRedshiftTableSensor, redshift.NewTableSensor(manager, sensorMode, wholeFileExtractor))
	assignSensorExecutor(pipeline.AssetTypeDuckDBQuerySensor, ansisql.NewQuerySensor(manager, wholeFileExtractor, sensorMode))
	assignSensorExecutor(pipeline.AssetTypeS3KeySensor, s3.NewKeySensor(manager, sensorMode))

	metadataPushOperators := map[directMetadataPushBackend]bruinexecutor.Operator{
		directMetadataPushPostgres:  pgMetadataPushOperator,
		directMetadataPushBigQuery:  bqMetadataPushOperator,
		directMetadataPushSnowflake: snowflakeMetadataPushOperator,
	}
	for assetType, cfg := range executors {
		backend, enabled := directMetadataPushBackendForAssetType(assetType)
		if !enabled {
			continue
		}
		if cfg == nil {
			cfg = bruinexecutor.Config{}
			executors[assetType] = cfg
		}
		cfg[scheduler.TaskInstanceTypeMetadataPush] = metadataPushOperators[backend]
	}
	ensureExecutorConfig(pipeline.AssetTypeOracleQuery)
	executors[pipeline.AssetTypeOracleQuery][scheduler.TaskInstanceTypeMain] = directOracleBasicOperator{connection: manager, extractor: wholeFileExtractor}
	ensureExecutorConfig(pipeline.AssetTypePython)
	executors[pipeline.AssetTypePython][scheduler.TaskInstanceTypeMain] = newRenartPythonOperator(manager, directPythonEnvVariables(pl), renartPythonOperatorOptions{
		registry:          registry,
		enableBroker:      true,
		duckDBCoordinator: coordinator,
		workspaceRoot:     workspaceRoot,
	})
	if pipelineUsesIngestr(pl) {
		ingestrOperator, err := bruiningestr.NewBasicOperator(manager, renderer)
		if err != nil {
			return nil, err
		}
		ensureExecutorConfig(pipeline.AssetTypeIngestr)
		executors[pipeline.AssetTypeIngestr][scheduler.TaskInstanceTypeMain] = ingestrOperator
	}
	sharedCheckExecutors, err := buildDirectCheckExecutors(manager, renderer)
	if err != nil {
		return nil, err
	}
	mergeDirectCheckExecutors(executors, sharedCheckExecutors)
	return executors, nil
}

// buildDirectMainExecutorSequences keeps the configured and full-refresh
// operators side by side. Target lifecycle preflight chooses between them per
// asset only after a fresh warehouse lookup, so one asset's absent target can
// never change another asset's materialization strategy.
func buildDirectMainExecutorSequences(
	manager config.ConnectionAndDetailsGetter,
	renderer *jinja.Renderer,
	parser *sqlparser.SQLParser,
	pl *pipeline.Pipeline,
	cfg *config.Config,
	registry *runstate.Registry,
	coordinator *duckcoord.Coordinator,
	sessions *duckdbsession.Manager,
	workspaceRoot string,
	disableDuckDBFilesystemAccess bool,
	requestedFullRefresh bool,
	sensorMode string,
) (*bruinexecutor.Sequential, *bruinexecutor.Sequential, error) {
	configured, err := buildDirectMainExecutors(
		manager, renderer, parser, pl, cfg, registry, coordinator, sessions,
		workspaceRoot, disableDuckDBFilesystemAccess, requestedFullRefresh, sensorMode,
	)
	if err != nil {
		return nil, nil, err
	}
	configuredSequence := &bruinexecutor.Sequential{TaskTypeMap: configured}
	if requestedFullRefresh {
		return configuredSequence, configuredSequence, nil
	}

	full, err := buildDirectMainExecutors(
		manager, renderer, parser, pl, cfg, registry, coordinator, sessions,
		workspaceRoot, disableDuckDBFilesystemAccess, true, sensorMode,
	)
	if err != nil {
		return nil, nil, err
	}
	return configuredSequence, &bruinexecutor.Sequential{TaskTypeMap: full}, nil
}

func pipelineUsesIngestr(pl *pipeline.Pipeline) bool {
	if pl == nil {
		return false
	}
	for _, asset := range pl.Assets {
		if asset != nil && asset.Type == pipeline.AssetTypeIngestr {
			return true
		}
	}
	return false
}

func directPythonEnvVariables(pl *pipeline.Pipeline) map[string]string {
	if pl == nil {
		return map[string]string{}
	}
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)
	startDate := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 23, 59, 59, 0, time.UTC)
	return jinja.PythonEnvVariables(&startDate, &endDate, &now, pl.Name, "renart-run", false, "")
}

type directOracleBasicOperator struct {
	connection config.ConnectionGetter
	extractor  query.QueryExtractor
}

type directUnsupportedOperator struct {
	assetType pipeline.AssetType
}

func (o directUnsupportedOperator) Run(_ context.Context, _ scheduler.TaskInstance) error {
	return fmt.Errorf("direct execution is not implemented for asset type %q", o.assetType)
}

func (o directOracleBasicOperator) Run(ctx context.Context, ti scheduler.TaskInstance) error {
	return o.RunTask(ctx, ti.GetPipeline(), ti.GetAsset())
}

func (o directOracleBasicOperator) RunTask(ctx context.Context, p *pipeline.Pipeline, asset *pipeline.Asset) error {
	if asset.Materialization.Type != pipeline.MaterializationTypeNone {
		return fmt.Errorf("direct oracle execution only supports assets without materialization")
	}

	extractor, err := o.extractor.CloneForAsset(ctx, p, asset)
	if err != nil {
		return fmt.Errorf("failed to clone extractor for asset %s: %w", asset.Name, err)
	}
	queries, err := extractor.ExtractQueriesFromString(asset.ExecutableFile.Content)
	if err != nil {
		return fmt.Errorf("cannot extract queries from the task file: %w", err)
	}
	if len(queries) == 0 {
		return nil
	}

	connName, err := p.GetConnectionNameForAsset(asset)
	if err != nil {
		return err
	}
	rawConn, err := resolveRuntimeConnection(o.connection, connName)
	if err != nil {
		return err
	}
	if rawConn == nil {
		return config.NewConnectionNotFoundError(ctx, "", connName)
	}
	conn, ok := rawConn.(interface {
		RunQueryWithoutResult(context.Context, *query.Query) error
	})
	if !ok {
		return fmt.Errorf("connection %q cannot run oracle queries", connName)
	}

	for _, queryToRun := range queries {
		ansisql.LogQueryIfVerbose(ctx, ctx.Value(bruinexecutor.KeyPrinter), queryToRun.Query)
		if err := conn.RunQueryWithoutResult(ctx, queryToRun); err != nil {
			return err
		}
	}
	return nil
}
