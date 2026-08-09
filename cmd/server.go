package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/pipeline"
	gogit "github.com/go-git/go-git/v5"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v3"
	"renart/internal/sqlformat"
	"renart/internal/web/bus"
	"renart/internal/web/completion"
	"renart/internal/web/events"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	webretention "renart/internal/web/retention"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/secretstore"
	"renart/internal/web/service"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"
	webstatic "renart/internal/web/static"
	"renart/internal/web/watch"
	webui "renart/web"

	"go.uber.org/zap"
)

// serverConfig holds the validated settings shared by the web and
// standalone entry points.
type serverConfig struct {
	workspaceRoot           string
	staticDir               string
	watchMode               string
	watchPoll               time.Duration
	schedulerEnabled        bool
	schedulerStatePath      string
	disableFilesystemAccess bool
	// bootstrapRoot is a temporary Git-backed workspace used only when the
	// no-argument launcher starts outside a project. It lets the welcome UI
	// load without treating the launch directory as a project or writing
	// Renart state into it.
	bootstrapRoot            string
	suggestedCreateParentDir string
	// headless is the embedded-CLI mode (plans/cli-v1.md §2.4): the same
	// service graph without the pieces only a long-lived UI server needs —
	// no static assets, no filesystem watcher, no fingerprint pre-warm (and
	// callers keep the scheduler off so two River schedulers never share a
	// state DB).
	headless bool
}

// serverFlags returns the CLI flags shared by every mode that boots the
// Renart server. Callers get fresh flag instances so commands do not share
// flag state.
func serverFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "static-dir",
			Value: "web/dist",
			Usage: "optional override directory for static web assets",
		},
		&cli.StringFlag{
			Name:  "watch-mode",
			Value: "auto",
			Usage: "workspace watcher mode: auto, fsnotify, or poll",
		},
		&cli.DurationFlag{
			Name:  "watch-poll-interval",
			Value: 2 * time.Second,
			Usage: "poll interval used when watch-mode is poll or auto",
		},
		&cli.BoolFlag{
			Name:  "scheduler",
			Value: true,
			Usage: "run the local in-process scheduler",
		},
		&cli.StringFlag{
			Name:  "scheduler-state",
			Value: ".renart/state.db",
			Usage: "local scheduler SQLite state path",
		},
		&cli.BoolFlag{
			Name:  "enable-filesystem-access",
			Value: true,
			Usage: "allow DuckDB connections and SQL analysis to access local files",
		},
	}
}

// serverConfigFromCommand resolves and validates the shared server settings
// from CLI flags and the positional workspace root argument.
func serverConfigFromCommand(c *cli.Command) (serverConfig, error) {
	rootArg := strings.TrimSpace(c.Args().Get(0))
	root := rootArg
	if root == "" {
		root = "."
	}

	launchRoot, err := filepath.Abs(root)
	if err != nil {
		return serverConfig{}, fmt.Errorf("failed to resolve workspace root: %w", err)
	}

	staticDir := c.String("static-dir")
	if !filepath.IsAbs(staticDir) {
		staticDir = filepath.Join(launchRoot, staticDir)
	}

	watchMode := strings.ToLower(strings.TrimSpace(c.String("watch-mode")))
	if watchMode == "" {
		watchMode = "auto"
	}
	if watchMode != "auto" && watchMode != "fsnotify" && watchMode != "poll" {
		return serverConfig{}, fmt.Errorf("invalid watch-mode %q, expected one of: auto, fsnotify, poll", watchMode)
	}

	watchPoll := c.Duration("watch-poll-interval")
	if watchPoll <= 0 {
		return serverConfig{}, fmt.Errorf("watch-poll-interval must be greater than zero")
	}

	workspaceRoot, bootstrapRoot, err := resolveServerWorkspaceRoot(launchRoot, rootArg == "")
	if err != nil {
		return serverConfig{}, err
	}

	statePath := c.String("scheduler-state")
	if !filepath.IsAbs(statePath) {
		statePath = filepath.Join(workspaceRoot, statePath)
	}

	return serverConfig{
		workspaceRoot:            workspaceRoot,
		staticDir:                staticDir,
		watchMode:                watchMode,
		watchPoll:                watchPoll,
		schedulerEnabled:         c.Bool("scheduler"),
		schedulerStatePath:       statePath,
		disableFilesystemAccess:  !c.Bool("enable-filesystem-access"),
		bootstrapRoot:            bootstrapRoot,
		suggestedCreateParentDir: launchRoot,
	}, nil
}

// resolveServerWorkspaceRoot preserves explicit project validation while
// allowing the no-argument desktop/browser launcher to open from an arbitrary
// working directory. The temporary workspace is a real Git repository, so the
// per-project runtime keeps the same invariants as every user project.
func resolveServerWorkspaceRoot(launchRoot string, implicit bool) (string, string, error) {
	if _, err := git.FindRepoFromPath(launchRoot); err == nil {
		return launchRoot, "", nil
	} else if !implicit || !errors.Is(err, git.ErrNoGitRepoFound) {
		return "", "", fmt.Errorf("renart projects must live inside a git repository: %w", err)
	}

	bootstrapRoot, err := os.MkdirTemp("", "renart-welcome-")
	if err != nil {
		return "", "", fmt.Errorf("failed to prepare the welcome workspace: %w", err)
	}
	if _, err := gogit.PlainInit(bootstrapRoot, false); err != nil {
		_ = os.RemoveAll(bootstrapRoot)
		return "", "", fmt.Errorf("failed to initialize the welcome workspace: %w", err)
	}
	return bootstrapRoot, bootstrapRoot, nil
}

func cleanupServerBootstrap(cfg serverConfig) {
	if cfg.bootstrapRoot != "" {
		_ = os.RemoveAll(cfg.bootstrapRoot)
	}
}

// newWebServer wires all services, starts the scheduler (when enabled) and
// the filesystem watcher, and performs the initial workspace parse. The
// returned cleanup stops the scheduler and closes its store; the watcher
// stops when ctx is cancelled.
func newWebServer(ctx context.Context, cfg serverConfig, logger *zap.Logger) (*webServer, func(), error) {
	absRoot := cfg.workspaceRoot
	processStartedAt := time.Now().UTC()
	serverLease, err := acquireRuntimeWorkspaceLease(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	releaseLeaseOnError := serverLease != nil
	defer func() {
		if releaseLeaseOnError {
			_ = serverLease.Close()
		}
	}()
	if err := service.EnsureRuntimeGitExcludes(absRoot); err != nil {
		// Source-control hygiene must not make an otherwise writable workspace
		// impossible to serve (for example when Git metadata is mounted
		// read-only). This also covers embedded CLI runs, which open state.db even
		// though they intentionally skip the long-lived workspace lease.
		logger.Warn("failed to exclude Renart runtime files from Git status", zap.Error(err))
	}
	executionCoordinator, err := newWorkspaceExecutionCoordinator(absRoot)
	if err != nil {
		return nil, nil, err
	}

	// Every served workspace gets a stable identity; the health endpoint and
	// discovery file report it so CLI clients can address the right project.
	project, err := identity.EnsureProject(
		afero.NewOsFs(),
		filepath.Join(absRoot, ".renart", "project.yml"),
		filepath.Base(absRoot),
	)
	if err != nil {
		return nil, nil, err
	}
	if _, err := identity.NormalizeRetentionSettings(project.Retention); err != nil {
		return nil, nil, fmt.Errorf("invalid retention settings in .renart/project.yml: %w", err)
	}

	secretVault := secretstore.NewLocalVaultProvider("")
	secretResolver := secretstore.NewDefaultResolverWithLocalVault(secretVault)
	configPath := resolveConfigFilePath(absRoot)
	server := &webServer{
		projectID:     project.ID,
		projectName:   project.Name,
		workspaceRoot: absRoot,
		staticDir:     cfg.staticDir,
		watchMode:     cfg.watchMode,
		watchPoll:     cfg.watchPoll,
		workspaceSvc:  service.NewWorkspaceService(absRoot, configPath),
		configSvc: service.NewConfigService(
			absRoot,
			configPath,
			service.WithSecretResolver(secretResolver),
			service.WithSecretVault(secretVault),
		),
		secretVault: secretVault,
		pipelineSvc: service.NewPipelineService(absRoot),
		hub:         events.NewDebouncedHub(150 * time.Millisecond),
		executor:    nil,
		eventBus:    bus.New(),
		logger:      logger,
	}
	server.connectionFactory = service.NewResolvedConnectionFactory(
		absRoot,
		configPath,
		project.ID,
		secretResolver,
		service.WithDuckDBFilesystemAccess(!cfg.disableFilesystemAccess),
	)

	hybridExecutor := service.NewHybridBruinExecutor(absRoot, "", server.newConnectionManager, server.newPipelineBuilder)
	hybridExecutor.SetDuckDBFilesystemAccess(!cfg.disableFilesystemAccess)
	server.executor = hybridExecutor
	server.pipelineSvc.SetExternalRelationImporter(hybridExecutor)
	server.policyLoader = policy.NewLoader(filepath.Join(absRoot, ".renart", "environments.yml"))

	server.executionSvc = service.NewExecutionService(service.ExecutionDependencies{
		WorkspaceRoot:        absRoot,
		ConfigPath:           resolveConfigFilePath(absRoot),
		Executor:             server.executor,
		ResolveAssetByID:     server.resolveAssetByID,
		ResolveAssetNameByID: server.findAssetNameByID,
		FindInspectIDs:       server.findMaterializationInspectIDs,
		CurrentPipelines: func() []service.PipelineView {
			state := server.currentState()
			pipelines := make([]service.PipelineView, 0, len(state.Pipelines))
			for _, pipeline := range state.Pipelines {
				assets := make([]service.AssetView, 0, len(pipeline.Assets))
				for _, asset := range pipeline.Assets {
					qualityCheckCount := len(asset.CustomChecks)
					for _, column := range asset.Columns {
						qualityCheckCount += len(column.Checks)
					}
					assets = append(assets, service.AssetView{
						ID: asset.ID, Name: asset.Name, QualityCheckCount: qualityCheckCount,
					})
				}
				pipelines = append(pipelines, service.PipelineView{ID: pipeline.ID, UUID: pipeline.UUID, Name: pipeline.Name, Assets: assets})
			}
			return pipelines
		},
		Events:       server.eventBus,
		TargetWrites: serverTargetWriteStore{server: server},
		DispatchCompletion: func(ctx context.Context, event bus.RunCompleted) error {
			return server.dispatchRunCompletion(ctx, event)
		},
		AcquireExecutionLease: executionCoordinator.AcquireShared,
		PolicyFor: func(environment string) policy.EnvironmentPolicy {
			if strings.TrimSpace(environment) == "" {
				environment = server.currentState().SelectedEnvironment
			}
			return server.policyLoader.For(environment)
		},
		SelectedEnvironment: func() string { return server.currentState().SelectedEnvironment },
		ParseQueryOutput:    service.ParseQueryJSONOutput,
		NewPipelineBuilder:  server.newPipelineBuilder,
	})

	server.assetSvc = service.NewAssetService(service.AssetDependencies{
		Fs:                           afero.NewOsFs(),
		WorkspaceRoot:                absRoot,
		ConfigPath:                   resolveConfigFilePath(absRoot),
		Executor:                     server.executor,
		ResolveAssetByID:             server.resolveAssetByID,
		DefaultAssetContent:          defaultAssetContent,
		DerivedAssetContent:          defaultDerivedSQLAssetContent,
		EnsurePythonProject:          ensurePythonProjectFile,
		SuppressWatcher:              server.suppressWatcherFor,
		PushWorkspaceUpdateImmediate: server.pushWorkspaceUpdateImmediate,
		PushWorkspaceUpdateImmediateWithChangedIDs: server.pushWorkspaceUpdateImmediateWithChangedIDs,
		PushAssetContentUpdateImmediate:            server.pushAssetContentUpdateImmediate,
		ConnectionTypeFor: func(connectionName string) string {
			return server.currentState().Connections[connectionName]
		},
		SelectedEnvironment: func() string { return server.currentState().SelectedEnvironment },
		CurrentState:        func() service.WorkspaceState { return server.currentState() },
		MaterializedSchemaFresh: func(ctx context.Context, assetID, assetName, environment string) (bool, error) {
			if server.stalenessSvc == nil {
				return false, nil
			}
			state := server.currentState()
			if strings.TrimSpace(environment) == "" {
				environment = state.SelectedEnvironment
			}
			for _, candidate := range state.Pipelines {
				found := false
				for _, candidateAsset := range candidate.Assets {
					if candidateAsset.ID == assetID {
						found = true
						break
					}
				}
				if !found || strings.TrimSpace(candidate.UUID) == "" {
					continue
				}
				statuses, err := server.stalenessSvc.Statuses(ctx, staleness.Selection{
					PipelineUUID:      candidate.UUID,
					EncodedPipelineID: candidate.ID,
					Environment:       environment,
				})
				if err != nil {
					return false, err
				}
				for _, status := range statuses {
					if status.AssetName == assetName {
						return status.Status == staleness.StatusFresh, nil
					}
				}
				return false, nil
			}
			return false, nil
		},
	})

	server.sqlSvc = service.NewSQLService(service.SQLDependencies{
		Executor:             server.executor,
		NewConnectionManager: server.newConnectionManager,
		RunConnectionQuery:   server.executionSvc.RunConnectionQueryForEnvironment,
	})
	remoteCatalog := service.NewRemoteCatalogCache(service.RemoteCatalogDependencies{
		DiscoverDatabases: func(ctx context.Context, connection, environment string) ([]string, error) {
			result, apiErr := server.sqlSvc.Databases(ctx, connection, environment)
			if apiErr != nil {
				return nil, errors.New(apiErr.Message)
			}
			return result.Databases, nil
		},
		DiscoverTables: func(ctx context.Context, connection, database, environment string) ([]service.SQLDiscoveryTableItem, error) {
			result, apiErr := server.sqlSvc.Tables(ctx, connection, database, environment)
			if apiErr != nil {
				return nil, errors.New(apiErr.Message)
			}
			return result.Tables, nil
		},
		DiscoverColumns: func(ctx context.Context, connection, table, environment string) ([]service.SQLColumn, error) {
			result, status := server.sqlSvc.TableColumns(ctx, connection, table, environment)
			if status < http.StatusOK || status >= http.StatusMultipleChoices || result.Status != "ok" {
				if strings.TrimSpace(result.Error) != "" {
					return nil, errors.New(result.Error)
				}
				return nil, fmt.Errorf("column discovery failed with status %d", status)
			}
			return result.Columns, nil
		},
		PublishReady: func(event service.RemoteCatalogReadyEvent) {
			server.hub.PublishImmediate(event)
		},
	})
	server.sqlSvc.SetRemoteCatalogObserver(remoteCatalog)
	server.pipelineSvc.SetRemoteCatalogProvider(
		remoteCatalog,
		func() string { return server.currentState().SelectedEnvironment },
	)

	server.loadSvc = service.NewLoadService(service.LoadDependencies{
		WorkspaceRoot:        absRoot,
		NewConnectionManager: server.newConnectionManager,
	})

	server.notebookSvc = service.NewNotebookService(service.NotebookDependencies{
		WorkspaceRoot:           absRoot,
		ConfigPath:              resolveConfigFilePath(absRoot),
		DisableFilesystemAccess: cfg.disableFilesystemAccess,
		CurrentState:            func() service.WorkspaceState { return server.currentState() },
		RunConnectionQuery: func(
			ctx context.Context,
			connection string,
			environment string,
			query string,
		) ([]string, []map[string]any, error) {
			return server.executionSvc.RunConnectionQueryForEnvironment(
				secretstore.WithPurpose(ctx, secretstore.PurposeNotebookQuery),
				connection,
				environment,
				query,
			)
		},
		PushWorkspaceUpdate: server.pushWorkspaceUpdate,
		// Validate cells for server-side auto-recompute with the same
		// parse-context the editor uses (constructed below; referenced lazily).
		ValidateSQL: func(ctx context.Context, assetID, content string, schemaTables []service.ParseContextSchemaTable) (service.ParseContextResult, *service.APIError) {
			return server.parseContextSvc.Parse(ctx, assetID, content, schemaTables, "")
		},
		PublishEvent: func(payload any) { server.hub.PublishImmediate(payload) },
	})

	server.suggestionsSvc = service.NewSuggestionsService(service.SuggestionsDependencies{
		WorkspaceRoot:           absRoot,
		ConfigPath:              resolveConfigFilePath(absRoot),
		DisableFilesystemAccess: cfg.disableFilesystemAccess,
		ResolveAssetByID: func(ctx context.Context, assetID string) (string, any, any, error) {
			path, parsed, asset, err := server.resolveAssetByID(ctx, assetID)
			return path, parsed, asset, err
		},
		NewConnectionManager: server.newConnectionManager,
	})

	server.parseContextSvc = service.NewParseContextService(service.ParseContextDependencies{
		ResolveAssetByID: server.resolveAssetByID,
		CurrentState: func() service.WorkspaceState {
			return server.currentState()
		},
	})
	server.sqlLSPSvc = service.NewSQLLSPService(service.SQLLSPDependencies{
		WorkspaceRoot:           absRoot,
		DisableFilesystemAccess: cfg.disableFilesystemAccess,
		ResolveAssetByID:        server.resolveAssetByID,
		CurrentState: func() service.WorkspaceState {
			return server.currentState()
		},
		RemoteCatalog:  remoteCatalog,
		PolyglotClient: service.NewLazyPolyglotClient(),
	})
	sqlformat.PrewarmPolyglotCompiler()
	server.jinjaRenderSvc = service.NewJinjaRenderService(service.JinjaRenderDependencies{
		ResolveAssetByID: server.resolveAssetByID,
	})
	server.assetRenderSvc = service.NewAssetRenderService(absRoot)

	server.runSvc = service.NewRunService(service.RunDependencies{
		Executor:  server.executor,
		Execution: server.executionSvc,
		CurrentPipelineIDs: func() []string {
			pipelines := server.currentState().Pipelines
			pipelineIDs := make([]string, 0, len(pipelines))
			for _, currentPipeline := range pipelines {
				pipelineIDs = append(pipelineIDs, currentPipeline.ID)
			}
			return pipelineIDs
		},
		WorkspaceRoot:       absRoot,
		ConfigPath:          resolveConfigFilePath(absRoot),
		PolicyFor:           server.policyLoader.For,
		SelectedEnvironment: func() string { return server.currentState().SelectedEnvironment },
	})
	server.onboardingSvc = service.NewOnboardingService(absRoot, resolveConfigFilePath(absRoot), server.executor, server.executionSvc)
	server.sourceControlSvc = service.NewSourceControlService(absRoot)

	server.schedulerStore, err = webscheduler.OpenStore(cfg.schedulerStatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize scheduler store: %w", err)
	}
	server.fingerprintEngine = fingerprint.NewEngine()
	hybridExecutor.SetFingerprintEngine(server.fingerprintEngine)
	hybridExecutor.SetDependencyGraphResolver(server.resolveWorkspaceDependencyGraph)
	server.matlogStore = matlog.NewStore(server.schedulerStore.DB())
	server.completionStore = completion.NewStore(server.schedulerStore.DB())
	server.snapshotStore = snapshot.NewStore(server.schedulerStore.DB())
	shouldReconcileClaims := serverLease != nil
	var releaseClaimRecovery func() error
	if shouldReconcileClaims {
		releaseClaimRecovery, err = executionCoordinator.acquireExclusive(ctx)
	} else {
		var acquired bool
		releaseClaimRecovery, acquired, err = executionCoordinator.tryAcquireExclusive()
		shouldReconcileClaims = acquired
	}
	if err != nil {
		server.schedulerStore.Close()
		return nil, nil, fmt.Errorf("acquire physical target recovery lease: %w", err)
	}
	if shouldReconcileClaims {
		converted, claimErr := server.matlogStore.MarkActiveTargetWriteClaimsDirty(ctx, time.Now().UTC())
		releaseErr := releaseClaimRecovery()
		if claimErr != nil || releaseErr != nil {
			server.schedulerStore.Close()
			return nil, nil, fmt.Errorf("mark interrupted physical target writes uncertain: %w", errors.Join(claimErr, releaseErr))
		}
		if converted > 0 {
			logger.Warn("marked interrupted physical target writes uncertain", zap.Int64("targets", converted))
		}
	}
	recorder := matlog.NewRecorder(server.matlogStore, server.fingerprintEngine, server.resolvePipelineByUUID, server.parsePipelineDir, logger)
	// Subscription order matters: the recorder writes coverage before any
	// later subscriber (the staleness service) re-reads it.
	server.eventBus.OnRunCompleted(recorder.HandleRunCompleted)
	server.eventBus.OnAssetSaved(func(event bus.AssetSaved) {
		server.fingerprintEngine.Invalidate(event.AssetID)
	})
	if serverLease != nil {
		if replayErr := server.replayPendingCompletions(ctx); replayErr != nil {
			logger.Warn("failed to replay pending run completions", zap.Error(replayErr))
		}
	}

	server.stalenessSvc = staleness.New(staleness.Dependencies{
		Store:   server.matlogStore,
		Engine:  server.fingerprintEngine,
		Resolve: server.resolvePipelineByUUID,
		ResolveTargets: func(_ context.Context, selection staleness.Selection, parsed *pipeline.Pipeline) (map[string]staleness.PhysicalTarget, error) {
			resolved, resolveErr := service.ResolvePipelinePhysicalTargets(
				absRoot,
				resolveConfigFilePath(absRoot),
				selection.Environment,
				parsed,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			targets := make(map[string]staleness.PhysicalTarget, len(resolved))
			for assetID, target := range resolved {
				targets[assetID] = staleness.PhysicalTarget{
					Identity: target.Identity,
					Exact:    target.Fidelity == service.AssetRenderFidelityExact,
				}
			}
			return targets, nil
		},
		ResolveGraph: server.resolveWorkspaceDependencyGraph,
		Publish: func(event any) {
			server.hub.PublishImmediate(event)
		},
		Verify: server.verifyMaterializedAssets,
		Logger: logger,
	})
	server.stalenessSvc.AttachBus(server.eventBus)
	server.pipelinePlanSvc = service.NewPipelinePlanService(service.PipelinePlanDependencies{
		WorkspaceRoot:       absRoot,
		ConfigPath:          resolveConfigFilePath(absRoot),
		Snapshots:           server.snapshotStore,
		Staleness:           server.stalenessSvc,
		DependencyGraph:     server.resolveWorkspaceDependencyGraph,
		Fingerprints:        server.fingerprintEngine,
		Materializations:    server.matlogStore,
		ResolvePipelineUUID: server.findPipelineUUIDByID,
		PolicyFor:           server.policyLoader.For,
		ActiveRunID:         server.schedulerStore.ActiveRunID,
		ConflictingRunID: func(ctx context.Context, pipelineID, pipelineUUID string, resources service.PipelinePlanResources) (string, error) {
			claims := make([]webscheduler.PipelineRunResourceClaim, 0, len(resources.Claims))
			for _, claim := range resources.Claims {
				claims = append(claims, webscheduler.PipelineRunResourceClaim{
					Kind: claim.Kind, Identity: claim.Identity,
				})
			}
			return server.schedulerStore.ConflictingRunID(ctx, pipelineID, pipelineUUID, webscheduler.PipelineRunPlanResources{
				Isolation: resources.Isolation, Claims: claims,
			})
		},
		NewPipelineBuilder: func() *pipeline.Builder {
			return service.NewRenartPipelineBuilder(afero.NewOsFs())
		},
	})

	server.schedulerSvc = webscheduler.New(webscheduler.Options{
		Store:                server.schedulerStore,
		StateDir:             filepath.Dir(cfg.schedulerStatePath),
		Pipelines:            server.pipelineSvc.ListSchedules,
		ScheduleDeclarations: webscheduler.NewScheduleDeclarationStore(filepath.Join(absRoot, ".renart", "schedules.yml")),
		ResolveScheduleSecrets: func(
			ctx context.Context,
			environment string,
			references map[string]string,
		) (map[string]any, error) {
			requests := make([]secretstore.NamedRequest, 0, len(references))
			for variable, rawReference := range references {
				reference, err := secretstore.ParseRef(rawReference)
				if err != nil {
					return nil, fmt.Errorf("schedule variable %q: %w", variable, err)
				}
				requests = append(requests, secretstore.NamedRequest{
					Name: variable,
					Request: secretstore.ResolveRequest{
						ProjectID:   project.ID,
						Environment: environment,
						Reference:   reference,
						Purpose: secretstore.PurposeFromContext(
							ctx,
							secretstore.PurposeScheduleValidation,
						),
					},
				})
			}
			bundle, err := secretResolver.ResolveAll(ctx, requests)
			if err != nil {
				return nil, err
			}
			defer bundle.Close(ctx)
			resolved := make(map[string]any, len(references))
			for variable := range references {
				resolved[variable] = string(bundle.Value(variable))
			}
			return resolved, nil
		},
		Publish: func(event any) {
			server.hub.PublishImmediate(event)
		},
		Housekeeping: func(ctx context.Context) error {
			if replayErr := server.replayPendingCompletions(ctx); replayErr != nil {
				return replayErr
			}
			project, err := identity.LoadProject(
				afero.NewOsFs(),
				filepath.Join(absRoot, ".renart", "project.yml"),
			)
			if err != nil {
				return fmt.Errorf("load retention settings: %w", err)
			}
			retentionSettings, err := identity.NormalizeRetentionSettings(project.Retention)
			if err != nil {
				return fmt.Errorf("validate retention settings: %w", err)
			}
			now := time.Now().UTC()
			historyResult, err := server.schedulerStore.PruneHistory(ctx, webscheduler.HistoryRetentionPolicy{
				Runs: webscheduler.RetentionWindow{
					OlderThan:          now.AddDate(0, 0, -retentionSettings.RunMetadata.Days),
					MinimumPerPipeline: retentionSettings.RunMetadata.MinimumPerPipeline,
				},
				Logs: webscheduler.RetentionWindow{
					OlderThan:          now.AddDate(0, 0, -retentionSettings.FullLogs.Days),
					MinimumPerPipeline: retentionSettings.FullLogs.MinimumPerPipeline,
				},
				ScheduleHistoryBefore: now.AddDate(0, 0, -retentionSettings.ScheduleHistoryDays),
			})
			if err != nil {
				return err
			}
			materializationFacts, err := server.matlogStore.Prune(
				ctx,
				now.AddDate(0, 0, -retentionSettings.MaterializationFactsDays),
			)
			if err != nil {
				return fmt.Errorf("prune materialization facts: %w", err)
			}
			snapshotResult, err := server.snapshotStore.Prune(ctx, snapshot.RetentionPolicy{
				OlderThan:          now.AddDate(0, 0, -retentionSettings.Deployments.Days),
				MinimumPerPipeline: retentionSettings.Deployments.MinimumPerPipeline,
			})
			if err != nil {
				return err
			}
			temporaryDirectories, err := webretention.CleanupTemporaryDirectories(
				os.TempDir(),
				now.Add(-time.Duration(retentionSettings.TemporaryDirectoriesHours)*time.Hour),
				processStartedAt,
			)
			if err != nil {
				return err
			}
			if historyResult.RunLogs > 0 || historyResult.Runs > 0 ||
				historyResult.ScheduleOccurrences > 0 || historyResult.ArchivedSchedules > 0 ||
				materializationFacts > 0 || snapshotResult.Snapshots > 0 ||
				snapshotResult.Blobs > 0 || temporaryDirectories > 0 {
				logger.Info("completed local history retention",
					zap.Int64("run_logs", historyResult.RunLogs),
					zap.Int64("runs", historyResult.Runs),
					zap.Int64("schedule_occurrences", historyResult.ScheduleOccurrences),
					zap.Int64("archived_schedules", historyResult.ArchivedSchedules),
					zap.Int64("materialization_facts", materializationFacts),
					zap.Int64("snapshots", snapshotResult.Snapshots),
					zap.Int64("snapshot_blobs", snapshotResult.Blobs),
					zap.Int("temporary_directories", temporaryDirectories),
				)
			}
			return nil
		},
		ResolvePipelineRef: func(ctx context.Context, pipelineUUID string) (webscheduler.PipelineRef, bool) {
			for _, p := range server.currentState().Pipelines {
				if p.UUID == pipelineUUID {
					return webscheduler.PipelineRef{EncodedID: p.ID, Name: p.Name}, true
				}
			}
			return webscheduler.PipelineRef{}, false
		},
		DefaultEnvironment: func() string {
			return server.currentState().SelectedEnvironment
		},
		PipelineIntervalAware: func(ctx context.Context, pipelineUUID string) bool {
			parsed, err := server.resolvePipelineByUUID(ctx, pipelineUUID)
			if err != nil {
				return false
			}
			for _, asset := range parsed.Assets {
				if matlog.BackfillSafe(asset) {
					return true
				}
			}
			return false
		},
		DeployPipeline: func(ctx context.Context, pipelineUUID string) (string, error) {
			for _, p := range server.currentState().Pipelines {
				if p.UUID != pipelineUUID {
					continue
				}
				absPath, err := service.SafeJoin(absRoot, p.Path)
				if err != nil {
					return "", err
				}
				deployed, _, err := server.snapshotStore.Deploy(ctx, pipelineUUID, absPath, "schedule")
				if err != nil {
					return "", err
				}
				return deployed.VersionID, nil
			}
			return "", fmt.Errorf("pipeline %s not found in workspace", pipelineUUID)
		},
		LatestSnapshot: func(ctx context.Context, pipelineUUID string) (string, bool, error) {
			latest, err := server.snapshotStore.Latest(ctx, pipelineUUID)
			if err != nil || latest == nil {
				return "", false, err
			}
			return latest.VersionID, true, nil
		},
		ValidateSnapshot: func(ctx context.Context, pipelineUUID, versionID string) error {
			_, err := server.snapshotStore.Validate(ctx, versionID, pipelineUUID)
			return err
		},
		ValidateScheduleVariables: func(ctx context.Context, pipelineUUID, versionID string, overrides map[string]any) error {
			tempRoot, err := os.MkdirTemp("", "renart-schedule-vars-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tempRoot)
			pipelineRoot := filepath.Join(tempRoot, "pipeline")
			if err := os.MkdirAll(pipelineRoot, 0o755); err != nil {
				return err
			}
			if err := server.snapshotStore.MaterializeForPipelineExecution(
				ctx, versionID, pipelineUUID, pipelineRoot,
			); err != nil {
				return err
			}
			return service.ValidatePipelineVariableOverrides(
				ctx, service.NewRenartPipelineBuilder(afero.NewOsFs()), pipelineRoot, overrides,
			)
		},
		PlanScheduledRun: func(ctx context.Context, req webscheduler.ScheduledRunPlanRequest) (webscheduler.ScheduledRunPlanResult, error) {
			plan, apiErr := server.pipelinePlanSvc.Plan(ctx, req.PipelineID, service.PipelinePlanRequest{
				Purpose:       service.PipelinePlanPurposeExecution,
				Environment:   req.Environment,
				StartDate:     req.Start.UTC().Format(time.RFC3339Nano),
				EndDate:       req.End.UTC().Format(time.RFC3339Nano),
				ExecutionTime: req.ExecutionTime.UTC().Format(time.RFC3339Nano),
				Source: service.PipelinePlanSourceRequest{
					Kind: service.PipelinePlanSourceSnapshot, VersionID: req.SnapshotVersionID,
				},
				Selection:              service.PipelinePlanSelectionRequest{Mode: service.PipelinePlanSelectionAll},
				VariableOverrides:      req.VariableOverrides,
				VariableOverrideSource: "schedule_override",
				SkipActiveRunCheck:     true,
				SkipDataStateCheck:     true,
				Scheduled:              true,
			})
			if apiErr != nil {
				return webscheduler.ScheduledRunPlanResult{}, fmt.Errorf("%s: %s", apiErr.Code, apiErr.Message)
			}
			blockers := make([]string, 0, len(plan.Readiness.Blockers))
			for _, blocker := range plan.Readiness.Blockers {
				if message := strings.TrimSpace(blocker.Message); message != "" {
					blockers = append(blockers, message)
				}
			}
			if plan.Status == service.PipelinePlanStatusBlocked && len(blockers) == 0 {
				blockers = append(blockers, "the scheduled plan is not executable")
			}
			retainedPlan, err := scheduledPipelineRunPlan(plan, blockers)
			if err != nil {
				return webscheduler.ScheduledRunPlanResult{}, err
			}
			return webscheduler.ScheduledRunPlanResult{Plan: retainedPlan}, nil
		},
		ValidateReexecution: func(ctx context.Context, req webscheduler.RunReexecutionValidationRequest) error {
			environmentPolicy := policy.EnvironmentPolicy{}
			if server.policyLoader != nil {
				environmentPolicy = server.policyLoader.For(req.Environment)
			}
			if err := policy.Check(environmentPolicy, policy.RunRequest{
				Environment:          req.Environment,
				Interactive:          true,
				SnapshotBased:        req.Source == webscheduler.RunSourceSnapshot,
				Destructive:          req.FullRefresh || req.Backfill,
				ConfirmedEnvironment: req.ConfirmedEnvironment,
			}); err != nil {
				return err
			}

			pipelineID := req.PipelineID
			for _, current := range server.currentState().Pipelines {
				if current.UUID == req.PipelineUUID {
					pipelineID = current.ID
					break
				}
			}
			if req.Source == webscheduler.RunSourceWorkingTree && pipelineID == req.PipelineID {
				if currentUUID, ok := server.findPipelineUUIDByID(req.PipelineID); !ok || currentUUID != req.PipelineUUID {
					return fmt.Errorf("the original saved-workspace pipeline is no longer present")
				}
			}
			return server.pipelinePlanSvc.ValidateRetainedRunContext(ctx, service.RetainedRunContextValidationRequest{
				PipelineID:   pipelineID,
				PipelineUUID: req.PipelineUUID,
				Environment:  req.Environment,
				Source: service.PipelinePlanSourceRequest{
					Kind: string(req.Source), VersionID: req.SnapshotVersionID,
				},
				VariableOverrides:           req.VariableOverrides,
				ConfigurationAssetNames:     req.ConfigurationAssetNames,
				ExpectedSourceMerkle:        req.ExpectedSourceMerkle,
				ExpectedConfigurationDigest: req.ExpectedConfigurationDigest,
			})
		},
		SnapshotOrdinal: func(ctx context.Context, versionID string) (int64, error) {
			deployed, err := server.snapshotStore.Get(ctx, versionID)
			return deployed.Ordinal, err
		},
		CheckSnapshot: func(ctx context.Context, pipelineUUID, versionID string) error {
			_, err := server.snapshotStore.Validate(ctx, versionID, pipelineUUID)
			return err
		},
		RecoverRun: server.replayRecoveredRun,
		Runner: func(ctx context.Context, req webscheduler.RunRequest, onLog func(string)) webscheduler.RunResult {
			spec := pipelineRunSpecFromSchedulerRequest(req)
			spec.OnContextResolved = func(resolved service.ResolvedPipelineRunContext) error {
				if req.OnContextResolved == nil {
					return fmt.Errorf("scheduler run %s cannot persist resolved execution context", req.RunID)
				}
				return req.OnContextResolved(webscheduler.RunExecutionContext{
					Environment: resolved.Environment,
					WinStart:    resolved.WinStart,
					WinEnd:      resolved.WinEnd,
					FullRefresh: resolved.FullRefresh,
					Backfill:    resolved.Backfill,
					SensorMode:  resolved.SensorMode,
				})
			}
			spec.OnTargetsResolved = func(snapshot service.ExecutionTargetSnapshot) error {
				if req.OnTargetsResolved == nil {
					return fmt.Errorf("scheduler run %s cannot persist execution targets", req.RunID)
				}
				entries := make(map[string]webscheduler.ExecutionTargetSnapshotEntry, len(snapshot.Entries))
				for assetName, entry := range snapshot.Entries {
					upstreams := make([]webscheduler.ExecutionUpstreamSnapshot, 0, len(entry.Upstreams))
					for _, upstream := range entry.Upstreams {
						upstreams = append(upstreams, webscheduler.ExecutionUpstreamSnapshot{
							Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
							ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
							TargetIdentity: upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
							VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
							CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
						})
					}
					entries[assetName] = webscheduler.ExecutionTargetSnapshotEntry{
						AssetID:                     entry.AssetID,
						TargetIdentity:              entry.TargetIdentity,
						TargetFidelity:              string(entry.TargetFidelity),
						TargetWriteEvidenceRequired: entry.TargetWriteEvidenceRequired,
						WriteResourceKind:           entry.WriteResourceKind,
						WriteResourceIdentity:       entry.WriteResourceIdentity,
						WriteResourceFidelity:       string(entry.WriteResourceFidelity),
						ExecutionContract:           schedulerExecutionContractFromService(entry.ExecutionContract),
						Fingerprint:                 entry.Fingerprint,
						OwnContent:                  entry.OwnContent,
						ConsumedVarsHash:            entry.ConsumedVarsHash,
						VarsHash:                    entry.VarsHash,
						Upstreams:                   upstreams,
						CoverageMode:                string(entry.CoverageMode),
						RefreshRestricted:           entry.RefreshRestricted,
					}
				}
				return req.OnTargetsResolved(webscheduler.ExecutionTargetSnapshot{
					Version:               snapshot.Version,
					PipelineUUID:          snapshot.PipelineUUID,
					ConfigurationDigest:   snapshot.ConfigurationDigest,
					ConfigurationFidelity: snapshot.ConfigurationFidelity,
					Entries:               entries,
				})
			}
			if req.ConfirmedPlan == nil {
				spec.OnExecutionUnitsResolved = func(units []service.PipelineExecutionUnit) error {
					return persistSchedulerResolvedExecutionUnits(req, units)
				}
			}
			spec.OnUnit = func(event service.PipelineExecutionUnitEvent) error {
				if req.OnUnit == nil {
					return fmt.Errorf("scheduler run %s cannot persist execution unit", req.RunID)
				}
				return req.OnUnit(webscheduler.PipelineRunUnitEvent{
					Position: event.Position, Status: schedulerUnitStatusFromExecutionStatus(event.Status),
					StartedAt: event.StartedAt, FinishedAt: event.FinishedAt, Error: event.Error,
				})
			}
			cleanupSnapshot, err := server.resolveRunSnapshot(ctx, &spec, req.Scheduled, onLog)
			if err != nil {
				if onLog != nil {
					onLog("failed to prepare run source: " + err.Error() + "\n")
				}
				return webscheduler.RunResult{Status: "error", Error: err.Error()}
			}
			defer cleanupSnapshot()

			eventBridge := newSchedulerRunEventBridge(req)
			result := server.executionSvc.MaterializePipelineRun(ctx, spec, func(chunk []byte) {
				if onLog != nil {
					onLog(string(chunk))
				}
			}, eventBridge.Handle)
			// MaterializePipelineRun already forwards every output byte through
			// the chunk callback above. result.Output is the aggregate copy used
			// by non-streaming callers; appending it here would replay the run.
			return webscheduler.RunResult{Status: result.Status, Error: result.Error}
		},
	})
	server.executionSvc.SetInlineRunLedger(server.schedulerSvc)

	server.workspaceCoord = service.NewWorkspaceCoordinator(service.WorkspaceCoordinatorDependencies{
		WorkspaceService: server.workspaceSvc,
		Hub:              server.hub,
		Logger:           logger,
		Events:           server.eventBus,
	})

	if !cfg.headless {
		embeddedStaticFS, distErr := webui.DistFS()
		if distErr != nil {
			logger.Warn("embedded web assets unavailable, falling back to static dir", zap.Error(distErr))
			embeddedStaticFS = nil
		}

		server.staticHandler, err = webstatic.NewHandler(embeddedStaticFS, cfg.staticDir)
		if err != nil {
			server.schedulerStore.Close()
			return nil, nil, fmt.Errorf("failed to initialize static asset handler: %w", err)
		}
	}

	if err := server.refreshWorkspace(ctx); err != nil {
		logger.Warn("initial workspace parse failed", zap.Error(err))
	}

	// Notebook session DBs are disposable: remove files for notebooks that
	// no longer exist (covers kill -9 mid-session and deleted notebooks).
	if removed, err := server.notebookSvc.SweepSessions(); err != nil {
		logger.Warn("notebook session sweep failed", zap.Error(err))
	} else if len(removed) > 0 {
		logger.Info("removed stale notebook sessions", zap.Strings("notebooks", removed))
	}

	if !cfg.headless {
		// Warm the fingerprint engine's formatter cache off the request path:
		// the first SQL normalization per asset costs tens of milliseconds in
		// the wasm formatter, and the first staleness fetch should not pay it.
		go server.warmFingerprintCache(ctx)
	}

	if cfg.schedulerEnabled {
		if err := server.schedulerSvc.Start(ctx); err != nil {
			logger.Warn("failed to start local scheduler", zap.Error(err))
		}
	}

	if !cfg.headless {
		go watch.New(watch.Config{
			WorkspaceRoot: absRoot,
			Mode:          cfg.watchMode,
			PollInterval:  cfg.watchPoll,
		}, func(ctx context.Context, eventType, eventPath string) {
			if server.isWatcherSuppressed(eventPath) {
				return
			}
			server.pushWorkspaceUpdate(ctx, eventType, eventPath)
		}).Start(ctx)
	}

	cleanup := func() {
		server.secretVault.LockAll()
		server.schedulerSvc.Stop()
		server.schedulerStore.Close()
		if serverLease != nil {
			_ = serverLease.Close()
		}
	}

	releaseLeaseOnError = false
	return server, cleanup, nil
}

func pipelineRunSpecFromSchedulerRequest(req webscheduler.RunRequest) service.PipelineRunSpec {
	spec := service.PipelineRunSpec{
		RunID:                       req.RunID,
		PipelineID:                  req.PipelineID,
		PipelineUUID:                req.PipelineUUID,
		Environment:                 req.Environment,
		Scheduled:                   req.Scheduled,
		FullRefresh:                 req.FullRefresh,
		Backfill:                    req.Backfill,
		StartDate:                   req.Start,
		EndDate:                     req.End,
		ConfirmedEnvironment:        req.ConfirmedEnvironment,
		SensorMode:                  req.SensorMode,
		SnapshotVersionID:           req.SnapshotVersionID,
		ExecutionTime:               req.ExecutionTime,
		VariableOverrides:           req.VariableOverrides,
		ExpectedSourceMerkle:        req.ExpectedSourceMerkle,
		ExpectedConfigurationDigest: req.ExpectedConfigurationDigest,
	}
	if req.ConfirmedPlan != nil {
		units := make([]service.PipelineExecutionUnit, 0, len(req.ConfirmedPlan.ExecutionUnits))
		for position, unit := range req.ConfirmedPlan.ExecutionUnits {
			units = append(units, service.PipelineExecutionUnit{
				Position: position, AssetID: unit.AssetID, AssetName: unit.AssetName,
				StartDate: unit.StartDate, EndDate: unit.EndDate,
				RenderIndex: unit.RenderIndex, Reason: unit.Reason,
				DependencyPositions: append([]int(nil), unit.DependencyPositions...),
			})
		}
		prerequisites := make([]service.PipelinePlanPrerequisite, 0, len(req.ConfirmedPlan.Prerequisites))
		for _, prerequisite := range req.ConfirmedPlan.Prerequisites {
			prerequisites = append(prerequisites, servicePrerequisiteFromScheduler(prerequisite))
		}
		spec.Plan = &service.PipelineExecutionPlan{
			Version:        req.ConfirmedPlan.Version,
			SelectionMode:  req.ConfirmedPlan.Selection.Mode,
			MaxActiveSteps: req.ConfirmedPlan.MaxActiveSteps,
			Contracts:      serviceExecutionContractsFromScheduler(req.ConfirmedPlan.ExecutionContracts),
			Prerequisites:  prerequisites,
			Units:          units,
		}
	}
	return spec
}

func persistSchedulerResolvedExecutionUnits(
	req webscheduler.RunRequest,
	units []service.PipelineExecutionUnit,
) error {
	if len(units) == 0 {
		return nil
	}
	if req.OnExecutionUnitsResolved == nil {
		return fmt.Errorf("scheduler run %s cannot persist resolved execution units", req.RunID)
	}
	resolved := make([]webscheduler.PipelineRunExecutionUnit, 0, len(units))
	for position, unit := range units {
		if unit.Position != position {
			return fmt.Errorf(
				"scheduler run %s resolved unit %d has position %d",
				req.RunID,
				position,
				unit.Position,
			)
		}
		resolved = append(resolved, webscheduler.PipelineRunExecutionUnit{
			AssetID:             unit.AssetID,
			AssetName:           unit.AssetName,
			StartDate:           unit.StartDate,
			EndDate:             unit.EndDate,
			RenderIndex:         unit.RenderIndex,
			Reason:              unit.Reason,
			DependencyPositions: append([]int(nil), unit.DependencyPositions...),
		})
	}
	return req.OnExecutionUnitsResolved(resolved)
}

func scheduledPipelineRunPlan(plan service.PipelinePlan, blockers []string) (webscheduler.PipelineRunPlan, error) {
	artifact, err := json.Marshal(plan)
	if err != nil {
		return webscheduler.PipelineRunPlan{}, fmt.Errorf("encode scheduled pipeline plan: %w", err)
	}
	units := make([]webscheduler.PipelineRunExecutionUnit, 0, len(plan.ExecutionUnits))
	for _, unit := range plan.ExecutionUnits {
		units = append(units, webscheduler.PipelineRunExecutionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.StartDate, EndDate: unit.EndDate,
			RenderIndex: unit.RenderIndex, Reason: unit.Reason,
			DependencyPositions: append([]int(nil), unit.DependencyPositions...),
		})
	}
	claims := make([]webscheduler.PipelineRunResourceClaim, 0, len(plan.Resources.Claims))
	for _, claim := range plan.Resources.Claims {
		claims = append(claims, webscheduler.PipelineRunResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	contracts := make([]webscheduler.PipelineRunExecutionContract, 0, len(plan.ExecutionContracts))
	for _, contract := range plan.ExecutionContracts {
		contracts = append(contracts, schedulerExecutionContractFromService(contract))
	}
	prerequisites := make([]webscheduler.PipelineRunPrerequisite, 0, len(plan.Prerequisites))
	for _, prerequisite := range plan.Prerequisites {
		prerequisites = append(prerequisites, schedulerPrerequisiteFromService(prerequisite))
	}
	return webscheduler.PipelineRunPlan{
		Version: webscheduler.PipelineRunPlanVersionV3,
		PlanID:  plan.ID, PipelineID: plan.PipelineID, PipelineUUID: plan.PipelineUUID,
		SourceMerkle: plan.Source.MerkleRoot, ConfigurationDigest: plan.Context.ConfigurationDigest,
		ExecutionTime: plan.Context.ExecutionTime, MaxActiveSteps: plan.Context.MaxActiveSteps,
		Blocked:  plan.Status == service.PipelinePlanStatusBlocked,
		Blockers: append([]string(nil), blockers...),
		Selection: webscheduler.PipelineRunPlanSelection{
			Mode: plan.Selection.Mode, AssetName: plan.Selection.AssetName,
			Scope: plan.Selection.Scope, DataStateToken: plan.Selection.DataStateToken,
		},
		Resources: webscheduler.PipelineRunPlanResources{
			Isolation: plan.Resources.Isolation,
			Claims:    claims,
		},
		ExecutionContracts: contracts,
		Prerequisites:      prerequisites,
		ExecutionUnits:     units,
		Artifact:           artifact,
	}, nil
}

func schedulerPrerequisiteFromService(item service.PipelinePlanPrerequisite) webscheduler.PipelineRunPrerequisite {
	return webscheduler.PipelineRunPrerequisite{
		Status: item.Status, Reason: item.Reason,
		ConsumerAssetID: item.ConsumerAssetID, ConsumerAssetName: item.ConsumerAssetName,
		URI:                item.URI,
		ProducerPipelineID: item.ProducerPipelineID, ProducerPipelineUUID: item.ProducerPipelineUUID,
		ProducerPipelineName: item.ProducerPipelineName,
		ProducerAssetID:      item.ProducerAssetID, ProducerAssetName: item.ProducerAssetName,
		Environment: item.Environment, RequiredStart: item.RequiredStart, RequiredEnd: item.RequiredEnd,
		ExpectedFingerprint: item.ExpectedFingerprint, TargetIdentity: item.TargetIdentity, VarsHash: item.VarsHash,
		TargetGeneration: item.TargetGeneration, WriterRunID: item.WriterRunID,
		WriterSnapshotVersionID: item.WriterSnapshotVersionID,
		WriterCompletionID:      item.WriterCompletionID, WriterCompletionOrdinal: item.WriterCompletionOrdinal,
		WriterMaterializedAt: item.WriterMaterializedAt,
		CoveredSeconds:       item.CoveredSeconds, RequiredSeconds: item.RequiredSeconds,
	}
}

func servicePrerequisiteFromScheduler(item webscheduler.PipelineRunPrerequisite) service.PipelinePlanPrerequisite {
	return service.PipelinePlanPrerequisite{
		Status: item.Status, Reason: item.Reason,
		ConsumerAssetID: item.ConsumerAssetID, ConsumerAssetName: item.ConsumerAssetName,
		URI:                item.URI,
		ProducerPipelineID: item.ProducerPipelineID, ProducerPipelineUUID: item.ProducerPipelineUUID,
		ProducerPipelineName: item.ProducerPipelineName,
		ProducerAssetID:      item.ProducerAssetID, ProducerAssetName: item.ProducerAssetName,
		Environment: item.Environment, RequiredStart: item.RequiredStart, RequiredEnd: item.RequiredEnd,
		ExpectedFingerprint: item.ExpectedFingerprint, TargetIdentity: item.TargetIdentity, VarsHash: item.VarsHash,
		TargetGeneration: item.TargetGeneration, WriterRunID: item.WriterRunID,
		WriterSnapshotVersionID: item.WriterSnapshotVersionID,
		WriterCompletionID:      item.WriterCompletionID, WriterCompletionOrdinal: item.WriterCompletionOrdinal,
		WriterMaterializedAt: item.WriterMaterializedAt,
		CoveredSeconds:       item.CoveredSeconds, RequiredSeconds: item.RequiredSeconds,
	}
}

func schedulerExecutionContractFromService(
	contract service.PipelinePlanExecutionContract,
) webscheduler.PipelineRunExecutionContract {
	return webscheduler.PipelineRunExecutionContract{
		AssetID:               contract.AssetID,
		AssetName:             contract.AssetName,
		ConnectionKeys:        append([]string(nil), contract.ConnectionKeys...),
		MutationResources:     schedulerResourcesFromService(contract.MutationResources),
		CoordinationResources: schedulerResourcesFromService(contract.CoordinationResources),
	}
}

func schedulerResourcesFromService(
	resources service.PipelinePlanResources,
) webscheduler.PipelineRunPlanResources {
	claims := make([]webscheduler.PipelineRunResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, webscheduler.PipelineRunResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return webscheduler.PipelineRunPlanResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func serviceExecutionContractsFromScheduler(
	contracts []webscheduler.PipelineRunExecutionContract,
) []service.PipelinePlanExecutionContract {
	result := make([]service.PipelinePlanExecutionContract, 0, len(contracts))
	for _, contract := range contracts {
		result = append(result, service.PipelinePlanExecutionContract{
			AssetID:               contract.AssetID,
			AssetName:             contract.AssetName,
			ConnectionKeys:        append([]string(nil), contract.ConnectionKeys...),
			MutationResources:     serviceResourcesFromScheduler(contract.MutationResources),
			CoordinationResources: serviceResourcesFromScheduler(contract.CoordinationResources),
		})
	}
	return result
}

func serviceResourcesFromScheduler(
	resources webscheduler.PipelineRunPlanResources,
) service.PipelinePlanResources {
	claims := make([]service.PipelinePlanResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, service.PipelinePlanResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return service.PipelinePlanResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func persistAllPlanUnitEvent(req webscheduler.RunRequest, event service.ExecutionAssetEvent) error {
	if req.OnUnit == nil || req.ConfirmedPlan == nil {
		return fmt.Errorf("scheduler run %s cannot persist execution unit", req.RunID)
	}
	position := -1
	for index, unit := range req.ConfirmedPlan.ExecutionUnits {
		if unit.AssetName == event.Asset {
			if position >= 0 {
				return fmt.Errorf("all-assets plan contains multiple units for %s", event.Asset)
			}
			position = index
		}
	}
	if position < 0 {
		return fmt.Errorf("execution asset %s is not present in the confirmed plan", event.Asset)
	}
	return req.OnUnit(webscheduler.PipelineRunUnitEvent{
		Position: position, Status: schedulerUnitStatusFromExecutionStatus(event.Status),
		StartedAt: event.StartedAt, FinishedAt: event.FinishedAt, Error: event.Error,
	})
}

type schedulerRunEventBridge struct {
	req   webscheduler.RunRequest
	first map[string]int
	last  map[string]int
}

func newSchedulerRunEventBridge(req webscheduler.RunRequest) *schedulerRunEventBridge {
	bridge := &schedulerRunEventBridge{req: req, first: map[string]int{}, last: map[string]int{}}
	if req.ConfirmedPlan == nil {
		return bridge
	}
	for position, unit := range req.ConfirmedPlan.ExecutionUnits {
		if _, exists := bridge.first[unit.AssetName]; !exists {
			bridge.first[unit.AssetName] = position
		}
		bridge.last[unit.AssetName] = position
	}
	return bridge
}

func (b *schedulerRunEventBridge) Handle(event service.ExecutionAssetEvent) error {
	if b == nil || b.req.OnStep == nil {
		return fmt.Errorf("scheduler run cannot persist execution step")
	}
	selectionMode := ""
	if b.req.ConfirmedPlan != nil {
		selectionMode = b.req.ConfirmedPlan.Selection.Mode
	}
	if selectionMode == service.PipelinePlanSelectionAll &&
		(b.req.ConfirmedPlan == nil || b.req.ConfirmedPlan.Version < webscheduler.PipelineRunPlanVersionV3) {
		if err := persistAllPlanUnitEvent(b.req, event); err != nil {
			return err
		}
	}
	completionOrdinal := event.CompletionOrdinal
	hasDurableUnitEvents := b.req.ConfirmedPlan != nil &&
		b.req.ConfirmedPlan.Version >= webscheduler.PipelineRunPlanVersionV3
	if selectionMode != "" && (selectionMode != service.PipelinePlanSelectionAll || hasDurableUnitEvents) {
		if !event.HasUnitPosition || event.UnitPosition < 0 || event.UnitPosition >= len(b.req.ConfirmedPlan.ExecutionUnits) {
			return fmt.Errorf("execution asset %s has no confirmed unit position", event.Asset)
		}
		unit := b.req.ConfirmedPlan.ExecutionUnits[event.UnitPosition]
		if unit.AssetName != event.Asset {
			return fmt.Errorf("execution unit %d belongs to %s, not %s", event.UnitPosition, unit.AssetName, event.Asset)
		}
		status := schedulerStatusFromExecutionStatus(event.Status)
		first, last := b.first[event.Asset], b.last[event.Asset]
		if status == webscheduler.RunStatusRunning && event.UnitPosition != first {
			return nil
		}
		if status == webscheduler.RunStatusSuccess && event.UnitPosition != last {
			return nil
		}
	}

	var upstreamWriters map[string]webscheduler.UpstreamWriterSnapshot
	if event.HasUpstreamWriterSnapshot {
		upstreamWriters = make(map[string]webscheduler.UpstreamWriterSnapshot, len(event.UpstreamWriters))
		for assetID, writer := range event.UpstreamWriters {
			upstreamWriters[assetID] = webscheduler.UpstreamWriterSnapshot{
				AssetID: writer.AssetID, TargetIdentity: writer.TargetIdentity,
				Fingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
				TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
				CompletionOrdinal: writer.CompletionOrdinal, MaterializedAt: writer.MaterializedAt,
			}
		}
	}
	return b.req.OnStep(webscheduler.RunStepEvent{
		Asset: event.Asset, Status: schedulerStatusFromExecutionStatus(event.Status),
		StartedAt: event.StartedAt, FinishedAt: event.FinishedAt, Error: event.Error,
		CompletionOrdinal: completionOrdinal, UpstreamWriters: upstreamWriters,
		HasUpstreamWriterSnapshot: event.HasUpstreamWriterSnapshot,
	})
}

func schedulerUnitStatusFromExecutionStatus(status string) webscheduler.PipelineRunUnitStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "ok", "finished":
		return webscheduler.PipelineRunUnitSuccess
	case "failed", "failure", "error", "errored":
		return webscheduler.PipelineRunUnitFailed
	case "cancelled", "canceled":
		return webscheduler.PipelineRunUnitCancelled
	case "skipped":
		return webscheduler.PipelineRunUnitSkipped
	default:
		return webscheduler.PipelineRunUnitRunning
	}
}

// newSessionToken mints the per-process secret written into the discovery
// files; CLI requests present it to bypass the Origin guard explicitly.
func newSessionToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// Token auth is an additive trust path; without entropy we simply
		// don't offer it and requests fall back to the no-Origin allowance.
		return ""
	}
	return hex.EncodeToString(raw)
}

// newHTTPServer returns the http.Server used by both modes. Request contexts
// inherit the process lifecycle so long-lived SSE handlers leave promptly
// before Shutdown waits for active connections.
// No ReadTimeout/WriteTimeout: both would sever long-lived SSE streams;
// IdleTimeout only applies between requests.
func newHTTPServer(ctx context.Context, address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
}

func serverLoggerConfig() zap.Config {
	cfg := zap.NewDevelopmentConfig()
	cfg.DisableCaller = true
	cfg.DisableStacktrace = true
	return cfg
}

func newServerLogger() (*zap.Logger, error) {
	logger, err := serverLoggerConfig().Build()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}
	return logger, nil
}
