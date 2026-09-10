package service

import (
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// validateLoaderMaterialization rejects modes that can never execute for Load
// and HTTP API assets while allowing temporarily incomplete edit states. In
// particular, users must be able to select merge before marking a column as a
// primary key. Execution calls slingMaterializationArgs, which enforces that
// prerequisite before starting the loader.
func validateLoaderMaterialization(asset *pipeline.Asset) error {
	if asset == nil || (!isAPIAsset(asset) && !isLoadAsset(asset)) {
		return nil
	}
	if isLoadAsset(asset) {
		if _, err := loadParallelism(asset); err != nil {
			return err
		}
	}
	materializationType := strings.ToLower(strings.TrimSpace(string(asset.Materialization.Type)))
	if materializationType != "" && materializationType != "table" {
		return fmt.Errorf("materialization type %q is not supported for %s assets", materializationType, asset.Type)
	}
	strategy := strings.ToLower(strings.TrimSpace(string(asset.Materialization.Strategy)))
	switch strategy {
	case "", "create+replace", "create_replace", "full-refresh", "full_refresh",
		"truncate+insert", "truncate_insert", "truncate", "append", "merge":
		return nil
	default:
		return fmt.Errorf("materialization strategy %q is not supported for %s assets", strategy, asset.Type)
	}
}

func loaderMaterializationAPIError(asset *pipeline.Asset) *APIError {
	if err := validateLoaderMaterialization(asset); err != nil {
		return badRequestError("unsupported_materialization", err.Error())
	}
	return nil
}
