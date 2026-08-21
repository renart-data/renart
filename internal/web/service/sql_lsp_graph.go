package service

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/authoringdiag"
	"renart/internal/sqllsp"
)

// LoadSQLLSPGraph enriches the provider-neutral filesystem graph with Bruin
// asset/header findings. It is used by the stdio LSP at startup and on watched
// file changes. SQL semantics remain per-document; this saved-state pass does
// does not rerun semantic SQL validation.
func LoadSQLLSPGraph(ctx context.Context, workspaceRoot string) (sqllsp.CanonicalGraph, error) {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return sqllsp.CanonicalGraph{}, err
	}
	graph, err := sqllsp.LoadGraphFromDir(ctx, absRoot)
	if err != nil {
		return graph, err
	}
	pipelineDirs, err := sqlLSPPipelineDirs(ctx, absRoot)
	if err != nil {
		return graph, err
	}
	fsys := afero.NewOsFs()
	builder := NewRenartPipelineBuilder(fsys)
	parsedPipelines := make([]*pipeline.Pipeline, 0, len(pipelineDirs))
	for _, pipelineDir := range pipelineDirs {
		if ctx.Err() != nil {
			return graph, ctx.Err()
		}
		parsed, loadErr := builder.CreatePipelineFromPath(ctx, pipelineDir, pipeline.WithMutate())
		if loadErr != nil || parsed == nil {
			// The lightweight graph loader still provides syntax and graph
			// features for files that can be indexed. A single malformed Bruin
			// pipeline must not take the whole language server offline.
			continue
		}
		parsedPipelines = append(parsedPipelines, parsed)
		tw, windowErr := ResolveExecutionTimeWindow(string(parsed.Schedule), "", "", time.Now().UTC())
		if windowErr != nil {
			continue
		}
		assetsByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
		for _, asset := range parsed.Assets {
			if asset != nil {
				assetsByName[asset.Name] = asset
			}
		}
		for _, checked := range CheckPipelineAssetFindings(ctx, fsys, parsed, absRoot, tw) {
			asset := assetsByName[checked.Name]
			if asset == nil {
				continue
			}
			uri := typeCheckAssetURI(absRoot, asset)
			for _, finding := range checked.Findings {
				delivery, registered := authoringdiag.TypeCheckDelivery(finding.Code)
				if !registered || delivery != authoringdiag.DeliveryAssetHeader {
					continue
				}
				graph.AssetDiagnostics = append(graph.AssetDiagnostics, sqllsp.AssetDiagnostic{
					AssetID: graphAssetIDForURI(graph, uri),
					URI:     uri,
					Diagnostic: authoringdiag.Diagnostic{
						Code:       finding.Code,
						Source:     finding.Source,
						Severity:   authoringSeverity(finding.Severity),
						Message:    finding.Message,
						URI:        string(uri),
						Scope:      authoringdiag.ScopeAsset,
						Confidence: authoringdiag.Confidence(finding.Confidence),
					},
				})
			}
		}
	}
	for _, parsed := range parsedPipelines {
		graph = resolveAuthoringSchemaGraph(ctx, graph, parsed, nil)
	}
	return graph, nil
}

func sqlLSPPipelineDirs(ctx context.Context, root string) ([]string, error) {
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "target" || name == ".venv" || name == "venv") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "pipeline.yml" || entry.Name() == "pipeline.yaml" {
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(dirs))
	for dir := range dirs {
		result = append(result, dir)
	}
	sort.Strings(result)
	return result, nil
}

func graphAssetIDForURI(graph sqllsp.CanonicalGraph, uri sqllsp.URI) string {
	for _, asset := range graph.Assets {
		if asset.URI == uri {
			return asset.ID
		}
	}
	return ""
}

func authoringSeverity(severity string) authoringdiag.Severity {
	if severity == typeCheckSeverityError {
		return authoringdiag.SeverityError
	}
	return authoringdiag.SeverityWarning
}
