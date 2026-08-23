package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/git"
	bruinpath "github.com/bruin-data/bruin/pkg/path"
	"github.com/spf13/afero"
	"renart/internal/web/secretstore"
)

type OnboardingImportRequest struct {
	ConnectionName  string
	EnvironmentName string
	PipelineName    string
	Schema          string
	Pattern         string
	Tables          []string
	DisableColumns  bool
	CreateIfMissing bool
}

type OnboardingQuickstartRequest struct {
	EnvironmentName string
	PipelineName    string
	ConnectionName  string
	DatabasePath    string
	Materialize     bool
}

type OnboardingImportFormState struct {
	Database       string `json:"database,omitempty"`
	PipelineName   string `json:"pipeline_name,omitempty"`
	Schema         string `json:"schema,omitempty"`
	Pattern        string `json:"pattern,omitempty"`
	DisableColumns bool   `json:"disable_columns,omitempty"`
}

type OnboardingImportResultState struct {
	Output       string   `json:"output,omitempty"`
	Error        string   `json:"error,omitempty"`
	PipelinePath string   `json:"pipeline_path,omitempty"`
	AssetPaths   []string `json:"asset_paths,omitempty"`
}

// renart:web
type OnboardingSessionState struct {
	Active          bool                         `json:"active"`
	Step            string                       `json:"step,omitempty"`
	SelectedType    string                       `json:"selected_type,omitempty"`
	EnvironmentName string                       `json:"environment_name,omitempty"`
	DraftValues     map[string]any               `json:"draft_values,omitempty"`
	ImportForm      OnboardingImportFormState    `json:"import_form,omitempty"`
	SelectedTables  []string                     `json:"selected_tables,omitempty"`
	ImportResult    *OnboardingImportResultState `json:"import_result,omitempty"`
}

type OnboardingDiscoveryRequest struct {
	EnvironmentName string
	Type            string
	Values          map[string]any
	SecretChanges   map[string]WorkspaceConnectionSecretChange
	Database        string
}

// renart:web
// renart:web-name OnboardingDiscoveryResponse
type OnboardingDiscoveryResult struct {
	Status           string                  `json:"status"`
	ConnectionType   string                  `json:"connection_type,omitempty"`
	Databases        []string                `json:"databases"`
	SelectedDatabase string                  `json:"selected_database,omitempty"`
	Tables           []SQLDiscoveryTableItem `json:"tables"`
	Error            string                  `json:"error,omitempty"`
}

// renart:web
// renart:web-name OnboardingPathSuggestionsResponse
type OnboardingPathSuggestionsResult struct {
	Status      string           `json:"status"`
	Suggestions []SuggestionItem `json:"suggestions"`
	Error       string           `json:"error,omitempty"`
}

type OnboardingImportResult struct {
	Status       string
	Operation    OperationMetadata
	Output       string
	Error        string
	PipelinePath string
	AssetPaths   []string
	HTTPCode     int
}

type OnboardingService struct {
	workspaceRoot string
	executor      BruinCommandExecutor
	execution     RunMaterializer
	configPath    string
	statePath     string
}

const (
	OnboardingStateStart      = "start"
	OnboardingStateConnection = "connection-type"
	OnboardingStateConfig     = "connection-config"
	OnboardingStateImport     = "import"
	OnboardingStateQuickstart = "quickstart"
	OnboardingStateSuccess    = "success"
)

func NewOnboardingService(workspaceRoot, configPath string, executor BruinCommandExecutor, execution RunMaterializer) *OnboardingService {
	return &OnboardingService{
		workspaceRoot: workspaceRoot,
		executor:      executor,
		execution:     execution,
		configPath:    configPath,
		statePath:     filepath.Join(workspaceRoot, ".renart-onboarding.json"),
	}
}

func (s *OnboardingService) GetState() (OnboardingSessionState, error) {
	state, err := s.loadState()
	if err == nil {
		return normalizeOnboardingSessionState(state), nil
	}
	if !os.IsNotExist(err) {
		return OnboardingSessionState{}, err
	}

	return s.defaultState(), nil
}

func (s *OnboardingService) UpdateState(state OnboardingSessionState) error {
	normalized := normalizeOnboardingSessionState(state)
	fs := afero.NewOsFs()
	if err := fs.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return err
	}
	if err := git.EnsureGivenPatternIsInGitignore(fs, s.workspaceRoot, filepath.Base(s.statePath)); err != nil {
		return err
	}

	contents, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}

	return afero.WriteFile(fs, s.statePath, contents, 0o644)
}

func (s *OnboardingService) PreviewDiscovery(ctx context.Context, req OnboardingDiscoveryRequest) (OnboardingDiscoveryResult, int) {
	configService := NewConfigService(s.workspaceRoot, s.configPath)
	cfg, _, err := configService.LoadForEditing()
	if err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: err.Error()}, 500
	}

	environmentName := strings.TrimSpace(req.EnvironmentName)
	if environmentName == "" {
		environmentName = cfg.SelectedEnvironmentName
	}
	if environmentName == "" {
		environmentName = cfg.DefaultEnvironmentName
	}
	if environmentName == "" {
		environmentName = "default"
	}

	typeName := strings.TrimSpace(req.Type)
	if typeName == "" {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: "connection type is required"}, 400
	}

	values := cloneAnyMap(req.Values)
	selectedDatabase := strings.TrimSpace(req.Database)
	if err := ensureDiscoveryDraftValues(typeName, values, selectedDatabase); err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: err.Error()}, 400
	}
	selectedDatabase = stringValue(values["database"])
	if typeName == "duckdb" && selectedDatabase == "" {
		selectedDatabase = stringValue(values["path"])
	}

	connectionName := DefaultOnboardingConnectionName(typeName)
	if err := cfg.DeleteConnection(environmentName, connectionName); err != nil && !strings.Contains(err.Error(), "does not exist") {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: err.Error()}, 400
	}
	resolvedSecretChanges, draftSecretBundle, err := configService.resolveDraftConnectionSecretChanges(
		ctx,
		environmentName,
		req.SecretChanges,
	)
	if err != nil {
		return OnboardingDiscoveryResult{
			Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{},
			Error: safeSecretResolutionMessage(err),
		}, 400
	}
	if draftSecretBundle != nil {
		defer draftSecretBundle.Close(ctx)
	}
	if err := configService.AddConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: environmentName,
		Name:            connectionName,
		Type:            typeName,
		Values:          values,
		SecretChanges:   resolvedSecretChanges,
	}); err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: err.Error()}, 400
	}

	selectedCfg, err := selectConfigEnvironment(cfg, environmentName)
	if err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: err.Error()}, 400
	}
	factory := NewResolvedConnectionFactory(
		s.workspaceRoot,
		s.configPath,
		configService.ProjectIdentity().ID,
		configService.secretResolver,
	)
	resolved, err := factory.ResolveConfigForConnections(
		ctx,
		selectedCfg,
		environmentName,
		secretstore.PurposeInspect,
		connectionName,
	)
	if err != nil {
		return OnboardingDiscoveryResult{
			Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{},
			Error: safeSecretResolutionMessage(err),
		}, 400
	}
	defer resolved.Close(ctx)

	manager, err := newConnectionManagerFromConfig(ctx, resolved.Config)
	if err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: resolved.Redactor.Mask(err.Error())}, 400
	}

	conn, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return OnboardingDiscoveryResult{
			Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{},
			Error: safeSecretResolutionMessage(err),
		}, 400
	}
	if conn == nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: fmt.Sprintf("connection '%s' not found", connectionName)}, 400
	}

	result := OnboardingDiscoveryResult{
		Status:           "ok",
		ConnectionType:   strings.TrimSpace(manager.GetConnectionType(connectionName)),
		Databases:        []string{},
		SelectedDatabase: selectedDatabase,
		Tables:           []SQLDiscoveryTableItem{},
	}

	fetcher, ok := conn.(interface {
		GetDatabases(ctx context.Context) ([]string, error)
	})
	if !ok {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: fmt.Sprintf("connection '%s' does not support discovery", connectionName)}, 400
	}

	databases, err := fetcher.GetDatabases(ctx)
	if err != nil {
		return OnboardingDiscoveryResult{Status: "error", Databases: []string{}, Tables: []SQLDiscoveryTableItem{}, Error: redactWorkspaceConnectionMessage(selectedCfg, err.Error())}, 400
	}
	sort.Strings(databases)
	result.Databases = databases

	if selectedDatabase == "" {
		return result, 200
	}

	if fetcherWithSchemas, ok := conn.(interface {
		GetTablesWithSchemas(ctx context.Context, databaseName string) (map[string][]string, error)
	}); ok {
		items, err := fetcherWithSchemas.GetTablesWithSchemas(ctx, selectedDatabase)
		if err != nil {
			return OnboardingDiscoveryResult{Status: "error", Databases: databases, Tables: []SQLDiscoveryTableItem{}, Error: redactWorkspaceConnectionMessage(selectedCfg, err.Error())}, 400
		}
		result.Tables = BuildSQLDiscoveryTableItemsForConnectionType(result.ConnectionType, selectedDatabase, items)
		return result, 200
	}

	if tableFetcher, ok := conn.(interface {
		GetTables(ctx context.Context, databaseName string) ([]string, error)
	}); ok {
		items, err := tableFetcher.GetTables(ctx, selectedDatabase)
		if err != nil {
			return OnboardingDiscoveryResult{Status: "error", Databases: databases, Tables: []SQLDiscoveryTableItem{}, Error: redactWorkspaceConnectionMessage(selectedCfg, err.Error())}, 400
		}
		result.Tables = BuildSQLDiscoveryTableItemsWithoutSchemasForConnectionType(result.ConnectionType, selectedDatabase, items)
		return result, 200
	}

	return OnboardingDiscoveryResult{Status: "error", Databases: databases, Tables: []SQLDiscoveryTableItem{}, Error: fmt.Sprintf("connection '%s' does not support table discovery", connectionName)}, 400
}

func (s *OnboardingService) loadState() (OnboardingSessionState, error) {
	contents, err := afero.ReadFile(afero.NewOsFs(), s.statePath)
	if err != nil {
		return OnboardingSessionState{}, err
	}

	var state OnboardingSessionState
	if err := json.Unmarshal(contents, &state); err != nil {
		return OnboardingSessionState{}, err
	}

	return state, nil
}

func (s *OnboardingService) defaultState() OnboardingSessionState {
	if s.shouldActivateByDefault() {
		return OnboardingSessionState{
			Active: true,
			Step:   OnboardingStateStart,
			ImportForm: OnboardingImportFormState{
				PipelineName: "analytics",
			},
		}
	}

	return OnboardingSessionState{Active: false}
}

func (s *OnboardingService) shouldActivateByDefault() bool {
	pipelinePaths, err := bruinpath.GetPipelinePaths(s.workspaceRoot, PipelineDefinitionFiles)
	if err == nil && len(pipelinePaths) > 0 {
		return false
	}

	fs := afero.NewOsFs()
	if exists, _ := afero.Exists(fs, s.configPath); exists {
		cfg, cfgErr := config.LoadFromFileOrEnv(fs, s.configPath)
		if cfgErr == nil {
			for _, env := range cfg.Environments {
				if env.Connections != nil && len(env.Connections.ConnectionsSummaryList()) > 0 {
					return false
				}
			}
		}
	}

	return true
}

func normalizeOnboardingSessionState(state OnboardingSessionState) OnboardingSessionState {
	if !state.Active {
		return OnboardingSessionState{Active: false}
	}

	step := strings.TrimSpace(state.Step)
	switch step {
	case OnboardingStateStart, OnboardingStateConnection, OnboardingStateConfig, OnboardingStateImport, OnboardingStateQuickstart, OnboardingStateSuccess:
	default:
		step = OnboardingStateStart
	}

	result := OnboardingSessionState{
		Active:          true,
		Step:            step,
		SelectedType:    strings.TrimSpace(state.SelectedType),
		EnvironmentName: strings.TrimSpace(state.EnvironmentName),
		DraftValues:     nonSensitiveWorkspaceConnectionValues(state.SelectedType, state.DraftValues),
		ImportForm: OnboardingImportFormState{
			Database:       strings.TrimSpace(state.ImportForm.Database),
			PipelineName:   strings.TrimSpace(state.ImportForm.PipelineName),
			Schema:         strings.TrimSpace(state.ImportForm.Schema),
			Pattern:        strings.TrimSpace(state.ImportForm.Pattern),
			DisableColumns: state.ImportForm.DisableColumns,
		},
		SelectedTables: append([]string(nil), state.SelectedTables...),
	}

	if result.ImportForm.PipelineName == "" {
		result.ImportForm.PipelineName = "analytics"
	}

	if state.ImportResult != nil {
		result.ImportResult = &OnboardingImportResultState{
			Output:       state.ImportResult.Output,
			Error:        state.ImportResult.Error,
			PipelinePath: strings.TrimSpace(state.ImportResult.PipelinePath),
			AssetPaths:   append([]string(nil), state.ImportResult.AssetPaths...),
		}
	}

	return result
}

func nonSensitiveWorkspaceConnectionValues(typeName string, values map[string]any) map[string]any {
	fieldDefs := workspaceConnectionFieldDefsForType(strings.TrimSpace(typeName))
	if len(fieldDefs) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(fieldDefs))
	for _, fieldDef := range fieldDefs {
		if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
			allowed[fieldDef.Name] = struct{}{}
		}
	}
	result := make(map[string]any)
	for name, value := range values {
		if _, ok := allowed[name]; ok {
			result[name] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *OnboardingService) PathSuggestions(prefix string) (OnboardingPathSuggestionsResult, int) {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		trimmed = "./"
	}

	var (
		suggestions []SuggestionItem
		err         error
	)

	if strings.HasPrefix(trimmed, "/") {
		suggestions, err = BuildAbsolutePathSuggestionItems(trimmed)
	} else {
		suggestions, err = BuildWorkspacePathSuggestionItems(s.workspaceRoot, trimmed)
	}
	if err != nil {
		return OnboardingPathSuggestionsResult{Status: "error", Suggestions: []SuggestionItem{}, Error: err.Error()}, 400
	}

	return OnboardingPathSuggestionsResult{Status: "ok", Suggestions: suggestions}, 200
}

func (s *OnboardingService) ImportDatabase(ctx context.Context, req OnboardingImportRequest) OnboardingImportResult {
	connectionName := strings.TrimSpace(req.ConnectionName)
	pipelineName := strings.TrimSpace(req.PipelineName)
	if connectionName == "" {
		return OnboardingImportResult{Status: "error", Error: "connection name is required", HTTPCode: 400}
	}
	if pipelineName == "" {
		return OnboardingImportResult{Status: "error", Error: "pipeline name is required", HTTPCode: 400}
	}

	relPipelinePath := filepath.ToSlash(pipelineName)
	absPipelinePath, err := SafeJoin(s.workspaceRoot, relPipelinePath)
	if err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 400}
	}

	if req.CreateIfMissing {
		fs := afero.NewOsFs()
		if err := fs.MkdirAll(absPipelinePath, 0o755); err != nil {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
		}
		pipelineFile := filepath.Join(absPipelinePath, "pipeline.yml")
		if exists, statErr := afero.Exists(fs, pipelineFile); statErr != nil {
			return OnboardingImportResult{Status: "error", Error: statErr.Error(), HTTPCode: 500}
		} else if !exists {
			content := fmt.Sprintf("name: %s\nschedule: daily\nstart_date: \"2024-01-01\"\n", filepath.Base(relPipelinePath))
			if writeErr := afero.WriteFile(fs, pipelineFile, []byte(content), 0o644); writeErr != nil {
				return OnboardingImportResult{Status: "error", Error: writeErr.Error(), HTTPCode: 500}
			}
		}
	}

	operation := importDatabaseOperation(relPipelinePath, connectionName, req.EnvironmentName)

	output, runErr := s.executor.ImportDatabase(ctx, ImportDatabaseRequest{
		PipelinePath:   relPipelinePath,
		ConnectionName: connectionName,
		Schema:         req.Schema,
		Tables:         req.Tables,
		DisableColumns: req.DisableColumns,
		Environment:    req.EnvironmentName,
		ConfigFilePath: s.configPath,
	})
	if runErr != nil {
		return OnboardingImportResult{
			Status:       "error",
			Operation:    operation,
			Output:       string(output),
			Error:        runErr.Error(),
			PipelinePath: relPipelinePath,
			HTTPCode:     400,
		}
	}

	patchOp := patchOperation("fill-asset-dependencies", relPipelinePath)
	patchOutput, patchErr := s.executor.ApplyPatch(ctx, PatchRequest{
		Operation:  "fill-asset-dependencies",
		TargetPath: relPipelinePath,
	})
	if patchErr != nil {
		return OnboardingImportResult{
			Status:       "error",
			Operation:    patchOp,
			Output:       string(patchOutput),
			Error:        patchErr.Error(),
			PipelinePath: relPipelinePath,
			HTTPCode:     400,
		}
	}

	assetPaths := make([]string, 0, len(req.Tables))
	for _, table := range req.Tables {
		trimmed := strings.TrimSpace(table)
		if trimmed == "" {
			continue
		}
		parts := strings.Split(trimmed, ".")
		if len(parts) >= 2 {
			schemaName := parts[len(parts)-2]
			shortName := parts[len(parts)-1]
			assetPaths = append(assetPaths, filepath.ToSlash(filepath.Join(relPipelinePath, "assets", schemaName, shortName+".asset.yml")))
			continue
		}
		shortName := parts[len(parts)-1]
		assetPaths = append(assetPaths, filepath.ToSlash(filepath.Join(relPipelinePath, "assets", shortName+".asset.yml")))
	}

	return OnboardingImportResult{
		Status:       "ok",
		Operation:    operation,
		Output:       strings.TrimSpace(string(output) + "\n" + string(patchOutput)),
		PipelinePath: relPipelinePath,
		AssetPaths:   assetPaths,
		HTTPCode:     200,
	}
}

func (s *OnboardingService) CreateDuckDBQuickstart(ctx context.Context, req OnboardingQuickstartRequest) OnboardingImportResult {
	environmentName := strings.TrimSpace(req.EnvironmentName)
	if environmentName == "" {
		environmentName = "default"
	}
	duckDBConnectionName := strings.TrimSpace(req.ConnectionName)
	if duckDBConnectionName == "" {
		duckDBConnectionName = "duckdb-default"
	}
	pipelineName := strings.Trim(strings.TrimSpace(req.PipelineName), `/\\`)
	if pipelineName == "" {
		pipelineName = "quickstart"
	}
	databasePath := filepath.ToSlash(strings.TrimSpace(req.DatabasePath))
	if databasePath == "" {
		databasePath = "duckdb-files/chess_playground.duckdb"
	}

	relPipelinePath := filepath.ToSlash(pipelineName)
	absPipelinePath, err := SafeJoin(s.workspaceRoot, relPipelinePath)
	if err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 400}
	}
	fs := afero.NewOsFs()
	if exists, statErr := afero.Exists(fs, absPipelinePath); statErr != nil {
		return OnboardingImportResult{Status: "error", Error: statErr.Error(), HTTPCode: 500}
	} else if exists {
		return OnboardingImportResult{Status: "error", Error: fmt.Sprintf("pipeline %q already exists", relPipelinePath), HTTPCode: 400}
	}

	configService := NewConfigService(s.workspaceRoot, s.configPath)
	cfg, _, err := configService.LoadForEditing()
	if err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
	}
	if _, exists := cfg.Environments[environmentName]; exists {
		if err := cfg.DeleteConnection(environmentName, duckDBConnectionName); err != nil && !strings.Contains(err.Error(), "does not exist") {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 400}
		}
		if err := cfg.DeleteConnection(environmentName, "chess-default"); err != nil && !strings.Contains(err.Error(), "does not exist") {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 400}
		}
	}
	if _, exists := cfg.Environments[environmentName]; !exists {
		if err := cfg.AddEnvironment(environmentName, ""); err != nil {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
		}
	}
	if strings.TrimSpace(cfg.DefaultEnvironmentName) == "" {
		cfg.DefaultEnvironmentName = environmentName
	}
	if strings.TrimSpace(cfg.SelectedEnvironmentName) == "" {
		cfg.SelectedEnvironmentName = environmentName
	}
	if err := cfg.AddConnection(environmentName, duckDBConnectionName, "duckdb", map[string]any{"path": databasePath}); err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 400}
	}
	if _, err := configService.Persist(cfg); err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
	}

	if err := s.prepareQuickstartDuckDBPath(databasePath); err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
	}
	assetsDir := filepath.Join(absPipelinePath, "assets")
	if err := fs.MkdirAll(assetsDir, 0o755); err != nil {
		return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
	}

	files := map[string]string{
		"pipeline.yml": quickstartPipelineYAML(filepath.Base(relPipelinePath), duckDBConnectionName),
		filepath.Join("assets", "quickstart", "players.asset.yml"):      chessPlayersAPIYAML(),
		filepath.Join("assets", "quickstart", "games.asset.yml"):        chessGamesAPIYAML(),
		filepath.Join("assets", "quickstart", "game_results.sql"):       chessGameResultsSQL("quickstart"),
		filepath.Join("assets", "quickstart", "player_performance.sql"): chessPlayerPerformanceSQL("quickstart"),
		filepath.Join("assets", "quickstart", "opening_repertoire.sql"): chessOpeningRepertoireSQL("quickstart"),
	}
	for relPath, content := range files {
		absPath := filepath.Join(absPipelinePath, relPath)
		if err := fs.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
		}
		if err := afero.WriteFile(fs, absPath, []byte(content), 0o644); err != nil {
			return OnboardingImportResult{Status: "error", Error: err.Error(), HTTPCode: 500}
		}
	}

	assetPaths := []string{
		filepath.ToSlash(filepath.Join(relPipelinePath, "assets", "quickstart", "players.asset.yml")),
		filepath.ToSlash(filepath.Join(relPipelinePath, "assets", "quickstart", "games.asset.yml")),
		filepath.ToSlash(filepath.Join(relPipelinePath, "assets", "quickstart", "game_results.sql")),
		filepath.ToSlash(filepath.Join(relPipelinePath, "assets", "quickstart", "player_performance.sql")),
		filepath.ToSlash(filepath.Join(relPipelinePath, "assets", "quickstart", "opening_repertoire.sql")),
	}
	output := fmt.Sprintf("Created DuckDB chess quickstart pipeline at %s", relPipelinePath)
	pipelineID := EncodeID(relPipelinePath)
	operation := runOperation(relPipelinePath, pipelineID, "", environmentName)
	if req.Materialize {
		if s.execution == nil {
			return OnboardingImportResult{
				Status:       "error",
				Operation:    operation,
				Output:       output,
				Error:        "completion-aware pipeline execution is unavailable",
				PipelinePath: relPipelinePath,
				AssetPaths:   assetPaths,
				HTTPCode:     500,
			}
		}
		runResult := s.execution.MaterializePipelineStreamWithSensorMode(
			ctx, pipelineID, environmentName, false, false, false,
			"", "", "", "", nil,
		)
		output = strings.TrimSpace(output + "\n" + runResult.Output)
		if runResult.Status != "ok" {
			return OnboardingImportResult{
				Status:       "error",
				Operation:    operation,
				Output:       output,
				Error:        runResult.Error,
				PipelinePath: relPipelinePath,
				AssetPaths:   assetPaths,
				HTTPCode:     400,
			}
		}
	}

	return OnboardingImportResult{
		Status:       "ok",
		Operation:    operation,
		Output:       output,
		PipelinePath: relPipelinePath,
		AssetPaths:   assetPaths,
		HTTPCode:     200,
	}
}

func (s *OnboardingService) prepareQuickstartDuckDBPath(databasePath string) error {
	configDir := filepath.Dir(s.configPath)
	if strings.TrimSpace(configDir) == "" {
		configDir = s.workspaceRoot
	}
	absDatabasePath, err := SafeJoin(configDir, filepath.FromSlash(databasePath))
	if err != nil {
		return err
	}
	fs := afero.NewOsFs()
	if err := fs.MkdirAll(filepath.Dir(absDatabasePath), 0o755); err != nil {
		return err
	}

	info, err := fs.Stat(absDatabasePath)
	if err == nil && !info.IsDir() && info.Size() == 0 {
		return fs.Remove(absDatabasePath)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func quickstartPipelineYAML(name, connectionName string) string {
	return fmt.Sprintf("name: %s\nconcurrency: 1\n\ndefault_connections:\n  duckdb: %s\n", name, connectionName)
}

func chessPlayersAPIYAML() string {
	return `type: api

parameters:
  request:
    url: https://api.chess.com/pub/player/{{ username }}
    method: GET
    headers:
      Accept: application/json
      User-Agent: "Renart Demo (https://github.com/renart-data/renart)"

  iterate:
    as: username
    over:
      - MagnusCarlsen
      - Hikaru
      - GothamChess
      - AnnaCramling
      - FabianoCaruana
      - AlexandraBotez

  response:
    fields:
      username: username
      name: name
      title: title
      country: country
      followers: followers
      joined: joined
      last_online: last_online
      fide: fide
      is_streamer: is_streamer
      player_id: player_id
      url: url
`
}

func chessGamesAPIYAML() string {
	return `type: api

parameters:
  request:
    url: https://api.chess.com/pub/player/{{ username }}/games/2024/01
    method: GET
    headers:
      Accept: application/json
      User-Agent: "Renart Demo (https://github.com/renart-data/renart)"

  iterate:
    as: username
    over:
      - MagnusCarlsen
      - Hikaru
      - GothamChess
      - AnnaCramling
      - FabianoCaruana
      - AlexandraBotez

  response:
    records_path: games
    fields:
      url: url
      pgn: pgn
      time_control: time_control
      time_class: time_class
      end_time: end_time
      rated: rated
      rules: rules
      eco: eco
      white_username: white.username
      white_result: white.result
      white_rating: white.rating
      white_accuracy: accuracies.white
      black_username: black.username
      black_result: black.result
      black_rating: black.rating
      black_accuracy: accuracies.black
`
}

func chessGameResultsSQL(schema string) string {
	return `/* @bruin
name: ` + schema + `.game_results
type: duckdb.sql
materialization:
  type: table
depends:
  - ` + schema + `.players
  - ` + schema + `.games
meta:
  web_table_dense: "false"
  web_view: table
@bruin */

WITH unique_games AS (
    SELECT *
    FROM ` + schema + `.games
    WHERE rules = 'chess'
    QUALIFY row_number() OVER (PARTITION BY url ORDER BY end_time DESC) = 1
),

player_games AS (
    SELECT
        url AS game_url,
        end_time,
        time_class,
        time_control,
        rated,
        pgn,
        eco AS eco_url,
        lower(white_username) AS player_key,
        'white' AS color,
        white_result AS result_code,
        white_rating AS rating,
        white_accuracy AS accuracy,
        black_username AS opponent_username,
        black_rating AS opponent_rating
    FROM unique_games

    UNION ALL

    SELECT
        url AS game_url,
        end_time,
        time_class,
        time_control,
        rated,
        pgn,
        eco AS eco_url,
        lower(black_username) AS player_key,
        'black' AS color,
        black_result AS result_code,
        black_rating AS rating,
        black_accuracy AS accuracy,
        white_username AS opponent_username,
        white_rating AS opponent_rating
    FROM unique_games
)

SELECT
    players.username,
    coalesce(nullif(players.name, ''), players.username) AS name,
    players.title,
    upper(regexp_extract(players.country, '/country/([^/]+)$', 1)) AS country_code,
    player_games.game_url,
    to_timestamp(player_games.end_time) AS ended_at,
    player_games.time_class,
    player_games.time_control,
    player_games.rated,
    player_games.color,
    player_games.rating,
    player_games.accuracy,
    player_games.opponent_username,
    player_games.opponent_rating,
    player_games.result_code,
    CASE
        WHEN player_games.result_code = 'win' THEN 'win'
        WHEN player_games.result_code IN (
            'agreed', 'repetition', 'stalemate', 'insufficient', '50move', 'timevsinsufficient'
        ) THEN 'draw'
        ELSE 'loss'
    END AS outcome,
    CASE
        WHEN player_games.result_code = 'win' THEN 1.0
        WHEN player_games.result_code IN (
            'agreed', 'repetition', 'stalemate', 'insufficient', '50move', 'timevsinsufficient'
        ) THEN 0.5
        ELSE 0.0
    END AS score,
    nullif(regexp_extract(player_games.pgn, '\[ECO "([^"]+)"\]', 1), '') AS eco_code,
    CASE
        WHEN nullif(player_games.eco_url, '') IS NULL THEN 'Unknown'
        ELSE replace(regexp_extract(player_games.eco_url, '/openings/(.*)$', 1), '-', ' ')
    END AS opening
FROM player_games
INNER JOIN ` + schema + `.players AS players
    ON lower(players.username) = player_games.player_key
`
}

func chessPlayerPerformanceSQL(schema string) string {
	return `/* @bruin
name: ` + schema + `.player_performance
type: duckdb.sql
materialization:
  type: table
depends:
  - ` + schema + `.game_results
meta:
  web_view: chart
  web_chart_type: bar
  web_chart_x: username
  web_chart_series: score_percent
  web_chart_title: January 2024 score percentage
@bruin */

SELECT
    name,
    username,
    title,
    country_code,
    count(*) AS games,
    count(*) FILTER (WHERE time_class = 'bullet') AS bullet_games,
    count(*) FILTER (WHERE time_class = 'blitz') AS blitz_games,
    count(*) FILTER (WHERE time_class = 'rapid') AS rapid_games,
    count(*) FILTER (WHERE outcome = 'win') AS wins,
    count(*) FILTER (WHERE outcome = 'draw') AS draws,
    count(*) FILTER (WHERE outcome = 'loss') AS losses,
    round(100 * avg(score), 1) AS score_percent,
    round(avg(rating), 0) AS average_rating,
    max(rating) AS peak_rating,
    round(avg(accuracy), 1) AS average_accuracy
FROM ` + schema + `.game_results
GROUP BY name, username, title, country_code
ORDER BY score_percent DESC, games DESC
`
}

func chessOpeningRepertoireSQL(schema string) string {
	return `/* @bruin
name: ` + schema + `.opening_repertoire
type: duckdb.sql
materialization:
  type: table
depends:
  - ` + schema + `.game_results
meta:
  web_table_dense: "false"
  web_view: table
@bruin */

WITH opening_rollup AS (
    SELECT
        name,
        username,
        color,
        time_class,
        eco_code,
        min(opening) AS opening,
        count(*) AS games,
        round(100 * avg(score), 1) AS score_percent,
        round(avg(accuracy), 1) AS average_accuracy
    FROM ` + schema + `.game_results
    WHERE eco_code IS NOT NULL
    GROUP BY name, username, color, time_class, eco_code
    HAVING count(*) >= 2
),

ranked_openings AS (
    SELECT
        *,
        row_number() OVER (
            PARTITION BY username, color, time_class
            ORDER BY games DESC, score_percent DESC, eco_code
        ) AS repertoire_rank
    FROM opening_rollup
)

SELECT * EXCLUDE (repertoire_rank)
FROM ranked_openings
WHERE repertoire_rank <= 5
ORDER BY username, time_class, color, games DESC, score_percent DESC
`
}

func DefaultOnboardingConnectionName(typeName string) string {
	trimmed := strings.TrimSpace(typeName)
	if trimmed == "" {
		return "default-connection"
	}
	return trimmed + "-default"
}

func ensureDiscoveryDraftValues(typeName string, values map[string]any, selectedDatabase string) error {
	trimmedType := strings.TrimSpace(typeName)
	trimmedDatabase := strings.TrimSpace(selectedDatabase)

	if trimmedType == "duckdb" {
		if stringValue(values["path"]) == "" {
			return fmt.Errorf("path is required")
		}
		return nil
	}

	if stringValue(values["database"]) != "" {
		return nil
	}

	if trimmedDatabase != "" {
		values["database"] = trimmedDatabase
		return nil
	}

	switch trimmedType {
	case "postgres":
		values["database"] = "postgres"
	case "redshift":
		values["database"] = "dev"
	}

	return nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	trimmed := strings.TrimSpace(fmt.Sprint(value))
	if trimmed == "<nil>" {
		return ""
	}
	return trimmed
}
