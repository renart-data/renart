package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"renart/internal/web/secretstore"
	"renart/internal/web/service"
)

func TestSecretsSetStatusAndRemoveEnvironmentBinding(t *testing.T) {
	t.Setenv("RENART_TEST_ORIGINAL_PASSWORD", "original-canary")
	t.Setenv("RENART_TEST_ROTATED_PASSWORD", "rotated-canary")
	root := writeCLISecretsWorkspace(t, "RENART_TEST_ORIGINAL_PASSWORD")

	var setOutput bytes.Buffer
	app := Root("test")
	app.Reader = strings.NewReader("")
	app.Writer = &setOutput
	app.ErrWriter = &setOutput
	err := app.Run(t.Context(), []string{
		"renart", "secrets", "set",
		"--workspace", root,
		"--from-env", "RENART_TEST_ROTATED_PASSWORD",
		"warehouse", "password",
	})
	require.NoError(t, err)
	assert.Contains(t, setOutput.String(), "Bound warehouse.password to env:RENART_TEST_ROTATED_PASSWORD")
	assert.NotContains(t, setOutput.String(), "rotated-canary")

	manifest, err := secretstore.LoadManifest(filepath.Join(root, ".renart", "secrets.yml"))
	require.NoError(t, err)
	binding, found := manifest.Binding("default", "warehouse", "password")
	require.True(t, found)
	assert.Equal(t, "RENART_WAREHOUSE_PASSWORD", binding.Symbol)
	assert.Equal(t, "env:RENART_TEST_ROTATED_PASSWORD", binding.Reference.String())

	configContents, err := os.ReadFile(filepath.Join(root, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "${RENART_WAREHOUSE_PASSWORD}")
	assert.NotContains(t, string(configContents), "original-canary")
	assert.NotContains(t, string(configContents), "rotated-canary")

	var statusOutput bytes.Buffer
	app = Root("test")
	app.Writer = &statusOutput
	err = app.Run(t.Context(), []string{
		"renart", "secrets", "status",
		"--workspace", root,
		"--json",
		"warehouse", "password",
	})
	require.NoError(t, err)
	var status []cliSecretStatus
	require.NoError(t, json.Unmarshal(statusOutput.Bytes(), &status))
	require.Len(t, status, 1)
	assert.Equal(t, "configured", status[0].Status)
	assert.Equal(t, "env", status[0].Provider)
	assert.Equal(t, "env:RENART_TEST_ROTATED_PASSWORD", status[0].Reference)

	var removeOutput bytes.Buffer
	app = Root("test")
	app.Reader = strings.NewReader("")
	app.Writer = &removeOutput
	err = app.Run(t.Context(), []string{
		"renart", "secrets", "remove",
		"--workspace", root,
		"--yes",
		"warehouse", "password",
	})
	require.NoError(t, err)
	assert.Contains(t, removeOutput.String(), "Removed warehouse.password")

	manifest, err = secretstore.LoadManifest(filepath.Join(root, ".renart", "secrets.yml"))
	require.NoError(t, err)
	_, found = manifest.Binding("default", "warehouse", "password")
	assert.False(t, found)
}

func TestSecretsRemoveRequiresExplicitNonInteractiveConfirmation(t *testing.T) {
	t.Setenv("RENART_TEST_PASSWORD", "canary")
	root := writeCLISecretsWorkspace(t, "RENART_TEST_PASSWORD")

	app := Root("test")
	app.Reader = strings.NewReader("")
	var handled error
	app.ExitErrHandler = func(_ context.Context, _ *cli.Command, err error) {
		handled = err
	}
	_ = app.Run(t.Context(), []string{
		"renart", "secrets", "remove",
		"--workspace", root,
		"warehouse", "password",
	})
	require.ErrorContains(t, handled, "requires --yes")

	manifest, err := secretstore.LoadManifest(filepath.Join(root, ".renart", "secrets.yml"))
	require.NoError(t, err)
	_, found := manifest.Binding("default", "warehouse", "password")
	assert.True(t, found)
}

func TestSecretsExecProvidesOnlyChildScopedSymbol(t *testing.T) {
	t.Setenv("RENART_TEST_SECRET_EXEC_HELPER", "1")
	t.Setenv("RENART_TEST_SOURCE_PASSWORD", "child-secret-canary")
	t.Setenv("RENART_WAREHOUSE_PASSWORD", "parent-value")
	root := writeCLISecretsWorkspace(t, "RENART_TEST_SOURCE_PASSWORD")

	var output bytes.Buffer
	var errorOutput bytes.Buffer
	app := Root("test")
	app.Writer = &output
	app.ErrWriter = &errorOutput
	var handled error
	app.ExitErrHandler = func(_ context.Context, _ *cli.Command, err error) {
		handled = err
	}
	err := app.Run(t.Context(), []string{
		"renart", "secrets", "exec",
		"--workspace", root,
		"--",
		os.Args[0],
		"-test.run=^TestSecretsExecChildProcess$",
	})
	require.NoError(t, err)
	require.NoError(t, handled, errorOutput.String())
	assert.Equal(t, "child-secret-canary", output.String())
	assert.Equal(t, "parent-value", os.Getenv("RENART_WAREHOUSE_PASSWORD"))
}

func TestSecretsEncryptedVaultSetAndExec(t *testing.T) {
	t.Setenv("RENART_VAULT_DIR", filepath.Join(t.TempDir(), "vaults"))
	t.Setenv("RENART_VAULT_PASSPHRASE", "test vault passphrase")
	t.Setenv("RENART_TEST_SECRET_EXEC_HELPER", "1")
	t.Setenv("RENART_TEST_EXPECT_NO_VAULT_PASSPHRASE", "1")
	root := writeCLISecretsWorkspace(t, "RENART_TEST_SOURCE_PASSWORD")

	var output bytes.Buffer
	app := Root("test")
	app.Reader = strings.NewReader("")
	app.Writer = &output
	app.ErrWriter = &output
	require.NoError(t, app.Run(t.Context(), []string{
		"renart", "secrets", "vault", "init", "--workspace", root,
	}))
	assert.Contains(t, output.String(), "Encrypted vault initialized")

	output.Reset()
	app = Root("test")
	app.Reader = strings.NewReader("vault-secret-canary\n")
	app.Writer = &output
	app.ErrWriter = &output
	require.NoError(t, app.Run(t.Context(), []string{
		"renart", "secrets", "set",
		"--workspace", root,
		"--store", "vault",
		"warehouse", "password",
	}))
	assert.Contains(t, output.String(), "encrypted local vault")
	assert.NotContains(t, output.String(), "vault-secret-canary")

	manifest, err := secretstore.LoadManifest(filepath.Join(root, ".renart", "secrets.yml"))
	require.NoError(t, err)
	binding, found := manifest.Binding("default", "warehouse", "password")
	require.True(t, found)
	assert.Equal(t, "local-vault:warehouse/password", binding.Reference.String())

	vaultFiles, err := filepath.Glob(filepath.Join(os.Getenv("RENART_VAULT_DIR"), "*.age"))
	require.NoError(t, err)
	require.Len(t, vaultFiles, 1)
	ciphertext, err := os.ReadFile(vaultFiles[0])
	require.NoError(t, err)
	assert.NotContains(t, string(ciphertext), "vault-secret-canary")

	output.Reset()
	app = Root("test")
	app.Writer = &output
	app.ErrWriter = &output
	require.NoError(t, app.Run(t.Context(), []string{
		"renart", "secrets", "exec",
		"--workspace", root,
		"--",
		os.Args[0],
		"-test.run=^TestSecretsExecChildProcess$",
	}))
	assert.Equal(t, "vault-secret-canary", output.String())
}

func TestSecretsExecChildProcess(t *testing.T) {
	if os.Getenv("RENART_TEST_SECRET_EXEC_HELPER") != "1" {
		return
	}
	if os.Getenv("RENART_TEST_EXPECT_NO_VAULT_PASSPHRASE") == "1" &&
		os.Getenv("RENART_VAULT_PASSPHRASE") != "" {
		os.Exit(4) //nolint:gocritic // test helper process must bypass the test harness
	}
	value := os.Getenv("RENART_WAREHOUSE_PASSWORD")
	for _, argument := range os.Args {
		if strings.Contains(argument, value) {
			os.Exit(3) //nolint:gocritic // test helper process must bypass the test harness
		}
	}
	_, _ = fmt.Fprint(os.Stdout, value)
	os.Exit(0) //nolint:gocritic // test helper process must bypass the test harness
}

func TestReadCLISecretValuePreservesContentAndDropsOneLineEnding(t *testing.T) {
	command := &cli.Command{Reader: strings.NewReader("  secret value  \r\n")}
	value, err := readCLISecretValue(command)
	require.NoError(t, err)
	assert.Equal(t, []byte("  secret value  "), value)
	clearCLIBytes(value)

	command.Reader = strings.NewReader(strings.Repeat("x", maxCLISecretBytes+1))
	_, err = readCLISecretValue(command)
	require.ErrorContains(t, err, "exceeds")
}

func TestOverlayCLIEnvironmentReplacesOnlyExactKeys(t *testing.T) {
	result := overlayCLIEnvironment(
		[]string{"PATH=/bin", "TOKEN=old", "TOKEN_SUFFIX=keep"},
		map[string]string{"TOKEN": "new", "Z_SECRET": "last"},
	)
	assert.Equal(t, []string{
		"PATH=/bin",
		"TOKEN_SUFFIX=keep",
		"TOKEN=new",
		"Z_SECRET=last",
	}, result)
}

func writeCLISecretsWorkspace(t *testing.T, sourceVariable string) string {
	t.Helper()
	root := t.TempDir()
	// Keep parent repositories (including one in TMPDIR) out of CLI discovery.
	output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput()
	require.NoError(t, err, "%s", output)
	configService := service.NewConfigService(root, filepath.Join(root, ".bruin.yml"))
	_, err = configService.CreateConnectionAndPersist(
		t.Context(),
		service.UpsertWorkspaceConnectionParams{
			EnvironmentName: "default",
			Name:            "warehouse",
			Type:            "postgres",
			Values: map[string]any{
				"host":     "localhost",
				"port":     5432,
				"database": "analytics",
				"username": "renart",
			},
			SecretChanges: map[string]service.WorkspaceConnectionSecretChange{
				"password": {
					Action: "replace",
					Binding: &service.WorkspaceConnectionSecretBinding{
						Ref: "env:" + sourceVariable,
					},
				},
			},
		},
	)
	require.NoError(t, err)
	loaded, loadedPath, err := configService.LoadReadOnly()
	require.NoError(t, err)
	require.Equal(t, loadedPath, resolveConfigFilePath(root), "CLI must use the explicitly selected workspace configuration")
	response := configService.BuildResponse(loadedPath, loaded)
	require.Len(t, response.Environments, 1)
	require.Len(t, response.Environments[0].Connections, 1, "fixture connection must survive persistence")
	return root
}
