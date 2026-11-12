package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sync"

	"github.com/bruin-data/bruin/pkg/git"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"
	"go.uber.org/zap"

	"renart/internal/clientapi"
	webapi "renart/internal/web/api"
	webhttpapi "renart/internal/web/httpapi"
	"renart/internal/web/identity"
	"renart/internal/web/registry"
	"renart/internal/web/service"
)

// projectRuntime is one open project: the fully wired per-root server plus
// its own router, exactly what cmd/server.go used to wire for the single
// argv root.
type projectRuntime struct {
	id      string
	name    string
	root    string
	server  *webServer
	router  chi.Router
	cleanup func()
}

// projectManager holds one runtime per open project with lazy open. The
// default project (the argv root) is always open; others open on first
// request, keyed by the stable UUID from .renart/project.yml.
type projectManager struct {
	ctx      context.Context
	logger   *zap.Logger
	baseCfg  serverConfig
	registry *registry.Registry

	mu        sync.Mutex
	defaultID string
	runtimes  map[string]*projectRuntime
	// bootstrapDefault marks the temporary runtime used when Renart was
	// launched without a workspace. It is never registered as a user project.
	bootstrapDefault bool
	// discovery, when set, makes the manager maintain .renart/server.json in
	// every open project root so the CLI can find and delegate to this
	// server (plans/cli-v1.md §2.1).
	discovery *serverDiscovery
}

// serverDiscovery is the listen-address material for the discovery files;
// it exists only after the HTTP listener is bound.
type serverDiscovery struct {
	baseURL   string
	token     string
	startedAt time.Time
}

// registryPath resolves the projects.json location; RENART_PROJECTS_REGISTRY
// overrides it for tests.
func registryPath() (string, error) {
	if override := os.Getenv("RENART_PROJECTS_REGISTRY"); override != "" {
		return override, nil
	}
	return registry.DefaultPath()
}

func newProjectManager(ctx context.Context, logger *zap.Logger, baseCfg serverConfig, defaultRuntime *projectRuntime) (*projectManager, error) {
	regPath, err := registryPath()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project registry path: %w", err)
	}

	manager := &projectManager{
		ctx:              ctx,
		logger:           logger,
		baseCfg:          baseCfg,
		registry:         registry.New(regPath),
		defaultID:        defaultRuntime.id,
		runtimes:         map[string]*projectRuntime{defaultRuntime.id: defaultRuntime},
		bootstrapDefault: baseCfg.bootstrapRoot != "",
	}
	if !manager.bootstrapDefault {
		manager.record(defaultRuntime)
	}
	return manager, nil
}

// newProjectRuntime wires a per-root server. The returned runtime's router
// carries only routes (no middleware): the root router's middleware stack
// already ran by the time requests are rewritten into it.
func newProjectRuntime(ctx context.Context, logger *zap.Logger, cfg serverConfig) (*projectRuntime, error) {
	server, cleanup, err := newWebServer(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	server.registerRoutes(router)

	return &projectRuntime{
		id:      server.projectID,
		name:    server.projectName,
		root:    cfg.workspaceRoot,
		server:  server,
		router:  router,
		cleanup: cleanup,
	}, nil
}

func (m *projectManager) record(rt *projectRuntime) {
	if _, err := m.registry.Upsert(registry.Entry{
		ID:           rt.id,
		Name:         rt.name,
		Path:         rt.root,
		Type:         "local",
		LastOpenedAt: time.Now().UTC(),
	}); err != nil {
		m.logger.Warn("failed to record project in registry", zap.Error(err))
	}
}

// OpenProject opens the project at path (or returns the already-open
// runtime) and records it in the registry.
func (m *projectManager) OpenProject(path string) (webhttpapi.ProjectInfo, error) {
	rt, err := m.openPath(path)
	if err != nil {
		return webhttpapi.ProjectInfo{}, err
	}
	return webhttpapi.ProjectInfo{
		ID:           rt.id,
		Name:         rt.name,
		Path:         rt.root,
		Type:         "local",
		LastOpenedAt: time.Now().UTC(),
		Open:         true,
		Exists:       true,
		Default:      rt.id == m.defaultID,
	}, nil
}

// CreateProject scaffolds a project from a template and registers it. Path
// selects an existing directory (e.g. the current empty workspace);
// otherwise a new directory named req.Name is created under req.ParentDir
// (defaulting to the default project's parent directory) and gets its own
// git repository.
func (m *projectManager) CreateProject(req webhttpapi.CreateProjectRequest) (webhttpapi.CreateProjectResponse, error) {
	var target, configPath string
	newRepository := false

	if strings.TrimSpace(req.Path) != "" {
		absTarget, err := filepath.Abs(strings.TrimSpace(req.Path))
		if err != nil {
			return webhttpapi.CreateProjectResponse{}, err
		}
		info, err := os.Stat(absTarget)
		if err != nil {
			return webhttpapi.CreateProjectResponse{}, err
		}
		if !info.IsDir() {
			return webhttpapi.CreateProjectResponse{}, fmt.Errorf("%s is not a directory", absTarget)
		}
		target = absTarget
		configPath = resolveConfigFilePath(target)
	} else {
		name := strings.TrimSpace(req.Name)
		if err := validateProjectDirName(name); err != nil {
			return webhttpapi.CreateProjectResponse{}, err
		}
		parent, err := m.resolveCreateParentDir(req.ParentDir)
		if err != nil {
			return webhttpapi.CreateProjectResponse{}, err
		}
		target = filepath.Join(parent, name)
		if _, err := os.Stat(target); err == nil {
			return webhttpapi.CreateProjectResponse{}, fmt.Errorf("%s already exists", target)
		} else if !os.IsNotExist(err) {
			return webhttpapi.CreateProjectResponse{}, err
		}
		// The new directory becomes its own repository, so its config
		// belongs at its root even when an enclosing repo exists.
		configPath = filepath.Join(target, ".bruin.yml")
		newRepository = true
	}

	scaffold, err := service.ScaffoldProject(service.ScaffoldProjectRequest{
		TargetDir:     target,
		ConfigPath:    configPath,
		Template:      req.Template,
		NewRepository: newRepository,
	})
	if err != nil {
		return webhttpapi.CreateProjectResponse{}, err
	}

	rt, err := m.openPath(target)
	if err != nil {
		return webhttpapi.CreateProjectResponse{}, err
	}
	// An in-place scaffold lands in an already-open runtime whose workspace
	// predates the new files; refresh now so the response is immediately
	// reflected by /api/workspace instead of waiting on the file watcher.
	if err := rt.server.refreshWorkspace(m.ctx); err != nil {
		m.logger.Warn("failed to refresh workspace after project scaffold", zap.Error(err))
	}
	info := webhttpapi.ProjectInfo{
		ID:           rt.id,
		Name:         rt.name,
		Path:         rt.root,
		Type:         "local",
		LastOpenedAt: time.Now().UTC(),
		Open:         true,
		Exists:       true,
		Default:      rt.id == m.defaultID,
	}

	return webhttpapi.CreateProjectResponse{
		Status:         "ok",
		Project:        info,
		PipelineID:     scaffold.PipelineID,
		PipelinePath:   scaffold.PipelinePath,
		Files:          scaffold.Files,
		GitInitialized: scaffold.GitInitialized,
	}, nil
}

// resolveCreateParentDir picks the directory a new project is created in:
// the caller's choice, else next to the default project, else the home dir.
func (m *projectManager) resolveCreateParentDir(parentDir string) (string, error) {
	parent := strings.TrimSpace(parentDir)
	if parent == "" && m.bootstrapDefault {
		parent = strings.TrimSpace(m.baseCfg.suggestedCreateParentDir)
	}
	if parent == "" {
		m.mu.Lock()
		defaultRuntime := m.runtimes[m.defaultID]
		m.mu.Unlock()
		if defaultRuntime != nil {
			parent = filepath.Dir(defaultRuntime.root)
		}
	}
	if parent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		parent = home
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absParent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absParent)
	}
	return absParent, nil
}

// SuggestedCreateParentDir exposes the same default used by CreateProject so
// the welcome screen can show the effective location before the user submits.
func (m *projectManager) SuggestedCreateParentDir() (string, error) {
	return m.resolveCreateParentDir("")
}

// CreateDirectory creates one visible child directory for the process-level
// directory picker. Nested paths are deliberately rejected: navigation and
// creation stay separate, reviewable actions in the UI.
func (m *projectManager) CreateDirectory(parentDir, name string) (string, error) {
	parent := strings.TrimSpace(parentDir)
	if parent == "" {
		return "", fmt.Errorf("parent directory is required")
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absParent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absParent)
	}

	dirName := strings.TrimSpace(name)
	if dirName == "" {
		return "", fmt.Errorf("directory name is required")
	}
	if dirName == "." || dirName == ".." || strings.HasPrefix(dirName, ".") {
		return "", fmt.Errorf("directory name must not start with a dot")
	}
	if strings.ContainsAny(dirName, `/\\`) || filepath.Base(dirName) != dirName {
		return "", fmt.Errorf("directory name must not contain path separators")
	}

	target := filepath.Join(absParent, dirName)
	if err := os.Mkdir(target, 0o755); err != nil {
		return "", err
	}
	return target, nil
}

func validateProjectDirName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	if len(name) > 100 {
		return fmt.Errorf("project name is too long")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("project name must not start with a dot")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("project name may only contain letters, digits, dots, dashes and underscores")
		}
	}
	return nil
}

func (m *projectManager) openPath(path string) (*projectRuntime, error) {
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absRoot)
	}
	if _, err := git.FindRepoFromPath(absRoot); err != nil {
		return nil, fmt.Errorf("projects must live inside a git repository: %w", err)
	}

	// Resolve identity first so an already-open project (even under a
	// different path spelling) reuses its runtime.
	project, err := identity.EnsureProject(
		afero.NewOsFs(),
		filepath.Join(absRoot, ".renart", "project.yml"),
		filepath.Base(absRoot),
	)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if rt, ok := m.runtimes[project.ID]; ok {
		m.record(rt)
		return rt, nil
	}

	cfg := m.baseCfg
	cfg.workspaceRoot = absRoot
	cfg.schedulerStatePath = filepath.Join(absRoot, ".renart", "state.db")
	cfg.bootstrapRoot = ""
	cfg.suggestedCreateParentDir = ""

	rt, err := newProjectRuntime(m.ctx, m.logger, cfg)
	if err != nil {
		return nil, err
	}
	m.runtimes[rt.id] = rt
	m.record(rt)
	m.writeDiscoveryFileLocked(rt)
	m.logger.Info("opened project runtime",
		zap.String("project_id", rt.id),
		zap.String("root", rt.root),
	)
	return rt, nil
}

// runtimeByID returns the open runtime for id, lazily opening it from its
// registered path when needed.
func (m *projectManager) runtimeByID(id string) (*projectRuntime, error) {
	m.mu.Lock()
	rt, ok := m.runtimes[id]
	m.mu.Unlock()
	if ok {
		return rt, nil
	}

	file, err := m.registry.Load()
	if err != nil {
		return nil, err
	}
	for _, entry := range file.Projects {
		if entry.ID != id {
			continue
		}
		rt, err := m.openPath(entry.Path)
		if err != nil {
			return nil, err
		}
		if rt.id != id {
			return nil, fmt.Errorf("project at %s now identifies as %s (expected %s); its project.yml changed", entry.Path, rt.id, id)
		}
		return rt, nil
	}
	return nil, fmt.Errorf("unknown project %s", id)
}

func (m *projectManager) ListProjects() webhttpapi.ProjectListResponse {
	response := webhttpapi.ProjectListResponse{
		Status:           "ok",
		DefaultProjectID: m.defaultID,
		Bootstrap:        m.bootstrapDefault,
		Projects:         []webhttpapi.ProjectInfo{},
	}

	file, err := m.registry.Load()
	if err != nil {
		m.logger.Warn("failed to load project registry", zap.Error(err))
		return response
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range file.Projects {
		_, open := m.runtimes[entry.ID]
		exists := false
		if info, err := os.Stat(entry.Path); err == nil && info.IsDir() {
			exists = true
		}
		response.Projects = append(response.Projects, webhttpapi.ProjectInfo{
			ID:           entry.ID,
			Name:         entry.Name,
			Path:         entry.Path,
			Type:         entry.Type,
			LastOpenedAt: entry.LastOpenedAt,
			Open:         open,
			Exists:       exists,
			Default:      entry.ID == m.defaultID,
		})
	}
	return response
}

func (m *projectManager) RemoveProject(id string) error {
	if id == m.defaultID {
		return fmt.Errorf("cannot remove the project this server was started on")
	}
	m.mu.Lock()
	_, open := m.runtimes[id]
	m.mu.Unlock()
	if open {
		return fmt.Errorf("project is currently open; close its tabs first")
	}
	_, err := m.registry.Remove(id)
	return err
}

// mountProjectRoutes exposes every open (or lazily openable) project under
// /api/projects/{projectID}/... by rewriting the path back to the /api/...
// shape the per-project routers expect. The unprefixed /api/* routes stay
// aliased to the default project so existing pages and the e2e suite keep
// working during the migration.
func (m *projectManager) mountProjectRoutes(router chi.Router) {
	webhttpapi.RegisterProjectRoutes(router, &webhttpapi.ProjectsAPI{Directory: m})

	router.HandleFunc("/api/projects/{projectID}/*", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "projectID")
		rt, err := m.runtimeByID(id)
		if err != nil {
			webapi.WriteNotFound(w, "project_not_found", err.Error())
			return
		}

		rest := chi.URLParam(r, "*")
		proxied := r.Clone(r.Context())
		proxied.URL.Path = "/api/" + rest
		proxied.URL.RawPath = ""
		// The per-project router must match from scratch, not inherit this
		// route's chi context.
		routeCtx := chi.NewRouteContext()
		proxied = proxied.WithContext(context.WithValue(proxied.Context(), chi.RouteCtxKey, routeCtx))
		rt.router.ServeHTTP(w, proxied)
	})
}

// buildRootRouter assembles the process-level router: middleware, project
// directory routes, per-project mounts, and the default project's routes
// (API alias + static assets) at the root.
func buildRootRouter(manager *projectManager, defaultRuntime *projectRuntime, access webhttpapi.AccessOptions) chi.Router {
	return newRootRouter(defaultRuntime.server.logger, access, manager.mountProjectRoutes, defaultRuntime.server.registerRoutes)
}

// newRootRouter applies the middleware stack ahead of every route the
// register functions add, so the guard sees the API, the project mounts and
// the static files alike.
func newRootRouter(logger *zap.Logger, access webhttpapi.AccessOptions, register ...func(chi.Router)) chi.Router {
	router := chi.NewRouter()
	router.Use(
		webhttpapi.Recoverer(logger),
		webhttpapi.SameOriginGuardWithOptions(access),
		webhttpapi.RequestLogger(logger),
	)
	for _, fn := range register {
		fn(router)
	}
	return router
}

// closeAll tears down every runtime except the default one, whose cleanup
// the caller already owns.
func (m *projectManager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.runtimes {
		if id == m.defaultID {
			continue
		}
		rt.cleanup()
		delete(m.runtimes, id)
	}
}

// EnableDiscovery starts maintaining .renart/server.json in every open (and
// subsequently opened) project root. Call once the listener is bound.
func (m *projectManager) EnableDiscovery(baseURL, token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discovery = &serverDiscovery{baseURL: baseURL, token: token, startedAt: time.Now().UTC()}
	for _, rt := range m.runtimes {
		m.writeDiscoveryFileLocked(rt)
	}
}

// DisableDiscovery removes the discovery files this server wrote; defer it
// next to the runtime cleanups so a graceful shutdown leaves no stale file.
func (m *projectManager) DisableDiscovery() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.discovery == nil {
		return
	}
	for _, rt := range m.runtimes {
		clientapi.RemoveServerFile(rt.root)
	}
	m.discovery = nil
}

// writeDiscoveryFileLocked records this server in rt's workspace. The API
// base is the project mount, so the CLI addresses the right runtime no
// matter which project it discovered. Best-effort: a read-only workspace
// just misses out on delegation.
func (m *projectManager) writeDiscoveryFileLocked(rt *projectRuntime) {
	if m.discovery == nil {
		return
	}
	if m.bootstrapDefault && rt.id == m.defaultID {
		return
	}
	err := clientapi.WriteServerFile(rt.root, clientapi.ServerFile{
		PID:           os.Getpid(),
		BaseURL:       m.discovery.baseURL,
		APIBaseURL:    m.discovery.baseURL + "/api/projects/" + rt.id,
		ProjectID:     rt.id,
		WorkspaceRoot: rt.root,
		Version:       buildVersion,
		Token:         m.discovery.token,
		StartedAt:     m.discovery.startedAt,
	})
	if err != nil {
		m.logger.Warn("failed to write server discovery file", zap.String("root", rt.root), zap.Error(err))
	}
}
