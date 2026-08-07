package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/bruincompat"
	"renart/internal/web/service/assetmeta"
)

type dependencyParser interface {
	Start() error
	UsedTables(query, dialect string) ([]string, error)
	GetMissingDependenciesForAsset(asset *pipeline.Asset, pl *pipeline.Pipeline, renderer jinja.RendererInterface) ([]string, error)
	Close() error
}

var newDependencyParser = func(ctx context.Context) (dependencyParser, error) {
	return bruincompat.NewDependencyParser(ctx), nil
}

func (s *AssetService) RefactorDirectDependencies(ctx context.Context, parsedPipeline *pipeline.Pipeline, oldName, newName string) ([]string, []string, error) {
	if parsedPipeline == nil || strings.TrimSpace(oldName) == strings.TrimSpace(newName) {
		return nil, nil, nil
	}

	sqlParserInstance, err := newDependencyParser(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create sql parser: %w", err)
	}
	defer sqlParserInstance.Close()

	renderer := jinja.NewRendererWithYesterday(parsedPipeline.Name, "web-rename")
	fs := s.fs()
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
		} else if isLoadAsset(current) {
			if sourceTable, ok := current.Parameters.GetString(loadParamSourceTable); ok && strings.EqualFold(strings.TrimSpace(sourceTable), oldName) {
				current.Parameters[loadParamSourceTable] = newName
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
			if err := reconcileSQLAssetDependenciesFS(ctx, fs, current, parsedPipeline, sqlParserInstance, renderer); err != nil {
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

func updateSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance dependencyParser, renderer *jinja.Renderer) error {
	return reconcileSQLAssetDependencies(ctx, asset, parsedPipeline, sqlParserInstance, renderer)
}

func (s *AssetService) reconcileSQLAssetDependencies(ctx context.Context, relAssetPath string) error {
	assetID := EncodeID(filepath.ToSlash(relAssetPath))
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return err
	}

	sqlParserInstance, err := newDependencyParser(ctx)
	if err != nil {
		return err
	}
	defer sqlParserInstance.Close()

	renderer := jinja.NewRendererWithYesterday(parsedPipeline.Name, "web-asset-update")
	return reconcileSQLAssetDependenciesFS(ctx, s.fs(), asset, parsedPipeline, sqlParserInstance, renderer)
}

func reconcileSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance dependencyParser, renderer *jinja.Renderer) error {
	return reconcileSQLAssetDependenciesFS(ctx, afero.NewOsFs(), asset, parsedPipeline, sqlParserInstance, renderer)
}

// reconcileLoadAssetDependencies auto-infers a Load asset's upstream from its
// source mapping: when source_table (or a single declared upstream) resolves to
// an existing asset, that asset is recorded as an *inferred* dependency. User
// edits (manual adds / ignores) are preserved through the same assetmeta model
// the SQL path uses.
func (s *AssetService) reconcileLoadAssetDependencies(ctx context.Context, relAssetPath string) error {
	assetID := EncodeID(filepath.ToSlash(relAssetPath))
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if !isLoadAsset(asset) || parsedPipeline == nil {
		return nil
	}

	inferred := make([]pipeline.Upstream, 0, 1)
	if source := resolveLoadSourceAsset(parsedPipeline, asset); source != nil {
		sourceName := strings.TrimSpace(source.Name)
		if sourceName != "" && !strings.EqualFold(sourceName, strings.TrimSpace(asset.Name)) {
			inferred = append(inferred, pipeline.Upstream{Type: "asset", Value: sourceName, Mode: pipeline.UpstreamModeFull})
		}
	}

	final, next := assetmeta.ReconcileDependencies(assetmeta.DependencyReconcileInput{
		AssetName: asset.Name,
		Inferred:  inferred,
		Current:   asset.Upstreams,
		Prev:      assetmeta.ParseAsset(asset),
	})
	asset.Upstreams = final
	next.ApplyToAsset(asset)

	if apiErr := s.persistYAMLAssetPreservingInferredName(asset); apiErr != nil {
		return fmt.Errorf("persist load dependencies for %q: %s", asset.Name, apiErr.Message)
	}
	return nil
}

func reconcileSQLAssetDependenciesFS(ctx context.Context, fs afero.Fs, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance dependencyParser, renderer *jinja.Renderer) error {
	if asset == nil || parsedPipeline == nil {
		return nil
	}
	if fs == nil {
		fs = afero.NewOsFs()
	}

	inferredNames, err := inferAllSQLAssetDependencies(ctx, asset, parsedPipeline, sqlParserInstance, renderer)
	if err != nil {
		return err
	}
	inferredUpstreams := make([]pipeline.Upstream, 0, len(inferredNames))
	for _, name := range inferredNames {
		inferredUpstreams = append(inferredUpstreams, pipeline.Upstream{Type: "asset", Value: name, Mode: pipeline.UpstreamModeFull})
	}
	final, next := assetmeta.ReconcileDependencies(assetmeta.DependencyReconcileInput{
		AssetName: asset.Name,
		Inferred:  inferredUpstreams,
		Current:   asset.Upstreams,
		Prev:      assetmeta.ParseAsset(asset),
	})
	asset.Upstreams = final
	next.ApplyToAsset(asset)
	originalHadExplicitName := assetContentHasExplicitName(asset.ExecutableFile.Content)

	if err := asset.Persist(fs, parsedPipeline); err != nil {
		return fmt.Errorf("failed to persist asset '%s': %w", asset.Name, err)
	}
	if !originalHadExplicitName {
		if err := removePersistedAssetNameField(asset); err != nil {
			return err
		}
	}

	return nil
}

func inferAllSQLAssetDependencies(ctx context.Context, asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance dependencyParser, renderer *jinja.Renderer) ([]string, error) {
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

func inferSQLAssetDependenciesFromUsedTables(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, sqlParserInstance dependencyParser, renderer jinja.RendererInterface) ([]string, error) {
	if asset == nil || parsedPipeline == nil {
		return nil, nil
	}

	dialect, err := bruincompat.AssetTypeToDialect(asset.Type)
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

	if strings.Contains(name, ".") {
		return name
	}
	if asset == nil {
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

// applyManualAssetUpstreams sets the asset's manual (user-declared) upstreams
// from the requested list while preserving inferred (renart-managed) ones and
// any non-asset upstreams, and records the manual set in the provenance
// (renart_dep_add). Inferred upstreams carry no name tracking under the new
// model, so the "currently inferred" set is simply the asset upstreams that are
// not in the previous d.add — a subsequent SQL reconcile re-derives them.
func applyManualAssetUpstreams(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline, requested []string) {
	if asset == nil {
		return
	}

	prev := assetmeta.ParseAsset(asset)
	prevManual := make(map[string]struct{}, len(prev.DepAdd))
	for _, key := range prev.DepAdd {
		prevManual[assetmeta.DependencyMatchKey(key)] = struct{}{}
	}

	// Build the requested manual upstreams (resolve canonical names, dedupe,
	// skip self-references).
	nextManual := make([]pipeline.Upstream, 0, len(requested))
	manualKeys := make(map[string]struct{}, len(requested))
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
		upstream := pipeline.Upstream{Type: "asset", Value: name, Mode: pipeline.UpstreamModeFull}
		key := assetmeta.DependencyMatchKey(assetmeta.DependencyKey(upstream))
		if _, ok := manualKeys[key]; ok {
			continue
		}
		manualKeys[key] = struct{}{}
		nextManual = append(nextManual, upstream)
	}

	// Preserve non-asset upstreams and inferred asset upstreams (everything that
	// was not a previous manual dep and is not now requested as manual).
	preservedNonAsset := make([]pipeline.Upstream, 0)
	preservedInferred := make([]pipeline.Upstream, 0)
	for _, upstream := range asset.Upstreams {
		if !isAssetUpstream(upstream) {
			preservedNonAsset = append(preservedNonAsset, upstream)
			continue
		}
		key := assetmeta.DependencyMatchKey(assetmeta.DependencyKey(upstream))
		if _, isManualNow := manualKeys[key]; isManualNow {
			continue // promoted to manual; carried by nextManual
		}
		if _, wasManual := prevManual[key]; wasManual {
			continue // an old manual dep the user is replacing
		}
		preservedInferred = append(preservedInferred, upstream)
	}

	// Inferred first, then manual (§19).
	asset.Upstreams = append(append(preservedInferred, preservedNonAsset...), nextManual...)

	next := prev
	next.Version = assetmeta.SchemaVersion
	next.Generator = assetmeta.GeneratorVersion
	next.LegacyInferred = nil
	depAdd := make([]string, 0, len(nextManual))
	for _, upstream := range nextManual {
		depAdd = append(depAdd, assetmeta.DependencyKey(upstream))
	}
	next.DepAdd = depAdd
	next.ApplyToAsset(asset)
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
