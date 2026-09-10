package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/bruin-data/bruin/pkg/git"
	"github.com/urfave/cli/v3"

	"renart/internal/web/service"
)

// Init scaffolds a new project from the same templates the welcome UI
// offers (POST /api/projects), completing the terminal story:
// init → run → web.
func Init() *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "scaffold a new renart project",
		ArgsUsage: "[directory]",
		Category:  categoryProject,
		Description: "Creates the project files in the given directory (default: the current\n" +
			"directory) and initializes a git repository when none encloses it.\n" +
			"Templates: empty (a minimal pipeline), retail (offline SQL demo),\n" +
			"chess (live Chess.com API demo).",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "template",
				Value: "empty",
				Usage: "project template: empty, retail, or chess",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			templateID, err := initTemplateID(c.String("template"))
			if err != nil {
				return cli.Exit(err.Error(), 2)
			}

			target := c.Args().Get(0)
			if target == "" {
				target = "."
			}
			absTarget, err := filepath.Abs(target)
			if err != nil {
				return err
			}

			// A directory inside an existing repository joins it; anywhere
			// else becomes its own repository with an initial commit.
			newRepository := false
			configPath := filepath.Join(absTarget, ".bruin.yml")
			if _, repoErr := git.FindRepoFromPath(absTarget); repoErr != nil {
				newRepository = true
			} else {
				configPath = resolveConfigFilePath(absTarget)
			}

			scaffold, err := service.ScaffoldProject(service.ScaffoldProjectRequest{
				TargetDir:     absTarget,
				ConfigPath:    configPath,
				Template:      templateID,
				NewRepository: newRepository,
			})
			if err != nil {
				return err
			}

			for _, file := range scaffold.Files {
				fmt.Printf("  created %s\n", file)
			}
			if scaffold.GitInitialized {
				fmt.Println("  initialized a git repository")
			}
			fmt.Printf("\nProject ready in %s. Next:\n", absTarget)
			if target != "." {
				fmt.Printf("  cd %s\n", target)
			}
			fmt.Println("  renart web    # open the workspace")
			fmt.Println("  renart run    # or run the pipeline right here")
			return nil
		},
	}
}

// initTemplateID maps the user-facing template names onto the service IDs
// (the UI-only "bare" import template is deliberately not offered).
func initTemplateID(name string) (string, error) {
	switch name {
	case "empty":
		return service.ProjectTemplateEmpty, nil
	case "retail":
		return service.ProjectTemplateRetailDemo, nil
	case "chess":
		return service.ProjectTemplateChessDemo, nil
	default:
		return "", fmt.Errorf("unknown template %q (expected empty, retail, or chess)", name)
	}
}
