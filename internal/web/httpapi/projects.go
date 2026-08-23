package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

// ProjectInfo describes one project known to this server: a registry entry
// enriched with runtime state.
type ProjectInfo struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Type         string    `json:"type"`
	LastOpenedAt time.Time `json:"last_opened_at"`
	Open         bool      `json:"open"`
	Exists       bool      `json:"exists"`
	Default      bool      `json:"default"`
}

// renart:web
type ProjectListResponse struct {
	Status           string        `json:"status"`
	DefaultProjectID string        `json:"default_project_id"`
	Bootstrap        bool          `json:"bootstrap"`
	Projects         []ProjectInfo `json:"projects"`
}

type OpenProjectRequest struct {
	Path string `json:"path"`
}

// renart:web
type OpenProjectResponse struct {
	Status  string      `json:"status"`
	Project ProjectInfo `json:"project"`
}

// CreateProjectRequest scaffolds a new project from a template. Either Path
// (scaffold into an existing directory, e.g. the current empty workspace) or
// ParentDir+Name (create a fresh directory) selects the target.
// renart:web
type CreateProjectRequest struct {
	Template  string `json:"template"`
	Name      string `json:"name,omitempty"`
	ParentDir string `json:"parent_dir,omitempty"`
	Path      string `json:"path,omitempty"`
}

// renart:web
type CreateProjectResponse struct {
	Status         string      `json:"status"`
	Project        ProjectInfo `json:"project"`
	PipelineID     string      `json:"pipeline_id"`
	PipelinePath   string      `json:"pipeline_path"`
	Files          []string    `json:"files"`
	GitInitialized bool        `json:"git_initialized"`
}

type BrowseDirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsProject bool   `json:"is_project"`
}

// renart:web
type BrowseDirsResponse struct {
	Status  string           `json:"status"`
	Path    string           `json:"path"`
	Parent  string           `json:"parent,omitempty"`
	Entries []BrowseDirEntry `json:"entries"`
}

// renart:web
type CreateDirectoryRequest struct {
	ParentDir string `json:"parent_dir"`
	Name      string `json:"name"`
}

// renart:web
type CreateDirectoryResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ProjectDirectory is the project manager as seen by the HTTP layer.
type ProjectDirectory interface {
	ListProjects() ProjectListResponse
	OpenProject(path string) (ProjectInfo, error)
	CreateProject(req CreateProjectRequest) (CreateProjectResponse, error)
	SuggestedCreateParentDir() (string, error)
	CreateDirectory(parentDir, name string) (string, error)
	RemoveProject(id string) error
}

type ProjectsAPI struct {
	Directory ProjectDirectory
}

func RegisterProjectRoutes(router chi.Router, api *ProjectsAPI) {
	router.Get("/api/projects", api.HandleListProjects)
	router.Post("/api/projects", api.HandleCreateProject)
	router.Get("/api/projects/templates", api.HandleProjectTemplates)
	router.Post("/api/projects/open", api.HandleOpenProject)
	router.Get("/api/projects/browse", api.HandleBrowseDirs)
	router.Post("/api/projects/directories", api.HandleCreateDirectory)
	router.Delete("/api/projects/{projectID}", api.HandleRemoveProject)
}

func (a *ProjectsAPI) HandleProjectTemplates(w http.ResponseWriter, _ *http.Request) {
	webapi.WriteJSON(w, http.StatusOK, service.ProjectTemplatesResponse{
		Status:    "ok",
		Templates: service.ProjectTemplates(),
	})
}

func (a *ProjectsAPI) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[CreateProjectRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if strings.TrimSpace(req.Template) == "" {
		webapi.WriteBadRequest(w, "missing_template", "template is required")
		return
	}
	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.Name) == "" {
		webapi.WriteBadRequest(w, "missing_target", "either path or name is required")
		return
	}

	response, err := a.Directory.CreateProject(req)
	if err != nil {
		webapi.WriteBadRequest(w, "project_create_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (a *ProjectsAPI) HandleListProjects(w http.ResponseWriter, _ *http.Request) {
	webapi.WriteJSON(w, http.StatusOK, a.Directory.ListProjects())
}

func (a *ProjectsAPI) HandleOpenProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[OpenProjectRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		webapi.WriteBadRequest(w, "missing_path", "path is required")
		return
	}

	info, err := a.Directory.OpenProject(path)
	if err != nil {
		webapi.WriteBadRequest(w, "project_open_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, OpenProjectResponse{Status: "ok", Project: info})
}

func (a *ProjectsAPI) HandleRemoveProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "projectID"))
	if id == "" {
		webapi.WriteBadRequest(w, "missing_project_id", "project id is required")
		return
	}
	if err := a.Directory.RemoveProject(id); err != nil {
		webapi.WriteBadRequest(w, "project_remove_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, a.Directory.ListProjects())
}

// HandleBrowseDirs lists subdirectories for the "Open project" picker. It
// only reveals directory names the OS user can read anyway; the server is
// loopback-bound and origin-guarded like every other route.
func (a *ProjectsAPI) HandleBrowseDirs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		var err error
		if strings.TrimSpace(r.URL.Query().Get("purpose")) == "create" {
			path, err = a.Directory.SuggestedCreateParentDir()
		} else {
			path, err = os.UserHomeDir()
		}
		if err != nil {
			webapi.WriteInternalError(w, "default_directory_unavailable", err.Error())
			return
		}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_path", err.Error())
		return
	}

	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		webapi.WriteBadRequest(w, "path_unreadable", err.Error())
		return
	}

	response := BrowseDirsResponse{Status: "ok", Path: absPath, Entries: []BrowseDirEntry{}}
	if parent := filepath.Dir(absPath); parent != absPath {
		response.Parent = parent
	}
	for _, entry := range dirEntries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		childPath := filepath.Join(absPath, entry.Name())
		response.Entries = append(response.Entries, BrowseDirEntry{
			Name:      entry.Name(),
			Path:      childPath,
			IsProject: looksLikeProject(childPath),
		})
	}
	sort.Slice(response.Entries, func(i, j int) bool {
		return response.Entries[i].Name < response.Entries[j].Name
	})

	webapi.WriteJSON(w, http.StatusOK, response)
}

func (a *ProjectsAPI) HandleCreateDirectory(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[CreateDirectoryRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	path, err := a.Directory.CreateDirectory(
		strings.TrimSpace(req.ParentDir),
		strings.TrimSpace(req.Name),
	)
	if err != nil {
		webapi.WriteBadRequest(w, "directory_create_failed", err.Error())
		return
	}

	webapi.WriteJSON(w, http.StatusCreated, CreateDirectoryResponse{Status: "ok", Path: path})
}

func looksLikeProject(dir string) bool {
	for _, marker := range []string{".bruin.yml", filepath.Join(".renart", "project.yml")} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}
