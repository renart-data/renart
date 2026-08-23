package cmd

import (
	"context"

	"github.com/bruin-data/bruin/pkg/config"
	"renart/internal/web/secretstore"
	"renart/internal/web/service"
)

// configurePresentationService keeps presentation-specific adapters together
// at the composition root. The service facade preserves route compatibility;
// document and runtime ownership live in internal/web/presentation.
func configurePresentationService(server *webServer, workspaceRoot string) {
	server.presentationSvc = service.NewPresentationService(service.PresentationDependencies{
		WorkspaceRoot:    workspaceRoot,
		ConfigPath:       resolveConfigFilePath(workspaceRoot),
		CurrentState:     func() service.WorkspaceState { return server.currentState() },
		ResolveAssetByID: server.resolveAssetByID,
		NewConnectionManager: func(ctx context.Context, environment string) (config.ConnectionAndDetailsGetter, error) {
			return server.newConnectionManager(secretstore.WithPurpose(ctx, secretstore.PurposeQuery), environment)
		},
		RunConnectionQuery: func(
			ctx context.Context,
			connection string,
			environment string,
			query string,
		) ([]string, []map[string]any, error) {
			return server.executionSvc.RunConnectionQueryForEnvironment(
				secretstore.WithPurpose(ctx, secretstore.PurposeQuery),
				connection,
				environment,
				query,
			)
		},
		PushWorkspaceUpdate: server.pushWorkspaceUpdate,
	})
}
