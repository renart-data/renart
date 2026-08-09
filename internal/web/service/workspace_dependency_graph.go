package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/dependencygraph"
)

// WorkspaceDependencyGraphResolver builds the canonical dependency graph for
// the current workspace. Callers may replace selected pipelines with already
// parsed immutable snapshot sources while retaining the rest of the workspace.
type WorkspaceDependencyGraphResolver func(
	ctx context.Context,
	overrides map[string]*pipeline.Pipeline,
) (dependencygraph.Graph, error)

// ResolveWorkspaceDependencyGraph constructs the shared graph from the
// workspace coordinator's current identity state and parsed Bruin pipelines.
// The state supplies browser-facing IDs; parsed sources remain authoritative
// for dependency and fingerprint semantics.
func ResolveWorkspaceDependencyGraph(
	ctx context.Context,
	state WorkspaceState,
	resolve func(context.Context, string) (*pipeline.Pipeline, error),
	overrides map[string]*pipeline.Pipeline,
) (dependencygraph.Graph, error) {
	inputs := make([]dependencygraph.PipelineInput, 0, len(state.Pipelines))
	for _, summary := range state.Pipelines {
		if err := ctx.Err(); err != nil {
			return dependencygraph.Graph{}, err
		}
		pipelineUUID := strings.TrimSpace(summary.UUID)
		if pipelineUUID == "" {
			continue
		}
		parsed := overrides[pipelineUUID]
		if parsed == nil {
			if resolve == nil {
				return dependencygraph.Graph{}, fmt.Errorf("workspace dependency graph: pipeline resolver is unavailable")
			}
			var err error
			parsed, err = resolve(ctx, pipelineUUID)
			if err != nil {
				return dependencygraph.Graph{}, fmt.Errorf("workspace dependency graph: resolve pipeline %s: %w", pipelineUUID, err)
			}
		}
		workspaceIDs := make(map[string]string, len(summary.Assets))
		for _, asset := range summary.Assets {
			workspaceIDs[asset.Name] = asset.ID
		}
		inputs = append(inputs, dependencygraph.PipelineInput{
			UUID:              pipelineUUID,
			ID:                summary.ID,
			Name:              summary.Name,
			Path:              summary.Path,
			Parsed:            parsed,
			AssetWorkspaceIDs: workspaceIDs,
		})
	}
	return dependencygraph.Resolve(inputs), nil
}
