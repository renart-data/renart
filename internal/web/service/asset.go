package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

type AssetUpdateRequest struct {
	Name                    *string                          `json:"name,omitempty"`
	Type                    *string                          `json:"type,omitempty"`
	Content                 *string                          `json:"content,omitempty"`
	Connection              *string                          `json:"connection,omitempty"`
	ConnectionSelection     *AssetConnectionSelectionRequest `json:"connection_selection,omitempty"`
	MaterializationType     *string                          `json:"materialization_type,omitempty"`
	MaterializationStrategy *string                          `json:"materialization_strategy,omitempty"`
	IncrementalKey          *string                          `json:"incremental_key,omitempty"`
	PartitionBy             *string                          `json:"partition_by,omitempty"`
	ClusterBy               []string                         `json:"cluster_by,omitempty"`
	TimeGranularity         *string                          `json:"time_granularity,omitempty"`
	Owner                   *string                          `json:"owner,omitempty"`
	Tags                    []string                         `json:"tags,omitempty"`
	Meta                    map[string]string                `json:"meta,omitempty"`
	Upstreams               []string                         `json:"upstreams,omitempty"`
	Parameters              map[string]string                `json:"parameters,omitempty"`
}

// AssetConnectionSelectionRequest is the semantic existing-asset connection
// edit contract. The server derives the concrete asset type from the selected
// connection and keeps the type/connection write atomic. Cross-engine changes
// require an explicit confirmation and an expected current type so a stale UI
// cannot silently migrate a newer filesystem edit.
type AssetConnectionSelectionRequest struct {
	Environment          string `json:"environment,omitempty"`
	Connection           string `json:"connection,omitempty"`
	UsePipelineDefault   bool   `json:"use_pipeline_default,omitempty"`
	ExpectedAssetType    string `json:"expected_asset_type,omitempty"`
	ConfirmTypeMigration bool   `json:"confirm_type_migration,omitempty"`
}

type AssetMutationResponse struct {
	Status     string `json:"status"`
	AssetID    string `json:"asset_id,omitempty"`
	AssetPath  string `json:"asset_path,omitempty"`
	AssetType  string `json:"asset_type,omitempty"`
	Connection string `json:"connection,omitempty"`
	Dialect    string `json:"dialect,omitempty"`
}

// SeedFilePreviewResponse is the bounded, text-only view of a seed sidecar
// used by the guided editor. Displayable is false for sources that should not
// be read into the browser (for example remote, binary, or oversized files).
type SeedFilePreviewResponse struct {
	Status            string `json:"status"`
	AssetID           string `json:"asset_id"`
	FileType          string `json:"file_type,omitempty"`
	SizeBytes         int64  `json:"size_bytes,omitempty"`
	Displayable       bool   `json:"displayable"`
	Content           string `json:"content,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type StatusResponse struct {
	Status string `json:"status"`
}

type FormatSQLAssetRequest struct {
	Content string `json:"content"`
}

type FormatSQLAssetResponse struct {
	Status  string `json:"status"`
	AssetID string `json:"asset_id"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

type FormatPythonAssetRequest struct {
	Content string `json:"content"`
}

type FormatPythonAssetResponse struct {
	Status  string `json:"status"`
	AssetID string `json:"asset_id"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

type PythonDiagnosticsRequest struct {
	Content string `json:"content"`
}

type PythonCompletionsRequest struct {
	Content  string `json:"content"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Snippets bool   `json:"snippets"`
}

type PythonPositionRequest struct {
	Content string `json:"content"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

type PythonDiagnosticsResponse struct {
	Status      string             `json:"status"`
	AssetID     string             `json:"asset_id"`
	Diagnostics []PythonDiagnostic `json:"diagnostics,omitempty"`
	Error       string             `json:"error,omitempty"`
}

type PythonDiagnostic struct {
	ID         string       `json:"id"`
	Code       string       `json:"code,omitempty"`
	Source     string       `json:"source,omitempty"`
	Message    string       `json:"message"`
	Severity   string       `json:"severity"`
	Range      *PythonRange `json:"range,omitempty"`
	Display    string       `json:"display,omitempty"`
	Scope      string       `json:"scope,omitempty"`
	Confidence string       `json:"confidence,omitempty"`
}

type PythonRange struct {
	Start PythonPosition `json:"start"`
	End   PythonPosition `json:"end"`
}

type PythonPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type PythonCompletionsResponse struct {
	Status      string             `json:"status"`
	AssetID     string             `json:"asset_id"`
	Completions []PythonCompletion `json:"completions,omitempty"`
	Error       string             `json:"error,omitempty"`
}

type PythonCompletion struct {
	Label               string           `json:"label"`
	Kind                string           `json:"kind,omitempty"`
	Detail              string           `json:"detail,omitempty"`
	InsertText          string           `json:"insert_text,omitempty"`
	InsertTextFormat    string           `json:"insert_text_format"`
	Documentation       string           `json:"documentation,omitempty"`
	ModuleName          string           `json:"module_name,omitempty"`
	AdditionalTextEdits []PythonTextEdit `json:"additional_text_edits,omitempty"`
}

type PythonTextEdit struct {
	Range PythonRange `json:"range"`
	Text  string      `json:"text"`
}

type PythonHoverResponse struct {
	Status  string       `json:"status"`
	AssetID string       `json:"asset_id"`
	Hover   *PythonHover `json:"hover,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type PythonHover struct {
	Contents string       `json:"contents"`
	Range    *PythonRange `json:"range,omitempty"`
}

type PythonSignatureHelpResponse struct {
	Status        string               `json:"status"`
	AssetID       string               `json:"asset_id"`
	SignatureHelp *PythonSignatureHelp `json:"signature_help,omitempty"`
	Error         string               `json:"error,omitempty"`
}

type PythonSignatureHelp struct {
	Signatures      []PythonSignature `json:"signatures"`
	ActiveSignature *int              `json:"active_signature,omitempty"`
	ActiveParameter *int              `json:"active_parameter,omitempty"`
}

type PythonSignature struct {
	Label           string                     `json:"label"`
	Documentation   string                     `json:"documentation,omitempty"`
	Parameters      []PythonSignatureParameter `json:"parameters"`
	ActiveParameter *int                       `json:"active_parameter,omitempty"`
}

type PythonSignatureParameter struct {
	Label         string `json:"label"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Documentation string `json:"documentation,omitempty"`
}

type PythonGotoDefinitionResponse struct {
	Status  string             `json:"status"`
	AssetID string             `json:"asset_id"`
	Targets []PythonGotoTarget `json:"targets,omitempty"`
	Error   string             `json:"error,omitempty"`
}

type PythonGotoTarget struct {
	Path       string      `json:"path"`
	FocusRange PythonRange `json:"focus_range"`
	FullRange  PythonRange `json:"full_range"`
}

type AssetDependencies struct {
	Fs                                         afero.Fs
	WorkspaceRoot                              string
	ConfigPath                                 string
	Executor                                   BruinCommandExecutor
	ResolveAssetByID                           func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	DefaultAssetContent                        func(string, string, string) string
	DerivedAssetContent                        func(string, string, string, string, string) string
	EnsurePythonProject                        func(string, string, string) error
	SuppressWatcher                            func(string)
	PushWorkspaceUpdateImmediate               func(context.Context, string, string)
	PushWorkspaceUpdateImmediateWithChangedIDs func(context.Context, string, string, []string)
	PushAssetContentUpdateImmediate            func(string, string, []string, string)
	ConnectionTypeFor                          func(string) string
	SelectedEnvironment                        func() string
	CurrentState                               func() WorkspaceState
	// MaterializedSchemaFresh reports whether the selected asset's current
	// materialized output was produced from its current source fingerprint. It
	// is optional; schema reconciliation fails closed to advisory trust when the
	// staleness service is unavailable.
	MaterializedSchemaFresh func(context.Context, string, string, string) (bool, error)
}

type AssetService struct {
	deps                    AssetDependencies
	createMu                sync.Mutex
	assetFileMu             sync.Mutex
	assetFileLocks          map[string]*sync.Mutex
	pythonPackageMountMu    sync.Mutex
	pythonPackageMountCache map[string]pythonPackageMountCacheEntry
	pythonTySessionMu       sync.Mutex
	pythonTySessionFiles    map[string]string
	pythonTyCallCount       int
	seedSchemaMu            sync.Mutex
	seedSchemaCache         map[string][]WorkspaceColumn
	seedSchemaCacheOrder    []string
	seedSchemaInflight      map[string]*seedSchemaDiscoveryCall
}

func NewAssetService(deps AssetDependencies) *AssetService {
	return &AssetService{
		deps:                    deps,
		assetFileLocks:          make(map[string]*sync.Mutex),
		pythonPackageMountCache: make(map[string]pythonPackageMountCacheEntry),
		pythonTySessionFiles:    make(map[string]string),
		seedSchemaCache:         make(map[string][]WorkspaceColumn),
		seedSchemaInflight:      make(map[string]*seedSchemaDiscoveryCall),
	}
}

// lockAssetFile serializes read-modify-write access to a single asset file.
// Without it, two overlapping saves can interleave their truncate + write
// cycles, letting one goroutine read a torn/empty file, lose the @bruin header,
// and persist a headerless file that makes the asset disappear.
// Returns the unlock function. Keyed by the cleaned absolute path.
func (s *AssetService) lockAssetFile(absPath string) func() {
	key := filepath.Clean(absPath)
	s.assetFileMu.Lock()
	if s.assetFileLocks == nil {
		s.assetFileLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := s.assetFileLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		s.assetFileLocks[key] = mu
	}
	s.assetFileMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func (s *AssetService) fs() afero.Fs {
	if s.deps.Fs != nil {
		return s.deps.Fs
	}
	return afero.NewOsFs()
}

func (s *AssetService) resolver() *WorkspaceResolver {
	return NewWorkspaceResolver(s.deps.WorkspaceRoot, nil)
}

func (s *AssetService) Create(ctx context.Context, pipelineID string, req CreateAssetParams) (AssetMutationResponse, *APIError) {
	_, pipelinePath, err := s.resolver().DecodePipelineID(pipelineID)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(400, "invalid_pipeline_id", "invalid pipeline id")
	}
	if req.Name == "" && req.Path == "" && req.SourceAssetID == "" {
		return AssetMutationResponse{}, newAPIError(400, "missing_name_or_path", "name or path is required")
	}

	var sourceAsset *pipeline.Asset
	var sourcePipeline *pipeline.Pipeline
	var sourceConnectionName string
	if strings.TrimSpace(req.SourceAssetID) != "" {
		_, resolvedPipeline, resolvedAsset, resolveErr := s.deps.ResolveAssetByID(ctx, req.SourceAssetID)
		if resolveErr != nil {
			return AssetMutationResponse{}, newAPIError(400, "invalid_source_asset_id", resolveErr.Error())
		}
		if !pipelinePathsReferToSameRoot(resolvedPipeline.DefinitionFile.Path, pipelinePath) {
			return AssetMutationResponse{}, newAPIError(400, "invalid_source_asset", "source asset must belong to the selected pipeline")
		}
		sourceAsset = resolvedAsset
		sourcePipeline = resolvedPipeline
		if conn, connErr := targetConnectionNameForAsset(sourceAsset, sourcePipeline); connErr == nil {
			sourceConnectionName = conn
		}
	}
	if normalizeAssetCreationKind(req.Kind) == assetCreationKindLoad && sourceAsset != nil {
		if req.Parameters == nil {
			req.Parameters = map[string]string{}
		}
		if strings.TrimSpace(req.Parameters[loadParamSourceConnection]) == "" {
			req.Parameters[loadParamSourceConnection] = sourceConnectionName
		}
		if strings.TrimSpace(req.Parameters[loadParamSourceTable]) == "" {
			req.Parameters[loadParamSourceTable] = sourceAsset.Name
		}
	}

	var creationResolution AssetCreationResolution
	if strings.TrimSpace(req.Kind) != "" {
		resolved, resolveErr := s.resolveAssetCreation(ctx, pipelinePath, req)
		if resolveErr != nil {
			return AssetMutationResponse{}, resolveErr
		}
		creationResolution = resolved
		req.Type = resolved.AssetType
	} else if validationErr := s.validateLegacyAssetCreation(ctx, pipelinePath, req); validationErr != nil {
		return AssetMutationResponse{}, validationErr
	}

	assetName := strings.TrimSpace(req.Name)
	if assetName == "" && sourceAsset != nil {
		assetName = deriveDownstreamAssetName(sourceAsset.Name, sourcePipeline)
	}
	if assetName != "" && !strings.Contains(assetName, ".") {
		return AssetMutationResponse{}, newAPIError(400, "missing_asset_prefix", "asset name must include a prefix, for example analytics.orders")
	}

	relAssetPath := req.Path
	if relAssetPath == "" {
		if sourceAsset != nil {
			assetTypeForPath := strings.TrimSpace(req.Type)
			if assetTypeForPath == "" {
				assetTypeForPath = deriveSQLAssetTypeForSource(sourceAsset, sourcePipeline, sourceConnectionName)
			}
			// Root the path at the pipeline's assets/ dir using the full prefixed
			// name so bruin can infer it back from the path (SQL assets carry no
			// explicit name:). Joining the source's dir with only the leaf dropped
			// the prefix when the source lived directly under assets/ (e.g. a
			// notebook-promoted asset), yielding an unprefixed, invalid path.
			relAssetPath = assetPathForInferredName(assetName, extensionForAssetType(assetTypeForPath))
		} else {
			relAssetPath = assetPathForInferredName(assetName, extensionForAssetType(req.Type))
		}
	}
	if inferredAssetNameFromPath(relAssetPath) == "" || !strings.Contains(inferredAssetNameFromPath(relAssetPath), ".") {
		return AssetMutationResponse{}, newAPIError(400, "missing_asset_prefix", "asset path must infer a prefixed asset name under assets/<prefix>/")
	}

	absAssetPath, err := SafeJoin(pipelinePath, relAssetPath)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(400, "invalid_asset_path", err.Error())
	}
	fs := s.fs()

	assetType := strings.TrimSpace(req.Type)
	if assetType == "" {
		if sourceAsset != nil {
			assetType = deriveSQLAssetTypeForSource(sourceAsset, sourcePipeline, sourceConnectionName)
		} else {
			assetType = inferAssetTypeFromPath(relAssetPath)
		}
	}

	content := req.Content
	semanticAsset := strings.HasSuffix(strings.ToLower(assetType), ".seed") || isSensorAssetType(pipeline.AssetType(assetType))
	semanticFiles := semanticAssetFiles{}
	if semanticAsset {
		var semanticErr *APIError
		semanticFiles, semanticErr = s.buildSemanticAssetFiles(assetName, assetType, absAssetPath, req)
		if semanticErr != nil {
			return AssetMutationResponse{}, semanticErr
		}
		content = string(semanticFiles.definition)
	} else if isLoadAssetType(assetType) {
		// Load creation is semantic: always render the canonical single-file
		// definition from the request fields so callers cannot reintroduce the
		// removed destination_connection/destination_table/mode representation.
		createPipeline := sourcePipeline
		if createPipeline == nil {
			createPipeline, _ = NewRenartPipelineBuilder(fs).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
		}
		sourceConnection := strings.TrimSpace(req.Parameters[loadParamSourceConnection])
		sourceTable := strings.TrimSpace(req.Parameters[loadParamSourceTable])
		depends := []string(nil)
		if sourceAsset != nil {
			if sourceConnection == "" {
				sourceConnection = sourceConnectionName
			}
			if sourceTable == "" {
				sourceTable = sourceAsset.Name
			}
			depends = []string{sourceAsset.Name}
		}
		loadAsset := &pipeline.Asset{
			Name:       assetName,
			Type:       pipeline.AssetType(loadAssetType),
			Connection: strings.TrimSpace(req.Connection),
		}
		if _, connectionErr := loadConnectionNameForAsset(loadAsset, createPipeline); connectionErr != nil {
			return AssetMutationResponse{}, newAPIError(400, "invalid_load_target_connection", connectionErr.Error())
		}
		var renderErr error
		content, renderErr = renderLoadAssetContent(
			req.Connection,
			sourceConnection,
			sourceTable,
			req.Parameters[loadParamDestinationObject],
			depends,
		)
		if renderErr != nil {
			return AssetMutationResponse{}, newAPIError(400, "invalid_load_asset", renderErr.Error())
		}
	} else {
		if content == "" {
			if sourceAsset != nil {
				content = s.deps.DerivedAssetContent(assetName, assetType, relAssetPath, sourceAsset.Name, sourceConnectionName)
			} else {
				content = s.deps.DefaultAssetContent(assetName, assetType, relAssetPath)
			}
		}
		if req.ExecutableContent != "" {
			content = MergeExecutableContent(content, req.ExecutableContent)
		}
		if creationResolution.Kind == assetCreationKindAPI {
			var canonicalizeErr error
			content, canonicalizeErr = canonicalizeCreatedAPIAssetContent(content, req.Connection)
			if canonicalizeErr != nil {
				return AssetMutationResponse{}, newAPIError(400, "invalid_api_asset", canonicalizeErr.Error())
			}
		} else if creationResolution.Kind == assetCreationKindSQL || creationResolution.Kind == assetCreationKindPython {
			content = applyCreatedExecutableConnection(content, req.Connection)
		}
	}

	// Semantic seed/sensor definitions are rendered above. Uploaded seed bytes
	// and their definition are staged and committed together, so a failed write
	// cannot leave a half-created asset in the workspace.
	if semanticAsset {
		s.createMu.Lock()
		defer s.createMu.Unlock()
		for _, candidate := range []string{absAssetPath, semanticFiles.sidecarPath} {
			if candidate == "" {
				continue
			}
			exists, existsErr := afero.Exists(fs, candidate)
			if existsErr != nil {
				return AssetMutationResponse{}, newAPIError(500, "asset_stat_failed", existsErr.Error())
			}
			if exists {
				return AssetMutationResponse{}, newAPIError(409, "asset_path_exists", fmt.Sprintf("a file already exists at %s", filepath.Base(candidate)))
			}
		}
		if err := fs.MkdirAll(filepath.Dir(absAssetPath), 0o755); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_dir_create_failed", err.Error())
		}
		if err := writeNewSemanticAssetFiles(fs, absAssetPath, semanticFiles); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_write_failed", err.Error())
		}
	} else {
		// Load assets are now a single flat-parameter .asset.yml (the DefaultAsset/
		// DerivedAsset content producers emit that shape), so they write like any
		// other single-file asset — no .sling.yml replication sidecar.
		if err := fs.MkdirAll(filepath.Dir(absAssetPath), 0o755); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_dir_create_failed", err.Error())
		}
		if err := afero.WriteFile(fs, absAssetPath, []byte(content), 0o644); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_write_failed", err.Error())
		}
	}
	if !semanticAsset {
		if err := s.deps.EnsurePythonProject(absAssetPath, assetType, relAssetPath); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "pyproject_write_failed", err.Error())
		}
	}
	relWorkspaceAssetPath, _ := filepath.Rel(s.deps.WorkspaceRoot, absAssetPath)
	assetPath := filepath.ToSlash(relWorkspaceAssetPath)
	if semanticFiles.sidecarPath != "" {
		if relSidecarPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, semanticFiles.sidecarPath); relErr == nil {
			s.deps.SuppressWatcher(filepath.ToSlash(relSidecarPath))
		}
	}
	if strings.HasSuffix(strings.ToLower(assetPath), ".sql") {
		if err := s.reconcileSQLAssetDependencies(ctx, assetPath); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_dependency_reconcile_failed", err.Error())
		}
	} else if isLoadAssetType(assetType) {
		// Auto-infer the upstream from the source mapping (best-effort: a newly
		// created skeleton often has no resolvable source yet).
		if err := s.reconcileLoadAssetDependencies(ctx, assetPath); err != nil {
			_ = err
		}
	}
	s.deps.SuppressWatcher(assetPath)
	s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.created", assetPath)
	dialect := creationResolution.Dialect
	if dialect == "" {
		dialect, _ = AssetTypeToDialect(pipeline.AssetType(assetType))
	}
	effectiveConnection := creationResolution.EffectiveConnection
	if effectiveConnection == "" {
		effectiveConnection = strings.TrimSpace(req.Connection)
	}
	return AssetMutationResponse{
		Status:     "ok",
		AssetID:    EncodeID(assetPath),
		AssetPath:  assetPath,
		AssetType:  assetType,
		Connection: effectiveConnection,
		Dialect:    dialect,
	}, nil
}

type CreateAssetParams struct {
	Name               string            `json:"name"`
	Kind               string            `json:"kind,omitempty"`
	Type               string            `json:"type"`
	Path               string            `json:"path"`
	Content            string            `json:"content"`
	ExecutableContent  string            `json:"executable_content"`
	Connection         string            `json:"connection"`
	Environment        string            `json:"environment,omitempty"`
	UsePipelineDefault bool              `json:"use_pipeline_default,omitempty"`
	Variant            string            `json:"variant,omitempty"`
	Parameters         map[string]string `json:"parameters"`
	SourceAssetID      string            `json:"source_asset_id"`
	SeedFileName       string            `json:"seed_file_name"`
	SeedFileContent    string            `json:"seed_file_content"`
	SeedFileBytes      []byte            `json:"-"`
}

// applyCreatedExecutableConnection only operates on Renart's freshly generated
// SQL/Python templates. It keeps an explicit target in the @bruin header, or
// removes the Python starter's historical DuckDB default when the user chose
// the resolved pipeline default.
func applyCreatedExecutableConnection(content, connection string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	headerEnd := -1
	connectionLine := -1
	insertAfter := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if index > 0 && (trimmed == "@bruin */" || trimmed == `@bruin \"\"\"`) {
			headerEnd = index
			break
		}
		if strings.HasPrefix(trimmed, "connection:") {
			connectionLine = index
		}
		if strings.HasPrefix(trimmed, "type:") || strings.HasPrefix(trimmed, "image:") {
			insertAfter = index
		}
	}
	if headerEnd < 0 {
		return content
	}
	connection = strings.TrimSpace(connection)
	if connectionLine >= 0 {
		if connection == "" {
			lines = append(lines[:connectionLine], lines[connectionLine+1:]...)
		} else {
			lines[connectionLine] = "connection: " + connection
		}
		return strings.Join(lines, "\n")
	}
	if connection == "" {
		return strings.Join(lines, "\n")
	}
	if insertAfter < 0 || insertAfter >= headerEnd {
		insertAfter = 0
	}
	lines = append(lines[:insertAfter+1], append([]string{"connection: " + connection}, lines[insertAfter+1:]...)...)
	return strings.Join(lines, "\n")
}

func (s *AssetService) Update(ctx context.Context, assetID string, req AssetUpdateRequest) (AssetMutationResponse, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(400, "invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(400, "invalid_asset_path", err.Error())
	}

	// Serialize the whole read-modify-write against any concurrent save or the
	// async SQL patch for the same file, so neither can observe a torn write.
	unlock := s.lockAssetFile(absAssetPath)
	defer unlock()

	fs := s.fs()
	originalBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(500, "asset_read_failed", err.Error())
	}
	originalHadExplicitName := assetContentHasExplicitName(string(originalBytes))
	originalExecutable := ExtractExecutableContent(string(originalBytes))
	desiredExecutable := originalExecutable
	if req.Content != nil {
		desiredExecutable = *req.Content
	}
	executableChanged := req.Content != nil && normalizeExecutableContent(desiredExecutable) != normalizeExecutableContent(originalExecutable)

	changedAssetIDs := []string{assetID}
	changedAssetPaths := []string{filepath.ToSlash(relAssetPath)}
	nextAssetID := assetID
	nextRelAssetPath := filepath.ToSlash(relAssetPath)
	inferredRenameRelAssetPath := ""
	// Set when a YAML-defined asset has already been written through the
	// node-preserving codec, so we skip the executable-file rewrite below (which
	// would clobber the codec's output for api assets whose definition == executable).
	persistedViaCodec := false
	loadAssetUpdated := false
	connectionResolution := AssetCreationResolution{}
	hasConnectionResolution := false

	if req.Name != nil || req.Type != nil || req.Connection != nil || req.ConnectionSelection != nil || req.MaterializationType != nil || req.MaterializationStrategy != nil || req.IncrementalKey != nil || req.PartitionBy != nil || req.ClusterBy != nil || req.TimeGranularity != nil || req.Owner != nil || req.Tags != nil || req.Meta != nil || req.Upstreams != nil || req.Parameters != nil {
		_, parsedPipeline, asset, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr != nil {
			return AssetMutationResponse{}, newAPIError(400, "asset_resolve_failed", resolveErr.Error())
		}
		pipelinePath := filepath.Dir(parsedPipeline.DefinitionFile.Path)
		if req.ConnectionSelection != nil {
			if req.Type != nil || req.Connection != nil {
				return AssetMutationResponse{}, newAPIError(400, "conflicting_connection_update", "connection_selection cannot be combined with type or connection")
			}
			resolved, selectionErr := s.resolveAssetConnectionSelection(ctx, pipelinePath, asset, *req.ConnectionSelection)
			if selectionErr != nil {
				return AssetMutationResponse{}, selectionErr
			}
			nextType := resolved.AssetType
			nextConnection := strings.TrimSpace(req.ConnectionSelection.Connection)
			req.Type = &nextType
			req.Connection = &nextConnection
			connectionResolution = resolved
			hasConnectionResolution = true
		}
		if isLoadAsset(asset) {
			originalHadExplicitName = true
			loadAssetUpdated = true
		}

		originalAssetName := asset.Name
		renamedAsset := false
		if req.Name != nil {
			nextName := strings.TrimSpace(*req.Name)
			if nextName == "" {
				return AssetMutationResponse{}, newAPIError(400, "invalid_asset_name", "asset name cannot be empty")
			}
			if !strings.Contains(nextName, ".") {
				return AssetMutationResponse{}, newAPIError(400, "missing_asset_prefix", "asset name must include a prefix, for example analytics.orders")
			}
			if existing := getAssetByNameCaseInsensitiveLocal(parsedPipeline, nextName); existing != nil && existing.DefinitionFile.Path != asset.DefinitionFile.Path {
				return AssetMutationResponse{}, newAPIError(400, "duplicate_asset_name", fmt.Sprintf("an asset named %q already exists", nextName))
			}
			if nextName != asset.Name {
				asset.Name = nextName
				renamedAsset = true
				if !originalHadExplicitName {
					extension := filepath.Ext(relAssetPath)
					inferredRenameRelAssetPath = filepath.ToSlash(filepath.Join(pipelineRelPathForAsset(relAssetPath), assetPathForInferredName(nextName, extension)))
				}
			}
		}
		if req.Type != nil {
			nextType := strings.TrimSpace(*req.Type)
			if nextType == "" {
				return AssetMutationResponse{}, newAPIError(400, "invalid_asset_type", "asset type cannot be empty")
			}
			if nextType != strings.TrimSpace(string(asset.Type)) && !hasConnectionResolution {
				return AssetMutationResponse{}, newAPIError(409, "asset_type_change_requires_migration", "asset type is derived from its connection; use the reviewed connection migration")
			}
			asset.Type = pipeline.AssetType(nextType)
		}
		if req.MaterializationType != nil {
			asset.Materialization.Type = pipeline.MaterializationType(strings.ToLower(strings.TrimSpace(*req.MaterializationType)))
		}
		if req.MaterializationStrategy != nil {
			asset.Materialization.Strategy = pipeline.MaterializationStrategy(strings.ToLower(strings.TrimSpace(*req.MaterializationStrategy)))
		}
		if req.IncrementalKey != nil {
			asset.Materialization.IncrementalKey = strings.TrimSpace(*req.IncrementalKey)
		}
		if req.PartitionBy != nil {
			asset.Materialization.PartitionBy = strings.TrimSpace(*req.PartitionBy)
		}
		if req.ClusterBy != nil {
			asset.Materialization.ClusterBy = compactWorkspaceStringArray(req.ClusterBy)
		}
		if req.TimeGranularity != nil {
			granularity := strings.ToLower(strings.TrimSpace(*req.TimeGranularity))
			switch pipeline.MaterializationTimeGranularity(granularity) {
			case "", pipeline.MaterializationTimeGranularityDate, pipeline.MaterializationTimeGranularityTimestamp:
				asset.Materialization.TimeGranularity = pipeline.MaterializationTimeGranularity(granularity)
			default:
				return AssetMutationResponse{}, badRequestError("invalid_time_granularity", "time granularity must be date or timestamp")
			}
		}
		if req.Connection != nil {
			if !hasConnectionResolution {
				if connectionErr := s.validateDirectAssetConnectionUpdate(ctx, pipelinePath, asset, *req.Connection); connectionErr != nil {
					return AssetMutationResponse{}, connectionErr
				}
			}
			asset.Connection = strings.TrimSpace(*req.Connection)
		}
		if req.Owner != nil {
			asset.Owner = strings.TrimSpace(*req.Owner)
		}
		if req.Tags != nil {
			nextTags := make([]string, 0, len(req.Tags))
			for _, tag := range req.Tags {
				if trimmed := strings.TrimSpace(tag); trimmed != "" {
					nextTags = append(nextTags, trimmed)
				}
			}
			asset.Tags = nextTags
		}
		if req.Meta != nil {
			nextMeta := make(map[string]string)
			for rawKey, rawValue := range req.Meta {
				key := strings.TrimSpace(rawKey)
				if key == "" {
					continue
				}
				nextMeta[key] = rawValue
			}
			if len(nextMeta) == 0 {
				asset.Meta = nil
			} else {
				asset.Meta = nextMeta
			}
		}
		if req.Upstreams != nil {
			applyManualAssetUpstreams(asset, parsedPipeline, req.Upstreams)
		}
		if req.Parameters != nil {
			nextParameters := pipeline.ParameterMap{}
			for rawKey, rawValue := range req.Parameters {
				key := strings.TrimSpace(rawKey)
				if key == "" {
					continue
				}
				nextParameters[key] = rawValue
			}
			asset.Parameters = nextParameters
		}
		if (req.Type != nil || req.Connection != nil || req.Parameters != nil) &&
			(strings.HasSuffix(strings.ToLower(string(asset.Type)), ".seed") || isSensorAssetType(asset.Type)) {
			if semanticErr := s.validateSemanticAssetUpdate(asset); semanticErr != nil {
				return AssetMutationResponse{}, semanticErr
			}
		}
		materializationChanged := req.Type != nil || req.Connection != nil || req.MaterializationType != nil || req.MaterializationStrategy != nil || req.IncrementalKey != nil || req.PartitionBy != nil || req.ClusterBy != nil || req.TimeGranularity != nil
		if materializationChanged {
			connectionTypes := map[string]string{}
			if s.deps.ConnectionTypeFor != nil {
				if connectionName, connectionErr := targetConnectionNameForAsset(asset, parsedPipeline); connectionErr == nil {
					connectionTypes[connectionName] = s.deps.ConnectionTypeFor(connectionName)
				}
			}
			destinationType := materializationDestinationType(asset, parsedPipeline, connectionTypes)
			if capabilityErr := validateMaterializationCapability(asset, destinationType); capabilityErr != nil {
				return AssetMutationResponse{}, badRequestError("unsupported_materialization", capabilityErr.Error())
			}
			profile := materializationProfileFor(asset, destinationType)
			capability, capabilityKnown := materializationCapabilityForMode(profile, normalizedMaterializationMode(asset))
			if req.PartitionBy != nil && asset.Materialization.PartitionBy != "" && capabilityKnown && !capability.SupportsPartitionBy {
				return AssetMutationResponse{}, badRequestError("unsupported_partition_by", "partition_by is not supported by this materialization mode and destination")
			}
			if req.ClusterBy != nil && len(asset.Materialization.ClusterBy) > 0 && capabilityKnown && !capability.SupportsClusterBy {
				return AssetMutationResponse{}, badRequestError("unsupported_cluster_by", "cluster_by is not supported by this materialization mode and destination")
			}
		}
		if apiErr := loaderMaterializationAPIError(asset); apiErr != nil {
			return AssetMutationResponse{}, apiErr
		}
		if isYAMLDefinedAsset(asset) {
			// api/load/ingestr/plain-yaml: overlay managed fields onto the
			// definition file, preserving request-specific and other unmanaged
			// content (and columns, which the old per-type writers silently dropped).
			if apiErr := s.persistYAMLAssetPreservingInferredName(asset); apiErr != nil {
				return AssetMutationResponse{}, apiErr
			}
			persistedViaCodec = true
			if relDefinitionPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, asset.DefinitionFile.Path); relErr == nil {
				changedAssetPaths = appendUniqueStrings(changedAssetPaths, filepath.ToSlash(relDefinitionPath))
			}
		} else if err := asset.Persist(fs, parsedPipeline); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_persist_failed", err.Error())
		}
		if renamedAsset {
			affectedIDs, affectedPaths, refactorErr := s.RefactorDirectDependencies(ctx, parsedPipeline, originalAssetName, asset.Name)
			if refactorErr != nil {
				return AssetMutationResponse{}, newAPIError(500, "asset_rename_refactor_failed", refactorErr.Error())
			}
			changedAssetIDs = appendUniqueStrings(changedAssetIDs, affectedIDs...)
			changedAssetPaths = appendUniqueStrings(changedAssetPaths, affectedPaths...)
		}
	}

	// A YAML-defined asset was fully written by the codec above; rewriting the
	// executable file here would overwrite that (for API/Load the executable is
	// the definition file).
	if !persistedViaCodec {
		latestBytes, err := afero.ReadFile(fs, absAssetPath)
		if err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_read_failed", err.Error())
		}
		mergedContent := desiredExecutable
		if !isAPIExecutablePath(relAssetPath) {
			mergedContent = MergeExecutableContent(string(latestBytes), desiredExecutable)
		}
		if err := afero.WriteFile(fs, absAssetPath, []byte(mergedContent), 0o644); err != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_write_failed", err.Error())
		}
	}

	shouldReconcileDependencies := strings.HasSuffix(strings.ToLower(relAssetPath), ".sql") && (executableChanged || req.Upstreams != nil)
	if shouldReconcileDependencies {
		// Dependency reconciliation parses the SQL, which routinely fails while
		// the user is mid-typing an incomplete query. The content is already
		// saved, so treat this as best-effort: don't fail the save (which would
		// surface as a spurious error and leave the editor thinking it lost the
		// write). A later completed editor save retries reconciliation.
		if err := s.reconcileSQLAssetDependencies(ctx, relAssetPath); err != nil {
			_ = err // best-effort: keep the saved content
		}
	}
	if loadAssetUpdated {
		// Re-infer the sling upstream from the (possibly edited) source mapping.
		// Best-effort: a save with an unresolved source should still succeed.
		if err := s.reconcileLoadAssetDependencies(ctx, relAssetPath); err != nil {
			_ = err
		}
	}
	if inferredRenameRelAssetPath != "" && inferredRenameRelAssetPath != filepath.ToSlash(relAssetPath) {
		newAbsAssetPath, pathErr := s.resolver().JoinPath(inferredRenameRelAssetPath)
		if pathErr != nil {
			return AssetMutationResponse{}, newAPIError(400, "invalid_asset_path", pathErr.Error())
		}
		if exists, existsErr := afero.Exists(fs, newAbsAssetPath); existsErr != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_stat_failed", existsErr.Error())
		} else if exists {
			return AssetMutationResponse{}, newAPIError(400, "asset_path_exists", "an asset already exists at the inferred path")
		}
		currentBytes, readErr := afero.ReadFile(fs, absAssetPath)
		if readErr != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_read_failed", readErr.Error())
		}
		if mkdirErr := fs.MkdirAll(filepath.Dir(newAbsAssetPath), 0o755); mkdirErr != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_dir_create_failed", mkdirErr.Error())
		}
		if writeErr := afero.WriteFile(fs, newAbsAssetPath, []byte(removeAssetNameFieldFromContent(string(currentBytes))), 0o644); writeErr != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_write_failed", writeErr.Error())
		}
		if removeErr := fs.Remove(absAssetPath); removeErr != nil {
			return AssetMutationResponse{}, newAPIError(500, "asset_remove_failed", removeErr.Error())
		}
		nextRelAssetPath = inferredRenameRelAssetPath
		nextAssetID = EncodeID(inferredRenameRelAssetPath)
		changedAssetIDs = appendUniqueStrings(changedAssetIDs, nextAssetID)
		changedAssetPaths = appendUniqueStrings(changedAssetPaths, inferredRenameRelAssetPath)
	}
	for _, changedPath := range changedAssetPaths {
		s.deps.SuppressWatcher(changedPath)
	}
	s.deps.PushWorkspaceUpdateImmediateWithChangedIDs(ctx, "asset.updated", nextRelAssetPath, changedAssetIDs)
	response := AssetMutationResponse{Status: "ok", AssetID: nextAssetID, AssetPath: nextRelAssetPath}
	if hasConnectionResolution {
		response.AssetType = connectionResolution.AssetType
		response.Connection = connectionResolution.EffectiveConnection
		response.Dialect = connectionResolution.Dialect
	}
	return response, nil
}

func (s *AssetService) Delete(ctx context.Context, assetID string) (StatusResponse, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return StatusResponse{}, newAPIError(400, "invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return StatusResponse{}, newAPIError(400, "invalid_asset_path", err.Error())
	}
	fs := s.fs()
	var sidecarPath string
	if s.deps.ResolveAssetByID != nil {
		if _, _, asset, resolveErr := s.deps.ResolveAssetByID(ctx, assetID); resolveErr == nil {
			sidecarPath = ownedSeedSidecar(asset, absAssetPath)
		}
	}
	definitionBytes, readErr := afero.ReadFile(fs, absAssetPath)
	if readErr != nil {
		return StatusResponse{}, newAPIError(500, "asset_read_failed", readErr.Error())
	}
	if err := fs.Remove(absAssetPath); err != nil {
		return StatusResponse{}, newAPIError(500, "asset_delete_failed", err.Error())
	}
	if sidecarPath != "" {
		if err := fs.Remove(sidecarPath); err != nil && !os.IsNotExist(err) {
			if restoreErr := afero.WriteFile(fs, absAssetPath, definitionBytes, 0o644); restoreErr != nil {
				return StatusResponse{}, newAPIError(500, "seed_file_delete_failed", fmt.Sprintf("%v; restoring asset definition also failed: %v", err, restoreErr))
			}
			return StatusResponse{}, newAPIError(500, "seed_file_delete_failed", err.Error())
		}
		if relSidecarPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, sidecarPath); relErr == nil {
			s.deps.SuppressWatcher(filepath.ToSlash(relSidecarPath))
		}
	}
	s.deps.SuppressWatcher(filepath.ToSlash(relAssetPath))
	s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.deleted", relAssetPath)
	return StatusResponse{Status: "ok"}, nil
}

func extensionForAssetType(assetType string) string {
	lowered := strings.ToLower(strings.TrimSpace(assetType))
	switch {
	case isLoadAssetType(lowered):
		return ".asset.yml"
	case isAPIAssetType(lowered):
		return ".asset.yml"
	case isSensorAssetType(pipeline.AssetType(lowered)):
		return ".asset.yml"
	case strings.HasSuffix(lowered, ".seed"):
		return ".asset.yml"
	case strings.HasSuffix(lowered, ".py") || strings.Contains(lowered, "python"):
		return ".py"
	case strings.HasSuffix(lowered, ".sql") || strings.Contains(lowered, "sql"):
		return ".sql"
	default:
		return ".sql"
	}
}

func inferAssetTypeFromPath(path string) string {
	lowered := strings.ToLower(strings.TrimSpace(path))
	switch filepath.Ext(lowered) {
	case ".py":
		return "python"
	case ".sql":
		return "duckdb.sql"
	default:
		return "duckdb.sql"
	}
}

func deriveDownstreamAssetName(sourceAssetName string, parsedPipeline *pipeline.Pipeline) string {
	trimmed := strings.TrimSpace(sourceAssetName)
	if trimmed == "" {
		trimmed = "asset"
	}

	prefix := ""
	leaf := trimmed
	if lastDot := strings.LastIndex(trimmed, "."); lastDot >= 0 {
		prefix = trimmed[:lastDot]
		leaf = trimmed[lastDot+1:]
	}

	baseLeaf := SlugUnderscore(leaf)
	if baseLeaf == "" {
		baseLeaf = "asset"
	}
	baseLeaf += "_child"

	buildCandidate := func(index int) string {
		candidate := fmt.Sprintf("%s_%d", baseLeaf, index)
		if prefix == "" {
			return candidate
		}
		return prefix + "." + candidate
	}

	if parsedPipeline == nil {
		return buildCandidate(1)
	}

	exists := func(name string) bool {
		for _, asset := range parsedPipeline.Assets {
			if asset != nil && strings.EqualFold(strings.TrimSpace(asset.Name), name) {
				return true
			}
		}
		return false
	}

	for index := 1; index < 1000; index += 1 {
		candidate := buildCandidate(index)
		if !exists(candidate) {
			return candidate
		}
	}

	return buildCandidate(1)
}

func deriveSQLAssetTypeForSource(sourceAsset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sourceConnectionName string) string {
	if sourceAsset != nil {
		assetType := strings.TrimSpace(string(sourceAsset.Type))
		if strings.Contains(strings.ToLower(assetType), "sql") {
			return assetType
		}
		if strings.EqualFold(assetType, "ingestr") {
			destination, _ := sourceAsset.Parameters.GetString("destination")
			destination = strings.TrimSpace(destination)
			if destination != "" {
				if assetType, ok := sqlAssetTypeForIngestrDestination(destination); ok {
					return assetType
				}
				if assetType, ok := sqlAssetTypeForConnectionName(parsedPipeline, destination); ok {
					return assetType
				}
			}
			destinationConnection, _ := sourceAsset.Parameters.GetString("destination_connection")
			destinationConnection = strings.TrimSpace(destinationConnection)
			if assetType, ok := sqlAssetTypeForConnectionName(parsedPipeline, destinationConnection); ok {
				return assetType
			}
		}
	}
	if parsedPipeline != nil {
		for _, current := range parsedPipeline.Assets {
			if current == nil {
				continue
			}
			assetType := strings.TrimSpace(string(current.Type))
			if strings.Contains(strings.ToLower(assetType), "sql") {
				return assetType
			}
		}
	}
	return "duckdb.sql"
}

func DefaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName string) string {
	queryTarget := sourceAssetName
	if strings.TrimSpace(queryTarget) == "" {
		queryTarget = strings.TrimSuffix(filepath.Base(assetPath), filepath.Ext(assetPath))
	}
	lowered := strings.ToLower(strings.TrimSpace(assetType))

	// Python downstream: a Python asset that reads the upstream table through
	// the runner-injected Renart SDK and declares the dependency so lineage and
	// execution ordering stay correct.
	if lowered == "python" || strings.HasSuffix(strings.ToLower(assetPath), ".py") {
		connectionLine := ""
		if strings.TrimSpace(connectionName) != "" {
			connectionLine = fmt.Sprintf("connection: %s\n", strings.TrimSpace(connectionName))
		}
		return fmt.Sprintf("\"\"\" @bruin\n\nname: %s\ntype: python\n%smaterialization:\n  type: table\n\ndepends:\n  - %s\n\n@bruin \"\"\"\n\nfrom renart import query\n\n\ndef materialize():\n    return query(\"select * from %s\")\n", assetName, connectionLine, sourceAssetName, queryTarget)
	}

	// Load downstream: a single flat-parameter .asset.yml that reads the upstream
	// asset as its source. bruin parses `parameters` as flat strings, so the whole
	// replication intent stays in one bruin-loadable file (no .sling.yml sidecar).
	if isLoadAssetType(assetType) {
		sourceConnection := strings.TrimSpace(connectionName)
		if sourceConnection == "" {
			sourceConnection = "your_source_connection"
		}
		content, _ := renderLoadAssetContent(
			"your_destination_connection",
			sourceConnection,
			sourceAssetName,
			"",
			[]string{sourceAssetName},
		)
		return content
	}

	header := fmt.Sprintf("/* @bruin\n\ntype: %s\nmaterialization:\n  type: view\n\n@bruin */\n\n", assetType)
	return header + fmt.Sprintf("select * from %s\n", queryTarget)
}

// EnsurePythonProjectFile seeds a default pipeline-level pyproject.toml (pandas)
// for a new Python asset so it runs out of the box with uv. It is a no-op when any
// ancestor up to the workspace root already declares Python dependencies
// (pyproject.toml or a legacy requirements.txt), avoiding redundant manifests.
func EnsurePythonProjectFile(absAssetPath, assetType, relAssetPath string) error {
	loweredType := strings.ToLower(strings.TrimSpace(assetType))
	loweredPath := strings.ToLower(strings.TrimSpace(relAssetPath))
	if !strings.HasSuffix(loweredPath, ".py") && !strings.Contains(loweredType, "python") {
		return nil
	}
	startDir := filepath.Dir(absAssetPath)
	workspaceRoot := filepath.Clean(strings.TrimSuffix(absAssetPath, filepath.FromSlash(relAssetPath)))
	if nearestPythonDependencyFile(startDir, workspaceRoot, pyprojectFile) != "" ||
		nearestPythonDependencyFile(startDir, workspaceRoot, "requirements.txt") != "" {
		return nil
	}
	projectDir := nearestPipelineRoot(startDir, workspaceRoot)
	if projectDir == "" {
		projectDir = startDir
	}
	return writePyprojectDependencies(filepath.Join(projectDir, pyprojectFile), "renart-pipeline", []string{"pandas"})
}

func defaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName string) string {
	return DefaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName)
}

func ensurePythonProjectFile(absAssetPath, assetType, relAssetPath string) error {
	return EnsurePythonProjectFile(absAssetPath, assetType, relAssetPath)
}
