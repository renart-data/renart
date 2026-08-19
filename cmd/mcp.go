package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	"renart/internal/clientapi"
	"renart/internal/notebookmcp"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

// MCP exposes the local notebook developer integration over stdio. Stdout is
// exclusively owned by the protocol; all diagnostics go to stderr.
func MCP() *cli.Command {
	return &cli.Command{
		Name:     "mcp",
		Usage:    "serve workspace-scoped notebook tools over MCP stdio",
		Category: categoryIDE,
		Flags:    []cli.Flag{workspaceFlag()},
		Description: "Starts a local MCP server for semantic notebook reads, reviewed edits, and explicit runs.\n" +
			"It does not expose filesystem, shell, Git, secrets, or generic API tools.",
		Action: func(ctx context.Context, command *cli.Command) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			workspaceRoot, err := findWorkspaceRoot(command.String("workspace"), cwd)
			if err != nil {
				return cli.Exit(err.Error(), 2)
			}

			backend, cleanup, err := notebookMCPBackend(ctx, workspaceRoot)
			if err != nil {
				return fmt.Errorf("initialize notebook MCP backend: %w", err)
			}
			defer cleanup()

			// The SDK reports a normal stdio EOF as an error-level lifecycle log.
			// Suppress SDK internals here; command failures still return through the
			// CLI and are printed to stderr, while stdout remains protocol-only.
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(100)}))
			server := notebookmcp.New(ctx, backend, buildVersion, logger)
			if err := server.Protocol().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil && !normalMCPClientClose(err) {
				return fmt.Errorf("serve notebook MCP over stdio: %w", err)
			}
			return nil
		},
	}
}

func normalMCPClientClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return true
	}
	// The SDK preserves ErrServerClosing as the wrapped error and appends the
	// transport EOF as text. EOF is the normal stdio lifecycle when the owning
	// client exits or restarts the server.
	return strings.HasSuffix(err.Error(), ": EOF")
}

func notebookMCPBackend(ctx context.Context, workspaceRoot string) (notebookmcp.Backend, func(), error) {
	if configured := clientapi.FromEnv(); configured != nil {
		health, err := configured.Health(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("RENART_SERVER is not responding: %w", err)
		}
		if !sameWorkspaceRoot(health.WorkspaceRoot, workspaceRoot) {
			return nil, nil, fmt.Errorf("RENART_SERVER serves %s, not %s", health.WorkspaceRoot, workspaceRoot)
		}
		return clientNotebookBackend{client: configured}, func() {}, nil
	}
	if client, _ := clientapi.Discover(ctx, workspaceRoot); client != nil {
		return clientNotebookBackend{client: client}, func() {}, nil
	}
	server, cleanup, err := newEmbeddedServer(ctx, workspaceRoot)
	if err != nil {
		return nil, nil, err
	}
	return embeddedNotebookBackend{server: server}, cleanup, nil
}

func sameWorkspaceRoot(left, right string) bool {
	resolve := func(path string) string {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
			return resolved
		}
		return cleaned
	}
	return resolve(left) == resolve(right)
}

type clientNotebookBackend struct{ client *clientapi.Client }

func (b clientNotebookBackend) Workspace(ctx context.Context) (model.WorkspaceState, error) {
	return b.client.Workspace(ctx)
}

func (b clientNotebookBackend) Notebook(ctx context.Context, id string) (model.Notebook, error) {
	return b.client.Notebook(ctx, id)
}

func (b clientNotebookBackend) Runtime(ctx context.Context, id string) (service.NotebookRuntimeSnapshot, error) {
	return b.client.NotebookRuntime(ctx, id)
}

func (b clientNotebookBackend) PrepareChangeSet(ctx context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangePlan, error) {
	return b.client.PrepareNotebookChangeSet(ctx, id, change)
}

func (b clientNotebookBackend) ApplyChangeSet(ctx context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangeApplyResult, error) {
	return b.client.ApplyNotebookChangeSet(ctx, id, change)
}

func (b clientNotebookBackend) CheckVisualization(ctx context.Context, id string, request service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, error) {
	return b.client.CheckNotebookVisualization(ctx, id, request)
}

func (b clientNotebookBackend) Run(ctx context.Context, id string, request service.RunNotebookRequest) (service.RunNotebookResult, error) {
	return b.client.RunNotebook(ctx, id, request)
}

func (b clientNotebookBackend) Cancel(ctx context.Context, id string) error {
	return b.client.CancelNotebookRun(ctx, id)
}

type embeddedNotebookBackend struct{ server *webServer }

func (b embeddedNotebookBackend) Workspace(ctx context.Context) (model.WorkspaceState, error) {
	return b.server.workspaceSvc.ComputeState(ctx)
}

func (b embeddedNotebookBackend) Notebook(_ context.Context, id string) (model.Notebook, error) {
	result, apiErr := b.server.notebookSvc.Get(id)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) Runtime(_ context.Context, id string) (service.NotebookRuntimeSnapshot, error) {
	result, apiErr := b.server.notebookSvc.Runtime(id)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) PrepareChangeSet(_ context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangePlan, error) {
	result, apiErr := b.server.notebookSvc.PrepareChangeSet(id, change)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) ApplyChangeSet(_ context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangeApplyResult, error) {
	result, apiErr := b.server.notebookSvc.ApplyChangeSet(id, change)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) CheckVisualization(ctx context.Context, id string, request service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, error) {
	result, apiErr := b.server.notebookSvc.CheckVisualization(ctx, id, request)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) Run(ctx context.Context, id string, request service.RunNotebookRequest) (service.RunNotebookResult, error) {
	result, apiErr := b.server.notebookSvc.Run(ctx, id, request)
	return result, notebookAPIError(apiErr)
}

func (b embeddedNotebookBackend) Cancel(ctx context.Context, id string) error {
	return notebookAPIError(b.server.notebookSvc.CancelRuns(ctx, id))
}

func notebookAPIError(apiErr *service.APIError) error {
	if apiErr == nil {
		return nil
	}
	return apiErr
}
