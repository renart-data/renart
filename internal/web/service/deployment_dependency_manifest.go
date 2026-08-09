package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/identity"
	"renart/internal/web/snapshot"
)

// BuildDeploymentDependencyManifest converts the canonical workspace graph
// into the immutable URI-owner contract retained by one consumer deployment.
func BuildDeploymentDependencyManifest(
	graph dependencygraph.Graph,
	pipelineUUID string,
) (snapshot.DependencyManifest, error) {
	pipelineUUID = strings.TrimSpace(pipelineUUID)
	if pipelineUUID == "" {
		return snapshot.DependencyManifest{}, fmt.Errorf("deployment dependency manifest requires a pipeline UUID")
	}
	problems := make([]string, 0)
	for _, diagnostic := range graph.Diagnostics {
		owner, _, ok := identity.SplitAssetID(diagnostic.AssetID)
		if ok && owner == pipelineUUID && diagnostic.Severity == dependencygraph.SeverityError {
			problems = append(problems, diagnostic.Message)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return snapshot.DependencyManifest{}, fmt.Errorf(
			"deployment dependency manifest is invalid: %s", strings.Join(problems, "; "),
		)
	}

	manifest := snapshot.EmptyDependencyManifest()
	for _, edge := range graph.Edges {
		consumer := graph.Nodes[edge.ConsumerID]
		if consumer == nil || consumer.PipelineUUID != pipelineUUID || edge.Type != "uri" {
			continue
		}
		item := snapshot.DependencyManifestItem{
			ConsumerAssetID: edge.ConsumerID,
			URI:             strings.TrimSpace(edge.Value),
			Mode:            edge.Mode.String(),
		}
		if item.Mode == "" {
			item.Mode = "full"
		}
		if edge.Resolved {
			producer := graph.Nodes[edge.ProducerID]
			if producer == nil {
				return snapshot.DependencyManifest{}, fmt.Errorf(
					"deployment dependency manifest cannot load producer for URI %q", edge.Value,
				)
			}
			if item.Mode == "full" && isSourceAssetType(producer.Asset.Type) {
				return snapshot.DependencyManifest{}, fmt.Errorf(
					"full dependency URI %q resolves to external source %s; use symbolic mode for lineage-only links",
					item.URI, producer.AssetName,
				)
			}
			item.ProducerPipelineUUID = producer.PipelineUUID
			item.ProducerAssetURI = producer.URI
		}
		manifest.Dependencies = append(manifest.Dependencies, item)
	}
	return manifest, nil
}

// ResolveDeploymentDependencyManifest reads a stable deploy contract from the
// current workspace and returns the source Merkle root that the snapshot store
// must recheck while persisting it.
func ResolveDeploymentDependencyManifest(
	ctx context.Context,
	workspaceRoot string,
	configPath string,
	pipelineUUID string,
) (snapshot.DependencyManifest, string, string, error) {
	workspace := NewWorkspaceService(workspaceRoot, configPath)
	state, err := workspace.ComputeState(ctx)
	if err != nil {
		return snapshot.DependencyManifest{}, "", "", err
	}
	paths := make(map[string]string, len(state.Pipelines))
	for _, item := range state.Pipelines {
		paths[strings.TrimSpace(item.UUID)] = item.Path
	}
	targetPath, ok := paths[strings.TrimSpace(pipelineUUID)]
	if !ok {
		return snapshot.DependencyManifest{}, "", "", fmt.Errorf("pipeline %s is not present in the workspace", pipelineUUID)
	}
	resolve := func(ctx context.Context, uuid string) (*pipeline.Pipeline, error) {
		relPath, found := paths[strings.TrimSpace(uuid)]
		if !found {
			return nil, fmt.Errorf("pipeline %s is not present in the workspace", uuid)
		}
		absPath, joinErr := SafeJoin(workspaceRoot, relPath)
		if joinErr != nil {
			return nil, joinErr
		}
		return NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
			ctx, absPath, pipeline.WithMutate(),
		)
	}
	graph, err := ResolveWorkspaceDependencyGraph(ctx, state, resolve, nil)
	if err != nil {
		return snapshot.DependencyManifest{}, "", "", err
	}
	manifest, err := BuildDeploymentDependencyManifest(graph, pipelineUUID)
	if err != nil {
		return snapshot.DependencyManifest{}, "", "", err
	}
	pipelineDir, err := SafeJoin(workspaceRoot, filepath.ToSlash(targetPath))
	if err != nil {
		return snapshot.DependencyManifest{}, "", "", err
	}
	sourceManifest, err := snapshot.CollectManifestHashes(pipelineDir)
	if err != nil {
		return snapshot.DependencyManifest{}, "", "", err
	}
	if len(sourceManifest) == 0 {
		return snapshot.DependencyManifest{}, "", "", fmt.Errorf("snapshot: no files found under %s", pipelineDir)
	}
	return manifest, snapshot.ManifestRoot(sourceManifest), pipelineDir, nil
}
