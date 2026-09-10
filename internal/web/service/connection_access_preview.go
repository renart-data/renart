package service

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	bruinpath "github.com/bruin-data/bruin/pkg/path"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/policy"
)

// ConnectionAccessPreview is advisory working-tree impact, not run authority.
// Runtime checks also protect pinned deployments and queued executions.
// renart:web
type ConnectionAccessPreview struct {
	Assets   []ConnectionAccessImpact `json:"assets"`
	Warnings []string                 `json:"warnings"`
}
type ConnectionAccessImpact struct {
	Pipeline   string   `json:"pipeline"`
	Asset      string   `json:"asset"`
	Scheduled  bool     `json:"scheduled"`
	Operations []string `json:"operations"`
}

func (s *ConfigService) PreviewConnectionReadOnly(ctx context.Context, environment, name string) (ConnectionAccessPreview, error) {
	result := ConnectionAccessPreview{Assets: []ConnectionAccessImpact{}, Warnings: []string{}}
	cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), s.configPath, environment)
	if err != nil {
		return result, policy.InvalidError(err.Error())
	}
	if _, exists := selectedConfigurationConnection(cfg, name); !exists {
		return result, newAPIError(404, "connection_not_found", "Connection not found in this environment.")
	}
	if _, _, err := connectionEnvironmentPolicy(s.workspaceRoot, cfg); err != nil {
		return result, err
	}
	paths, err := bruinpath.GetPipelinePaths(s.workspaceRoot, PipelineDefinitionFiles)
	if err != nil {
		return result, err
	}
	sort.Strings(paths)
	ctx = context.WithValue(ctx, config.EnvironmentContextKey, cfg.SelectedEnvironment)
	ctx = context.WithValue(ctx, config.EnvironmentNameContextKey, cfg.SelectedEnvironmentName)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		pl, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(ctx, path, pipeline.WithMutate())
		if err != nil {
			result.Warnings = append(result.Warnings, "Some assets in "+filepath.Base(path)+" could not be checked. Run code checks after saving.")
			continue
		}
		renderer := jinja.NewRendererWithYesterday(pl.Name, "connection-access-preview")
		for _, asset := range pl.Assets {
			rendered, err := renderedAccessAsset(ctx, pl, asset, renderer)
			if err != nil {
				rendered = asset
			}
			requirements, err := assetAccessRequirements(pl, rendered)
			if err != nil {
				result.Warnings = append(result.Warnings, asset.Name+" could not be checked.")
				continue
			}
			impact := ConnectionAccessImpact{Pipeline: pl.Name, Asset: asset.Name, Scheduled: strings.TrimSpace(string(pl.Schedule)) != "", Operations: []string{}}
			for _, requirement := range requirements {
				if requirement.Connection == name && requirement.Effect != policy.Read {
					impact.Operations = appendUniqueString(impact.Operations, requirement.Operation)
				}
			}
			if len(impact.Operations) > 0 {
				result.Assets = append(result.Assets, impact)
			}
		}
	}
	sort.Slice(result.Assets, func(i, j int) bool {
		return result.Assets[i].Pipeline+result.Assets[i].Asset < result.Assets[j].Pipeline+result.Assets[j].Asset
	})
	return result, nil
}
