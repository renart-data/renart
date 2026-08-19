package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/git"
	gogit "github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestResolveServerWorkspaceRootKeepsGitProject(t *testing.T) {
	project := t.TempDir()
	_, err := gogit.PlainInit(project, false)
	require.NoError(t, err)

	root, bootstrap, err := resolveServerWorkspaceRoot(project, true)

	require.NoError(t, err)
	require.Equal(t, project, root)
	require.Empty(t, bootstrap)
}

func TestResolveServerWorkspaceRootBootstrapsImplicitLauncher(t *testing.T) {
	launchRoot := t.TempDir()

	root, bootstrap, err := resolveServerWorkspaceRoot(launchRoot, true)
	require.NoError(t, err)
	require.NotEqual(t, launchRoot, root)
	require.Equal(t, root, bootstrap)
	t.Cleanup(func() { _ = os.RemoveAll(bootstrap) })

	repo, err := git.FindRepoFromPath(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), filepath.Clean(repo.Path))
}

func TestResolveServerWorkspaceRootRejectsExplicitNonProject(t *testing.T) {
	launchRoot := t.TempDir()

	root, bootstrap, err := resolveServerWorkspaceRoot(launchRoot, false)

	require.ErrorContains(t, err, "projects must live inside a git repository")
	require.Empty(t, root)
	require.Empty(t, bootstrap)
}

func TestCleanupServerBootstrapRemovesOnlyTemporaryRoot(t *testing.T) {
	root := t.TempDir()
	bootstrap := filepath.Join(root, "bootstrap")
	require.NoError(t, os.Mkdir(bootstrap, 0o755))

	cleanupServerBootstrap(serverConfig{workspaceRoot: bootstrap, bootstrapRoot: bootstrap})

	require.NoDirExists(t, bootstrap)
	require.DirExists(t, root)
}

func TestServerFilesystemAccessFlagDefaultsOnAndCanBeDisabled(t *testing.T) {
	project := t.TempDir()
	_, err := gogit.PlainInit(project, false)
	require.NoError(t, err)

	parse := func(args ...string) serverConfig {
		t.Helper()
		var result serverConfig
		command := &cli.Command{
			Name:  "test-server-config",
			Flags: serverFlags(),
			Action: func(_ context.Context, command *cli.Command) error {
				var configErr error
				result, configErr = serverConfigFromCommand(command)
				return configErr
			},
		}
		require.NoError(t, command.Run(t.Context(), append([]string{"test-server-config"}, args...)))
		return result
	}

	require.False(t, parse(project).disableFilesystemAccess)
	require.True(t, parse("--enable-filesystem-access=false", project).disableFilesystemAccess)
}

func TestServerNotebookSnapshotBudgetsDefaultOverrideAndValidate(t *testing.T) {
	project := t.TempDir()
	_, err := gogit.PlainInit(project, false)
	require.NoError(t, err)

	parse := func(args ...string) (serverConfig, error) {
		t.Helper()
		var result serverConfig
		command := &cli.Command{
			Name:  "test-server-config",
			Flags: serverFlags(),
			Action: func(_ context.Context, command *cli.Command) error {
				var configErr error
				result, configErr = serverConfigFromCommand(command)
				return configErr
			},
		}
		err := command.Run(t.Context(), append([]string{"test-server-config"}, args...))
		return result, err
	}

	defaults, err := parse(project)
	require.NoError(t, err)
	require.EqualValues(t, 2<<30, defaults.notebookSnapshotMaxBytes)
	require.Equal(t, 30*time.Minute, defaults.notebookSnapshotTimeout)

	overridden, err := parse(
		"--notebook-snapshot-max-bytes=1048576",
		"--notebook-snapshot-timeout=45s",
		project,
	)
	require.NoError(t, err)
	require.EqualValues(t, 1048576, overridden.notebookSnapshotMaxBytes)
	require.Equal(t, 45*time.Second, overridden.notebookSnapshotTimeout)

	_, err = parse("--notebook-snapshot-max-bytes=0", project)
	require.ErrorContains(t, err, "notebook-snapshot-max-bytes must be greater than zero")
	_, err = parse("--notebook-snapshot-timeout=0s", project)
	require.ErrorContains(t, err, "notebook-snapshot-timeout must be greater than zero")
}
