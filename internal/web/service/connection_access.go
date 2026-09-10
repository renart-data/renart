package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/policy"
)

// assetAccessRequirements describes the operator's effects, not its locks or
// secret purpose. For templated reads, callers pass an operation-local rendered
// clone; unsupported SQL remains unknown, never implicitly safe.
func assetAccessRequirements(pl *pipeline.Pipeline, asset *pipeline.Asset) ([]policy.Requirement, error) {
	if asset == nil {
		return nil, nil
	}
	if isSourceAssetType(asset.Type) && asset.Materialization.Type != pipeline.MaterializationTypeNone {
		return nil, newAPIError(400, "source_materialization_invalid", "Source assets describe external relations and cannot have materialization.")
	}
	var requirements []policy.Requirement
	add := func(name string, effect policy.Effect, operation string) {
		if strings.TrimSpace(name) != "" && !isLocalLoadConnection(name) {
			requirements = append(requirements, policy.Requirement{Connection: name, Effect: effect, Operation: operation})
		}
	}
	if isLoadAsset(asset) {
		params, err := resolvedLoadParams(asset, pl)
		if err != nil {
			return nil, err
		}
		add(params.SourceConnection, policy.Read, "load_source")
		add(params.DestinationConnection, policy.Write, "load_destination")
	} else {
		name, err := targetConnectionNameForAsset(asset, pl)
		if err != nil {
			return nil, err
		}
		effect, operation := policy.Unknown, "operator"
		switch {
		case isSourceAssetType(asset.Type):
			effect, operation = policy.Read, "source"
		case asset.Materialization.Type != pipeline.MaterializationTypeNone:
			effect, operation = policy.Write, "materialization"
		case isAPIAsset(asset):
			effect, operation = policy.Write, "api_destination"
		case isQueryAssetType(asset.Type):
			effect, operation = sqlAccessEffect(asset.ExecutableFile.Content, asset.Type), "sql"
		case isQuerySensorAssetType(asset.Type):
			query, _ := asset.Parameters.GetString("query")
			effect, operation = sqlAccessEffect(query, asset.Type), "query_sensor"
		case isSensorAssetType(asset.Type):
			if _, known := pipeline.AssetTypeConnectionMapping[asset.Type]; known {
				effect, operation = policy.Read, "sensor"
			}
		case strings.HasSuffix(string(asset.Type), ".seed"):
			effect, operation = policy.Write, "seed_destination"
		}
		add(name, effect, operation)
	}
	if !isSourceAssetType(asset.Type) {
		name, _ := targetConnectionNameForAsset(asset, pl)
		if asset.Type == pipeline.AssetTypeIngestr {
			source, _ := asset.Parameters.GetString("source_connection")
			add(source, policy.Unknown, "ingestr_source")
		}
		if pl != nil && pl.MetadataPush.HasAnyEnabled() {
			if _, supported := directMetadataPushBackendForAssetType(asset.Type); supported {
				add(name, policy.Write, "metadata_push")
			}
		}
		for _, hook := range append(append([]pipeline.Hook(nil), asset.Hooks.Pre...), asset.Hooks.Post...) {
			add(name, sqlAccessEffect(hook.Query, asset.Type), "sql_hook")
		}
		for _, check := range asset.CustomChecks {
			add(name, sqlAccessEffect(check.Query, asset.Type), "custom_check")
		}
		// Credential injection gives opaque code unrestricted access outside the
		// query broker. Read-only credentials must not be suggested as a sandbox.
		for _, secret := range asset.Secrets {
			add(secret.SecretKey, policy.Unknown, "credential_injection")
		}
	}
	return requirements, nil
}

func sqlAccessEffect(sql string, assetType pipeline.AssetType) policy.Effect {
	dialect, err := bruincompat.AssetTypeToDialect(assetType)
	if err != nil {
		if connectionType, ok := pipeline.AssetTypeConnectionMapping[assetType]; ok {
			dialect, _ = bruincompat.AnalyzerDialectForConnectionType(connectionType)
		}
	}
	if dialect == "" {
		return policy.Unknown
	}
	if readOnly, err := sqlintelligence.IsReadOnlySingleQuery(sql, dialect); err == nil && readOnly {
		return policy.Read
	}
	return policy.Unknown
}

func connectionEnvironmentPolicy(root string, cfg *config.Config) (policy.EnvironmentPolicy, string, error) {
	snapshot, err := policy.NewLoader(filepath.Join(root, ".renart", "environments.yml")).Snapshot()
	if err != nil {
		return policy.EnvironmentPolicy{}, "", policy.InvalidError(err.Error())
	}
	environment := ""
	if cfg != nil {
		environment = cfg.SelectedEnvironmentName
		if environment == "" {
			environment = cfg.DefaultEnvironmentName
		}
	}
	p := snapshot.Config.For(environment)
	for name := range p.Connections {
		if _, ok := selectedConfigurationConnection(cfg, name); !ok {
			return p, environment, policy.InvalidError(fmt.Sprintf("connection %q does not exist in environment %q; review its policy entry", name, environment))
		}
	}
	return p, environment, nil
}

func checkConnectionRequirements(root string, cfg *config.Config, requirements []policy.Requirement) error {
	p, environment, err := connectionEnvironmentPolicy(root, cfg)
	if err != nil {
		return err
	}
	for _, requirement := range requirements {
		connection, _ := selectedConfigurationConnection(cfg, requirement.Connection)
		if err := policy.CheckAccess(p, environment, requirement, nativeConnectionReadOnly(connection)); err != nil {
			return err
		}
	}
	return nil
}

func checkAssetsConnectionAccess(root string, cfg *config.Config, pl *pipeline.Pipeline, assets []*pipeline.Asset) error {
	var requirements []policy.Requirement
	for _, asset := range assets {
		resolved, err := assetAccessRequirements(pl, asset)
		if err != nil {
			return err
		}
		requirements = append(requirements, resolved...)
	}
	return checkConnectionRequirements(root, cfg, requirements)
}

// Only policy for connections actually used by this contract participates in
// review identity. Permissions never enter data fingerprints or physical locks.
func connectionAccessIdentity(root string, cfg *config.Config, requirements []policy.Requirement) (string, error) {
	p, _, err := connectionEnvironmentPolicy(root, cfg)
	if err != nil {
		return "", err
	}
	entries := make([]string, 0, len(requirements))
	for _, req := range requirements {
		connection, _ := selectedConfigurationConnection(cfg, req.Connection)
		entries = append(entries, req.Connection+"\x00"+string(policy.EffectiveMode(p, req.Connection, nativeConnectionReadOnly(connection))))
	}
	sort.Strings(entries)
	data, _ := json.Marshal(entries)
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func connectionConfigFromContext(ctx context.Context) *config.Config {
	env, _ := ctx.Value(config.EnvironmentContextKey).(*config.Environment)
	name, _ := ctx.Value(config.EnvironmentNameContextKey).(string)
	return &config.Config{SelectedEnvironment: env, SelectedEnvironmentName: name}
}

func renderedAccessAsset(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, renderer jinja.RendererInterface) (*pipeline.Asset, error) {
	if renderer == nil || asset == nil {
		return asset, nil
	}
	if concrete, ok := renderer.(*jinja.Renderer); ok && concrete == nil {
		return asset, nil
	}
	clone := *asset
	assetRenderer, err := renderer.CloneForAsset(ctx, pl, asset)
	if err != nil {
		return nil, err
	}
	if isQueryAssetType(asset.Type) {
		clone.ExecutableFile.Content, err = assetRenderer.Render(asset.ExecutableFile.Content)
		if err != nil {
			return nil, err
		}
	}
	if isQuerySensorAssetType(asset.Type) {
		clone.Parameters = pipeline.ParameterMap{}
		for key, value := range asset.Parameters {
			clone.Parameters[key] = value
		}
		query, _ := asset.Parameters.GetString("query")
		clone.Parameters["query"], err = assetRenderer.Render(query)
		if err != nil {
			return nil, err
		}
	}
	clone.Hooks, err = resolveAssetHookTemplates(ctx, pl, asset, renderer)
	if err != nil {
		return nil, err
	}
	clone.CustomChecks = append([]pipeline.CustomCheck(nil), asset.CustomChecks...)
	for index := range clone.CustomChecks {
		clone.CustomChecks[index].Query, err = assetRenderer.Render(clone.CustomChecks[index].Query)
		if err != nil {
			return nil, err
		}
	}
	return &clone, nil
}

func (e *HybridBruinExecutor) checkRenderedConnectionAccess(ctx context.Context, cfg *config.Config, pl *pipeline.Pipeline, assets []*pipeline.Asset, renderer jinja.RendererInterface) error {
	rendered := make([]*pipeline.Asset, 0, len(assets))
	for _, asset := range assets {
		clone, err := renderedAccessAsset(ctx, pl, asset, renderer)
		if err != nil {
			return err
		}
		rendered = append(rendered, clone)
	}
	return checkAssetsConnectionAccess(e.workspaceRoot, cfg, pl, rendered)
}

func (e *HybridBruinExecutor) currentAccessConfig(ctx context.Context, environment string) (*config.Config, error) {
	cfg := connectionConfigFromContext(ctx)
	if environment == "" {
		environment = cfg.SelectedEnvironmentName
	}
	// Authorization uses current originating-project configuration even when
	// source and execution parameters were restored from an older deployment.
	path := filepath.Join(e.workspaceRoot, ".bruin.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	return loadSelectedConfigReadOnlyFS(afero.NewOsFs(), path, environment)
}

func (e *HybridBruinExecutor) checkRuntimeAssetAccess(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, renderer jinja.RendererInterface) error {
	cfg, err := e.currentAccessConfig(ctx, "")
	if err != nil {
		return policy.InvalidError(err.Error())
	}
	return e.checkRenderedConnectionAccess(ctx, cfg, pl, []*pipeline.Asset{asset}, renderer)
}
