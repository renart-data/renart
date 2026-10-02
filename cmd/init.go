package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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
			"Templates:\n" + initTemplateHelp(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "template",
				Value: "empty",
				Usage: "project template: " + strings.Join(initTemplateNames(), ", "),
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

// initTemplateName is the terminal name of a welcome-catalog template: its ID
// without the "demo:" prefix. The UI-only "bare" import template is
// deliberately not offered.
func initTemplateName(id string) string {
	return strings.TrimPrefix(id, "demo:")
}

func initTemplates() []service.ProjectTemplateInfo {
	templates := []service.ProjectTemplateInfo{}
	for _, template := range service.ProjectTemplates() {
		if template.ID != service.ProjectTemplateBare {
			templates = append(templates, template)
		}
	}
	return templates
}

func initTemplateNames() []string {
	names := []string{}
	for _, template := range initTemplates() {
		names = append(names, initTemplateName(template.ID))
	}
	return names
}

func initTemplateHelp() string {
	var help strings.Builder
	for _, template := range initTemplates() {
		fmt.Fprintf(&help, "  %-12s %s\n", initTemplateName(template.ID), template.Title)
	}
	return strings.TrimRight(help.String(), "\n")
}

// initTemplateID maps a terminal template name onto its service ID.
func initTemplateID(name string) (string, error) {
	for _, template := range initTemplates() {
		if initTemplateName(template.ID) == strings.TrimSpace(name) {
			return template.ID, nil
		}
	}
	return "", fmt.Errorf("unknown template %q (expected one of: %s)", name, strings.Join(initTemplateNames(), ", "))
}
