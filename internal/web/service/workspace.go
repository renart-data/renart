package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/glossary"
	"github.com/bruin-data/bruin/pkg/jinja"
	bruinpath "github.com/bruin-data/bruin/pkg/path"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/dependencygraph"
	"renart/internal/web/identity"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/policy"
)

// PipelineDefinitionFiles are the filenames that define a pipeline.
var PipelineDefinitionFiles = []string{"pipeline.yml", "pipeline.yaml"}

// AssetsDirectoryNames are the directories that contain assets.
var AssetsDirectoryNames = []string{"assets", "tasks"}

// BuilderConfig holds the default builder configuration.
var BuilderConfig = pipeline.BuilderConfig{
	PipelineFileName:    PipelineDefinitionFiles,
	TasksDirectoryNames: AssetsDirectoryNames,
	TasksFileSuffixes:   []string{"task.yml", "task.yaml", "asset.yml", "asset.yaml"},
}

// DefaultGlossaryReader is the default glossary reader.
var DefaultGlossaryReader = &glossary.GlossaryReader{
	RepoFinder: &git.RepoFinder{},
	FileNames:  []string{"glossary.yml", "glossary.yaml"},
}

// WorkspaceService manages workspace state and operations.
type WorkspaceService struct {
	workspaceRoot string
	configPath    string
	stateMu       sync.RWMutex
	state         model.WorkspaceState
}

// NewDefaultPipelineBuilder constructs the standard Bruin pipeline builder
// used across services and commands.
func NewDefaultPipelineBuilder() *pipeline.Builder {
	osFS := afero.NewOsFs()
	return pipeline.NewBuilder(
		BuilderConfig,
		pipeline.CreateTaskFromYamlDefinition(osFS),
		pipeline.CreateTaskFromFileComments(osFS),
		osFS,
		DefaultGlossaryReader,
		jinja.VariantRendererFactory,
	)
}

// NewWorkspaceService creates a new workspace service.
func NewWorkspaceService(workspaceRoot, configPath string) *WorkspaceService {
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(workspaceRoot, ".bruin.yml")
	}

	return &WorkspaceService{
		workspaceRoot: workspaceRoot,
		configPath:    configPath,
	}
}

// GetState returns the current workspace state.
func (s *WorkspaceService) GetState() model.WorkspaceState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state
}

// SetState updates the current workspace state.
func (s *WorkspaceService) SetState(state model.WorkspaceState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.state = state
}

// Refresh recomputes the workspace state from disk.
func (s *WorkspaceService) Refresh(ctx context.Context) error {
	state, err := s.ComputeState(ctx)
	if err != nil {
		return err
	}
	s.SetState(state)
	return nil
}

// WorkspaceRoot returns the workspace root path.
func (s *WorkspaceService) WorkspaceRoot() string {
	return s.workspaceRoot
}

// NewPipelineBuilder creates a new pipeline builder.
func (s *WorkspaceService) NewPipelineBuilder() *pipeline.Builder {
	return NewRenartPipelineBuilder(afero.NewOsFs())
}

func NewRenartPipelineBuilder(fs afero.Fs) *pipeline.Builder {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return pipeline.NewBuilder(
		BuilderConfig,
		apiAwareYamlTaskCreator(fs),
		pipeline.CreateTaskFromFileComments(fs),
		fs,
		DefaultGlossaryReader,
		jinja.VariantRendererFactory,
	)
}

func (s *WorkspaceService) resolver() *WorkspaceResolver {
	return NewWorkspaceResolver(s.workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		return s.NewPipelineBuilder().CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	})
}

// ComputeState computes the current workspace state from disk.
func (s *WorkspaceService) ComputeState(ctx context.Context) (model.WorkspaceState, error) {
	state := model.WorkspaceState{
		Pipelines:         make([]model.Pipeline, 0),
		Connections:       map[string]string{},
		QueryConnections:  make([]model.WorkspaceQueryConnection, 0),
		AssetCapabilities: assetAuthoringCapabilities(),
		Errors:            make([]string, 0),
		UpdatedAt:         time.Now().UTC(),
		Metadata:          map[string][]string{},
	}

	fs := afero.NewOsFs()
	environmentRefreshRestricted := false
	if exists, _ := afero.Exists(fs, s.configPath); exists {
		cfg, cfgErr := loadSelectedConfig(s.configPath, "")
		if cfgErr == nil {
			state.SelectedEnvironment = cfg.SelectedEnvironmentName
			environmentRefreshRestricted = selectedEnvironmentRestrictsFullRefresh(cfg)
			if cfg.SelectedEnvironment != nil && cfg.SelectedEnvironment.Connections != nil {
				state.Connections = cfg.SelectedEnvironment.Connections.ConnectionsSummaryList()
			}
		} else {
			state.Errors = append(state.Errors, "config parse error: "+cfgErr.Error())
		}
	}
	state.QueryConnections = workspaceQueryConnections(state.Connections)

	if project, projectErr := identity.LoadProject(fs, filepath.Join(s.workspaceRoot, ".renart", "project.yml")); projectErr == nil {
		state.Features = project.Features
	}

	if policyConfig, policyErr := policy.Load(filepath.Join(s.workspaceRoot, ".renart", "environments.yml")); policyErr == nil && len(policyConfig.Environments) > 0 {
		state.EnvironmentPolicies = make(map[string]model.EnvironmentPolicy, len(policyConfig.Environments))
		for name, envPolicy := range policyConfig.Environments {
			state.EnvironmentPolicies[name] = model.EnvironmentPolicy{
				Protected:          envPolicy.Protected,
				DeployedOnly:       envPolicy.DeployedOnly,
				ConfirmDestructive: envPolicy.ConfirmDestructive,
			}
		}
	} else if policyErr != nil {
		state.Errors = append(state.Errors, "environment policy parse error: "+policyErr.Error())
	}

	pipelinePaths, err := bruinpath.GetPipelinePaths(s.workspaceRoot, PipelineDefinitionFiles)
	if err != nil {
		return state, err
	}

	builder := s.NewPipelineBuilder()
	dependencyInputs := make([]dependencygraph.PipelineInput, 0, len(pipelinePaths))

	sort.Strings(pipelinePaths)
	for _, pPath := range pipelinePaths {
		parsed, parseErr := builder.CreatePipelineFromPath(ctx, pPath, pipeline.WithMutate())
		if parseErr != nil {
			// A single unparseable asset must not hide the whole pipeline. Retry
			// with a tolerant builder that turns the broken asset into a placeholder
			// (carrying its error) so the pipeline and its other assets stay visible
			// and the user can open and fix the offending file.
			recovered, recoverErr := NewRenartTolerantPipelineBuilder(fs).CreatePipelineFromPath(ctx, pPath, pipeline.WithMutate())
			state.Errors = append(state.Errors, pPath+": "+parseErr.Error())
			if recoverErr != nil || recovered == nil {
				continue
			}
			parsed = recovered
		}

		relPipelinePath, relErr := filepath.Rel(s.workspaceRoot, pPath)
		if relErr != nil {
			relPipelinePath = pPath
		}

		pipelineUUID := strings.TrimSpace(parsed.LegacyID)
		if pipelineUUID == "" {
			generatedID, generated, idErr := identity.EnsurePipelineID(fs, pipelineDefinitionFilePath(parsed, pPath))
			if idErr != nil {
				state.Errors = append(state.Errors, pPath+": failed to assign pipeline id: "+idErr.Error())
			} else {
				pipelineUUID = generatedID
				if generated {
					state.Metadata["notices"] = append(state.Metadata["notices"],
						fmt.Sprintf("assigned stable id %s to pipeline %s", generatedID, filepath.ToSlash(relPipelinePath)))
				}
			}
		}

		pSummary := model.Pipeline{
			ID:       EncodeID(relPipelinePath),
			UUID:     pipelineUUID,
			Name:     parsed.Name,
			Path:     filepath.ToSlash(relPipelinePath),
			Schedule: string(parsed.Schedule),
			Assets:   make([]model.Asset, 0, len(parsed.Assets)),
		}

		if pSummary.Name == "" {
			pSummary.Name = filepath.Base(pPath)
		}

		definitionSchemas := newAssetDefinitionSchemaResolver(parsed)
		for _, asset := range parsed.Assets {
			assetPath := asset.ExecutableFile.Path
			if assetPath == "" {
				assetPath = asset.DefinitionFile.Path
			}
			content := asset.ExecutableFile.Content
			if strings.TrimSpace(content) == "" && asset.ExecutableFile.Path == "" {
				if definitionBytes, readErr := afero.ReadFile(fs, asset.DefinitionFile.Path); readErr == nil {
					content = string(definitionBytes)
				}
			}

			relAssetPath, relErr := filepath.Rel(s.workspaceRoot, assetPath)
			if relErr != nil {
				relAssetPath = assetPath
			}

			upstreams := make([]string, 0, len(asset.Upstreams))
			dependencies := make([]model.AssetDependency, 0, len(asset.Upstreams))
			for _, up := range asset.Upstreams {
				upstreams = append(upstreams, up.Value)
				dependencyType := strings.ToLower(strings.TrimSpace(up.Type))
				if dependencyType == "" {
					dependencyType = "asset"
				}
				mode := up.Mode.String()
				if mode == "" {
					mode = "full"
				}
				dependencies = append(dependencies, model.AssetDependency{
					Type: dependencyType, Value: strings.TrimSpace(up.Value), Mode: mode,
				})
			}

			connectionName := ""
			if conn, connErr := targetConnectionNameForAsset(asset, parsed); connErr == nil {
				connectionName = conn
			}
			parameters := parameterStrings(asset.Parameters)
			if isAPIAsset(asset) {
				if summary := apiSummaryParameters(content, asset, parsed); len(summary) > 0 {
					parameters = summary
				}
			}

			declaredMatType := string(asset.Materialization.Type)
			destinationType := materializationDestinationType(asset, parsed, state.Connections)
			capabilityAsset := asset
			if environmentRefreshRestricted && (asset.RefreshRestricted == nil || !*asset.RefreshRestricted) {
				capabilityAsset = new(pipeline.Asset)
				*capabilityAsset = *asset
				restricted := true
				capabilityAsset.RefreshRestricted = &restricted
			}
			materializationProfile := materializationProfileFor(capabilityAsset, destinationType)
			columns := definitionSchemas.Available(ctx, asset)

			// A placeholder emitted by the tolerant builder carries its parse error
			// in meta; lift it into a first-class field and keep it out of the meta map.
			assetMeta := asset.Meta
			parseError := ""
			if msg, ok := assetMeta[parseErrorMetaKey]; ok {
				parseError = msg
				cleaned := make(pipeline.EmptyStringMap, len(assetMeta))
				for key, value := range assetMeta {
					if key == parseErrorMetaKey {
						continue
					}
					cleaned[key] = value
				}
				assetMeta = cleaned
			}

			pSummary.Assets = append(pSummary.Assets, model.Asset{
				ID:                          EncodeID(filepath.ToSlash(relAssetPath)),
				Name:                        asset.Name,
				URI:                         strings.TrimSpace(asset.URI),
				Type:                        string(asset.Type),
				Path:                        filepath.ToSlash(relAssetPath),
				Content:                     content,
				Upstreams:                   upstreams,
				Dependencies:                dependencies,
				Parameters:                  parameters,
				Meta:                        assetMeta,
				Columns:                     PipelineColumnsToModelColumns(columns),
				CustomChecks:                PipelineCustomChecksToModelCustomChecks(asset.CustomChecks),
				PreHooks:                    pipelineHookQueries(asset.Hooks.Pre),
				PostHooks:                   pipelineHookQueries(asset.Hooks.Post),
				ColumnInferenceSources:      columnInferenceSourcesForAsset(asset, connectionName),
				Connection:                  connectionName,
				ExplicitConnection:          strings.TrimSpace(asset.Connection),
				MaterializationType:         declaredMatType,
				MaterializationStrategy:     string(asset.Materialization.Strategy),
				IncrementalKey:              asset.Materialization.IncrementalKey,
				PartitionBy:                 asset.Materialization.PartitionBy,
				ClusterBy:                   append([]string(nil), asset.Materialization.ClusterBy...),
				TimeGranularity:             string(asset.Materialization.TimeGranularity),
				MaterializationCapabilities: editableMaterializationCapabilities(materializationProfile),
				SupportsFullRefresh:         supportsFullRefreshForAsset(capabilityAsset, materializationProfile),
				RefreshRestricted:           capabilityAsset.RefreshRestricted != nil && *capabilityAsset.RefreshRestricted,
				Owner:                       asset.Owner,
				Tags:                        asset.Tags,
				IsMaterialized:              false,
				Class:                       notebook.ClassPipeline,
				ParseError:                  parseError,
			})
		}

		assetWorkspaceIDs := make(map[string]string, len(pSummary.Assets))
		for _, asset := range pSummary.Assets {
			assetWorkspaceIDs[asset.Name] = asset.ID
		}
		dependencyInputs = append(dependencyInputs, dependencygraph.PipelineInput{
			UUID: pipelineUUID, ID: pSummary.ID, Name: pSummary.Name, Path: pSummary.Path,
			Parsed: parsed, AssetWorkspaceIDs: assetWorkspaceIDs,
		})

		state.Pipelines = append(state.Pipelines, pSummary)
	}

	resolvedDependencies := dependencygraph.Resolve(dependencyInputs)
	state.DependencyGraphRevision = resolvedDependencies.Revision
	for pipelineIndex := range state.Pipelines {
		pipelineSummary := &state.Pipelines[pipelineIndex]
		for assetIndex := range pipelineSummary.Assets {
			assetSummary := &pipelineSummary.Assets[assetIndex]
			stableAssetID := identity.AssetID(pipelineSummary.UUID, assetSummary.Name)
			edges := resolvedDependencies.EdgesByConsumer[stableAssetID]
			dependencies := make([]model.AssetDependency, 0, len(edges))
			for _, edge := range edges {
				dependency := model.AssetDependency{
					Type: edge.Type, Value: edge.Value, Mode: edge.Mode.String(),
				}
				if producer := resolvedDependencies.Nodes[edge.ProducerID]; edge.Resolved && producer != nil {
					dependency.ResolvedAssetID = producer.WorkspaceAssetID
					dependency.ResolvedAssetName = producer.AssetName
					dependency.ResolvedPipelineID = producer.PipelineID
					dependency.ResolvedPipeline = producer.PipelineName
				}
				dependencies = append(dependencies, dependency)
			}
			assetSummary.Dependencies = dependencies
		}
	}
	for _, diagnostic := range resolvedDependencies.Diagnostics {
		state.DependencyDiagnostics = append(state.DependencyDiagnostics, model.WorkspaceDependencyDiagnostic{
			AssetID: diagnostic.WorkspaceAssetID, PipelineID: diagnostic.PipelineID,
			Code: diagnostic.Code, Severity: string(diagnostic.Severity), Message: diagnostic.Message,
		})
	}

	s.appendNotebooks(&state)
	validateAssetClassDirection(&state)
	state.ArtifactIndex = BuildArtifactIndex(state)

	state.Metadata["pipeline_definition_files"] = PipelineDefinitionFiles
	state.Metadata["asset_directories"] = AssetsDirectoryNames

	return state, nil
}

func workspaceQueryConnections(connections map[string]string) []model.WorkspaceQueryConnection {
	result := make([]model.WorkspaceQueryConnection, 0, len(connections))
	for name, connectionType := range connections {
		assetType, ok := queryAssetTypeForConnectionType(connectionType)
		if !ok {
			continue
		}
		dialect, err := AssetTypeToDialect(assetType)
		if err != nil {
			continue
		}
		result = append(result, model.WorkspaceQueryConnection{
			Name:           name,
			ConnectionType: normalizeConnectionType(connectionType),
			AssetType:      string(assetType),
			Dialect:        dialect,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func parameterStrings(parameters pipeline.ParameterMap) map[string]string {
	result := make(map[string]string, len(parameters))
	for key := range parameters {
		if value, ok := parameters.GetString(key); ok {
			result[key] = value
		}
	}
	return result
}

// validateAssetClassDirection enforces the dependency direction rule: a
// pipeline asset depending on a notebook cell is an error, not a warning
// (promote the cell first). Notebook → pipeline dependencies are fine.
func validateAssetClassDirection(state *model.WorkspaceState) {
	if len(state.Notebooks) == 0 {
		return
	}

	pipelineAssetNames := make(map[string]bool)
	for _, p := range state.Pipelines {
		for _, asset := range p.Assets {
			pipelineAssetNames[strings.ToLower(asset.Name)] = true
		}
	}

	cellNames := make(map[string]string)
	for _, nb := range state.Notebooks {
		for _, cell := range nb.Cells {
			cellNames[strings.ToLower(cell.Name)] = nb.Title
		}
	}

	for _, p := range state.Pipelines {
		for _, asset := range p.Assets {
			for _, upstream := range asset.Upstreams {
				key := strings.ToLower(upstream)
				if pipelineAssetNames[key] {
					continue
				}
				if nbTitle, ok := cellNames[key]; ok {
					state.Errors = append(state.Errors, fmt.Sprintf(
						"%s: pipeline assets cannot depend on notebook cells (%q is a cell of notebook %q); promote the cell first",
						asset.Name, upstream, nbTitle))
				}
			}
		}
	}
}

// pipelineDefinitionFilePath returns the pipeline.yml path for a parsed
// pipeline, falling back to probing the pipeline directory when the parser
// did not record it.
func pipelineDefinitionFilePath(parsed *pipeline.Pipeline, pipelineDir string) string {
	if parsed != nil && strings.TrimSpace(parsed.DefinitionFile.Path) != "" {
		return parsed.DefinitionFile.Path
	}
	for _, name := range PipelineDefinitionFiles {
		candidate := filepath.Join(pipelineDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(pipelineDir, PipelineDefinitionFiles[0])
}

// ResolveAssetByID finds an asset by its encoded ID. Pipeline assets resolve
// through the pipeline resolver; notebook cells (which live outside any
// pipeline) resolve against their notebook folder, so the same SQL
// intelligence endpoints — parse-context, formatting, path suggestions —
// work for cells unchanged.
func (s *WorkspaceService) ResolveAssetByID(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
	relPath, parsed, asset, err := s.resolver().ResolveAssetByID(ctx, assetID)
	if err == nil {
		return relPath, parsed, asset, nil
	}

	if cellRel, cellPipeline, cellAsset, cellErr := s.resolveNotebookCellByID(assetID); cellErr == nil {
		return cellRel, cellPipeline, cellAsset, nil
	}
	return "", nil, nil, err
}

// resolveNotebookCellByID resolves a notebook cell file to its asset plus a
// synthetic pipeline whose assets are the notebook's cells (so dialect and
// sibling-column resolution behave like a normal pipeline).
func (s *WorkspaceService) resolveNotebookCellByID(assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return "", nil, nil, err
	}
	absAssetPath := filepath.Join(s.workspaceRoot, filepath.FromSlash(relAssetPath))

	dir := filepath.Dir(absAssetPath)
	if _, statErr := os.Stat(filepath.Join(dir, notebook.ManifestFileName)); statErr != nil {
		return "", nil, nil, ErrAssetNotFound
	}

	fs := afero.NewOsFs()
	loader := notebook.NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), nil)
	nb, err := loader.Load(dir)
	if err != nil {
		return "", nil, nil, err
	}

	assets := make([]*pipeline.Asset, 0, len(nb.Cells))
	var target *pipeline.Asset
	for _, cell := range nb.Cells {
		assets = append(assets, cell.Asset)
		if filepath.Clean(cell.Path) == filepath.Clean(absAssetPath) {
			target = cell.Asset
		}
	}
	if target == nil {
		return "", nil, nil, ErrAssetNotFound
	}

	synthetic := &pipeline.Pipeline{Name: nb.Title, Assets: assets}
	return filepath.ToSlash(relAssetPath), synthetic, target, nil
}

// ErrAssetNotFound is returned when an asset cannot be found.
var ErrAssetNotFound = &AssetNotFoundError{}

// AssetNotFoundError is the error type for missing assets.
type AssetNotFoundError struct{}

func (e *AssetNotFoundError) Error() string {
	return "asset not found in pipeline"
}

// PipelineColumnsToModelColumns converts pipeline columns to web model columns.
func PipelineColumnsToModelColumns(columns []pipeline.Column) []model.Column {
	result := make([]model.Column, 0, len(columns))
	for _, column := range columns {
		var nullable *bool
		if column.Nullable.Value != nil {
			value := *column.Nullable.Value
			nullable = &value
		}

		checks := make([]model.ColumnCheck, 0, len(column.Checks))
		for _, check := range column.Checks {
			checks = append(checks, model.ColumnCheck{
				Name:        check.Name,
				Value:       columnCheckValueToAny(check.Value),
				Blocking:    check.Blocking.Value,
				Description: check.Description,
			})
		}
		var foreignKey *model.ColumnReference
		if column.ForeignKey != nil {
			foreignKey = &model.ColumnReference{Table: column.ForeignKey.Table, Column: column.ForeignKey.Column}
		}

		result = append(result, model.Column{
			Name:          column.Name,
			SourceColumn:  column.SourceColumn,
			Type:          column.Type,
			Mask:          column.Mask,
			Description:   column.Description,
			Tags:          column.Tags,
			PrimaryKey:    column.PrimaryKey,
			UpdateOnMerge: column.UpdateOnMerge,
			MergeSQL:      column.MergeSQL,
			Nullable:      nullable,
			Default:       column.Default,
			Precision:     cloneIntPointer(column.Precision),
			Scale:         cloneIntPointer(column.Scale),
			Length:        cloneIntPointer(column.Length),
			Collation:     column.Collation,
			ForeignKey:    foreignKey,
			Owner:         column.Owner,
			Domains:       column.Domains,
			Meta:          column.Meta,
			Checks:        checks,
		})
	}
	return result
}

// ModelColumnsToPipelineColumns converts web model columns to pipeline columns.
func ModelColumnsToPipelineColumns(columns []model.Column) []pipeline.Column {
	result := make([]pipeline.Column, 0, len(columns))
	for _, column := range columns {
		checks := make([]pipeline.ColumnCheck, 0, len(column.Checks))
		for _, check := range column.Checks {
			checks = append(checks, pipeline.ColumnCheck{
				Name:        check.Name,
				Value:       anyToColumnCheckValue(check.Value),
				Blocking:    pipeline.DefaultTrueBool{Value: check.Blocking},
				Description: check.Description,
			})
		}
		var foreignKey *pipeline.ColumnReference
		if column.ForeignKey != nil {
			foreignKey = &pipeline.ColumnReference{Table: column.ForeignKey.Table, Column: column.ForeignKey.Column}
		}

		result = append(result, pipeline.Column{
			Name:          column.Name,
			SourceColumn:  column.SourceColumn,
			Type:          column.Type,
			Mask:          column.Mask,
			Description:   column.Description,
			Tags:          column.Tags,
			PrimaryKey:    column.PrimaryKey,
			UpdateOnMerge: column.UpdateOnMerge,
			MergeSQL:      column.MergeSQL,
			Nullable:      pipeline.DefaultTrueBool{Value: column.Nullable},
			Default:       column.Default,
			Precision:     cloneIntPointer(column.Precision),
			Scale:         cloneIntPointer(column.Scale),
			Length:        cloneIntPointer(column.Length),
			Collation:     column.Collation,
			ForeignKey:    foreignKey,
			Owner:         column.Owner,
			Domains:       column.Domains,
			Meta:          column.Meta,
			Checks:        checks,
		})
	}
	return result
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func PipelineCustomChecksToModelCustomChecks(checks []pipeline.CustomCheck) []model.CustomCheck {
	result := make([]model.CustomCheck, 0, len(checks))
	for _, check := range checks {
		result = append(result, model.CustomCheck{
			Name:        check.Name,
			Description: check.Description,
			Value:       check.Value,
			Count:       check.Count,
			Blocking:    check.Blocking.Value,
			Query:       check.Query,
			Retries:     check.Retries,
		})
	}
	return result
}

func pipelineHookQueries(hooks []pipeline.Hook) []string {
	result := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		if query := strings.TrimSpace(hook.Query); query != "" {
			result = append(result, query)
		}
	}
	return result
}

func ModelCustomCheckToPipelineCustomCheck(check model.CustomCheck) pipeline.CustomCheck {
	return pipeline.CustomCheck{
		Name:        strings.TrimSpace(check.Name),
		Description: strings.TrimSpace(check.Description),
		Value:       check.Value,
		Count:       check.Count,
		Blocking:    pipeline.DefaultTrueBool{Value: check.Blocking},
		Query:       strings.TrimSpace(check.Query),
		Retries:     check.Retries,
	}
}

func columnCheckValueToAny(value pipeline.ColumnCheckValue) any {
	if value.IntArray != nil {
		return *value.IntArray
	}
	if value.Int != nil {
		return *value.Int
	}
	if value.Float != nil {
		return *value.Float
	}
	if value.StringArray != nil {
		return *value.StringArray
	}
	if value.String != nil {
		return *value.String
	}
	if value.Bool != nil {
		return *value.Bool
	}
	return nil
}

func anyToColumnCheckValue(value any) pipeline.ColumnCheckValue {
	result := pipeline.ColumnCheckValue{}
	if value == nil {
		return result
	}

	switch v := value.(type) {
	case bool:
		result.Bool = &v
	case string:
		result.String = &v
	case int:
		result.Int = &v
	case int64:
		converted := int(v)
		result.Int = &converted
	case float64:
		if v == float64(int(v)) {
			converted := int(v)
			result.Int = &converted
		} else {
			result.Float = &v
		}
	case []string:
		result.StringArray = &v
	case []any:
		stringArr := make([]string, 0, len(v))
		intArr := make([]int, 0, len(v))
		allStrings := true
		allInts := true
		for _, item := range v {
			s, sOK := item.(string)
			if sOK {
				stringArr = append(stringArr, s)
			} else {
				allStrings = false
			}

			n, nOK := item.(float64)
			if nOK && n == float64(int(n)) {
				intArr = append(intArr, int(n))
			} else {
				allInts = false
			}
		}

		if allStrings {
			result.StringArray = &stringArr
		} else if allInts {
			result.IntArray = &intArr
		}
	}

	return result
}
