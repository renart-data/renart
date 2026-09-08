package service

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/apperror"
	"renart/internal/web/navigationtarget"
	"renart/internal/web/policy"
)

func connectionAccessFinding(err error, connection string) TypeCheckFinding {
	finding := TypeCheckFinding{Code: "connection_access_unknown", Source: "renart", Severity: "error", Message: err.Error(), Scope: "connection"}
	var apiErr *apperror.Error
	if errors.As(err, &apiErr) {
		finding.Code = apiErr.Code
	}
	if connection != "" {
		finding.Target = &navigationtarget.Target{Kind: "connection", Connection: connection, Field: "access_mode"}
	}
	return finding
}

func connectionAccessFindings(ctx context.Context, fs afero.Fs, root, environment string, pl *pipeline.Pipeline, asset *pipeline.Asset, renderer jinja.RendererInterface) []TypeCheckFinding {
	if root == "" {
		return nil
	}
	path := filepath.Join(root, ".bruin.yml")
	var cfg *config.Config
	// Pure type-check tests and standalone SQL directories need no project
	// config. A present but invalid config/policy is never assumed unrestricted.
	if exists, _ := afero.Exists(fs, path); exists {
		var err error
		cfg, err = loadSelectedConfigReadOnlyFS(fs, path, environment)
		if err != nil {
			// Catalog-only checks can run without a matching project config.
			// Configuration validation already owns that error. This pass adds
			// policy diagnostics only if an authored policy exists.
			if exists, _ := afero.Exists(fs, filepath.Join(root, ".renart", "environments.yml")); !exists {
				return nil
			}
			return []TypeCheckFinding{connectionAccessFinding(policy.InvalidError(err.Error()), "")}
		}
	}
	p, env, err := connectionEnvironmentPolicy(root, cfg)
	if err != nil {
		return []TypeCheckFinding{connectionAccessFinding(err, "")}
	}
	clone, err := renderedAccessAsset(ctx, pl, asset, renderer)
	if err != nil {
		return nil
	} // The ordinary template diagnostic owns this.
	requirements, err := assetAccessRequirements(pl, clone)
	if err != nil {
		if isSourceAssetType(asset.Type) && asset.Materialization.Type != "" {
			return []TypeCheckFinding{connectionAccessFinding(err, "")}
		}
		return nil // Existing definition/connection diagnostics own resolution.
	}
	findings := []TypeCheckFinding{}
	seen := map[string]bool{}
	for _, requirement := range requirements {
		connection, _ := selectedConfigurationConnection(cfg, requirement.Connection)
		if err := policy.CheckAccess(p, env, requirement, nativeConnectionReadOnly(connection)); err != nil && !seen[requirement.Connection] {
			findings = append(findings, connectionAccessFinding(err, requirement.Connection))
			seen[requirement.Connection] = true
		}
	}
	return findings
}

// CheckPipelineInEnvironment gives the CLI the same environment-aware checks
// as the UI, without changing the selected environment in project files.
func CheckPipelineInEnvironment(ctx context.Context, fs afero.Fs, pl *pipeline.Pipeline, root string, tw ExecutionTimeWindow, environment string) TypeCheckReport {
	return checkPipelineAt(ctx, fs, pl, root, tw, time.Now().UTC(), typeCheckOptions{Environment: environment})
}
