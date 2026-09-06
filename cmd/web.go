package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v3"
	webapi "renart/internal/web/api"
	"renart/internal/web/bus"
	"renart/internal/web/completion"
	"renart/internal/web/databrowser"
	"renart/internal/web/dependencygraph"
	"renart/internal/web/events"
	"renart/internal/web/fingerprint"
	webhttpapi "renart/internal/web/httpapi"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/secretstore"
	"renart/internal/web/service"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

// workspaceState is the canonical workspace DTO from the model package,
// re-exported by the service package.
type workspaceState = service.WorkspaceState

type webServer struct {
	workspaceRoot     string
	projectID         string
	projectName       string
	staticDir         string
	staticHandler     http.Handler
	watchMode         string
	watchPoll         time.Duration
	workspaceSvc      *service.WorkspaceService
	configSvc         *service.ConfigService
	secretVault       *secretstore.LocalVaultProvider
	connectionFactory *service.ResolvedConnectionFactory
	pipelineSvc       *service.PipelineService
	executionSvc      *service.ExecutionService
	assetSvc          *service.AssetService
	sqlSvc            *service.SQLService
	dataBrowserSvc    *databrowser.Service
	loadSvc           *service.LoadService
	suggestionsSvc    *service.SuggestionsService
	parseContextSvc   *service.ParseContextService
	sqlLSPSvc         *service.SQLLSPService
	jinjaRenderSvc    *service.JinjaRenderService
	assetRenderSvc    *service.AssetRenderService
	pipelinePlanSvc   *service.PipelinePlanService
	runSvc            *service.RunService
	notebookSvc       *service.NotebookService
	notebookAgentSvc  *service.NotebookAgentService
	presentationSvc   *service.PresentationService
	onboardingSvc     *service.OnboardingService
	sourceControlSvc  *service.SourceControlService
	schedulerSvc      *webscheduler.Service
	schedulerStore    *webscheduler.Store
	completionStore   *completion.Store
	stalenessSvc      *staleness.Service
	snapshotStore     *snapshot.Store
	policyLoader      *policy.Loader
	workspaceCoord    *service.WorkspaceCoordinator

	hub               *events.Hub
	executor          service.BruinCommandExecutor
	eventBus          *bus.Bus
	fingerprintEngine *fingerprint.Engine
	matlogStore       *matlog.Store
	completionMu      sync.Mutex
	logger            *zap.Logger
}

func Web() *cli.Command {
	return &cli.Command{
		Name:      "web",
		Usage:     "start the Renart IDE in your browser",
		ArgsUsage: "[workspace root]",
		Category:  categoryIDE,
		Flags: append(serverFlags(),
			&cli.StringFlag{
				Name:  "host",
				Value: "127.0.0.1",
				Usage: "host interface to bind",
			},
			&cli.IntFlag{
				Name:  "port",
				Value: 8080,
				Usage: "HTTP port",
			},
			&cli.BoolFlag{
				Name:  "unsafe-allow-remote",
				Usage: "allow binding to a non-loopback host without remote authentication",
			},
			&cli.StringFlag{
				Name:  "tls-cert",
				Usage: "optional TLS certificate path; enables HTTPS and HTTP/2 when used with --tls-key",
			},
			&cli.StringFlag{
				Name:  "tls-key",
				Usage: "optional TLS private key path; enables HTTPS and HTTP/2 when used with --tls-cert",
			},
			&cli.BoolFlag{
				Name:  "no-open",
				Usage: "do not open Renart in the default browser after startup",
			},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			// Ctrl-C / SIGTERM cancel the context so the deferred cleanups
			// actually run (discovery-file removal, scheduler drain); a
			// second signal falls back to the default hard kill.
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			host := strings.TrimSpace(c.String("host"))
			if err := validateWebBindHost(host, c.Bool("unsafe-allow-remote")); err != nil {
				return err
			}

			cfg, err := serverConfigFromCommand(c)
			if err != nil {
				return err
			}
			defer cleanupServerBootstrap(cfg)

			logger, err := newServerLogger()
			if err != nil {
				return err
			}
			defer func() { _ = logger.Sync() }()
			if !isLoopbackBindHost(host) {
				logger.Warn("Renart is listening on a non-loopback interface without remote authentication",
					zap.String("host", host),
					zap.String("warning", "any client that can reach this server may edit workspace files and run pipeline code"),
				)
				fmt.Fprintf(os.Stderr, "WARNING: --unsafe-allow-remote exposes Renart without remote authentication; reachable clients may edit files and run code.\n")
			}

			port := c.Int("port")
			tlsCert := strings.TrimSpace(c.String("tls-cert"))
			tlsKey := strings.TrimSpace(c.String("tls-key"))
			if (tlsCert == "") != (tlsKey == "") {
				return fmt.Errorf("--tls-cert and --tls-key must be provided together")
			}
			if tlsCert != "" {
				if _, err := tls.LoadX509KeyPair(tlsCert, tlsKey); err != nil {
					return fmt.Errorf("failed to load TLS certificate and key: %w", err)
				}
			}

			listener, address, err := listenWithDefaultPortFallback(host, port)
			if err != nil {
				return err
			}
			defer listener.Close()

			startup := newStartupGate()
			httpCtx, cancelHTTP := context.WithCancel(ctx)
			httpServer := newHTTPServer(httpCtx, address, startup)
			if tlsCert != "" {
				if err := http2.ConfigureServer(httpServer, &http2.Server{}); err != nil {
					cancelHTTP()
					return fmt.Errorf("failed to configure HTTP/2: %w", err)
				}
			}
			shutdownHTTP := func() error {
				cancelHTTP()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return httpServer.Shutdown(shutdownCtx)
			}
			defer func() { _ = shutdownHTTP() }()

			serveDone := make(chan error, 1)
			go func() {
				var serveErr error
				if tlsCert != "" {
					serveErr = httpServer.ServeTLS(listener, tlsCert, tlsKey)
				} else {
					serveErr = httpServer.Serve(listener)
				}
				if errors.Is(serveErr, http.ErrServerClosed) {
					serveErr = nil
				}
				serveDone <- serveErr
			}()

			scheme := "http"
			if tlsCert != "" {
				scheme = "https"
			}
			detail := ""
			if tlsCert != "" {
				detail = " (HTTP/2 enabled)"
			}
			printRenartWelcome(c.Writer, scheme+"://"+address, detail)

			stopShutdownObserver := startGracefulShutdown(ctx, stop, logger, func() {
				if err := shutdownHTTP(); err != nil {
					logger.Warn("HTTP server did not stop gracefully", zap.Error(err))
				}
			})
			defer stopShutdownObserver()

			defaultRuntime, err := newProjectRuntime(ctx, logger, cfg)
			if err != nil {
				return err
			}
			defer defaultRuntime.cleanup()

			manager, err := newProjectManager(ctx, logger, cfg, defaultRuntime)
			if err != nil {
				return err
			}
			defer manager.closeAll()

			sessionToken := newSessionToken()
			router := buildRootRouter(manager, defaultRuntime, sessionToken)
			manager.EnableDiscovery(scheme+"://"+loopbackAddress(address), sessionToken)
			defer manager.DisableDiscovery()
			startup.Activate(router)

			if !c.Bool("no-open") {
				go openBrowserWhenReachable(ctx, scheme+"://"+address, address)
			}

			if err := <-serveDone; err != nil {
				return err
			}

			return nil
		},
	}
}

func validateWebBindHost(host string, allowRemote bool) error {
	if isLoopbackBindHost(host) || allowRemote {
		return nil
	}
	return fmt.Errorf("refusing to bind Renart to non-loopback host %q: the API can edit workspace files and run code and does not provide remote authentication; use --unsafe-allow-remote only behind a trusted access layer", host)
}

func isLoopbackBindHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if zoneIndex := strings.LastIndexByte(host, '%'); zoneIndex >= 0 {
		host = host[:zoneIndex]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackAddress rewrites a wildcard listen address into one a local CLI
// can actually dial (0.0.0.0/:: bind still means loopback for local
// clients); concrete hosts pass through.
func loopbackAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return address
}

func openBrowserWhenReachable(ctx context.Context, url, address string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			if err := openBrowser(url); err != nil {
				fmt.Printf("warning: failed to open browser: %v\n", err)
			}
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	fmt.Printf("warning: server did not become reachable quickly enough to open browser automatically; open %s manually\n", url)
}

func openBrowser(url string) error {
	var command string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{url}
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		command = "xdg-open"
		args = []string{url}
	}

	return exec.Command(command, args...).Start()
}

func listenWithDefaultPortFallback(host string, port int) (net.Listener, string, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", address)
	if err == nil {
		return listener, address, nil
	}

	if port != 8080 {
		return nil, "", fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	firstErr := err
	for fallbackPort := 8081; fallbackPort <= 8099; fallbackPort++ {
		fallbackAddress := fmt.Sprintf("%s:%d", host, fallbackPort)
		listener, err = net.Listen("tcp", fallbackAddress)
		if err == nil {
			fmt.Printf("warning: %s is unavailable, using fallback port %d instead\n", address, fallbackPort)
			return listener, fallbackAddress, nil
		}
	}

	return nil, "", fmt.Errorf("failed to listen on %s and no fallback port from 8081 to 8099 was available: %w", address, firstErr)
}

func (s *webServer) registerRoutes(router chi.Router) {
	webhttpapi.RegisterHealthRoutes(router, func() webhttpapi.HealthInfo {
		return webhttpapi.HealthInfo{
			Version:       buildVersion,
			WorkspaceRoot: s.workspaceRoot,
			ProjectID:     s.projectID,
		}
	})
	webhttpapi.RegisterWorkspaceRoutes(router, &webhttpapi.WorkspaceHandlers{Reader: s})
	webhttpapi.RegisterConfigRoutes(router, &webhttpapi.ConfigHandlers{Service: s.configSvc, Policies: s.policyLoader, Publisher: s})
	webhttpapi.RegisterPipelineRoutes(router, &webhttpapi.PipelineHandlers{Service: s.pipelineSvc, Publisher: s})
	webhttpapi.RegisterExecutionRoutes(router, &webhttpapi.ExecutionAPI{Service: s.executionSvc})
	webhttpapi.RegisterAssetRoutes(router, &webhttpapi.AssetsAPI{Service: s.assetSvc})
	webhttpapi.RegisterAssetColumnRoutes(router, &webhttpapi.AssetColumnsAPI{Service: s.assetSvc})
	webhttpapi.RegisterPipelineExecutionRoutes(router, &webhttpapi.PipelineExecutionAPI{Service: s.executionSvc})
	webhttpapi.RegisterSQLRoutes(router, &webhttpapi.SQLAPI{Service: s.sqlSvc})
	webhttpapi.RegisterDataBrowserRoutes(router, &webhttpapi.DataBrowserAPI{Service: s.dataBrowserSvc, Sources: s.pipelineSvc, Publisher: s})
	webhttpapi.RegisterLoadRoutes(router, &webhttpapi.LoadAPI{Service: s.loadSvc})
	webhttpapi.RegisterSuggestionRoutes(router, &webhttpapi.SuggestionsAPI{Service: s.suggestionsSvc})
	webhttpapi.RegisterParseContextRoutes(router, &webhttpapi.ParseContextAPI{Service: s.parseContextSvc})
	webhttpapi.RegisterSQLLSPRoutes(router, &webhttpapi.SQLLSPAPI{Service: s.sqlLSPSvc})
	webhttpapi.RegisterJinjaRenderRoutes(router, &webhttpapi.JinjaRenderAPI{Service: s.jinjaRenderSvc})
	webhttpapi.RegisterAssetRenderRoutes(router, &webhttpapi.AssetRenderAPI{Service: s.assetRenderSvc})
	webhttpapi.RegisterPipelineAssetRenderRoutes(router, &webhttpapi.PipelineAssetRenderAPI{Service: s.pipelinePlanSvc})
	webhttpapi.RegisterPipelinePlanRoutes(router, &webhttpapi.PipelinePlanAPI{Service: s.pipelinePlanSvc, Runs: s})
	webhttpapi.RegisterRunRoutes(router, &webhttpapi.RunAPI{Service: s.runSvc})
	webhttpapi.RegisterNotebookRoutes(router, &webhttpapi.NotebookAPI{Service: s.notebookSvc})
	if s.notebookAgentSvc != nil {
		webhttpapi.RegisterNotebookAgentRoutes(router, &webhttpapi.NotebookAgentAPI{Service: s.notebookAgentSvc})
	}
	webhttpapi.RegisterPresentationRoutes(router, &webhttpapi.PresentationAPI{Service: s.presentationSvc})
	webhttpapi.RegisterPythonPackageRoutes(router, &webhttpapi.PythonPackagesAPI{Search: service.SearchPyPIPackages})
	// Warm the PyPI package index in the background so the first dependency
	// search does not pay the download cost.
	service.WarmPyPIIndex(context.Background())
	webhttpapi.RegisterSchedulerRoutes(router, &webhttpapi.SchedulerAPI{Service: s})
	webhttpapi.RegisterEnvScheduleRoutes(router, &webhttpapi.EnvSchedulesAPI{
		Service:             s.schedulerSvc,
		TriggerEnvSchedule:  s.TriggerEnvSchedule,
		ResolvePipelineUUID: s.findPipelineUUIDByID,
	})
	webhttpapi.RegisterOnboardingRoutes(router, &webhttpapi.OnboardingAPI{Service: s.onboardingSvc, Publisher: s})
	webhttpapi.RegisterSourceControlRoutes(router, &webhttpapi.SourceControlAPI{Service: s.sourceControlSvc})
	webhttpapi.RegisterStalenessRoutes(router, &webhttpapi.StalenessAPI{
		Service:             s.stalenessSvc,
		ResolvePipelineUUID: s.findPipelineUUIDByID,
		SelectedEnvironment: func() string { return s.currentState().SelectedEnvironment },
	})
	webhttpapi.RegisterBuildStaleRoutes(router, &webhttpapi.BuildStaleAPI{
		Staleness:                 s.stalenessSvc,
		ResolvePipelineUUID:       s.findPipelineUUIDByID,
		ResolveUpstreamAssetNames: s.findPipelineUpstreamNames,
		SelectedEnvironment:       func() string { return s.currentState().SelectedEnvironment },
		Execution:                 s.executionSvc,
	})
	webhttpapi.RegisterDeployRoutes(router, &webhttpapi.DeployAPI{
		Snapshots:       s.snapshotStore,
		ResolvePipeline: s.resolvePipelineForDeploy,
		ResolveDependencyManifest: func(ctx context.Context, pipelineUUID string) (snapshot.DependencyManifest, string, error) {
			manifest, sourceRoot, _, err := service.ResolveDeploymentDependencyManifest(
				ctx, s.workspaceRoot, resolveConfigFilePath(s.workspaceRoot), pipelineUUID,
			)
			return manifest, sourceRoot, err
		},
	})

	router.Get("/*", s.handleStatic)
}

func (s *webServer) currentState() workspaceState {
	return s.workspaceCoord.CurrentState()
}

func (s *webServer) refreshWorkspace(ctx context.Context) error {
	return s.workspaceCoord.Refresh(ctx)
}

func (s *webServer) newPipelineBuilder() *pipeline.Builder {
	return service.NewRenartPipelineBuilder(afero.NewOsFs())
}

func resolveConfigFilePath(workspaceRoot string) string {
	repoRoot, err := git.FindRepoFromPath(workspaceRoot)
	if err == nil && repoRoot != nil && strings.TrimSpace(repoRoot.Path) != "" {
		return filepath.Join(repoRoot.Path, ".bruin.yml")
	}

	return filepath.Join(workspaceRoot, ".bruin.yml")
}

func (s *webServer) resolveConfigFilePath() string {
	return resolveConfigFilePath(s.workspaceRoot)
}

func (s *webServer) ConfigChanged(ctx context.Context, relPath, eventType string) {
	s.workspaceCoord.SuppressWatcherFor(relPath)
	s.workspaceCoord.PushUpdateImmediate(ctx, eventType, relPath)
}

func (s *webServer) WorkspaceChanged(ctx context.Context, relPath, eventType string) {
	s.workspaceCoord.SuppressWatcherFor(relPath)
	s.workspaceCoord.PushUpdateImmediate(ctx, eventType, relPath)
	if s.schedulerSvc != nil {
		go func() {
			if err := s.schedulerSvc.Reconcile(context.Background()); err != nil && s.logger != nil {
				s.logger.Warn("scheduler reconcile failed", zap.Error(err))
			}
		}()
	}
}

func (s *webServer) ListSchedules(ctx context.Context) ([]webscheduler.PipelineSchedule, error) {
	if s.schedulerSvc != nil {
		return s.schedulerSvc.ListSchedules(ctx)
	}
	return s.pipelineSvc.ListSchedules(ctx)
}

func (s *webServer) GetPipelineSchedule(ctx context.Context, pipelineID string) (webscheduler.PipelineSchedule, error) {
	item, err := s.pipelineSvc.GetSchedule(ctx, pipelineID)
	if err != nil {
		return webscheduler.PipelineSchedule{}, err
	}
	return s.applyLocalScheduleSettings(ctx, item)
}

func (s *webServer) UpdatePipelineSchedule(ctx context.Context, pipelineID string, req webscheduler.UpdateScheduleRequest) (webscheduler.PipelineSchedule, error) {
	// This legacy endpoint edits pipeline.yml before bridging into the
	// per-environment schedule store. Preflight ownership before either write,
	// otherwise a follower could partially change the workspace without being
	// able to apply the corresponding River schedule.
	if s.schedulerSvc != nil {
		if err := s.schedulerSvc.RequireOwner(); err != nil {
			return webscheduler.PipelineSchedule{}, err
		}
	}
	current, err := s.pipelineSvc.GetSchedule(ctx, pipelineID)
	if err != nil {
		return webscheduler.PipelineSchedule{}, err
	}
	desiredSchedule := strings.TrimSpace(req.Schedule)
	if desiredSchedule == "" {
		desiredSchedule = strings.TrimSpace(current.Schedule)
	}
	desiredTimezone := strings.TrimSpace(req.Timezone)
	if desiredTimezone == "" {
		desiredTimezone = current.Timezone
	}
	if desiredTimezone == "" {
		desiredTimezone = "UTC"
	}
	if req.Enabled && desiredSchedule == "" {
		return webscheduler.PipelineSchedule{}, fmt.Errorf("schedule is required when scheduling is enabled")
	}

	updated := current
	if desiredSchedule != strings.TrimSpace(current.Schedule) || desiredTimezone != strings.TrimSpace(current.Timezone) || req.Catchup != current.Catchup {
		var relPath string
		relPath, updated, err = s.pipelineSvc.UpdateSchedule(ctx, pipelineID, webscheduler.UpdateScheduleRequest{Enabled: req.Enabled, Schedule: desiredSchedule, Timezone: desiredTimezone, Catchup: req.Catchup})
		if err != nil {
			return webscheduler.PipelineSchedule{}, err
		}
		s.WorkspaceChanged(ctx, relPath, "pipeline.updated")
	}

	// Bridge to the per-environment schedule model: this legacy endpoint
	// operates on the workspace's selected environment. Enabling deploys
	// the working tree (a schedule needs a deployed snapshot).
	if s.schedulerSvc != nil && updated.PipelineUUID != "" {
		environment := strings.TrimSpace(s.currentState().SelectedEnvironment)
		if environment == "" {
			environment = "default"
		}
		if req.Enabled {
			policy := webscheduler.CatchupSkip
			if req.Catchup {
				policy = webscheduler.CatchupRunOnce
			}
			if _, err := s.schedulerSvc.UpsertEnvSchedule(ctx, updated.PipelineUUID, webscheduler.UpsertEnvScheduleRequest{
				Environment:   environment,
				Cron:          desiredSchedule,
				Timezone:      desiredTimezone,
				CatchupPolicy: policy,
				DeployNow:     true,
			}); err != nil {
				return webscheduler.PipelineSchedule{}, err
			}
		} else if _, found, getErr := s.schedulerStore.GetEnvSchedule(ctx, updated.PipelineUUID, environment); getErr == nil && found {
			if err := s.schedulerSvc.SetEnvScheduleLifecycle(ctx, updated.PipelineUUID, environment, webscheduler.ScheduleStatusPaused); err != nil {
				return webscheduler.PipelineSchedule{}, err
			}
		}
		items, listErr := s.schedulerSvc.ListSchedules(ctx)
		if listErr == nil {
			for _, item := range items {
				if item.PipelineID == pipelineID {
					return item, nil
				}
			}
		}
	}
	return s.applyLocalScheduleSettings(ctx, updated)
}

func (s *webServer) applyLocalScheduleSettings(ctx context.Context, item webscheduler.PipelineSchedule) (webscheduler.PipelineSchedule, error) {
	if s.schedulerStore == nil {
		return item, nil
	}
	// Per-environment rows win: enabled means any active row for the
	// pipeline's stable UUID.
	if item.PipelineUUID != "" {
		rows, err := s.schedulerStore.ListEnvSchedules(ctx)
		if err != nil {
			return webscheduler.PipelineSchedule{}, err
		}
		seen := false
		enabled := false
		for _, row := range rows {
			if row.PipelineUUID != item.PipelineUUID {
				continue
			}
			seen = true
			if row.Status == webscheduler.ScheduleStatusActive {
				enabled = true
				break
			}
		}
		if seen {
			item.Enabled = enabled && strings.TrimSpace(item.Schedule) != ""
			return item, nil
		}
	}
	enabled, ok, err := s.schedulerStore.ScheduleEnabled(ctx, item.PipelineID)
	if err != nil {
		return webscheduler.PipelineSchedule{}, err
	}
	if ok {
		item.Enabled = enabled && strings.TrimSpace(item.Schedule) != ""
	}
	return item, nil
}

func (s *webServer) TriggerPipeline(ctx context.Context, pipelineID string, req webscheduler.TriggerRequest) (webscheduler.PipelineRun, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRun{}, fmt.Errorf("scheduler is not initialized")
	}
	pipelineSchedule, err := s.pipelineSvc.GetSchedule(ctx, pipelineID)
	if err != nil {
		return webscheduler.PipelineRun{}, err
	}
	req.Environment = normalizeTriggerEnvironment(req.Environment, s.currentState().SelectedEnvironment)
	environmentPolicy := policy.EnvironmentPolicy{}
	if s.policyLoader != nil {
		environmentPolicy = s.policyLoader.For(req.Environment)
	}
	if err := resolveTriggerRunSource(ctx, s.snapshotStore, pipelineSchedule.PipelineUUID, req.Environment, environmentPolicy, &req); err != nil {
		return webscheduler.PipelineRun{}, err
	}
	if err := policy.Check(environmentPolicy, policy.RunRequest{
		Environment:          req.Environment,
		Interactive:          true,
		SnapshotBased:        req.Source == webscheduler.RunSourceSnapshot,
		Destructive:          req.FullRefresh || req.Backfill,
		ConfirmedEnvironment: strings.TrimSpace(req.ConfirmedEnvironment),
	}); err != nil {
		return webscheduler.PipelineRun{}, err
	}
	return s.schedulerSvc.Trigger(ctx, pipelineSchedule, req)
}

// TriggerEnvSchedule runs the exact source and private variable context stored
// on one schedule row, but keeps the invocation manual: it cannot advance the
// schedule watermark or impersonate an actual due occurrence.
func (s *webServer) TriggerEnvSchedule(
	ctx context.Context,
	pipelineID string,
	pipelineUUID string,
	environment string,
) (webscheduler.PipelineRun, error) {
	if s.schedulerStore == nil {
		return webscheduler.PipelineRun{}, fmt.Errorf("scheduler store is not initialized")
	}
	row, found, err := s.schedulerStore.GetEnvSchedule(ctx, strings.TrimSpace(pipelineUUID), strings.TrimSpace(environment))
	if err != nil {
		return webscheduler.PipelineRun{}, err
	}
	if !found {
		return webscheduler.PipelineRun{}, fmt.Errorf("schedule not found")
	}
	req, err := envScheduleTriggerRequest(row)
	if err != nil {
		return webscheduler.PipelineRun{}, err
	}
	return s.TriggerPipeline(ctx, pipelineID, req)
}

func envScheduleTriggerRequest(row webscheduler.EnvSchedule) (webscheduler.TriggerRequest, error) {
	if row.Status == webscheduler.ScheduleStatusArchived {
		return webscheduler.TriggerRequest{}, fmt.Errorf("schedule not found")
	}
	if row.Status == webscheduler.ScheduleStatusDelegated {
		return webscheduler.TriggerRequest{}, fmt.Errorf("delegated schedules cannot run in this Renart server")
	}
	return webscheduler.TriggerRequest{
		Environment:        row.Environment,
		Source:             webscheduler.RunSourceSnapshot,
		SnapshotVersionID:  row.SnapshotVersionID,
		VariableOverrides:  row.Vars,
		VariableReferences: row.SecretRefs,
	}, nil
}

func resolveTriggerRunSource(
	ctx context.Context,
	store *snapshot.Store,
	pipelineUUID string,
	environment string,
	environmentPolicy policy.EnvironmentPolicy,
	req *webscheduler.TriggerRequest,
) error {
	if req == nil {
		return fmt.Errorf("run request is required")
	}
	pipelineUUID = strings.TrimSpace(pipelineUUID)
	if pipelineUUID == "" {
		return fmt.Errorf("pipeline has no stable ID; save the pipeline before running it")
	}
	req.SnapshotVersionID = strings.TrimSpace(req.SnapshotVersionID)

	if req.Source == "" {
		if req.SnapshotVersionID != "" {
			return fmt.Errorf("source is required when snapshot_version_id is set")
		}
		if !environmentPolicy.DeployedOnly {
			req.Source = webscheduler.RunSourceWorkingTree
			return nil
		}
		if store == nil {
			return fmt.Errorf("environment %q only executes deployed snapshots: deploy the pipeline first", environment)
		}
		latest, err := store.Latest(ctx, pipelineUUID)
		if err != nil {
			return fmt.Errorf("resolve latest deployment for pipeline: %w", err)
		}
		if latest == nil {
			return fmt.Errorf("environment %q only executes deployed snapshots: deploy the pipeline first", environment)
		}
		req.Source = webscheduler.RunSourceSnapshot
		req.SnapshotVersionID = latest.VersionID
	}

	switch req.Source {
	case webscheduler.RunSourceWorkingTree:
		if req.SnapshotVersionID != "" {
			return fmt.Errorf("snapshot_version_id must be empty when source is working_tree")
		}
		return nil
	case webscheduler.RunSourceSnapshot:
		if req.SnapshotVersionID == "" {
			return fmt.Errorf("snapshot_version_id is required when source is snapshot")
		}
		if store == nil {
			return fmt.Errorf("snapshot store is unavailable")
		}
		if _, err := store.Validate(ctx, req.SnapshotVersionID, pipelineUUID); err != nil {
			return fmt.Errorf("deployment %s is not executable for this pipeline: %w", req.SnapshotVersionID, err)
		}
		return nil
	default:
		return fmt.Errorf("invalid run source %q: expected working_tree or snapshot", req.Source)
	}
}

func normalizeTriggerEnvironment(requested, selected string) string {
	if normalized := strings.TrimSpace(requested); normalized != "" {
		return normalized
	}
	return strings.TrimSpace(selected)
}

func (s *webServer) ListRuns(ctx context.Context, filter webscheduler.RunFilter) (webscheduler.RunList, error) {
	if s.schedulerSvc == nil {
		return webscheduler.RunList{}, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.ListRuns(ctx, filter)
}

func (s *webServer) GetRun(ctx context.Context, runID string) (webscheduler.PipelineRun, []webscheduler.LogLine, []webscheduler.PipelineRunStep, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRun{}, nil, nil, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.GetRun(ctx, runID)
}

func (s *webServer) GetRunPlan(ctx context.Context, runID string) (webscheduler.PipelineRunPlan, bool, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRunPlan{}, false, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.GetRunPlan(ctx, runID)
}

func (s *webServer) ListRunUnits(ctx context.Context, runID string) ([]webscheduler.PipelineRunUnit, error) {
	if s.schedulerSvc == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.ListRunUnits(ctx, runID)
}

func (s *webServer) GetRunReexecution(ctx context.Context, runID string) (webscheduler.PipelineRunReexecution, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRunReexecution{}, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.GetRunReexecution(ctx, runID)
}

func (s *webServer) ReexecuteRun(ctx context.Context, runID string) (webscheduler.PipelineRun, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRun{}, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.ReexecuteRun(ctx, runID)
}

func (s *webServer) CancelRun(ctx context.Context, runID string) (webscheduler.PipelineRun, error) {
	if s.schedulerSvc == nil {
		return webscheduler.PipelineRun{}, fmt.Errorf("scheduler is not initialized")
	}
	return s.schedulerSvc.CancelRun(ctx, runID)
}

func schedulerStatusFromExecutionStatus(status string) webscheduler.RunStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "ok", "finished":
		return webscheduler.RunStatusSuccess
	case "failed", "failure", "error", "errored":
		return webscheduler.RunStatusFailed
	case "cancelled", "canceled":
		return webscheduler.RunStatusCancelled
	case "queued":
		return webscheduler.RunStatusQueued
	default:
		return webscheduler.RunStatusRunning
	}
}

func (s *webServer) CurrentWorkspace() any {
	return s.currentState()
}

func (s *webServer) CurrentWorkspaceLite() any {
	return s.workspaceCoord.CurrentStateLiteEvent()
}

func (s *webServer) SubscribeWorkspaceEvents() chan []byte {
	return s.workspaceCoord.Subscribe()
}

func (s *webServer) UnsubscribeWorkspaceEvents(ch chan []byte) {
	s.workspaceCoord.Unsubscribe(ch)
}

func (s *webServer) writeJSON(w http.ResponseWriter, status int, body any) {
	webapi.WriteJSON(w, status, body)
}

func (s *webServer) resolveAssetByID(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
	return s.workspaceSvc.ResolveAssetByID(ctx, assetID)
}

// findPipelineUUIDByID maps the path-encoded API pipeline ID to the stable
// pipeline UUID from the current workspace state.
func (s *webServer) findPipelineUUIDByID(pipelineID string) (string, bool) {
	for _, p := range s.currentState().Pipelines {
		if p.ID == pipelineID && p.UUID != "" {
			return p.UUID, true
		}
	}
	return "", false
}

func (s *webServer) findPipelineUpstreamNames(pipelineID, assetName string) (map[string]struct{}, bool) {
	for _, p := range s.currentState().Pipelines {
		if p.ID == pipelineID {
			return service.PipelineUpstreamNames(p, assetName)
		}
	}
	return nil, false
}

// verifyMaterializedAssets is the staleness trust-but-verify hook: it asks
// the execution service which assets actually exist in the warehouse for
// the environment. Runs async and throttled by the staleness service.
func (s *webServer) verifyMaterializedAssets(ctx context.Context, selection staleness.Selection, assetNames []string) (map[string]bool, error) {
	resp, apiErr := s.executionSvc.GetPipelineMaterialization(ctx, selection.EncodedPipelineID, selection.Environment)
	if apiErr != nil {
		return nil, apiErr
	}
	return verifiedMaterializationPresence(resp.Assets, assetNames, s.findAssetNameByID), nil
}

func verifiedMaterializationPresence(
	assets []service.PipelineMaterializationState,
	assetNames []string,
	findAssetNameByID func(string) string,
) map[string]bool {
	existsByName := make(map[string]bool, len(assets))
	for _, asset := range assets {
		if !asset.VerificationAvailable {
			continue
		}
		if name := findAssetNameByID(asset.AssetID); name != "" {
			existsByName[name] = asset.IsMaterialized
		}
	}
	result := make(map[string]bool, len(assetNames))
	for _, name := range assetNames {
		if present, ok := existsByName[name]; ok {
			result[name] = present
		}
	}
	return result
}

// resolvePipelineForDeploy maps the encoded pipeline ID to (UUID, absolute
// directory) for the deploy/drift endpoints.
func (s *webServer) resolvePipelineForDeploy(pipelineID string) (string, string, bool) {
	for _, p := range s.currentState().Pipelines {
		if p.ID != pipelineID || p.UUID == "" {
			continue
		}
		absPath, err := service.SafeJoin(s.workspaceRoot, p.Path)
		if err != nil {
			return "", "", false
		}
		return p.UUID, absPath, true
	}
	return "", "", false
}

// parsePipelineDir parses a pipeline from an explicit directory — used by
// the materialization recorder to fingerprint snapshot content.
func (s *webServer) parsePipelineDir(ctx context.Context, pipelineDir string) (*pipeline.Pipeline, error) {
	return s.newPipelineBuilder().CreatePipelineFromPath(ctx, pipelineDir, pipeline.WithMutate())
}

// resolveRunSnapshot materializes an already-resolved exact deployment. It
// never chooses "latest" and never falls back to the working tree.
func (s *webServer) resolveRunSnapshot(ctx context.Context, spec *service.PipelineRunSpec, scheduled bool, onLog func(string)) (func(), error) {
	if spec == nil {
		return func() {}, fmt.Errorf("pipeline run specification is required")
	}
	pipelineUUID := strings.TrimSpace(spec.PipelineUUID)
	if strings.TrimSpace(spec.SnapshotVersionID) != "" {
		// New scheduler-backed runs carry the stable UUID in their durable
		// RunSpec. Retain path lookup only for rolling compatibility with jobs
		// admitted before that field was threaded through execution.
		if pipelineUUID == "" {
			var ok bool
			pipelineUUID, ok = s.findPipelineUUIDByID(spec.PipelineID)
			if !ok {
				return func() {}, fmt.Errorf("pipeline %s is not in the current workspace", spec.PipelineID)
			}
		}
	} else if expectedMerkle := strings.TrimSpace(spec.ExpectedSourceMerkle); expectedMerkle != "" {
		pipelineID := spec.PipelineID
		if pipelineUUID != "" {
			for _, current := range s.currentState().Pipelines {
				if current.UUID == pipelineUUID {
					pipelineID = current.ID
					break
				}
			}
		}
		target, err := service.ResolvePipelineRunTarget(pipelineID)
		if err != nil {
			return func() {}, fmt.Errorf("resolve confirmed pipeline source: %w", err)
		}
		pipelineDir, err := service.SafeJoin(s.workspaceRoot, target)
		if err != nil {
			return func() {}, fmt.Errorf("resolve confirmed pipeline source: %w", err)
		}
		tempDir, err := os.MkdirTemp("", "renart-working-tree-plan-")
		if err != nil {
			return func() {}, fmt.Errorf("create confirmed working-tree sandbox: %w", err)
		}
		manifest, err := snapshot.CopyPipelineSourceForExecution(pipelineDir, tempDir)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return func() {}, fmt.Errorf("copy confirmed pipeline source: %w", err)
		}
		if len(manifest) == 0 || snapshot.ManifestRoot(manifest) != expectedMerkle {
			_ = os.RemoveAll(tempDir)
			return func() {}, fmt.Errorf("pipeline source changed after plan confirmation")
		}
		spec.SnapshotDir = tempDir
		spec.ConfigPath = s.resolveConfigFilePath()
		if onLog != nil {
			onLog("executing confirmed working-tree plan\n")
		}
		return func() { _ = os.RemoveAll(tempDir) }, nil
	}
	return materializeExactRunSnapshot(ctx, s.snapshotStore, pipelineUUID, s.resolveConfigFilePath(), scheduled, spec, onLog)
}

func materializeExactRunSnapshot(
	ctx context.Context,
	store *snapshot.Store,
	pipelineUUID string,
	configPath string,
	scheduled bool,
	spec *service.PipelineRunSpec,
	onLog func(string),
) (func(), error) {
	cleanup := func() {}
	if spec == nil {
		return cleanup, fmt.Errorf("pipeline run specification is required")
	}
	versionID := strings.TrimSpace(spec.SnapshotVersionID)
	if versionID == "" {
		if scheduled {
			return cleanup, fmt.Errorf("scheduled run has no pinned deployment")
		}
		return cleanup, nil
	}
	if store == nil {
		return cleanup, fmt.Errorf("snapshot store is unavailable for deployment %s", versionID)
	}
	pipelineUUID = strings.TrimSpace(pipelineUUID)
	if pipelineUUID == "" {
		return cleanup, fmt.Errorf("pipeline has no stable ID for deployment %s", versionID)
	}
	if expectedMerkle := strings.TrimSpace(spec.ExpectedSourceMerkle); expectedMerkle != "" {
		deployed, err := store.ValidateMetadata(ctx, versionID, pipelineUUID)
		if err != nil {
			return cleanup, fmt.Errorf("validate deployment %s: %w", versionID, err)
		}
		if deployed.MerkleRoot != expectedMerkle {
			return cleanup, fmt.Errorf("pipeline source changed after plan confirmation")
		}
	}
	tempDir, err := os.MkdirTemp("", "renart-snapshot-")
	if err != nil {
		return cleanup, fmt.Errorf("create deployment sandbox: %w", err)
	}
	if err := store.MaterializeForPipelineExecution(ctx, versionID, pipelineUUID, tempDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return cleanup, fmt.Errorf("materialize deployment %s: %w", versionID, err)
	}
	spec.SnapshotDir = tempDir
	spec.SnapshotVersionID = versionID
	spec.ConfigPath = configPath
	if onLog != nil {
		onLog("executing deployed snapshot " + versionID + "\n")
	}
	return func() { _ = os.RemoveAll(tempDir) }, nil
}

// warmFingerprintCache fingerprints every workspace pipeline once so the
// formatter-normalized SQL cache is populated before the first staleness
// request arrives.
func (s *webServer) warmFingerprintCache(ctx context.Context) {
	started := time.Now()
	pipelines := s.currentState().Pipelines
	for _, p := range pipelines {
		if ctx.Err() != nil {
			return
		}
		if p.UUID == "" {
			continue
		}
		parsed, err := s.resolvePipelineByUUID(ctx, p.UUID)
		if err != nil {
			continue
		}
		vars := fingerprint.EffectiveVars(parsed, nil)
		if _, err := s.fingerprintEngine.DAG(parsed, vars); err != nil && s.logger != nil {
			s.logger.Debug("fingerprint warm-up failed for pipeline", zap.String("pipeline", p.Name), zap.Error(err))
		}
	}
	if s.logger != nil && len(pipelines) > 0 {
		s.logger.Info("fingerprint cache warmed", zap.Int("pipelines", len(pipelines)), zap.Duration("took", time.Since(started)))
	}
}

// resolvePipelineByUUID loads the parsed pipeline whose stable UUID matches.
func (s *webServer) resolvePipelineByUUID(ctx context.Context, pipelineUUID string) (*pipeline.Pipeline, error) {
	for _, p := range s.currentState().Pipelines {
		if p.UUID != pipelineUUID {
			continue
		}
		absPath, err := service.SafeJoin(s.workspaceRoot, p.Path)
		if err != nil {
			return nil, err
		}
		return s.newPipelineBuilder().CreatePipelineFromPath(ctx, absPath, pipeline.WithMutate())
	}
	return nil, fmt.Errorf("pipeline with id %s not found in workspace", pipelineUUID)
}

func (s *webServer) resolveWorkspaceDependencyGraph(
	ctx context.Context,
	overrides map[string]*pipeline.Pipeline,
) (dependencygraph.Graph, error) {
	return service.ResolveWorkspaceDependencyGraph(
		ctx,
		s.currentState(),
		s.resolvePipelineByUUID,
		overrides,
	)
}

func (s *webServer) newConnectionManager(ctx context.Context, environment string) (config.ConnectionAndDetailsGetter, error) {
	if s.connectionFactory == nil {
		return nil, fmt.Errorf("connection factory is unavailable")
	}
	return s.connectionFactory.NewConnectionManager(ctx, environment)
}

func (s *webServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.staticHandler != nil {
		s.staticHandler.ServeHTTP(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("Renart UI assets are unavailable."))
}

// suppressWatcherFor marks a path as recently handled by a server-initiated
// write (API handler or patch timer). Filesystem watcher events for this
// path will be suppressed for a short window to avoid duplicate notifications.
func (s *webServer) suppressWatcherFor(eventPath string) {
	s.workspaceCoord.SuppressWatcherFor(eventPath)
}

// isWatcherSuppressed returns true if the given path was recently handled by
// a server-initiated write and the filesystem watcher event should be skipped.
func (s *webServer) isWatcherSuppressed(eventPath string) bool {
	return s.workspaceCoord.IsWatcherSuppressed(eventPath)
}

func (s *webServer) pushWorkspaceUpdate(ctx context.Context, eventType, eventPath string) {
	s.workspaceCoord.PushUpdate(ctx, eventType, eventPath)
	if s.schedulerSvc != nil && scheduleDeclarationRelevantPath(eventPath) {
		go func() {
			if err := s.schedulerSvc.Reconcile(context.Background()); err != nil && s.logger != nil {
				s.logger.Warn("scheduler reconcile after workspace change failed",
					zap.String("path", filepath.ToSlash(eventPath)), zap.Error(err))
			}
		}()
	}
}

func scheduleDeclarationRelevantPath(eventPath string) bool {
	normalized := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(eventPath)), "./")
	if normalized == "" || normalized == "." || normalized == ".renart/schedules.yml" {
		return true
	}
	base := filepath.Base(normalized)
	return base == "pipeline.yml" || base == "pipeline.yaml"
}

// pushWorkspaceUpdateImmediate publishes immediately (bypasses debounce).
// Used by API handlers that need the client to see the change right away.
func (s *webServer) pushWorkspaceUpdateImmediate(ctx context.Context, eventType, eventPath string) {
	s.workspaceCoord.PushUpdateImmediate(ctx, eventType, eventPath)
}

func (s *webServer) pushWorkspaceUpdateImmediateWithChangedIDs(ctx context.Context, eventType, eventPath string, changedAssetIDs []string) {
	s.workspaceCoord.PushUpdateImmediateWithChangedIDs(ctx, eventType, eventPath, changedAssetIDs)
}

func (s *webServer) pushAssetContentUpdateImmediate(eventType, eventPath string, changedAssetIDs []string, content string) {
	s.workspaceCoord.PushAssetContentUpdateImmediate(eventType, eventPath, changedAssetIDs, content)
}

// findDirectlyChangedAssetIDs returns only the asset IDs whose source file
// matches the given event path. No downstream expansion — used for file-edit
// events where only the edited asset's inspect result would change (its SQL
// changed, but no table data changed yet).
func (s *webServer) findDirectlyChangedAssetIDs(eventPath string) []string {
	return s.workspaceCoord.FindDirectlyChangedAssetIDs(eventPath)
}

// findMaterializationInspectIDs returns the given asset IDs plus their direct
// (1-level) downstream dependents. Used after materialization — the materialized
// asset's table now has new data, so queries that read from it (direct
// downstreams) may return different results. Transitive downstreams (2+ hops)
// still read from the direct downstream's un-materialized table, so they are
// not affected for inspect purposes.
func (s *webServer) findMaterializationInspectIDs(assetIDs ...string) []string {
	return s.workspaceCoord.FindMaterializationInspectIDs(assetIDs...)
}

// findAssetNameByID looks up the asset name for a given encoded asset ID
// from the current workspace state.
func (s *webServer) findAssetNameByID(assetID string) string {
	return s.workspaceCoord.FindAssetNameByID(assetID)
}

func defaultAssetContent(assetName, assetType, assetPath string) string {
	base := service.DefaultAssetContent(assetName, assetType, assetPath)
	if strings.HasSuffix(strings.ToLower(assetPath), ".sql") {
		return fmt.Sprintf("/* @bruin\n\nname: %s\ntype: %s\nmaterialization:\n  type: view\n\n@bruin */\n", assetName, assetType)
	}
	return base
}

func defaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName string) string {
	return service.DefaultDerivedSQLAssetContent(assetName, assetType, assetPath, sourceAssetName, connectionName)
}

func ensurePythonProjectFile(absAssetPath, assetType, relAssetPath string) error {
	return service.EnsurePythonProjectFile(absAssetPath, assetType, relAssetPath)
}
