package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

// findWorkspaceRoot resolves the workspace the CLI operates on, git-style:
// walk up from startDir to the nearest directory containing the project
// config (.bruin.yml), falling back to the nearest .renart state directory,
// then to the enclosing git repository root. An explicit --workspace value
// short-circuits the walk.
func findWorkspaceRoot(explicit, startDir string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("workspace %s: %w", abs, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workspace %s is not a directory", abs)
		}
		return abs, nil
	}

	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	for _, marker := range []string{".bruin.yml", ".renart", ".git"} {
		if root, ok := findMarkerUp(abs, marker); ok {
			return root, nil
		}
	}
	return "", fmt.Errorf(
		"no renart workspace found in %s or any parent directory (looked for .bruin.yml, .renart, or a git repository); pass --workspace or run inside a project",
		abs,
	)
}

// findMarkerUp walks from dir to the filesystem root and returns the first
// directory containing the marker entry.
func findMarkerUp(dir, marker string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// workspacePipeline is one pipeline directory found under a workspace root.
type workspacePipeline struct {
	Name string
	Dir  string // absolute
}

// listWorkspacePipelines finds every pipeline.yml/pipeline.yaml under root
// (skipping dot-directories and common vendored trees) and reads each
// pipeline's name. Cheap by design: it parses only the pipeline manifests,
// not the assets.
func listWorkspacePipelines(root string) ([]workspacePipeline, error) {
	var pipelines []workspacePipeline
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable subtrees are skipped, not fatal
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "logs" || name == "venv") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "pipeline.yml" && entry.Name() != "pipeline.yaml" {
			return nil
		}
		var manifest struct {
			Name string `yaml:"name"`
		}
		if raw, readErr := os.ReadFile(path); readErr == nil {
			_ = yaml.Unmarshal(raw, &manifest)
		}
		dir := filepath.Dir(path)
		name := manifest.Name
		if name == "" {
			name = filepath.Base(dir)
		}
		pipelines = append(pipelines, workspacePipeline{Name: name, Dir: dir})
		// A pipeline directory never nests another pipeline.
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(pipelines, func(i, j int) bool { return pipelines[i].Name < pipelines[j].Name })
	return pipelines, nil
}

// resolvePipelineDir maps a target (pipeline name, directory path, or empty
// for "the pipeline around cwd") to the pipeline's absolute directory.
func resolvePipelineDir(workspaceRoot, target, cwd string) (string, error) {
	trimmed := strings.TrimSpace(target)

	if trimmed == "" {
		// No target: the pipeline whose directory encloses cwd.
		if dir, ok := findMarkerUp(cwd, "pipeline.yml"); ok {
			return dir, nil
		}
		if dir, ok := findMarkerUp(cwd, "pipeline.yaml"); ok {
			return dir, nil
		}
		return "", fmt.Errorf("not inside a pipeline directory; pass a pipeline name or directory")
	}

	// A directory path (absolute, or relative to cwd) wins over name lookup.
	for _, base := range []string{"", cwd} {
		candidate := trimmed
		if base != "" && !filepath.IsAbs(candidate) {
			candidate = filepath.Join(base, candidate)
		}
		if !filepath.IsAbs(candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if hasPipelineManifest(candidate) {
				return filepath.Clean(candidate), nil
			}
			return "", fmt.Errorf("%s has no pipeline.yml", candidate)
		}
	}

	pipelines, err := listWorkspacePipelines(workspaceRoot)
	if err != nil {
		return "", err
	}
	var matches []workspacePipeline
	for _, p := range pipelines {
		if p.Name == trimmed || filepath.Base(p.Dir) == trimmed {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].Dir, nil
	case 0:
		names := make([]string, 0, len(pipelines))
		for _, p := range pipelines {
			names = append(names, p.Name)
		}
		if len(names) == 0 {
			return "", fmt.Errorf("no pipelines found under %s", workspaceRoot)
		}
		return "", fmt.Errorf("no pipeline named %q in %s (available: %s)", trimmed, workspaceRoot, strings.Join(names, ", "))
	default:
		dirs := make([]string, 0, len(matches))
		for _, m := range matches {
			dirs = append(dirs, m.Dir)
		}
		return "", fmt.Errorf("pipeline name %q is ambiguous, pass a directory instead: %s", trimmed, strings.Join(dirs, ", "))
	}
}

// workspaceFlag is the shared --workspace override; without it the
// workspace is resolved by walking up from the current directory.
func workspaceFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:  "workspace",
		Usage: "workspace root (default: walk up from the current directory)",
	}
}

// resolvePipelineTarget combines the walk-up and the target grammar: it
// returns the pipeline directory for the command's positional target.
func resolvePipelineTarget(c *cli.Command) (string, error) {
	_, dir, err := resolvePipelineTargetAndWorkspace(c)
	return dir, err
}

func resolvePipelineTargetAndWorkspace(c *cli.Command) (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	workspaceRoot, err := findWorkspaceRoot(c.String("workspace"), cwd)
	if err != nil {
		return "", "", err
	}
	dir, err := resolvePipelineDir(workspaceRoot, c.Args().Get(0), cwd)
	if err != nil {
		return "", "", cli.Exit(err.Error(), 2)
	}
	return workspaceRoot, dir, nil
}

func hasPipelineManifest(dir string) bool {
	return fileExists(filepath.Join(dir, "pipeline.yml")) || fileExists(filepath.Join(dir, "pipeline.yaml"))
}
