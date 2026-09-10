package service

import (
	"context"
	"fmt"
	"io"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"renart/internal/bruincompat"
	"renart/internal/web/policy"
)

// Read-only SQL must never enter a materializing operator: even a plain SELECT
// can otherwise trigger adapter-specific full-refresh cleanup or schema setup.
// Keep native connection handling and coordination, but dispatch only verified
// reads. Checks remain separate scheduler tasks and use the same access guard.
func (e *HybridBruinExecutor) runReadOnlySQLTask(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, renderer jinja.RendererInterface, manager config.ConnectionAndDetailsGetter, output io.Writer) (bool, error) {
	if !isQueryAssetType(asset.Type) {
		return false, nil
	}
	cfg, err := e.currentAccessConfig(ctx, "")
	if err != nil {
		return true, policy.InvalidError(err.Error())
	}
	name, err := targetConnectionNameForAsset(asset, pl)
	if err != nil {
		return true, err
	}
	p, _, err := connectionEnvironmentPolicy(e.workspaceRoot, cfg)
	if err != nil {
		return true, err
	}
	details, _ := selectedConfigurationConnection(cfg, name)
	if policy.EffectiveMode(p, name, nativeConnectionReadOnly(details)) != policy.ReadOnly {
		return false, nil
	}
	rendered, err := renderedAccessAsset(ctx, pl, asset, renderer)
	if err != nil {
		return true, err
	}
	if err := checkAssetsConnectionAccess(e.workspaceRoot, cfg, pl, []*pipeline.Asset{rendered}); err != nil {
		return true, err
	}
	lease, err := e.acquireDuckDBConnections(ctx, manager, []string{name}, directTaskLeaseOwner(ctx, pl, asset), output)
	if err != nil {
		return true, err
	}
	defer lease.Release()
	connection, err := resolveRuntimeConnection(manager, name)
	if err != nil {
		return true, err
	}
	runner, ok := connection.(interface {
		RunQueryWithoutResult(context.Context, *query.Query) error
	})
	if !ok {
		return true, fmt.Errorf("connection %q has no safe read-only SQL execution adapter", name)
	}
	statements := make([]string, 0, len(rendered.Hooks.Pre)+len(rendered.Hooks.Post)+1)
	for _, hook := range rendered.Hooks.Pre {
		statements = append(statements, hook.Query)
	}
	statements = append(statements, rendered.ExecutableFile.Content)
	for _, hook := range rendered.Hooks.Post {
		statements = append(statements, hook.Query)
	}
	dialect, _ := bruincompat.AssetTypeToDialect(asset.Type)
	for _, sql := range statements {
		sql, err = applyDirectSchemaPrefix(ctx, sql, dialect, &directPipelineInfo{Config: cfg, Pipeline: pl, Asset: asset}, connection)
		if err != nil {
			return true, err
		}
		current, err := e.currentAccessConfig(ctx, "")
		if err != nil {
			return true, policy.InvalidError(err.Error())
		}
		if err := checkConnectionRequirements(e.workspaceRoot, current, []policy.Requirement{{Connection: name, Effect: sqlAccessEffect(sql, asset.Type), Operation: "sql"}}); err != nil {
			return true, err
		}
		if _, err := resolveRuntimeConnection(manager, name); err != nil {
			return true, err
		}
		ansisql.LogQueryIfVerbose(ctx, output, sql)
		if err := runner.RunQueryWithoutResult(ctx, &query.Query{Query: sql}); err != nil {
			return true, err
		}
	}
	return true, nil
}
