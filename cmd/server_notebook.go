package cmd

import (
	"context"
	"os"

	"github.com/bruin-data/bruin/pkg/config"
	"go.uber.org/zap"

	"renart/internal/web/secretstore"
	"renart/internal/web/service"
)

// configureNotebookServices keeps authored-document, runtime, warehouse, and
// agent adapters together at the composition root. Authored notebook locking,
// mutation, CAS, and recovery are owned by internal/web/notebookdoc; the broad
// service remains the compatibility facade for runtime and integration flows.
func configureNotebookServices(ctx context.Context, server *webServer, cfg serverConfig, logger *zap.Logger) {
	workspaceRoot := cfg.workspaceRoot
	server.notebookSvc = service.NewNotebookService(service.NotebookDependencies{
		WorkspaceRoot:           workspaceRoot,
		ConfigPath:              resolveConfigFilePath(workspaceRoot),
		DisableFilesystemAccess: cfg.disableFilesystemAccess,
		SnapshotMaxBytes:        cfg.notebookSnapshotMaxBytes,
		SnapshotTimeout:         cfg.notebookSnapshotTimeout,
		CurrentState:            func() service.WorkspaceState { return server.currentState() },
		NewConnectionManager: func(ctx context.Context, environment string) (config.ConnectionAndDetailsGetter, error) {
			return server.newConnectionManager(
				secretstore.WithPurpose(ctx, secretstore.PurposeNotebookQuery),
				environment,
			)
		},
		PushWorkspaceUpdate: server.pushWorkspaceUpdate,
		// Validate cells for server-side auto-recompute with the same
		// parse-context the editor uses. The callback is resolved lazily after
		// parseContextSvc is assembled later in newWebServer.
		ValidateSQL: func(ctx context.Context, assetID, content string, schemaTables []service.ParseContextSchemaTable) (service.ParseContextResult, *service.APIError) {
			return server.parseContextSvc.Parse(ctx, assetID, content, schemaTables, "")
		},
		PublishEvent: func(payload any) { server.hub.PublishImmediate(payload) },
	})

	renartExecutable, executableErr := os.Executable()
	if executableErr != nil {
		logger.Warn("notebook agent executable discovery failed", zap.Error(executableErr))
	}
	server.notebookAgentSvc = service.NewNotebookAgentService(ctx, service.NotebookAgentDependencies{
		WorkspaceRoot:    workspaceRoot,
		RenartExecutable: renartExecutable,
		ValidateNotebook: func(notebookID string) *service.APIError {
			_, apiErr := server.notebookSvc.Get(notebookID)
			return apiErr
		},
		ResolveReferences: func(notebookID string, references []service.NotebookAgentReferenceRequest) ([]service.NotebookAgentReference, *service.APIError) {
			notebook, apiErr := server.notebookSvc.Get(notebookID)
			if apiErr != nil {
				return nil, apiErr
			}
			return service.ResolveNotebookAgentReferences(notebook, server.currentState(), references)
		},
		PublishEvent: func(payload any) { server.hub.PublishImmediate(payload) },
	})
}
