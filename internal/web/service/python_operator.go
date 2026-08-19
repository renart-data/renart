package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/env"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/pipeline"
	bruinpython "github.com/bruin-data/bruin/pkg/python"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/bruin-data/bruin/pkg/sqlparser"

	"renart/internal/pysdk"
	"renart/internal/web/duckcoord"
	"renart/internal/web/pybroker"
	"renart/internal/web/runstate"
)

// renartPythonOperator runs Python assets. It replaces bruin's LocalOperator
// so that (a) every invocation carries the renart SDK and a per-task run
// broker — `renart.query()` executes in this process on the project's
// connections, credentials never enter Python — and (b) table materialization
// stages the materialize() result as Parquet and loads it Go-side (native SQL
// for DuckDB, Sling elsewhere) instead of shelling out to ingestr.
//
// Bruin behaviors kept for compatibility: BRUIN_* env vars, `secrets:`
// credential injection for users who opt in, the python version resolution
// (image tag → requires-python → 3.11), and the three dependency modes
// (pyproject.toml / requirements.txt / none) driven through uv.
type renartPythonOperator struct {
	manager      config.ConnectionAndDetailsGetter
	envVariables map[string]string
	registry     *runstate.Registry
	enableBroker bool
	// brokerDefaultConnection may differ from the asset's materialization
	// connection. Notebook cells, for example, query their live session through
	// a custom callback and only stage their result as Parquet.
	brokerDefaultConnection string
	// brokerRunQuery overrides project-connection lookup. Notebook cells use it
	// to query the already-open notebook session without exposing its file path.
	brokerRunQuery func(ctx context.Context, connection, sql string) (*query.QueryResult, error)
	// Notebook runs reuse the notebook service's long-lived SQL parser instead
	// of initializing a new parser for each task.
	brokerValidateSQL func(sql string) error
	brokerUsedTables  func(sql string) ([]string, error)
	// stagingOutputPath makes the operator stop after collecting materialize()
	// into this Parquet file. Notebook runners then load it directly into their
	// existing session instead of creating an intermediate DuckDB database.
	stagingOutputPath string
	duckDBCoordinator *duckcoord.Coordinator
	workspaceRoot     string

	uv     *bruinpython.UvChecker
	cmd    *bruinpython.CommandRunner
	module *bruinpython.ModulePathFinder
}

type renartPythonOperatorOptions struct {
	registry                *runstate.Registry
	enableBroker            bool
	brokerDefaultConnection string
	brokerRunQuery          func(ctx context.Context, connection, sql string) (*query.QueryResult, error)
	brokerValidateSQL       func(sql string) error
	brokerUsedTables        func(sql string) ([]string, error)
	stagingOutputPath       string
	duckDBCoordinator       *duckcoord.Coordinator
	workspaceRoot           string
}

type uvEnsurer interface {
	EnsureUvInstalled(context.Context) (string, error)
}

// uvPathCache avoids a `uv self version` subprocess on every Python cell. The
// binary lives in Renart/Bruin's managed home; a cheap stat still invalidates
// the cache if it is removed between runs.
type uvPathCache struct {
	mu   sync.Mutex
	path string
}

var renartUVCache uvPathCache

func (c *uvPathCache) ensure(ctx context.Context, checker uvEnsurer) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path != "" {
		if _, err := os.Stat(c.path); err == nil {
			return c.path, nil
		}
		c.path = ""
	}
	path, err := checker.EnsureUvInstalled(ctx)
	if err != nil {
		return "", err
	}
	c.path = path
	return path, nil
}

func newRenartPythonOperator(manager config.ConnectionAndDetailsGetter, envVariables map[string]string, opts renartPythonOperatorOptions) *renartPythonOperator {
	if envVariables == nil {
		envVariables = map[string]string{}
	}
	return &renartPythonOperator{
		manager:                 manager,
		envVariables:            envVariables,
		registry:                opts.registry,
		enableBroker:            opts.enableBroker,
		brokerDefaultConnection: opts.brokerDefaultConnection,
		brokerRunQuery:          opts.brokerRunQuery,
		brokerValidateSQL:       opts.brokerValidateSQL,
		brokerUsedTables:        opts.brokerUsedTables,
		stagingOutputPath:       opts.stagingOutputPath,
		duckDBCoordinator:       opts.duckDBCoordinator,
		workspaceRoot:           opts.workspaceRoot,
		uv:                      &bruinpython.UvChecker{},
		cmd:                     &bruinpython.CommandRunner{},
		module:                  &bruinpython.ModulePathFinder{},
	}
}

func (o *renartPythonOperator) Run(ctx context.Context, ti scheduler.TaskInstance) error {
	return o.RunTask(ctx, ti.GetPipeline(), ti.GetAsset())
}

func (o *renartPythonOperator) RunTask(ctx context.Context, p *pipeline.Pipeline, t *pipeline.Asset) error {
	repo, err := git.FindRepoFromPath(t.ExecutableFile.Path)
	if err != nil {
		return fmt.Errorf("failed to find the repository root for %s: %w", t.ExecutableFile.Path, err)
	}
	modulePath, err := o.module.FindModulePath(repo, &t.ExecutableFile)
	if err != nil {
		return fmt.Errorf("failed to build the module path: %w", err)
	}
	depConfig, err := o.module.FindDependencyConfig(repo.Path, &t.ExecutableFile)
	if err != nil || depConfig == nil {
		depConfig = &bruinpython.DependencyConfig{Type: bruinpython.DependencyTypeNone}
	}

	envVariables, err := o.buildEnv(ctx, p, t)
	if err != nil {
		return err
	}
	if depConfig.Type == bruinpython.DependencyTypePyproject {
		projectRoot := depConfig.ProjectRoot
		if projectRoot == "" {
			projectRoot = repo.Path
		}
		configureUVLinkMode(envVariables, projectRoot, repo.Path)
	}

	output := printerWriter(ctx)

	if o.enableBroker && (o.manager != nil || o.brokerRunQuery != nil) {
		broker, closeBroker, brokerErr := o.startBroker(ctx, p, t, output)
		if brokerErr != nil {
			return brokerErr
		}
		defer closeBroker()
		envVariables["RENART_API_URL"] = broker.URL
		envVariables["RENART_API_TOKEN"] = broker.Token
	}

	uvPath, err := renartUVCache.ensure(ctx, o.uv)
	if err != nil {
		return err
	}

	// The SDK wheel rides along on every invocation via --with. Failing to
	// build it degrades to running without the SDK rather than blocking the
	// asset (import renart will then fail with a normal Python error).
	sdkWheel, wheelErr := pysdk.EnsureWheel()
	if wheelErr != nil {
		fmt.Fprintf(output, "WARNING: the renart SDK is unavailable for this run: %v\n", wheelErr)
		sdkWheel = ""
	}

	run := pythonRun{
		uvPath:    uvPath,
		repo:      repo,
		module:    modulePath,
		depConfig: depConfig,
		asset:     t,
		pipeline:  p,
		env:       envVariables,
		sdkWheel:  sdkWheel,
		output:    output,
	}

	if t.Materialization.Type == pipeline.MaterializationTypeNone {
		return o.runScript(ctx, run)
	}
	return o.runWithMaterialization(ctx, run)
}

// buildEnv assembles the task env: BRUIN_* run context, bruin-compatible
// `secrets:` injection, and the operator's base variables.
func (o *renartPythonOperator) buildEnv(ctx context.Context, p *pipeline.Pipeline, t *pipeline.Asset) (map[string]string, error) {
	base := make(map[string]string, len(o.envVariables))
	for k, v := range o.envVariables {
		base[k] = v
	}
	withContext, err := env.SetupVariables(ctx, p, t, base)
	if err != nil {
		return nil, fmt.Errorf("failed to set up environment variables: %w", err)
	}

	envVariables := make(map[string]string, len(withContext)+8)
	for k, v := range withContext {
		envVariables[k] = v
	}
	envVariables["PYTHONUNBUFFERED"] = "1"
	envVariables["BRUIN_ASSET"] = t.Name
	envVariables["BRUIN_THIS"] = t.Name
	if t.Connection != "" {
		envVariables["BRUIN_CONNECTION"] = t.Connection
	}

	if o.manager == nil && len(t.Secrets) > 0 {
		return nil, fmt.Errorf("this run mode cannot inject secrets")
	}
	connectionTypes := make(map[string]string)
	for _, mapping := range t.Secrets {
		details := o.manager.GetConnectionDetails(mapping.SecretKey)
		if details == nil {
			return nil, fmt.Errorf("there's no secret with the name '%s'", mapping.SecretKey)
		}
		if connType := o.manager.GetConnectionType(mapping.SecretKey); connType != "" {
			connectionTypes[mapping.InjectedKey] = connType
		}
		if generic, ok := details.(*config.GenericConnection); ok {
			envVariables[mapping.InjectedKey] = generic.Value
			continue
		}
		encoded, err := json.Marshal(details)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal the connection for secret '%s': %w", mapping.SecretKey, err)
		}
		envVariables[mapping.InjectedKey] = string(encoded)
	}
	if len(connectionTypes) > 0 {
		encoded, err := json.Marshal(connectionTypes)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal connection types: %w", err)
		}
		envVariables["BRUIN_CONNECTION_TYPES"] = string(encoded)
	}
	return envVariables, nil
}

// startBroker wires a per-task pybroker instance to this operator's
// connection manager, run registry, and run context.
func (o *renartPythonOperator) startBroker(ctx context.Context, p *pipeline.Pipeline, t *pipeline.Asset, output io.Writer) (*pybroker.Broker, func(), error) {
	defaultConnection := o.brokerDefaultConnection
	if defaultConnection == "" {
		if name, err := p.GetConnectionNameForAsset(t); err == nil {
			defaultConnection = name
		}
	}

	doc := pybroker.ContextDocument{
		RunID:      contextString(ctx, pipeline.RunConfigRunID),
		Pipeline:   p.Name,
		Asset:      t.Name,
		Connection: defaultConnection,
	}
	if envName, ok := ctx.Value(config.EnvironmentNameContextKey).(string); ok {
		doc.Environment = envName
	}
	if start, ok := ctx.Value(pipeline.RunConfigStartDate).(time.Time); ok {
		doc.StartDate = start.UTC()
	}
	if end, ok := ctx.Value(pipeline.RunConfigEndDate).(time.Time); ok {
		doc.EndDate = end.UTC()
	}
	if executionDate, ok := ctx.Value(pipeline.RunConfigExecutionDate).(time.Time); ok {
		doc.ExecutionDate = executionDate.UTC()
	}
	if fullRefresh, ok := ctx.Value(pipeline.RunConfigFullRefresh).(bool); ok {
		doc.FullRefresh = fullRefresh
	}
	doc.Vars = p.Variables.Value()

	knownAssets := make([]string, 0, len(p.Assets))
	for _, asset := range p.Assets {
		if asset != nil {
			knownAssets = append(knownAssets, asset.Name)
		}
	}
	declared := make([]string, 0, len(t.Upstreams))
	for _, upstream := range t.Upstreams {
		if upstream.Type == "asset" {
			declared = append(declared, upstream.Value)
		}
	}

	var tools *brokerSQLTools
	validateSQL := o.brokerValidateSQL
	usedTables := o.brokerUsedTables
	if validateSQL == nil || usedTables == nil {
		tools = &brokerSQLTools{dialect: brokerQueryDialect(o.manager, defaultConnection)}
		if validateSQL == nil {
			validateSQL = tools.validateReadOnly
		}
		if usedTables == nil {
			usedTables = tools.usedTables
		}
	}
	runQuery := o.brokerRunQuery
	if runQuery == nil {
		runQuery = o.runBrokerQuery
	}

	broker, err := pybroker.Start(ctx, pybroker.Config{
		Context:           doc,
		DefaultConnection: defaultConnection,
		RunQuery:          runQuery,
		ValidateSQL:       validateSQL,
		UsedTables:        usedTables,
		Registry:          o.registry,
		KnownAssets:       knownAssets,
		DeclaredUpstreams: declared,
		Log:               output,
	})
	if err != nil {
		if tools != nil {
			tools.close()
		}
		return nil, nil, fmt.Errorf("failed to start the python run broker: %w", err)
	}
	return broker, func() {
		broker.Close()
		if tools != nil {
			tools.close()
		}
	}, nil
}

// runBrokerQuery executes one SDK query on a named project connection,
// inside this process — the Python side never sees the credentials.
func (o *renartPythonOperator) runBrokerQuery(ctx context.Context, connectionName, sql string) (*query.QueryResult, error) {
	conn, err := resolveRuntimeConnection(o.manager, connectionName)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %q was not found in the project", connectionName)
	}
	querier, ok := conn.(directSchemaQuerier)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support querying", connectionName)
	}
	return selectWithComplexJSONFallback(ctx, querier, sql)
}

// brokerSQLTools owns the lazily created bruin SQL parser the broker uses for
// the read-only check and table-reference extraction. The parser is created
// on first use (starting it costs real time and most scripts never query) and
// serialized by a mutex (the parser is not safe for concurrent calls).
type brokerSQLTools struct {
	mu      sync.Mutex
	parser  *sqlparser.SQLParser
	failed  bool
	dialect string
}

func (b *brokerSQLTools) ensureParser() *sqlparser.SQLParser {
	if b.parser != nil || b.failed {
		return b.parser
	}
	parser, err := sqlparser.NewSQLParser(false)
	if err != nil {
		b.failed = true
		return nil
	}
	b.parser = parser
	return b.parser
}

func (b *brokerSQLTools) validateReadOnly(sql string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	parser := b.ensureParser()
	if parser == nil {
		return fmt.Errorf("could not initialize SQL validation")
	}
	isSelect, err := parser.IsSingleSelectQuery(sql, b.dialect)
	if err != nil {
		return fmt.Errorf("could not validate query: %w", err)
	}
	if !isSelect {
		return fmt.Errorf("renart.query() only runs read-only single SELECT statements; use the asset's materialization for writes")
	}
	return nil
}

func (b *brokerSQLTools) usedTables(sql string) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	parser := b.ensureParser()
	if parser == nil {
		return nil, nil
	}
	return parser.UsedTables(sql, b.dialect)
}

func (b *brokerSQLTools) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.parser != nil {
		b.parser.Close()
		b.parser = nil
	}
}

// brokerQueryDialect maps the default connection's type to a SQL dialect for
// parsing SDK queries. Unknown types parse as duckdb (renart's default
// warehouse; sqlglot's duckdb dialect accepts standard SQL).
func brokerQueryDialect(manager config.ConnectionAndDetailsGetter, connectionName string) string {
	if manager == nil || connectionName == "" {
		return "duckdb"
	}
	switch manager.GetConnectionType(connectionName) {
	case "postgres", "redshift":
		return "postgres"
	case "snowflake":
		return "snowflake"
	case "google_cloud_platform", "bigquery":
		return "bigquery"
	case "mysql":
		return "mysql"
	case "mssql", "synapse", "fabric":
		return "tsql"
	case "clickhouse":
		return "clickhouse"
	case "databricks":
		return "databricks"
	case "athena":
		return "athena"
	case "trino":
		return "trino"
	case "oracle":
		return "oracle"
	default:
		return "duckdb"
	}
}

// pythonRun carries everything one task invocation needs.
type pythonRun struct {
	uvPath    string
	repo      *git.Repo
	module    string
	depConfig *bruinpython.DependencyConfig
	asset     *pipeline.Asset
	pipeline  *pipeline.Pipeline
	env       map[string]string
	sdkWheel  string
	output    io.Writer
}

func (r pythonRun) pythonVersion() (string, error) {
	if r.asset.Image != "" {
		parts := strings.Split(r.asset.Image, ":")
		if len(parts) > 1 && parts[0] == "python" && bruinpython.AvailablePythonVersions[parts[1]] {
			return parts[1], nil
		}
	}
	if r.depConfig != nil && r.depConfig.RequiresPython != "" {
		return bruinpython.ResolvePythonVersion(r.depConfig.RequiresPython, "3.11")
	}
	return "3.11", nil
}

func (r pythonRun) isPyproject() bool {
	return r.depConfig != nil && r.depConfig.Type == bruinpython.DependencyTypePyproject
}

// moduleForRun returns the module path and the directory uv runs from. In
// pyproject mode uv runs from the project root, so the module path is
// re-based when the pyproject.toml lives in a subdirectory of the repo.
func (r pythonRun) moduleForRun() (string, *git.Repo) {
	if !r.isPyproject() || r.depConfig.ProjectRoot == r.repo.Path {
		return r.module, r.repo
	}
	runRepo := &git.Repo{Path: r.depConfig.ProjectRoot}
	rel, err := filepath.Rel(r.repo.Path, r.depConfig.ProjectRoot)
	if err != nil {
		return r.module, runRepo
	}
	prefix := strings.ReplaceAll(rel, string(os.PathSeparator), ".") + "."
	if strings.HasPrefix(strings.ToLower(r.module), strings.ToLower(prefix)) {
		return r.module[len(prefix):], runRepo
	}
	return r.module, runRepo
}

// uvRunArgs builds the `uv run` invocation for either a module (`--module m`)
// or a script file, injecting the SDK wheel and any extra --with packages.
func (r pythonRun) uvRunArgs(pythonVersion string, target []string, extraWith []string) []string {
	args := []string{"run"}
	if !r.isPyproject() {
		args = append(args, "--no-config", "--no-project")
	}
	args = append(args, "--python", pythonVersion)
	if !r.isPyproject() && r.depConfig != nil && r.depConfig.Type == bruinpython.DependencyTypeRequirementsTxt && r.depConfig.RequirementsTxt != "" {
		args = append(args, "--with-requirements", r.depConfig.RequirementsTxt)
	}
	if r.sdkWheel != "" {
		args = append(args, "--with", r.sdkWheel)
	}
	for _, pkg := range extraWith {
		args = append(args, "--with", pkg)
	}
	return append(args, target...)
}

// runScript runs a Python asset without materialization: the module executes
// for its side effects (which may include renart.query() calls).
func (o *renartPythonOperator) runScript(ctx context.Context, run pythonRun) error {
	pythonVersion, err := run.pythonVersion()
	if err != nil {
		return err
	}
	module, runRepo := run.moduleForRun()
	return o.cmd.Run(ctx, runRepo, &bruinpython.CommandInstance{
		Name:    run.uvPath,
		Args:    run.uvRunArgs(pythonVersion, []string{"--module", module}, nil),
		EnvVars: run.env,
	})
}

// runWithMaterialization collects materialize()'s dataframe into a Parquet
// staging file via the wrapper script, then loads it into the destination
// Go-side — natively for DuckDB, via Sling for other warehouses. No ingestr.
func (o *renartPythonOperator) runWithMaterialization(ctx context.Context, run pythonRun) error {
	t := run.asset
	if t.Materialization.Type != pipeline.MaterializationTypeTable {
		return fmt.Errorf("python assets only support table materialization, got %q", t.Materialization.Type)
	}

	connectionName := ""
	isDuckDB := false
	if o.stagingOutputPath == "" {
		var err error
		connectionName, err = run.pipeline.GetConnectionNameForAsset(t)
		if err != nil {
			return fmt.Errorf("failed to resolve the asset's connection: %w", err)
		}
		isDuckDB = o.destinationIsDuckDB(connectionName)
		if err := validatePythonStrategy(t, isDuckDB); err != nil {
			return err
		}
	}

	pythonVersion, err := run.pythonVersion()
	if err != nil {
		return err
	}

	stagingDir := ""
	parquetPath := o.stagingOutputPath
	if parquetPath == "" {
		stagingDir, err = os.MkdirTemp("", "renart-pymat-")
		if err != nil {
			return fmt.Errorf("failed to allocate the staging directory: %w", err)
		}
		defer os.RemoveAll(stagingDir)
		parquetPath = filepath.Join(stagingDir, "materialize.parquet")
	} else if err := os.MkdirAll(filepath.Dir(parquetPath), 0o700); err != nil {
		return fmt.Errorf("failed to create the staging directory: %w", err)
	} else {
		stagingDir = filepath.Dir(parquetPath)
	}

	module, runRepo := run.moduleForRun()
	rootPath := runRepo.Path

	wrapper := strings.ReplaceAll(pythonParquetTemplate, "$REPO_ROOT", escapeForPython(rootPath))
	wrapper = strings.ReplaceAll(wrapper, "$MODULE_PATH", module)
	wrapper = strings.ReplaceAll(wrapper, "$PARQUET_PATH", escapeForPython(parquetPath))
	wrapperPath := filepath.Join(stagingDir, "renart_materialize.py")
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o600); err != nil {
		return fmt.Errorf("failed to write the materialization wrapper: %w", err)
	}

	// `uv run` performs the project lock/sync itself. The embedded SDK wheel
	// already depends on pyarrow; keep an explicit fallback only if assembling
	// the wheel failed.
	extraWith := []string(nil)
	if run.sdkWheel == "" {
		extraWith = []string{"pyarrow>=15.0.0"}
	}
	err = o.cmd.Run(ctx, runRepo, &bruinpython.CommandInstance{
		Name:    run.uvPath,
		Args:    run.uvRunArgs(pythonVersion, []string{wrapperPath}, extraWith),
		EnvVars: run.env,
	})
	if err != nil {
		return fmt.Errorf("failed to run the asset code: %w", err)
	}

	if _, statErr := os.Stat(parquetPath); os.IsNotExist(statErr) {
		fmt.Fprintln(run.output, "WARNING: materialize() returned None, skipping materialization")
		return nil
	}
	if o.stagingOutputPath != "" {
		return nil
	}
	if err := reportTargetWriteStarting(ctx, t.Name); err != nil {
		return fmt.Errorf("claim the Python materialization target: %w", err)
	}

	fmt.Fprintln(run.output, "Collected the materialize() result, loading it into the destination…")
	if isDuckDB {
		lease, leaseErr := o.acquireDuckDBDestination(ctx, run, connectionName)
		if leaseErr != nil {
			return leaseErr
		}
		err = func() error {
			defer lease.Release()
			return o.loadParquetIntoDuckDB(ctx, run, connectionName, parquetPath)
		}()
	} else {
		err = o.loadParquetViaSling(ctx, run, connectionName, parquetPath)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(run.output, "Loaded the data into the destination.")
	return nil
}

func (o *renartPythonOperator) acquireDuckDBDestination(ctx context.Context, run pythonRun, connectionName string) (*duckcoord.Lease, error) {
	if o.duckDBCoordinator == nil || o.manager == nil {
		return &duckcoord.Lease{}, nil
	}
	rawPath := ""
	switch connection := o.manager.GetConnectionDetails(connectionName).(type) {
	case *config.DuckDBConnection:
		if connection != nil {
			rawPath = connection.Path
		}
	case config.DuckDBConnection:
		rawPath = connection.Path
	default:
		return &duckcoord.Lease{}, nil
	}
	databasePath, err := duckcoord.CanonicalPath(o.workspaceRoot, rawPath)
	if err != nil {
		return nil, err
	}
	return o.duckDBCoordinator.Acquire(ctx, []string{databasePath}, duckcoord.Owner{
		Operation: "materialize python asset",
		Pipeline:  run.pipeline.Name,
		Asset:     run.asset.Name,
		RunID:     contextString(ctx, pipeline.RunConfigRunID),
		OnWait: func(path string) {
			_, _ = fmt.Fprintf(run.output, "Waiting for DuckDB database %s to become available...\n", filepath.Base(path))
		},
	})
}

func (o *renartPythonOperator) destinationIsDuckDB(connectionName string) bool {
	if o.manager == nil {
		return false
	}
	conn := o.manager.GetConnection(connectionName)
	if conn == nil {
		return false
	}
	_, ok := conn.(duck.DuckDBClient)
	return ok
}

// loadParquetIntoDuckDB loads the staging file with dialect-correct strategy
// SQL from bruin's DuckDB materializer, executed on the in-process
// connection: SELECT * FROM read_parquet(...) takes the place of a SQL
// asset's query, so create+replace / append / delete+insert / merge behave
// exactly as they do for SQL assets — and there is no second writer process.
func (o *renartPythonOperator) loadParquetIntoDuckDB(ctx context.Context, run pythonRun, connectionName, parquetPath string) error {
	rawConn, err := resolveRuntimeConnection(o.manager, connectionName)
	if err != nil {
		return err
	}
	conn, ok := rawConn.(duck.DuckDBClient)
	if !ok {
		return fmt.Errorf("connection %q is not a duckdb connection", connectionName)
	}

	fullRefresh, _ := ctx.Value(pipeline.RunConfigFullRefresh).(bool)
	materializer := duck.NewMaterializer(fullRefresh)
	selectFromStaging := fmt.Sprintf("SELECT * FROM read_parquet('%s')", strings.ReplaceAll(parquetPath, "'", "''"))
	rendered, err := materializer.Render(run.asset, selectFromStaging)
	if err != nil {
		return fmt.Errorf("failed to render the materialization query: %w", err)
	}

	if err := conn.CreateSchemaIfNotExist(ctx, run.asset); err != nil {
		return fmt.Errorf("failed to ensure the destination schema: %w", err)
	}
	if err := conn.RunQueryWithoutResult(ctx, &query.Query{Query: rendered}); err != nil {
		return fmt.Errorf("failed to load the data into %s: %w", run.asset.Name, err)
	}
	return nil
}

// loadParquetViaSling loads the staging file into a non-DuckDB destination
// with the Sling CLI — the same engine Load and HTTP API assets use, with the
// same strategy mapping. The connection URI travels via an env-named
// connection so credentials stay off the command line.
func (o *renartPythonOperator) loadParquetViaSling(ctx context.Context, run pythonRun, connectionName, parquetPath string) error {
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: func(chunk []byte) {
		_, _ = run.output.Write(chunk)
	}}
	uri, connectionWarning, err := loadConnectionURIWithWarning(o.manager, connectionName)
	if err != nil {
		return err
	}
	writeSlingConnectionWarning(writer, connectionWarning)
	modeArgs, err := slingMaterializationArgs(ctx, run.asset)
	if err != nil {
		return err
	}

	args := []string{
		"run",
		"--src-stream", "file://" + filepath.ToSlash(parquetPath),
		"--tgt-conn", "RENART_PY_TARGET",
		"--tgt-object", run.asset.Name,
	}
	args = append(args, modeArgs...)
	targetOptions, err := slingTargetOptionsArgs(o.manager, connectionName, nil)
	if err != nil {
		return err
	}
	args = append(args, targetOptions...)

	cmdName, cmdArgs, err := loadCommand(ctx, args, writer)
	if err != nil {
		return err
	}
	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, run.repo.Path, writer)
	cmd.Env = append(cmd.Env,
		"RENART_PY_TARGET="+uri,
	)
	if err := runStreamingCommand(ctx, cmd, writer); err != nil {
		return fmt.Errorf("failed to load the data into %s: %w", run.asset.Name, err)
	}
	return nil
}

// validatePythonStrategy rejects strategies the python load legs cannot
// execute, before any code runs.
func validatePythonStrategy(t *pipeline.Asset, isDuckDB bool) error {
	strategy := t.Materialization.Strategy
	allowed := map[pipeline.MaterializationStrategy]bool{
		pipeline.MaterializationStrategyNone:           true,
		pipeline.MaterializationStrategyCreateReplace:  true,
		pipeline.MaterializationStrategyAppend:         true,
		pipeline.MaterializationStrategyTruncateInsert: true,
		pipeline.MaterializationStrategyMerge:          true,
	}
	if isDuckDB {
		allowed[pipeline.MaterializationStrategyDeleteInsert] = true
	}
	if !allowed[strategy] {
		return fmt.Errorf("materialization strategy %q is not supported for python assets on this destination", strategy)
	}
	return nil
}

func contextString(ctx context.Context, key any) string {
	if value, ok := ctx.Value(key).(string); ok {
		return value
	}
	return ""
}

// printerWriter returns the run's output writer from the context, falling
// back to stdout (matching bruin's operators).
func printerWriter(ctx context.Context) io.Writer {
	if writer, ok := ctx.Value(bruinexecutor.KeyPrinter).(io.Writer); ok && writer != nil {
		return writer
	}
	return os.Stdout
}

func escapeForPython(path string) string {
	return strings.ReplaceAll(path, "\\", "\\\\")
}

// pythonParquetTemplate wraps the asset module: it imports the module, calls
// materialize(), and writes the result to $PARQUET_PATH as Parquet. It
// accepts the same shapes bruin's arrow template does — pandas / polars
// DataFrames, pyarrow Tables, lists of dicts, and generators (each yield
// becomes a row group, so large results never sit in memory whole). The
// missing-file case (materialize() returned None) is detected Go-side.
const pythonParquetTemplate = `
import importlib
import os
import sys
from pathlib import Path


ready_file = os.environ.get("RENART_PYTHON_READY_FILE")
if ready_file:
    Path(ready_file).touch()


def import_module_from_path(module_path, module_name):
    sys.path.insert(0, str(Path(module_path)))
    return importlib.import_module(module_name)


def convert_and_write(df):
    if df is None:
        return  # Go detects the missing staging file and logs a warning.

    import pyarrow as pa
    import pyarrow.parquet as pq

    def derives_from(obj, module_name, type_name):
        return any(
            cls.__name__ == type_name
            and (cls.__module__ == module_name or cls.__module__.startswith(module_name + "."))
            for cls in type(obj).__mro__
        )

    def write_tables(tables):
        iterator = iter(tables)
        try:
            first = next(iterator)
        except StopIteration:
            return
        if not isinstance(first, pa.Table):
            raise TypeError(f"Unsupported return type: {type(first)}")
        with pq.ParquetWriter("$PARQUET_PATH", first.schema) as writer:
            writer.write_table(first)
            for table in iterator:
                if not isinstance(table, pa.Table):
                    raise TypeError(f"Unsupported yielded type: {type(table)}; expected pyarrow.Table.")
                if not table.schema.equals(first.schema):
                    raise TypeError("All yielded pyarrow Tables must share one schema.")
                writer.write_table(table)

    if isinstance(df, pa.Table):
        write_tables([df])
        return

    if derives_from(df, "polars", "DataFrame"):
        import polars as pl
        if not isinstance(df, pl.DataFrame):
            raise TypeError(f"Unsupported polars return type: {type(df)}")
        df.write_parquet("$PARQUET_PATH")
        return

    if derives_from(df, "pandas", "DataFrame"):
        import pandas as pd
        if not isinstance(df, pd.DataFrame):
            raise TypeError(f"Unsupported pandas return type: {type(df)}")
        write_tables([pa.Table.from_pandas(df)])
        return

    if isinstance(df, (list, tuple)):
        if not df:
            return
        if isinstance(df[0], pa.Table):
            write_tables(df)
            return
        write_tables([pa.Table.from_pylist(list(df))])
        return

    if hasattr(df, "__iter__") and not isinstance(df, (str, bytes)):
        # Generators: each yield is a dict, a list of dicts (one page), or a
        # pyarrow Table. Parquet fixes the schema at the first row group, but
        # early rows may leave a column all-None ('null'-typed); buffer until
        # the inferred schema has no null-typed columns, then lock it and
        # conform every later batch to it.
        def rows_to_tables(items):
            locked_schema = None
            pending = []
            for item in items:
                if isinstance(item, pa.Table):
                    if pending:
                        yield pa.Table.from_pylist(pending, schema=item.schema)
                        pending = []
                    if locked_schema is None:
                        locked_schema = item.schema
                    yield item
                    continue
                rows = item if isinstance(item, (list, tuple)) else [item]
                if not rows:
                    continue
                if locked_schema is not None:
                    yield pa.Table.from_pylist(list(rows), schema=locked_schema)
                    continue
                pending.extend(rows)
                table = pa.Table.from_pylist(pending)
                if not any(pa.types.is_null(field.type) for field in table.schema):
                    locked_schema = table.schema
                    yield table
                    pending = []
            if pending:  # a column stayed all-None to the end
                yield pa.Table.from_pylist(pending)

        write_tables(rows_to_tables(df))
        return

    raise TypeError(f"Unsupported return type: {type(df)}")


module = import_module_from_path("$REPO_ROOT", "$MODULE_PATH")
if not hasattr(module, "materialize"):
    print(
        "ERROR: this asset has table materialization but defines no materialize() function; "
        "define materialize() returning a DataFrame, or remove the materialization.",
        file=sys.stderr,
    )
    sys.exit(1)
convert_and_write(module.materialize())
`
