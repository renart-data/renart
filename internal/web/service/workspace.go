package service

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/glossary"
	bruinpath "github.com/bruin-data/bruin/pkg/path"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/model"
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
	osFS := afero.NewOsFs()
	return pipeline.NewBuilder(
		BuilderConfig,
		pipeline.CreateTaskFromYamlDefinition(osFS),
		pipeline.CreateTaskFromFileComments(osFS),
		osFS,
		DefaultGlossaryReader,
	)
}

// ComputeState computes the current workspace state from disk.
func (s *WorkspaceService) ComputeState(ctx context.Context) (model.WorkspaceState, error) {
	state := model.WorkspaceState{
		Pipelines:   make([]model.Pipeline, 0),
		Connections: map[string]string{},
		Errors:      make([]string, 0),
		UpdatedAt:   time.Now().UTC(),
		Metadata:    map[string][]string{},
	}

	fs := afero.NewOsFs()
	if exists, _ := afero.Exists(fs, s.configPath); exists {
		cfg, cfgErr := config.LoadOrCreate(fs, s.configPath)
		if cfgErr == nil {
			state.SelectedEnvironment = cfg.SelectedEnvironmentName
			if cfg.SelectedEnvironment != nil && cfg.SelectedEnvironment.Connections != nil {
				state.Connections = cfg.SelectedEnvironment.Connections.ConnectionsSummaryList()
			}
		} else {
			state.Errors = append(state.Errors, "config parse error: "+cfgErr.Error())
		}
	}

	pipelinePaths, err := bruinpath.GetPipelinePaths(s.workspaceRoot, PipelineDefinitionFiles)
	if err != nil {
		return state, err
	}

	builder := s.NewPipelineBuilder()

	sort.Strings(pipelinePaths)
	for _, pPath := range pipelinePaths {
		parsed, parseErr := builder.CreatePipelineFromPath(ctx, pPath, pipeline.WithMutate())
		if parseErr != nil {
			state.Errors = append(state.Errors, pPath+": "+parseErr.Error())
			continue
		}

		relPipelinePath, relErr := filepath.Rel(s.workspaceRoot, pPath)
		if relErr != nil {
			relPipelinePath = pPath
		}

		pSummary := model.Pipeline{
			ID:     EncodeID(relPipelinePath),
			Name:   parsed.Name,
			Path:   filepath.ToSlash(relPipelinePath),
			Assets: make([]model.Asset, 0, len(parsed.Assets)),
		}

		if pSummary.Name == "" {
			pSummary.Name = filepath.Base(pPath)
		}

		for _, asset := range parsed.Assets {
			assetPath := asset.ExecutableFile.Path
			if assetPath == "" {
				assetPath = asset.DefinitionFile.Path
			}

			relAssetPath, relErr := filepath.Rel(s.workspaceRoot, assetPath)
			if relErr != nil {
				relAssetPath = assetPath
			}

			upstreams := make([]string, 0, len(asset.Upstreams))
			for _, up := range asset.Upstreams {
				upstreams = append(upstreams, up.Value)
			}

			connectionName := ""
			if conn, connErr := parsed.GetConnectionNameForAsset(asset); connErr == nil {
				connectionName = conn
			}

			declaredMatType := string(asset.Materialization.Type)

			pSummary.Assets = append(pSummary.Assets, model.Asset{
				ID:                  EncodeID(filepath.ToSlash(relAssetPath)),
				Name:                asset.Name,
				Type:                string(asset.Type),
				Path:                filepath.ToSlash(relAssetPath),
				Content:             asset.ExecutableFile.Content,
				Upstreams:           upstreams,
				Parameters:          asset.Parameters,
				Meta:                asset.Meta,
				Columns:             PipelineColumnsToModelColumns(asset.Columns),
				Connection:          connectionName,
				MaterializationType: declaredMatType,
				IsMaterialized:      false,
			})
		}

		state.Pipelines = append(state.Pipelines, pSummary)
	}

	state.Metadata["pipeline_definition_files"] = PipelineDefinitionFiles
	state.Metadata["asset_directories"] = AssetsDirectoryNames

	return state, nil
}

// ResolveAssetByID finds an asset by its encoded ID.
func (s *WorkspaceService) ResolveAssetByID(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return "", nil, nil, err
	}

	absAssetPath, err := SafeJoin(s.workspaceRoot, relAssetPath)
	if err != nil {
		return "", nil, nil, err
	}

	pipelinePath, err := bruinpath.GetPipelineRootFromTask(absAssetPath, PipelineDefinitionFiles)
	if err != nil {
		return "", nil, nil, err
	}

	builder := s.NewPipelineBuilder()
	parsed, err := builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	if err != nil {
		return "", nil, nil, err
	}

	normalizedTarget := filepath.ToSlash(relAssetPath)
	for _, current := range parsed.Assets {
		assetPath := current.ExecutableFile.Path
		if assetPath == "" {
			assetPath = current.DefinitionFile.Path
		}

		relCurrent, relErr := filepath.Rel(s.workspaceRoot, assetPath)
		if relErr != nil {
			continue
		}

		if filepath.ToSlash(relCurrent) == normalizedTarget {
			return normalizedTarget, parsed, current, nil
		}
	}

	return "", nil, nil, ErrAssetNotFound
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

		result = append(result, model.Column{
			Name:          column.Name,
			Type:          column.Type,
			Description:   column.Description,
			Tags:          column.Tags,
			PrimaryKey:    column.PrimaryKey,
			UpdateOnMerge: column.UpdateOnMerge,
			MergeSQL:      column.MergeSQL,
			Nullable:      nullable,
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

		result = append(result, pipeline.Column{
			Name:          column.Name,
			Type:          column.Type,
			Description:   column.Description,
			Tags:          column.Tags,
			PrimaryKey:    column.PrimaryKey,
			UpdateOnMerge: column.UpdateOnMerge,
			MergeSQL:      column.MergeSQL,
			Nullable:      pipeline.DefaultTrueBool{Value: column.Nullable},
			Owner:         column.Owner,
			Domains:       column.Domains,
			Meta:          column.Meta,
			Checks:        checks,
		})
	}
	return result
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
