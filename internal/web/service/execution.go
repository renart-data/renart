package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

type InspectResult struct {
	Status     string
	Columns    []string
	Rows       []map[string]any
	RawOutput  string
	Operation  OperationMetadata
	Error      string
	Attempts   int
	Retryable  bool
	HTTPStatus int
}

type MaterializeResult struct {
	Status          string
	Operation       OperationMetadata
	Output          string
	Error           string
	ExitCode        int
	ChangedAssetIDs []string
	MaterializedAt  *time.Time
}

type DuckDBExecutionInfo struct {
	ConnectionName string
	DatabasePath   string
	LockKey        string
}

type ExecutionDependencies struct {
	WorkspaceRoot         string
	ConfigPath            string
	Executor              BruinCommandExecutor
	ResolveAssetByID      func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	ResolveAssetNameByID  func(string) string
	FindInspectIDs        func(...string) []string
	RecordMaterialization func(string, time.Time, string)
	CurrentPipelines      func() []PipelineView
	DuckDBLock            func(string) *sync.Mutex
	ParseQueryOutput      func([]byte) ([]string, []map[string]any)
	NewPipelineBuilder    func() *pipeline.Builder
	FreshnessSnapshot     func() map[string]AssetTimestamps
}

type PipelineView struct {
	ID     string
	Assets []AssetView
}

type AssetView struct {
	ID   string
	Name string
}

type AssetTimestamps struct {
	MaterializedAt   *time.Time
	ContentChangedAt *time.Time
	LastStatus       string
}

type PipelineMaterializationInfo struct {
	AssetName       string
	Connection      string
	IsMaterialized  bool
	MaterializedAs  string
	FreshnessStatus string
	RowCount        *int64
	DeclaredMatType string
}

type PipelineMaterializationState struct {
	AssetID         string
	IsMaterialized  bool
	MaterializedAs  string
	FreshnessStatus string
	RowCount        *int64
	Connection      string
	DeclaredMatType string
}

type PipelineMaterializationResponse struct {
	PipelineID string
	Assets     []PipelineMaterializationState
}

type ExecutionService struct {
	deps ExecutionDependencies
}

const inspectReadOnlyErrorMessage = "Inspect only supports read-only single SELECT queries. Materialize the asset to run write, delete, copy, or multi-statement SQL."

func NewExecutionService(deps ExecutionDependencies) *ExecutionService {
	return &ExecutionService{deps: deps}
}

func (s *ExecutionService) InspectAsset(ctx context.Context, assetID, limit, environment string) InspectResult {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return InspectResult{Status: "error", Error: "invalid asset id", HTTPStatus: 400}
	}

	if guardErr := s.ensureAssetInspectable(ctx, assetID, environment); guardErr != nil {
		return InspectResult{
			Status:     "error",
			Columns:    []string{},
			Rows:       []map[string]any{},
			RawOutput:  guardErr.Error(),
			Operation:  queryAssetOperation(relAssetPath, limit, environment, ""),
			Error:      guardErr.Error(),
			Attempts:   0,
			Retryable:  false,
			HTTPStatus: 400,
		}
	}

	if result, ok := s.inspectMaterializedNonSQLAsset(ctx, assetID, relAssetPath, limit, environment); ok {
		return result
	}

	duckDBInfo, infoErr := s.findDuckDBExecutionInfoByAsset(ctx, assetID)
	if infoErr != nil {
		return InspectResult{Status: "error", Error: infoErr.Error(), HTTPStatus: 400}
	}

	queryReq := QueryAssetRequest{
		AssetPath:   relAssetPath,
		Limit:       limit,
		Environment: environment,
		Output:      "json",
	}
	operation := queryAssetOperation(relAssetPath, limit, environment, "")

	var output []byte
	var attempts int
	run := func() error {
		var runErr error
		output, runErr, attempts = s.deps.Executor.RunWithRetry(ctx, queryReq, 4, 150*time.Millisecond)
		return runErr
	}

	if duckDBInfo != nil {
		mu := s.deps.DuckDBLock(duckDBInfo.LockKey)
		mu.Lock()
		err = run()
		if err != nil && IsDuckDBLockError(err, output) {
			if readOnlyConfigPath, cleanup, cfgErr := s.buildReadOnlyConfigFile(duckDBInfo); cfgErr == nil {
				defer cleanup()
				queryReq.ConfigFile = readOnlyConfigPath
				operation.ConfigFile = readOnlyConfigPath
				err = run()
			}
		}
		mu.Unlock()
	} else {
		err = run()
	}

	if err != nil {
		statusCode := 400
		errorMessage := err.Error()
		rawOutput := extractInspectRawOutput(output)
		if rawOutput == "" {
			rawOutput = string(output)
		}
		if IsDuckDBLockError(err, output) {
			statusCode = 409
			errorMessage = "duckdb database is busy (lock held by another process), please retry"
		}
		return InspectResult{
			Status:     "error",
			Columns:    []string{},
			Rows:       []map[string]any{},
			RawOutput:  rawOutput,
			Operation:  operation,
			Error:      errorMessage,
			Attempts:   attempts,
			Retryable:  statusCode == 409,
			HTTPStatus: statusCode,
		}
	}

	columns, rows := s.deps.ParseQueryOutput(output)
	return InspectResult{
		Status:     "ok",
		Columns:    columns,
		Rows:       rows,
		RawOutput:  string(output),
		Operation:  operation,
		Attempts:   attempts,
		HTTPStatus: 200,
	}
}

func (s *ExecutionService) inspectMaterializedNonSQLAsset(ctx context.Context, assetID, relAssetPath, limit, environment string) (InspectResult, bool) {
	if s.deps.ResolveAssetByID == nil {
		return InspectResult{}, false
	}

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || parsedPipeline == nil || asset == nil || asset.IsSQLAsset() {
		return InspectResult{}, false
	}

	connectionName, err := parsedPipeline.GetConnectionNameForAsset(asset)
	if err != nil || strings.TrimSpace(connectionName) == "" {
		return InspectResult{}, false
	}

	rowLimit := normalizeInspectLimit(limit)
	query := fmt.Sprintf("select * from %s limit %d", asset.Name, rowLimit)
	operation := queryConnectionOperation(connectionName, query, environment)
	operation.AssetPath = relAssetPath
	operation.Target = relAssetPath
	operation.Limit = limit

	columns, rows, err := s.RunConnectionQueryForEnvironment(ctx, connectionName, environment, query)
	if err != nil {
		return InspectResult{
			Status:     "error",
			Columns:    []string{},
			Rows:       []map[string]any{},
			RawOutput:  err.Error(),
			Operation:  operation,
			Error:      fmt.Sprintf("No materialized table found for %s on connection %s. Materialize the asset first, then inspect again.", asset.Name, connectionName),
			Attempts:   0,
			Retryable:  false,
			HTTPStatus: 400,
		}, true
	}

	output, _ := json.Marshal(map[string]any{"columns": columns, "rows": rows})
	return InspectResult{
		Status:     "ok",
		Columns:    columns,
		Rows:       rows,
		RawOutput:  string(output),
		Operation:  operation,
		Attempts:   1,
		HTTPStatus: 200,
	}, true
}

func normalizeInspectLimit(limit string) int {
	trimmed := strings.TrimSpace(limit)
	if trimmed == "" {
		return 100
	}
	var value int
	if _, err := fmt.Sscanf(trimmed, "%d", &value); err != nil || value <= 0 {
		return 100
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func (s *ExecutionService) ensureAssetInspectable(ctx context.Context, assetID, environment string) error {
	if s.deps.ResolveAssetByID == nil {
		return nil
	}

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if asset == nil || parsedPipeline == nil || !asset.IsSQLAsset() {
		return nil
	}

	_, _, queryStr, err := getDirectConnectionAndQuery(ctx, &directPipelineInfo{Pipeline: parsedPipeline, Asset: asset, Config: loadExecutionConfigOrEmpty(s.deps.ConfigPath)}, environment)
	if err != nil {
		return nil
	}

	ok, err := isReadOnlySelectQuery(queryStr, asset.Type)
	if err != nil {
		return nil
	}
	if ok {
		return nil
	}

	return fmt.Errorf(inspectReadOnlyErrorMessage)
}

func loadExecutionConfigOrEmpty(configPath string) *config.Config {
	if strings.TrimSpace(configPath) == "" {
		return &config.Config{}
	}

	cfg, err := config.LoadOrCreate(afero.NewOsFs(), configPath)
	if err != nil || cfg == nil {
		return &config.Config{}
	}
	if cfg.SelectedEnvironmentName == "" {
		cfg.SelectedEnvironmentName = cfg.DefaultEnvironmentName
	}
	if cfg.SelectedEnvironment == nil && cfg.SelectedEnvironmentName != "" {
		_ = cfg.SelectEnvironment(cfg.SelectedEnvironmentName)
	}
	return cfg
}

func isReadOnlySelectQuery(queryStr string, assetType pipeline.AssetType) (bool, error) {
	dialect, err := sqlparser.AssetTypeToDialect(assetType)
	if err != nil {
		return false, err
	}

	parser, err := sqlparser.NewSQLParser(false)
	if err != nil {
		return false, err
	}
	defer parser.Close()

	return parser.IsSingleSelectQuery(queryStr, dialect)
}

func extractInspectRawOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}

	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		return trimmed
	}

	for _, key := range []string{"raw_output", "error"} {
		if value, ok := envelope[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}

	return trimmed
}

func (s *ExecutionService) MaterializeAssetStream(ctx context.Context, assetID, environment string, onChunk func([]byte)) MaterializeResult {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return MaterializeResult{Status: "error", Error: "invalid asset id", ExitCode: 1}
	}

	duckDBInfo, infoErr := s.findDuckDBExecutionInfoByAsset(ctx, assetID)
	if infoErr != nil {
		return MaterializeResult{Status: "error", Error: infoErr.Error(), ExitCode: 1}
	}

	operation := runOperation(relAssetPath, "", relAssetPath, environment)
	var output []byte
	run := func() error {
		var runErr error
		output, runErr = s.deps.Executor.RunAsset(ctx, RunAssetRequest{AssetPath: relAssetPath, Environment: environment}, onChunk)
		return runErr
	}

	var runErr error
	if duckDBInfo != nil {
		mu := s.deps.DuckDBLock(duckDBInfo.LockKey)
		mu.Lock()
		runErr = run()
		mu.Unlock()
	} else {
		runErr = run()
	}

	changedAssetIDs := make([]string, 0)
	var materializedAt *time.Time
	if runErr == nil {
		now := time.Now().UTC()
		materializedAt = &now
		if assetName := s.deps.ResolveAssetNameByID(assetID); assetName != "" {
			s.deps.RecordMaterialization(assetName, now, "succeeded")
		}
		changedAssetIDs = s.deps.FindInspectIDs(assetID)
	}

	status := "ok"
	errorMessage := ""
	exitCode := 0
	if runErr != nil {
		status = "error"
		exitCode = 1
		errorMessage = runErr.Error()
		if IsDuckDBLockError(runErr, output) {
			errorMessage = "duckdb database is busy (lock held by another process), please retry"
		}
	}

	return MaterializeResult{
		Status:          status,
		Operation:       operation,
		Output:          string(output),
		Error:           errorMessage,
		ExitCode:        exitCode,
		ChangedAssetIDs: changedAssetIDs,
		MaterializedAt:  materializedAt,
	}
}

func (s *ExecutionService) GetPipelineMaterialization(ctx context.Context, pipelineID, environment string) (PipelineMaterializationResponse, error) {
	relPipelinePath, err := DecodeID(pipelineID)
	if err != nil {
		return PipelineMaterializationResponse{}, fmt.Errorf("invalid pipeline id")
	}

	absPipelinePath, err := SafeJoin(s.deps.WorkspaceRoot, relPipelinePath)
	if err != nil {
		return PipelineMaterializationResponse{}, err
	}

	parsed, err := s.deps.NewPipelineBuilder().CreatePipelineFromPath(ctx, absPipelinePath, pipeline.WithMutate())
	if err != nil {
		return PipelineMaterializationResponse{}, err
	}

	matInfo := s.inspectPipelineMaterializations(ctx, parsed, environment)
	freshnessByAssetName := ComputePipelineFreshness(parsed, matInfo, s.deps.FreshnessSnapshot())
	assets := make([]PipelineMaterializationState, 0, len(parsed.Assets))

	for _, asset := range parsed.Assets {
		assetPath := asset.ExecutableFile.Path
		if assetPath == "" {
			assetPath = asset.DefinitionFile.Path
		}

		relAssetPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, assetPath)
		if relErr != nil {
			relAssetPath = assetPath
		}

		connectionName := ""
		if conn, connErr := parsed.GetConnectionNameForAsset(asset); connErr == nil {
			connectionName = conn
		}

		key := MaterializationAssetKey(asset.Name, connectionName)
		item := PipelineMaterializationState{
			AssetID:         EncodeID(filepath.ToSlash(relAssetPath)),
			Connection:      connectionName,
			DeclaredMatType: string(asset.Materialization.Type),
		}

		if info, ok := matInfo[key]; ok {
			item.IsMaterialized = info.IsMaterialized
			item.MaterializedAs = info.MaterializedAs
			item.FreshnessStatus = info.FreshnessStatus
			item.RowCount = info.RowCount
			if info.DeclaredMatType != "" {
				item.DeclaredMatType = info.DeclaredMatType
			}
		}

		if status, ok := freshnessByAssetName[asset.Name]; ok {
			item.FreshnessStatus = status
		}

		assets = append(assets, item)
	}

	return PipelineMaterializationResponse{PipelineID: pipelineID, Assets: assets}, nil
}

func (s *ExecutionService) MaterializePipelineStream(ctx context.Context, pipelineID, environment string, onChunk func([]byte)) MaterializeResult {
	target, err := ResolvePipelineRunTarget(pipelineID)
	if err != nil {
		return MaterializeResult{Status: "error", Error: "invalid pipeline id", ExitCode: 1}
	}

	operation := runOperation(target, pipelineID, "", environment)
	output, runErr := s.deps.Executor.RunPipeline(ctx, RunPipelineRequest{Target: target, Environment: environment}, onChunk)

	changedAssetIDs := make([]string, 0)
	var materializedAt *time.Time
	if runErr == nil {
		now := time.Now().UTC()
		materializedAt = &now
		for _, currentPipeline := range s.deps.CurrentPipelines() {
			if currentPipeline.ID != pipelineID {
				continue
			}
			for _, asset := range currentPipeline.Assets {
				changedAssetIDs = append(changedAssetIDs, asset.ID)
				if strings.TrimSpace(asset.Name) != "" {
					s.deps.RecordMaterialization(asset.Name, now, "succeeded")
				}
			}
			break
		}
	}

	status := "ok"
	errorMessage := ""
	exitCode := 0
	if runErr != nil {
		status = "error"
		errorMessage = runErr.Error()
		exitCode = 1
	}

	return MaterializeResult{
		Status:          status,
		Operation:       operation,
		Output:          string(output),
		Error:           errorMessage,
		ExitCode:        exitCode,
		ChangedAssetIDs: changedAssetIDs,
		MaterializedAt:  materializedAt,
	}
}

func ResolvePipelineRunTarget(pipelineID string) (string, error) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return "", err
	}

	cleaned := filepath.Clean(relPath)
	base := strings.ToLower(filepath.Base(cleaned))
	if base == "pipeline.yml" || base == "pipeline.yaml" || base == ".pipeline.yml" || base == ".pipeline.yaml" {
		dir := filepath.Dir(cleaned)
		if dir == "." {
			return ".", nil
		}
		return filepath.ToSlash(dir), nil
	}

	return filepath.ToSlash(cleaned), nil
}

func (s *ExecutionService) inspectPipelineMaterializations(ctx context.Context, parsed *pipeline.Pipeline, environment string) map[string]PipelineMaterializationInfo {
	result := make(map[string]PipelineMaterializationInfo)

	assetsByConnection := make(map[string][]*pipeline.Asset)
	for _, asset := range parsed.Assets {
		conn, err := parsed.GetConnectionNameForAsset(asset)
		if err != nil || conn == "" {
			continue
		}
		assetsByConnection[conn] = append(assetsByConnection[conn], asset)
	}

	for connName, assets := range assetsByConnection {
		objects, err := s.fetchObjectsForConnection(ctx, connName, environment)
		if err != nil || len(objects) == 0 {
			for _, asset := range assets {
				key := MaterializationAssetKey(asset.Name, connName)
				result[key] = PipelineMaterializationInfo{
					AssetName:       asset.Name,
					Connection:      connName,
					DeclaredMatType: string(asset.Materialization.Type),
				}
			}
			continue
		}

		wanted := make(map[string]struct{})
		for _, asset := range assets {
			wanted[NormalizeIdentifier(asset.Name)] = struct{}{}
			parts := strings.Split(NormalizeIdentifier(asset.Name), ".")
			if len(parts) > 1 {
				wanted[parts[len(parts)-1]] = struct{}{}
			}
		}

		candidateObjects := make([]DBObjectInfo, 0)
		for _, object := range objects {
			if _, ok := wanted[NormalizeIdentifier(object.QualifiedName)]; ok {
				candidateObjects = append(candidateObjects, object)
				continue
			}
			if _, ok := wanted[NormalizeIdentifier(object.Name)]; ok {
				candidateObjects = append(candidateObjects, object)
			}
		}

		tableObjects := make([]DBObjectInfo, 0, len(candidateObjects))
		for _, object := range candidateObjects {
			if object.Kind == "table" {
				tableObjects = append(tableObjects, object)
			}
		}

		rowCounts := s.fetchRowCountsForObjects(ctx, connName, environment, tableObjects)

		objectsByName := make(map[string]DBObjectInfo)
		for _, object := range objects {
			objectsByName[NormalizeIdentifier(object.QualifiedName)] = object
			objectsByName[NormalizeIdentifier(object.Name)] = object
		}

		for _, asset := range assets {
			normalized := NormalizeIdentifier(asset.Name)
			object, ok := objectsByName[normalized]
			if !ok {
				parts := strings.Split(normalized, ".")
				if len(parts) > 1 {
					object, ok = objectsByName[parts[len(parts)-1]]
				}
			}

			key := MaterializationAssetKey(asset.Name, connName)
			item := PipelineMaterializationInfo{
				AssetName:       asset.Name,
				Connection:      connName,
				DeclaredMatType: string(asset.Materialization.Type),
			}

			if ok {
				item.IsMaterialized = true
				item.MaterializedAs = object.Kind

				if count, hasCount := rowCounts[NormalizeIdentifier(object.QualifiedName)]; hasCount {
					c := count
					item.RowCount = &c
				} else if count, hasCount := rowCounts[NormalizeIdentifier(object.Name)]; hasCount {
					c := count
					item.RowCount = &c
				}
			}

			result[key] = item
		}
	}

	return result
}

func ComputePipelineFreshness(parsed *pipeline.Pipeline, matInfo map[string]PipelineMaterializationInfo, tracker map[string]AssetTimestamps) map[string]string {
	result := make(map[string]string, len(parsed.Assets))
	assetsByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		assetsByName[asset.Name] = asset
	}

	type visitState int
	const (
		visitUnknown visitState = iota
		visitActive
		visitDone
	)

	type freshnessEval struct {
		Fresh           bool
		EffectiveUpdate *time.Time
	}

	state := make(map[string]visitState, len(parsed.Assets))
	evals := make(map[string]freshnessEval, len(parsed.Assets))

	var evalAsset func(assetName string) freshnessEval
	evalAsset = func(assetName string) freshnessEval {
		if state[assetName] == visitDone {
			return evals[assetName]
		}
		if state[assetName] == visitActive {
			return freshnessEval{Fresh: false}
		}

		asset, ok := assetsByName[assetName]
		if !ok {
			return freshnessEval{Fresh: false}
		}

		state[assetName] = visitActive
		defer func() {
			state[assetName] = visitDone
		}()

		kind := "table"
		connectionName := ""
		if conn, err := parsed.GetConnectionNameForAsset(asset); err == nil {
			connectionName = conn
		}
		if info, ok := matInfo[MaterializationAssetKey(asset.Name, connectionName)]; ok {
			if strings.EqualFold(strings.TrimSpace(info.MaterializedAs), "view") {
				kind = "view"
			}
		}
		if strings.EqualFold(strings.TrimSpace(string(asset.Materialization.Type)), "view") {
			kind = "view"
		}

		trackerEntry, hasTracker := tracker[assetName]
		var materializedAt *time.Time
		if hasTracker && trackerEntry.MaterializedAt != nil {
			ts := trackerEntry.MaterializedAt.UTC()
			materializedAt = &ts
		}

		upstreamEvals := make([]freshnessEval, 0, len(asset.Upstreams))
		for _, up := range asset.Upstreams {
			upstreamEvals = append(upstreamEvals, evalAsset(up.Value))
		}

		if kind == "view" {
			if len(upstreamEvals) == 0 {
				fresh := materializedAt != nil
				e := freshnessEval{Fresh: fresh, EffectiveUpdate: materializedAt}
				evals[assetName] = e
				return e
			}

			fresh := true
			var latest *time.Time
			for _, up := range upstreamEvals {
				if !up.Fresh {
					fresh = false
				}
				latest = maxTimePtr(latest, up.EffectiveUpdate)
			}

			e := freshnessEval{Fresh: fresh, EffectiveUpdate: latest}
			evals[assetName] = e
			return e
		}

		if materializedAt == nil {
			e := freshnessEval{Fresh: false, EffectiveUpdate: nil}
			evals[assetName] = e
			return e
		}

		fresh := true
		for _, up := range upstreamEvals {
			if !up.Fresh {
				fresh = false
				continue
			}
			if up.EffectiveUpdate != nil && up.EffectiveUpdate.After(*materializedAt) {
				fresh = false
			}
		}

		e := freshnessEval{Fresh: fresh, EffectiveUpdate: materializedAt}
		evals[assetName] = e
		return e
	}

	for _, asset := range parsed.Assets {
		e := evalAsset(asset.Name)
		if e.Fresh {
			result[asset.Name] = "fresh"
		} else {
			result[asset.Name] = "stale"
		}
	}

	return result
}

type DBObjectInfo struct {
	Schema        string
	Name          string
	QualifiedName string
	Kind          string
}

func (s *ExecutionService) fetchObjectsForConnection(ctx context.Context, connectionName, environment string) ([]DBObjectInfo, error) {
	queries := []string{
		`SELECT table_schema, table_name, table_type FROM information_schema.tables`,
		`SHOW TABLES`,
	}

	var rows []map[string]any
	var lastErr error
	for _, query := range queries {
		_, qRows, err := s.RunConnectionQueryForEnvironment(ctx, connectionName, environment, query)
		if err != nil {
			lastErr = err
			continue
		}
		rows = qRows
		break
	}

	if len(rows) == 0 {
		return []DBObjectInfo{}, lastErr
	}

	objects := make([]DBObjectInfo, 0, len(rows))
	for _, row := range rows {
		name := ReadStringField(row, "table_name", "name", "table")
		if name == "" {
			continue
		}

		schema := ReadStringField(row, "table_schema", "schema", "database")
		qualifiedName := name
		if schema != "" {
			qualifiedName = schema + "." + name
		}

		kind := strings.ToLower(ReadStringField(row, "table_type", "type"))
		if strings.Contains(kind, "view") {
			kind = "view"
		} else if kind != "" {
			kind = "table"
		} else {
			kind = "table"
		}

		objects = append(objects, DBObjectInfo{Schema: schema, Name: name, QualifiedName: qualifiedName, Kind: kind})
	}

	return objects, nil
}

func (s *ExecutionService) fetchRowCountsForObjects(ctx context.Context, connectionName, environment string, objects []DBObjectInfo) map[string]int64 {
	result := make(map[string]int64)
	if len(objects) == 0 {
		return result
	}

	queries := make([]string, 0, len(objects))
	for _, object := range objects {
		queries = append(queries, fmt.Sprintf(
			"SELECT '%s' AS object_name, COUNT(*) AS row_count FROM %s",
			EscapeSQLLiteral(object.QualifiedName),
			QuoteQualifiedIdentifier(object.QualifiedName),
		))
	}

	countQuery := strings.Join(queries, " UNION ALL ")
	_, rows, err := s.RunConnectionQueryForEnvironment(ctx, connectionName, environment, countQuery)
	if err != nil {
		return result
	}

	for _, row := range rows {
		objName := ReadStringField(row, "object_name")
		if objName == "" {
			continue
		}

		if count, ok := ReadInt64Field(row, "row_count"); ok {
			result[NormalizeIdentifier(objName)] = count
			parts := strings.Split(NormalizeIdentifier(objName), ".")
			if len(parts) > 1 {
				result[parts[len(parts)-1]] = count
			}
		}
	}

	return result
}

func (s *ExecutionService) runConnectionQuery(ctx context.Context, connectionName, query string) ([]string, []map[string]any, error) {
	return s.RunConnectionQueryForEnvironment(ctx, connectionName, "", query)
}

func (s *ExecutionService) RunConnectionQueryForEnvironment(ctx context.Context, connectionName, environment, query string) ([]string, []map[string]any, error) {
	output, err := s.deps.Executor.QueryConnection(ctx, QueryConnectionRequest{
		ConnectionName: connectionName,
		Query:          query,
		Environment:    environment,
		Output:         "json",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("query failed for connection '%s': %w", connectionName, err)
	}

	columns, rows := ParseQueryJSONOutput(output)
	return columns, rows, nil
}

func ReadStringField(row map[string]any, keys ...string) string {
	for _, key := range keys {
		for rowKey, value := range row {
			if strings.EqualFold(rowKey, key) {
				s, ok := value.(string)
				if ok {
					return s
				}
			}
		}
	}
	return ""
}

func ReadInt64Field(row map[string]any, key string) (int64, bool) {
	for rowKey, value := range row {
		if !strings.EqualFold(rowKey, key) {
			continue
		}

		switch v := value.(type) {
		case int:
			return int64(v), true
		case int64:
			return v, true
		case float64:
			return int64(v), true
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				return 0, false
			}
			var parsed int64
			_, err := fmt.Sscan(trimmed, &parsed)
			if err == nil {
				return parsed, true
			}
		}
	}

	return 0, false
}

func ParseQueryJSONOutput(output []byte) ([]string, []map[string]any) {
	rows := make([]map[string]any, 0)

	var asRows []map[string]any
	if err := json.Unmarshal(output, &asRows); err == nil {
		rows = asRows
		return inferColumns(rows), rows
	}

	var asEnvelope map[string]any
	if err := json.Unmarshal(output, &asEnvelope); err == nil {
		columns := extractColumnNames(asEnvelope["columns"])

		if v, ok := asEnvelope["rows"]; ok {
			if parsedRows := castRows(v); len(parsedRows) > 0 {
				rows = parsedRows
			} else if parsedRowsByColumns := castRowsByColumns(v, columns); len(parsedRowsByColumns) > 0 {
				rows = parsedRowsByColumns
			}
		}
		if len(rows) == 0 {
			if v, ok := asEnvelope["data"]; ok {
				if parsedRows := castRows(v); len(parsedRows) > 0 {
					rows = parsedRows
				} else {
					rows = castRowsByColumns(v, columns)
				}
			}
		}

		if len(columns) == 0 {
			columns = inferColumns(rows)
		}

		return columns, rows
	}

	return []string{}, rows
}

func maxTimePtr(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.After(*a) {
		return b
	}
	return a
}

func extractColumnNames(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return []string{}
	}

	columns := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item.(string); ok {
			columns = append(columns, name)
			continue
		}

		columnMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		nameValue, ok := columnMap["name"]
		if !ok {
			continue
		}

		if name, ok := nameValue.(string); ok {
			columns = append(columns, name)
		}
	}

	return columns
}

func castRows(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}

	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok {
			rows = append(rows, row)
		}
	}

	return rows
}

func castRowsByColumns(value any, columns []string) []map[string]any {
	if len(columns) == 0 {
		return []map[string]any{}
	}

	items, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}

	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		cellValues, ok := item.([]any)
		if !ok {
			continue
		}

		row := make(map[string]any, len(columns))
		for idx, column := range columns {
			if idx < len(cellValues) {
				row[column] = cellValues[idx]
				continue
			}
			row[column] = nil
		}

		rows = append(rows, row)
	}

	return rows
}

func inferColumns(rows []map[string]any) []string {
	if len(rows) == 0 {
		return []string{}
	}

	columns := make([]string, 0)
	for key := range rows[0] {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

func (s *ExecutionService) findDuckDBExecutionInfoByAsset(ctx context.Context, assetID string) (*DuckDBExecutionInfo, error) {
	_, parsed, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return nil, err
	}

	connectionName, err := parsed.GetConnectionNameForAsset(asset)
	if err != nil || connectionName == "" {
		return nil, nil
	}

	fs := afero.NewOsFs()
	if exists, _ := afero.Exists(fs, s.deps.ConfigPath); !exists {
		return nil, nil
	}

	cfg, cfgErr := config.LoadOrCreate(fs, s.deps.ConfigPath)
	if cfgErr != nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return nil, nil
	}

	for _, conn := range cfg.SelectedEnvironment.Connections.DuckDB {
		if conn.Name != connectionName {
			continue
		}
		databasePath := strings.TrimSpace(conn.Path)
		if databasePath == "" {
			databasePath = connectionName
		} else {
			databasePath = filepath.Clean(databasePath)
		}
		return &DuckDBExecutionInfo{ConnectionName: connectionName, DatabasePath: databasePath, LockKey: "duckdb:" + databasePath}, nil
	}

	return nil, nil
}

func (s *ExecutionService) buildReadOnlyConfigFile(info *DuckDBExecutionInfo) (string, func(), error) {
	if info == nil || info.ConnectionName == "" {
		return "", nil, fmt.Errorf("duckdb read-only config requires connection info")
	}

	cfg, err := config.LoadOrCreate(afero.NewOsFs(), s.deps.ConfigPath)
	if err != nil {
		return "", nil, err
	}
	if cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return "", nil, fmt.Errorf("selected environment has no connections")
	}

	envName := cfg.SelectedEnvironmentName
	if envName == "" {
		envName = cfg.DefaultEnvironmentName
	}
	env, ok := cfg.Environments[envName]
	if !ok || env.Connections == nil {
		return "", nil, fmt.Errorf("environment '%s' not found", envName)
	}

	found := false
	for i := range env.Connections.DuckDB {
		if env.Connections.DuckDB[i].Name != info.ConnectionName {
			continue
		}
		env.Connections.DuckDB[i].Path = AppendDuckDBReadOnlyMode(env.Connections.DuckDB[i].Path)
		found = true
		break
	}
	if !found {
		return "", nil, fmt.Errorf("duckdb connection '%s' not found", info.ConnectionName)
	}
	cfg.Environments[envName] = env

	fs := afero.NewOsFs()
	tempFile, err := afero.TempFile(fs, "", "renart-readonly-*.yml")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = fs.Remove(tempFile.Name()) }
	if err := tempFile.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	content, err := yaml.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := afero.WriteFile(fs, tempFile.Name(), content, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return tempFile.Name(), cleanup, nil
}
