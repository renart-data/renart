package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
	"github.com/urfave/cli/v3"
	"renart/internal/web/identity"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/service"
	"renart/internal/web/snapshot"
)

// Deploy snapshots a pipeline's source files into the local state store so
// scheduled runs execute the deployed version regardless of working-tree
// edits.
func Deploy() *cli.Command {
	return &cli.Command{
		Name:      "deploy",
		Usage:     "snapshot a pipeline so scheduled runs execute the deployed version",
		ArgsUsage: "[pipeline name or directory]",
		Category:  categoryPipeline,
		Flags: []cli.Flag{
			workspaceFlag(),
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			absTarget, err := resolvePipelineTarget(c)
			if err != nil {
				return err
			}

			pipelineYml := filepath.Join(absTarget, "pipeline.yml")
			if !fileExists(pipelineYml) {
				if alt := filepath.Join(absTarget, "pipeline.yaml"); fileExists(alt) {
					pipelineYml = alt
				} else {
					return fmt.Errorf("no pipeline.yml found in %s", absTarget)
				}
			}
			pipelineUUID, generated, err := identity.EnsurePipelineID(afero.NewOsFs(), pipelineYml)
			if err != nil {
				return fmt.Errorf("failed to ensure pipeline id: %w", err)
			}
			if generated {
				fmt.Fprintf(os.Stderr, "notice: assigned stable id %s to %s\n", pipelineUUID, pipelineYml)
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			workspaceRoot, err := findWorkspaceRoot(c.String("workspace"), cwd)
			if err != nil {
				return err
			}
			statePath := filepath.Join(workspaceRoot, ".renart", "state.db")
			store, err := webscheduler.OpenStore(statePath)
			if err != nil {
				return fmt.Errorf("failed to open state store at %s: %w", statePath, err)
			}
			defer store.Close()

			dependencyManifest, sourceRoot, resolvedPipelineDir, err := service.ResolveDeploymentDependencyManifest(
				ctx, workspaceRoot, filepath.Join(workspaceRoot, ".bruin.yml"), pipelineUUID,
			)
			if err != nil {
				return err
			}
			if filepath.Clean(resolvedPipelineDir) != filepath.Clean(absTarget) {
				return fmt.Errorf("pipeline %s resolved to %s instead of %s", pipelineUUID, resolvedPipelineDir, absTarget)
			}
			deployed, created, err := snapshot.NewStore(store.DB()).DeployReviewedWithDependencies(
				ctx, pipelineUUID, absTarget, "cli", sourceRoot, dependencyManifest,
			)
			if err != nil {
				return err
			}
			if !created {
				fmt.Printf("already up to date: Deployment #%d (%s) matches the working tree\n", deployed.Ordinal, deployed.VersionID)
				return nil
			}
			fmt.Printf("created Deployment #%d (%s, %d files", deployed.Ordinal, deployed.VersionID, len(deployed.Manifest))
			if deployed.GitSHA != "" {
				dirty := ""
				if deployed.GitDirty {
					dirty = ", dirty"
				}
				fmt.Printf(", git %.10s%s", deployed.GitSHA, dirty)
			}
			fmt.Println(")")
			return nil
		},
	}
}
