package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/apperror"
)

func apiErrorFromConnectionAccess(err error) *APIError {
	var apiErr *apperror.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return newAPIError(400, "connection_access_unknown", err.Error())
}

func (s *AssetService) validateAuthoredConnectionAccess(pl *pipeline.Pipeline, asset *pipeline.Asset, environment string) *APIError {
	if environment == "" && s.deps.SelectedEnvironment != nil {
		environment = s.deps.SelectedEnvironment()
	}
	var cfg *config.Config
	if exists, _ := afero.Exists(s.fs(), s.deps.ConfigPath); exists {
		var err error
		cfg, err = loadSelectedConfigReadOnlyFS(s.fs(), s.deps.ConfigPath, environment)
		if err != nil {
			return newAPIError(400, "connection_policy_invalid", err.Error())
		}
	}
	if err := checkAssetsConnectionAccess(s.deps.WorkspaceRoot, cfg, pl, []*pipeline.Asset{asset}); err != nil {
		return apiErrorFromConnectionAccess(err)
	}
	return nil
}

// Validate the actual authored definition before any filesystem effect, so a
// legacy caller cannot bypass destination eligibility with custom content.
func (s *AssetService) validateCreatedConnectionAccess(ctx context.Context, pipelinePath, assetPath, assetName, content, environment string, files semanticAssetFiles) *APIError {
	overlay := afero.NewCopyOnWriteFs(s.fs(), afero.NewMemMapFs())
	if err := overlay.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		return newAPIError(400, "asset_definition_invalid", err.Error())
	}
	if err := afero.WriteFile(overlay, assetPath, []byte(content), 0o644); err != nil {
		return newAPIError(400, "asset_definition_invalid", err.Error())
	}
	if files.sidecarPath != "" {
		if err := afero.WriteFile(overlay, files.sidecarPath, files.sidecar, 0o644); err != nil {
			return newAPIError(400, "asset_definition_invalid", err.Error())
		}
	}
	creator := pipeline.CreateTaskFromFileComments(overlay)
	if strings.HasSuffix(assetPath, ".yml") || strings.HasSuffix(assetPath, ".yaml") {
		creator = pipeline.CreateTaskFromYamlDefinition(overlay)
	}
	asset, err := creator(assetPath)
	if err != nil {
		return newAPIError(400, "asset_definition_invalid", err.Error())
	}
	if asset == nil {
		return nil
	}
	asset.Name = assetName
	pl, err := NewRenartPipelineBuilder(s.fs()).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	if err != nil {
		return newAPIError(400, "pipeline_parse_failed", err.Error())
	}
	return s.validateAuthoredConnectionAccess(pl, asset, environment)
}
