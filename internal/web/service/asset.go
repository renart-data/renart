package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
)

type AssetAPIError struct {
	Status  int
	Code    string
	Message string
}

type AssetUpdateRequest struct {
	Name                *string
	Type                *string
	Content             *string
	MaterializationType *string
	Meta                map[string]string
	Upstreams           []string
}

type FormatSQLAssetRequest struct {
	Content string
}

type FormatSQLAssetResponse struct {
	Status  string
	AssetID string
	Content string
	Error   string
}

type AssetDependencies struct {
	WorkspaceRoot                              string
	Executor                                   BruinCommandExecutor
	ResolveAssetByID                           func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	DefaultAssetContent                        func(string, string, string) string
	DerivedAssetContent                        func(string, string, string, string, string) string
	EnsurePythonRequirements                   func(string, string, string) error
	SuppressWatcher                            func(string)
	PushWorkspaceUpdate                        func(context.Context, string, string)
	PushWorkspaceUpdateImmediate               func(context.Context, string, string)
	PushWorkspaceUpdateImmediateWithChangedIDs func(context.Context, string, string, []string)
}

type AssetService struct {
	deps        AssetDependencies
	patchMu     sync.Mutex
	patchTimers map[string]*time.Timer
}

const renartInferredUpstreamsMetaKey = "renart_inferred_upstreams"

func NewAssetService(deps AssetDependencies) *AssetService {
	return &AssetService{deps: deps, patchTimers: make(map[string]*time.Timer)}
}

func (s *AssetService) Create(ctx context.Context, pipelineID string, req CreateAssetParams) (map[string]string, *AssetAPIError) {
	relPipelinePath, err := DecodeID(pipelineID)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_pipeline_id", Message: "invalid pipeline id"}
	}
	if req.Name == "" && req.Path == "" && req.SourceAssetID == "" {
		return nil, &AssetAPIError{Status: 400, Code: "missing_name_or_path", Message: "name or path is required"}
	}

	pipelinePath, err := SafeJoin(s.deps.WorkspaceRoot, relPipelinePath)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_pipeline_path", Message: err.Error()}
	}

	var sourceAsset *pipeline.Asset
	var sourcePipeline *pipeline.Pipeline
	var sourceConnectionName string
	var sourceRelAssetPath string
	if strings.TrimSpace(req.SourceAssetID) != "" {
		resolvedRelPath, resolvedPipeline, resolvedAsset, resolveErr := s.deps.ResolveAssetByID(ctx, req.SourceAssetID)
		if resolveErr != nil {
			return nil, &AssetAPIError{Status: 400, Code: "invalid_source_asset_id", Message: resolveErr.Error()}
		}
		if !pipelinePathsReferToSameRoot(resolvedPipeline.DefinitionFile.Path, pipelinePath) {
			return nil, &AssetAPIError{Status: 400, Code: "invalid_source_asset", Message: "source asset must belong to the selected pipeline"}
		}
		sourceAsset = resolvedAsset
		sourcePipeline = resolvedPipeline
		sourceRelAssetPath = resolvedRelPath
		if conn, connErr := sourcePipeline.GetConnectionNameForAsset(sourceAsset); connErr == nil {
			sourceConnectionName = conn
		}
	}

	assetName := strings.TrimSpace(req.Name)
	if assetName == "" && sourceAsset != nil {
		assetName = deriveDownstreamAssetName(sourceAsset.Name, sourcePipeline)
	}

	relAssetPath := req.Path
	if relAssetPath == "" {
		if sourceAsset != nil {
			sourceAbsAssetPath, pathErr := SafeJoin(s.deps.WorkspaceRoot, sourceRelAssetPath)
			if pathErr != nil {
				return nil, &AssetAPIError{Status: 400, Code: "invalid_source_asset_path", Message: pathErr.Error()}
			}
			sourcePipelineRelativeDir, relErr := filepath.Rel(pipelinePath, filepath.Dir(sourceAbsAssetPath))
			if relErr != nil {
				sourcePipelineRelativeDir = "assets"
			}
			assetTypeForPath := strings.TrimSpace(req.Type)
			if assetTypeForPath == "" {
				assetTypeForPath = deriveSQLAssetTypeForSource(sourceAsset, sourcePipeline, sourceConnectionName)
			}
			relAssetPath = filepath.ToSlash(filepath.Join(sourcePipelineRelativeDir, SlugUnderscore(assetName)+extensionForAssetType(assetTypeForPath)))
		} else {
			relAssetPath = filepath.ToSlash(filepath.Join("assets", SlugUnderscore(assetName)+extensionForAssetType(req.Type)))
		}
	}

	absAssetPath, err := SafeJoin(pipelinePath, relAssetPath)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_path", Message: err.Error()}
	}
	fs := afero.NewOsFs()
	if err := fs.MkdirAll(filepath.Dir(absAssetPath), 0o755); err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_dir_create_failed", Message: err.Error()}
	}

	assetType := strings.TrimSpace(req.Type)
	if assetType == "" {
		if sourceAsset != nil {
			assetType = deriveSQLAssetTypeForSource(sourceAsset, sourcePipeline, sourceConnectionName)
		} else {
			assetType = inferAssetTypeFromPath(relAssetPath)
		}
	}

	content := req.Content
	if content == "" {
		if assetName == "" {
			assetName = strings.TrimSuffix(filepath.Base(relAssetPath), filepath.Ext(relAssetPath))
		}
		if sourceAsset != nil {
			content = s.deps.DerivedAssetContent(assetName, assetType, relAssetPath, sourceAsset.Name, sourceConnectionName)
		} else {
			content = s.deps.DefaultAssetContent(assetName, assetType, relAssetPath)
		}
	}

	if err := afero.WriteFile(fs, absAssetPath, []byte(content), 0o644); err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_write_failed", Message: err.Error()}
	}
	if err := s.deps.EnsurePythonRequirements(absAssetPath, assetType, relAssetPath); err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "requirements_write_failed", Message: err.Error()}
	}

	relWorkspaceAssetPath, _ := filepath.Rel(s.deps.WorkspaceRoot, absAssetPath)
	assetPath := filepath.ToSlash(relWorkspaceAssetPath)
	if strings.HasSuffix(strings.ToLower(assetPath), ".sql") {
		if err := s.reconcileSQLAssetDependencies(ctx, assetPath); err != nil {
			return nil, &AssetAPIError{Status: 500, Code: "asset_dependency_reconcile_failed", Message: err.Error()}
		}
	}
	s.deps.SuppressWatcher(assetPath)
	s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.created", assetPath)
	return map[string]string{"status": "ok", "asset_id": EncodeID(assetPath), "asset_path": assetPath}, nil
}

type CreateAssetParams struct {
	Name          string
	Type          string
	Path          string
	Content       string
	SourceAssetID string
}

func (s *AssetService) Update(ctx context.Context, assetID string, req AssetUpdateRequest) (map[string]string, *AssetAPIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_id", Message: "invalid asset id"}
	}
	absAssetPath, err := SafeJoin(s.deps.WorkspaceRoot, relAssetPath)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_path", Message: err.Error()}
	}

	fs := afero.NewOsFs()
	originalBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_read_failed", Message: err.Error()}
	}
	desiredExecutable := ExtractExecutableContent(string(originalBytes))
	if req.Content != nil {
		desiredExecutable = *req.Content
	}

	changedAssetIDs := []string{assetID}
	changedAssetPaths := []string{filepath.ToSlash(relAssetPath)}

	if req.Name != nil || req.Type != nil || req.MaterializationType != nil || req.Meta != nil || req.Upstreams != nil {
		_, parsedPipeline, asset, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr != nil {
			return nil, &AssetAPIError{Status: 400, Code: "asset_resolve_failed", Message: resolveErr.Error()}
		}

		originalAssetName := asset.Name
		renamedAsset := false
		if req.Name != nil {
			nextName := strings.TrimSpace(*req.Name)
			if nextName == "" {
				return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_name", Message: "asset name cannot be empty"}
			}
			if existing := getAssetByNameCaseInsensitiveLocal(parsedPipeline, nextName); existing != nil && existing.DefinitionFile.Path != asset.DefinitionFile.Path {
				return nil, &AssetAPIError{Status: 400, Code: "duplicate_asset_name", Message: fmt.Sprintf("an asset named %q already exists", nextName)}
			}
			if nextName != asset.Name {
				asset.Name = nextName
				renamedAsset = true
			}
		}
		if req.Type != nil {
			nextType := strings.TrimSpace(*req.Type)
			if nextType == "" {
				return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_type", Message: "asset type cannot be empty"}
			}
			asset.Type = pipeline.AssetType(nextType)
		}
		if req.MaterializationType != nil {
			asset.Materialization.Type = pipeline.MaterializationType(strings.ToLower(strings.TrimSpace(*req.MaterializationType)))
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
		if err := asset.Persist(afero.NewOsFs(), parsedPipeline); err != nil {
			return nil, &AssetAPIError{Status: 500, Code: "asset_persist_failed", Message: err.Error()}
		}
		if renamedAsset {
			affectedIDs, affectedPaths, refactorErr := s.RefactorDirectDependencies(ctx, parsedPipeline, originalAssetName, asset.Name)
			if refactorErr != nil {
				return nil, &AssetAPIError{Status: 500, Code: "asset_rename_refactor_failed", Message: refactorErr.Error()}
			}
			changedAssetIDs = appendUniqueStrings(changedAssetIDs, affectedIDs...)
			changedAssetPaths = appendUniqueStrings(changedAssetPaths, affectedPaths...)
		}
	}

	latestBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_read_failed", Message: err.Error()}
	}
	mergedContent := MergeExecutableContent(string(latestBytes), desiredExecutable)
	if err := afero.WriteFile(fs, absAssetPath, []byte(mergedContent), 0o644); err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_write_failed", Message: err.Error()}
	}

	if req.Content != nil && strings.HasSuffix(strings.ToLower(relAssetPath), ".sql") {
		if err := s.reconcileSQLAssetDependencies(ctx, relAssetPath); err != nil {
			return nil, &AssetAPIError{Status: 500, Code: "asset_dependency_reconcile_failed", Message: err.Error()}
		}
		s.ScheduleSQLPatches(relAssetPath)
	}
	for _, changedPath := range changedAssetPaths {
		s.deps.SuppressWatcher(changedPath)
	}
	s.deps.PushWorkspaceUpdateImmediateWithChangedIDs(ctx, "asset.updated", relAssetPath, changedAssetIDs)
	return map[string]string{"status": "ok"}, nil
}

func (s *AssetService) RefactorDirectDependencies(ctx context.Context, parsedPipeline *pipeline.Pipeline, oldName, newName string) ([]string, []string, error) {
	if parsedPipeline == nil || strings.TrimSpace(oldName) == strings.TrimSpace(newName) {
		return nil, nil, nil
	}

	sqlParserInstance, err := sqlparser.NewSQLParser(false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create sql parser: %w", err)
	}
	defer sqlParserInstance.Close()

	renderer := jinja.NewRendererWithYesterday(parsedPipeline.Name, "web-rename")
	fs := afero.NewOsFs()
	changedIDs := make([]string, 0)
	changedPaths := make([]string, 0)

	for _, current := range parsedPipeline.Assets {
		if strings.EqualFold(current.Name, newName) || strings.EqualFold(current.Name, oldName) {
			continue
		}

		updated := false
		for index, upstream := range current.Upstreams {
			if !strings.EqualFold(upstream.Value, oldName) {
				continue
			}

			current.Upstreams[index].Value = newName
			updated = true
		}

		isSQLAsset := isSQLAssetFile(current)
		if isSQLAsset {
			nextContent := ReplaceAssetNameReferences(current.ExecutableFile.Content, oldName, newName)
			if nextContent != current.ExecutableFile.Content {
				current.ExecutableFile.Content = nextContent
				updated = true
			}
		}

		if !updated {
			continue
		}

		if err := current.Persist(fs, parsedPipeline); err != nil {
			return nil, nil, fmt.Errorf("failed to persist renamed dependency updates for asset '%s': %w", current.Name, err)
		}

		if isSQLAsset {
			if err := reconcileSQLAssetDependencies(ctx, current, parsedPipeline, sqlParserInstance, renderer); err != nil {
				return nil, nil, fmt.Errorf("failed to refresh dependencies for asset '%s': %w", current.Name, err)
			}
		}

		assetPath := current.ExecutableFile.Path
		if assetPath == "" {
			assetPath = current.DefinitionFile.Path
		}

		relAssetPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, assetPath)
		if relErr != nil {
			relAssetPath = assetPath
		}

		normalizedPath := filepath.ToSlash(relAssetPath)
		changedIDs = append(changedIDs, EncodeID(normalizedPath))
		changedPaths = append(changedPaths, normalizedPath)
	}

	return changedIDs, changedPaths, nil
}

func (s *AssetService) ScheduleSQLPatches(relAssetPath string) {
	assetPath := filepath.ToSlash(relAssetPath)

	s.patchMu.Lock()
	if existing, ok := s.patchTimers[assetPath]; ok {
		existing.Stop()
	}

	s.patchTimers[assetPath] = time.AfterFunc(1500*time.Millisecond, func() {
		s.RunSQLPatches(assetPath)

		s.patchMu.Lock()
		delete(s.patchTimers, assetPath)
		s.patchMu.Unlock()
	})
	s.patchMu.Unlock()
}

func (s *AssetService) RunSQLPatches(relAssetPath string) {
	prefixedPath := relAssetPath
	if !strings.HasPrefix(prefixedPath, "./") {
		prefixedPath = "./" + strings.TrimPrefix(prefixedPath, "./")
	}

	if s.deps.Executor != nil {
		_, _ = s.deps.Executor.ApplyPatch(context.Background(), PatchRequest{
			Operation:  "fill-columns-from-db",
			TargetPath: prefixedPath,
		})
	}

	if err := s.reconcileSQLAssetDependencies(context.Background(), relAssetPath); err != nil {
		return
	}

	if s.deps.SuppressWatcher != nil {
		s.deps.SuppressWatcher(relAssetPath)
	}
	if s.deps.PushWorkspaceUpdate != nil {
		s.deps.PushWorkspaceUpdate(context.Background(), "asset.patched", relAssetPath)
	}
}

func (s *AssetService) Delete(ctx context.Context, assetID string) (map[string]string, *AssetAPIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_id", Message: "invalid asset id"}
	}
	absAssetPath, err := SafeJoin(s.deps.WorkspaceRoot, relAssetPath)
	if err != nil {
		return nil, &AssetAPIError{Status: 400, Code: "invalid_asset_path", Message: err.Error()}
	}
	if err := afero.NewOsFs().Remove(absAssetPath); err != nil {
		return nil, &AssetAPIError{Status: 500, Code: "asset_delete_failed", Message: err.Error()}
	}
	s.deps.SuppressWatcher(relAssetPath)
	s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.deleted", relAssetPath)
	return map[string]string{"status": "ok"}, nil
}

func (s *AssetService) FormatSQL(ctx context.Context, assetID string, req FormatSQLAssetRequest) (FormatSQLAssetResponse, *AssetAPIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 400, Code: "invalid_asset_id", Message: "invalid asset id"}
	}
	if !strings.HasSuffix(strings.ToLower(relAssetPath), ".sql") {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 400, Code: "invalid_asset_type", Message: "only SQL assets can be formatted"}
	}
	absAssetPath, err := SafeJoin(s.deps.WorkspaceRoot, relAssetPath)
	if err != nil {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 400, Code: "invalid_asset_path", Message: err.Error()}
	}
	fs := afero.NewOsFs()
	originalBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 500, Code: "asset_read_failed", Message: err.Error()}
	}
	mergedContent := MergeExecutableContent(string(originalBytes), req.Content)
	if err := afero.WriteFile(fs, absAssetPath, []byte(mergedContent), 0o644); err != nil {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 500, Code: "asset_write_failed", Message: err.Error()}
	}
	output, err := s.deps.Executor.FormatAsset(ctx, FormatAssetRequest{AssetPath: relAssetPath, UseSQLFluff: true})
	if err != nil {
		return FormatSQLAssetResponse{Status: "error", AssetID: assetID, Content: req.Content, Error: strings.TrimSpace(string(output))}, nil
	}
	formattedBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return FormatSQLAssetResponse{}, &AssetAPIError{Status: 500, Code: "asset_read_failed", Message: err.Error()}
	}
	s.deps.SuppressWatcher(relAssetPath)
	s.deps.PushWorkspaceUpdateImmediateWithChangedIDs(ctx, "asset.updated", relAssetPath, []string{assetID})
	return FormatSQLAssetResponse{Status: "ok", AssetID: assetID, Content: ExtractExecutableContent(string(formattedBytes))}, nil
}

func appendUniqueStrings(values []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(extras))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, extra := range extras {
		if extra == "" {
			continue
		}
		if _, ok := seen[extra]; ok {
			continue
		}
		seen[extra] = struct{}{}
		values = append(values, extra)
	}
	return values
}

func ReplaceAssetNameReferences(content, oldName, newName string) string {
	trimmedOld := strings.TrimSpace(oldName)
	trimmedNew := strings.TrimSpace(newName)
	if trimmedOld == "" || trimmedNew == "" || trimmedOld == trimmedNew {
		return content
	}

	pattern := fmt.Sprintf(`(?i)(^|[^A-Za-z0-9_.])(%s)([^A-Za-z0-9_.]|$)`, regexp.QuoteMeta(trimmedOld))
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllString(content, `${1}`+trimmedNew+`${3}`)
}

func isSQLAssetFile(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}

	assetPath := asset.ExecutableFile.Path
	if assetPath == "" {
		assetPath = asset.DefinitionFile.Path
	}
	assetPath = strings.ToLower(assetPath)
	assetType := strings.ToLower(string(asset.Type))
	return strings.HasSuffix(assetPath, ".sql") || strings.Contains(assetType, "sql")
}

func updateSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance *sqlparser.SQLParser, renderer *jinja.Renderer) error {
	return reconcileSQLAssetDependencies(ctx, asset, parsedPipeline, sqlParserInstance, renderer)
}

func (s *AssetService) reconcileSQLAssetDependencies(ctx context.Context, relAssetPath string) error {
	assetID := EncodeID(filepath.ToSlash(relAssetPath))
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return err
	}

	sqlParserInstance, err := sqlparser.NewSQLParser(false)
	if err != nil {
		return err
	}
	defer sqlParserInstance.Close()

	renderer := jinja.NewRendererWithYesterday(parsedPipeline.Name, "web-asset-update")
	return reconcileSQLAssetDependencies(ctx, asset, parsedPipeline, sqlParserInstance, renderer)
}

func reconcileSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance *sqlparser.SQLParser, renderer *jinja.Renderer) error {
	if asset == nil || parsedPipeline == nil {
		return nil
	}

	tracked := parseRenartInferredUpstreams(asset.Meta)
	manualAssetUpstreams := make([]pipeline.Upstream, 0)
	nonAssetUpstreams := make([]pipeline.Upstream, 0)
	manualNames := make(map[string]struct{})

	for _, upstream := range asset.Upstreams {
		if !isAssetUpstream(upstream) {
			nonAssetUpstreams = append(nonAssetUpstreams, upstream)
			continue
		}

		normalized := normalizeDependencyName(upstream.Value)
		if normalized == "" || strings.EqualFold(normalized, asset.Name) {
			continue
		}
		if _, ok := tracked[normalized]; ok {
			continue
		}

		manualAssetUpstreams = append(manualAssetUpstreams, upstream)
		manualNames[normalized] = struct{}{}
	}

	inferredNames, err := inferAllSQLAssetDependencies(ctx, asset, parsedPipeline, sqlParserInstance, renderer)
	if err != nil {
		return err
	}

	nextInferred := make([]string, 0, len(inferredNames))
	for _, name := range inferredNames {
		normalized := normalizeDependencyName(name)
		if normalized == "" || strings.EqualFold(normalized, asset.Name) {
			continue
		}
		if _, ok := manualNames[normalized]; ok {
			continue
		}
		nextInferred = append(nextInferred, name)
	}

	sort.SliceStable(nextInferred, func(i, j int) bool {
		return strings.ToLower(nextInferred[i]) < strings.ToLower(nextInferred[j])
	})

	nextUpstreams := make([]pipeline.Upstream, 0, len(nonAssetUpstreams)+len(manualAssetUpstreams)+len(nextInferred))
	nextUpstreams = append(nextUpstreams, nonAssetUpstreams...)
	nextUpstreams = append(nextUpstreams, manualAssetUpstreams...)
	for _, name := range nextInferred {
		nextUpstreams = append(nextUpstreams, pipeline.Upstream{Type: "asset", Value: name, Mode: pipeline.UpstreamModeFull})
	}

	asset.Upstreams = nextUpstreams
	setRenartInferredUpstreams(&asset.Meta, nextInferred)

	if err := asset.Persist(afero.NewOsFs(), parsedPipeline); err != nil {
		return fmt.Errorf("failed to persist asset '%s': %w", asset.Name, err)
	}

	return nil
}

func inferAllSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance *sqlparser.SQLParser, renderer *jinja.Renderer) ([]string, error) {
	cloned := *asset
	cloned.Upstreams = nil

	assetRenderer, err := renderer.CloneForAsset(ctx, parsedPipeline, &cloned)
	if err != nil {
		return nil, fmt.Errorf("failed to create renderer for asset '%s': %w", asset.Name, err)
	}

	missingDeps, err := sqlParserInstance.GetMissingDependenciesForAsset(&cloned, parsedPipeline, assetRenderer)
	if err != nil {
		return nil, fmt.Errorf("failed to infer dependencies for asset '%s': %w", asset.Name, err)
	}
	if len(missingDeps) == 0 {
		missingDeps, err = inferSQLAssetDependenciesFromUsedTables(&cloned, parsedPipeline, sqlParserInstance, assetRenderer)
		if err != nil {
			return nil, err
		}
	}

	result := make([]string, 0, len(missingDeps))
	seen := make(map[string]struct{}, len(missingDeps))
	for _, dep := range missingDeps {
		canonical := resolveInferredDependencyName(dep, asset, parsedPipeline)
		normalized := normalizeDependencyName(canonical)
		if normalized == "" {
			continue
		}

		key := normalizeDependencyName(canonical)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, canonical)
	}

	return result, nil
}

func inferSQLAssetDependenciesFromUsedTables(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance *sqlparser.SQLParser, renderer jinja.RendererInterface) ([]string, error) {
	if asset == nil || parsedPipeline == nil {
		return nil, nil
	}

	dialect, err := sqlparser.AssetTypeToDialect(asset.Type)
	if err != nil {
		return nil, nil
	}

	renderedQuery, err := renderer.Render(mergeAssetMacrosWithQuery(asset.ExecutableFile.Content, parsedPipeline.Macros))
	if err != nil {
		return nil, fmt.Errorf("failed to render the query before parsing the SQL")
	}

	usedTables, err := sqlParserInstance.UsedTables(renderedQuery, dialect)
	if err != nil {
		return nil, fmt.Errorf("failed to infer dependencies for asset '%s': failed to get used tables: %w", asset.Name, err)
	}

	result := make([]string, 0, len(usedTables))
	for _, table := range usedTables {
		canonical := resolveInferredDependencyName(table, asset, parsedPipeline)
		if found := getAssetByNameCaseInsensitiveLocal(parsedPipeline, canonical); found != nil {
			result = append(result, found.Name)
		}
	}

	return result, nil
}

func mergeAssetMacrosWithQuery(query string, macros []pipeline.Macro) string {
	if len(macros) == 0 {
		return query
	}

	var b strings.Builder
	for _, macro := range macros {
		b.WriteString(string(macro))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(query)

	return b.String()
}

func resolveInferredDependencyName(dep string, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline) string {
	name := strings.TrimSpace(dep)
	if name == "" || parsedPipeline == nil {
		return name
	}

	if found := getAssetByNameCaseInsensitiveLocal(parsedPipeline, name); found != nil {
		return found.Name
	}

	if strings.Contains(name, ".") || asset == nil {
		return name
	}

	lastDot := strings.LastIndex(strings.TrimSpace(asset.Name), ".")
	if lastDot <= 0 {
		return name
	}

	candidate := asset.Name[:lastDot+1] + name
	if found := getAssetByNameCaseInsensitiveLocal(parsedPipeline, candidate); found != nil {
		return found.Name
	}

	return name
}

func applyManualAssetUpstreams(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, requested []string) {
	if asset == nil {
		return
	}

	tracked := parseRenartInferredUpstreams(asset.Meta)
	preservedNonAsset := make([]pipeline.Upstream, 0)
	preservedTracked := make([]pipeline.Upstream, 0)
	manualNames := make(map[string]struct{})
	nextManual := make([]pipeline.Upstream, 0, len(requested))

	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if parsedPipeline != nil {
			if found := getAssetByNameCaseInsensitiveLocal(parsedPipeline, name); found != nil {
				name = found.Name
			}
		}
		if strings.EqualFold(name, asset.Name) {
			continue
		}

		normalized := normalizeDependencyName(name)
		if _, ok := manualNames[normalized]; ok {
			continue
		}
		manualNames[normalized] = struct{}{}
		nextManual = append(nextManual, pipeline.Upstream{Type: "asset", Value: name, Mode: pipeline.UpstreamModeFull})
	}

	for _, upstream := range asset.Upstreams {
		if !isAssetUpstream(upstream) {
			preservedNonAsset = append(preservedNonAsset, upstream)
			continue
		}

		normalized := normalizeDependencyName(upstream.Value)
		if normalized == "" {
			continue
		}
		if _, ok := tracked[normalized]; ok {
			if _, overridden := manualNames[normalized]; !overridden {
				preservedTracked = append(preservedTracked, upstream)
			}
		}
	}

	asset.Upstreams = append(append(preservedNonAsset, nextManual...), preservedTracked...)
	nextTracked := make([]string, 0, len(preservedTracked))
	for _, upstream := range preservedTracked {
		nextTracked = append(nextTracked, upstream.Value)
	}
	setRenartInferredUpstreams(&asset.Meta, nextTracked)
}

func parseRenartInferredUpstreams(meta map[string]string) map[string]string {
	result := make(map[string]string)
	if meta == nil {
		return result
	}

	for _, raw := range strings.Split(meta[renartInferredUpstreamsMetaKey], ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		result[normalizeDependencyName(name)] = name
	}

	return result
}

func getAssetByNameCaseInsensitiveLocal(parsedPipeline *pipeline.Pipeline, name string) *pipeline.Asset {
	if parsedPipeline == nil {
		return nil
	}

	for _, asset := range parsedPipeline.Assets {
		if asset != nil && strings.EqualFold(asset.Name, name) {
			return asset
		}
	}

	return nil
}

func setRenartInferredUpstreams(meta *pipeline.EmptyStringMap, upstreams []string) {
	unique := make([]string, 0, len(upstreams))
	seen := make(map[string]struct{}, len(upstreams))
	for _, upstream := range upstreams {
		name := strings.TrimSpace(upstream)
		if name == "" {
			continue
		}
		key := normalizeDependencyName(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, name)
	}

	if len(unique) == 0 {
		if *meta == nil {
			return
		}
		delete(*meta, renartInferredUpstreamsMetaKey)
		if len(*meta) == 0 {
			*meta = nil
		}
		return
	}

	sort.SliceStable(unique, func(i, j int) bool {
		return strings.ToLower(unique[i]) < strings.ToLower(unique[j])
	})

	if *meta == nil {
		*meta = pipeline.EmptyStringMap{}
	}
	(*meta)[renartInferredUpstreamsMetaKey] = strings.Join(unique, ",")
}

func normalizeDependencyName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isAssetUpstream(upstream pipeline.Upstream) bool {
	return upstream.Type == "" || strings.EqualFold(upstream.Type, "asset")
}

func pipelinePathsReferToSameRoot(sourcePipelinePath, targetPipelineRoot string) bool {
	normalizedSource := filepath.Clean(sourcePipelinePath)
	normalizedTarget := filepath.Clean(targetPipelineRoot)
	if normalizedSource == normalizedTarget {
		return true
	}
	return filepath.Clean(filepath.Dir(normalizedSource)) == normalizedTarget
}

func extensionForAssetType(assetType string) string {
	lowered := strings.ToLower(strings.TrimSpace(assetType))
	switch {
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
			destination := strings.TrimSpace(sourceAsset.Parameters["destination"])
			if destination != "" {
				if assetType, ok := sqlAssetTypeForIngestrDestination(destination); ok {
					return assetType
				}
				if assetType, ok := sqlAssetTypeForConnectionName(parsedPipeline, destination); ok {
					return assetType
				}
			}
			destinationConnection := strings.TrimSpace(sourceAsset.Parameters["destination_connection"])
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

func sqlAssetTypeForIngestrDestination(destination string) (string, bool) {
	assetType, ok := pipeline.IngestrTypeConnectionMapping[strings.ToLower(strings.TrimSpace(destination))]
	if !ok {
		return "", false
	}

	return string(assetType), true
}

func sqlAssetTypeForConnectionName(parsedPipeline *pipeline.Pipeline, connectionName string) (string, bool) {
	if parsedPipeline == nil || strings.TrimSpace(connectionName) == "" {
		return "", false
	}

	for connectionType, configuredName := range parsedPipeline.DefaultConnections {
		if strings.EqualFold(strings.TrimSpace(configuredName), strings.TrimSpace(connectionName)) {
			if assetType, ok := sqlAssetTypeForConnectionType(connectionType); ok {
				return assetType, true
			}
		}
	}

	return "", false
}

func sqlAssetTypeForConnectionType(connectionType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(connectionType)) {
	case "athena":
		return string(pipeline.AssetTypeAthenaQuery), true
	case "bigquery", "google_cloud_platform", "gcp":
		return string(pipeline.AssetTypeBigqueryQuery), true
	case "clickhouse":
		return string(pipeline.AssetTypeClickHouse), true
	case "databricks":
		return string(pipeline.AssetTypeDatabricksQuery), true
	case "duckdb":
		return string(pipeline.AssetTypeDuckDBQuery), true
	case "fabric":
		return string(pipeline.AssetTypeFabricQuery), true
	case "motherduck":
		return string(pipeline.AssetTypeMotherduckQuery), true
	case "mssql":
		return string(pipeline.AssetTypeMsSQLQuery), true
	case "mysql":
		return string(pipeline.AssetTypeMySQLQuery), true
	case "oracle":
		return string(pipeline.AssetTypeOracleQuery), true
	case "postgres":
		return string(pipeline.AssetTypePostgresQuery), true
	case "redshift":
		return string(pipeline.AssetTypeRedshiftQuery), true
	case "snowflake":
		return string(pipeline.AssetTypeSnowflakeQuery), true
	case "synapse":
		return string(pipeline.AssetTypeSynapseQuery), true
	case "trino":
		return string(pipeline.AssetTypeTrinoQuery), true
	case "vertica":
		return string(pipeline.AssetTypeVerticaQuery), true
	default:
		return "", false
	}
}

func DefaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName string) string {
	header := fmt.Sprintf("/* @bruin\n\nname: %s\ntype: %s\nmaterialization:\n  type: view\n\n@bruin */\n\n", assetName, assetType)
	queryTarget := sourceAssetName
	if strings.TrimSpace(queryTarget) == "" {
		queryTarget = strings.TrimSuffix(filepath.Base(assetPath), filepath.Ext(assetPath))
	}
	query := fmt.Sprintf("select * from %s\n", queryTarget)
	if strings.TrimSpace(connectionName) != "" {
		query += fmt.Sprintf("-- source connection: %s\n", connectionName)
	}
	return header + query
}

func EnsurePythonRequirementsFile(absAssetPath, assetType, relAssetPath string) error {
	loweredType := strings.ToLower(strings.TrimSpace(assetType))
	loweredPath := strings.ToLower(strings.TrimSpace(relAssetPath))
	if !strings.HasSuffix(loweredPath, ".py") && !strings.Contains(loweredType, "python") {
		return nil
	}
	requirementsPath := filepath.Join(filepath.Dir(absAssetPath), "requirements.txt")
	fs := afero.NewOsFs()
	if exists, err := afero.Exists(fs, requirementsPath); err != nil {
		return err
	} else if exists {
		return nil
	}
	return afero.WriteFile(fs, requirementsPath, []byte("pandas\n"), 0o644)
}

func defaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName string) string {
	return DefaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName)
}

func ensurePythonRequirementsFile(absAssetPath, assetType, relAssetPath string) error {
	return EnsurePythonRequirementsFile(absAssetPath, assetType, relAssetPath)
}
