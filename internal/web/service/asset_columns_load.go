package service

import (
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// resolveLoadSourceAsset finds the asset a Load asset reads from: first by
// matching the source_table parameter to an asset name, then by a single declared
// upstream asset.
func resolveLoadSourceAsset(parsedPipeline *pipeline.Pipeline, asset *pipeline.Asset) *pipeline.Asset {
	if parsedPipeline == nil || asset == nil {
		return nil
	}

	byName := make(map[string]*pipeline.Asset, len(parsedPipeline.Assets))
	for _, candidate := range parsedPipeline.Assets {
		if candidate != nil {
			byName[strings.TrimSpace(candidate.Name)] = candidate
		}
	}

	// 1. The source_table parameter names an existing asset.
	if sourceTable := strings.TrimSpace(loadParamsFromAsset(asset).SourceTable); sourceTable != "" {
		if match := byName[sourceTable]; match != nil {
			return match
		}
		for name, candidate := range byName {
			if strings.EqualFold(name, sourceTable) {
				return candidate
			}
		}
	}

	// 2. A single declared upstream asset.
	var upstreamAssets []*pipeline.Asset
	for _, upstream := range asset.Upstreams {
		if upstream.Type != "" && upstream.Type != "asset" {
			continue
		}
		if match := byName[strings.TrimSpace(upstream.Value)]; match != nil {
			upstreamAssets = append(upstreamAssets, match)
		}
	}
	if len(upstreamAssets) == 1 {
		return upstreamAssets[0]
	}
	return nil
}
