// Package notebookdoc owns the Git-authored notebook document lifecycle below
// the broad service compatibility facade. Runtime sessions, transfers,
// promotion, and agents remain separate adapters.
package notebookdoc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"renart/internal/sqlintelligence"
	"renart/internal/web/apperror"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/workspacefs"
)

type ModelMetadata struct {
	Dependencies     []string
	InstalledModules []string
}

type Dependencies struct {
	WorkspaceRoot string
	NewLoader     func() *notebook.Loader
	ModelMetadata func(*notebook.Notebook) ModelMetadata

	PipelineAssetNames        func() map[string]bool
	PushWorkspaceUpdate       func(path string)
	RemoveSession             func(notebookUUID string) error
	DropCellObjects           func(notebookUUID, cellID string) error
	OnCellChanged             func(notebookID string, nb *notebook.Notebook, cellID string)
	OnCellDeleted             func(notebookID, notebookUUID, cellID string)
	OnParametersChanged       func(notebookID string, nb *notebook.Notebook)
	CheckVisualizations       func(context.Context, *notebook.Notebook) (problems, blocking []string)
	ValidateStorageConnection func(connection string) *apperror.Error
	ResolveSourceAssetType    func(connection string) (string, *apperror.Error)
}

// Service is the single in-process authority for loading and locking authored
// notebook snapshots. All mutation paths must acquire these locks before
// checking revisions or writing manifest/cell files.
type Service struct {
	deps Dependencies

	cellMu    sync.Mutex
	cellLocks map[string]*documentLock

	notebookMu    sync.Mutex
	notebookLocks map[string]*documentLock
}

type documentLock struct {
	mu   sync.Mutex
	refs int
}

func New(deps Dependencies) *Service {
	return &Service{
		deps:          deps,
		cellLocks:     make(map[string]*documentLock),
		notebookLocks: make(map[string]*documentLock),
	}
}

func (s *Service) WorkspaceRoot() string {
	if s == nil {
		return ""
	}
	return s.deps.WorkspaceRoot
}

func (s *Service) LockCell(notebookID, cellID string) func() {
	return lockDocument(&s.cellMu, s.cellLocks, notebookID+":"+cellID)
}

func (s *Service) LockNotebook(notebookID string) func() {
	return lockDocument(&s.notebookMu, s.notebookLocks, notebookID)
}

func lockDocument(guard *sync.Mutex, locks map[string]*documentLock, key string) func() {
	guard.Lock()
	lock, ok := locks[key]
	if !ok {
		lock = &documentLock{}
		locks[key] = lock
	}
	lock.refs++
	guard.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		guard.Lock()
		lock.refs--
		if lock.refs == 0 && locks[key] == lock {
			delete(locks, key)
		}
		guard.Unlock()
	}
}

// ResolveDir maps an encoded notebook route ID to an existing notebook folder
// inside the configured Git workspace.
func (s *Service) ResolveDir(notebookID string) (string, *apperror.Error) {
	if s == nil {
		return "", &apperror.Error{Status: http.StatusInternalServerError, Code: "notebook_service_unavailable", Message: "notebook document service is unavailable"}
	}
	relDir, err := workspacefs.DecodePathID(notebookID)
	if err != nil {
		return "", &apperror.Error{Status: http.StatusBadRequest, Code: "invalid_notebook_id", Message: "invalid notebook id"}
	}
	absDir, err := workspacefs.Join(s.deps.WorkspaceRoot, relDir)
	if err != nil {
		return "", &apperror.Error{Status: http.StatusBadRequest, Code: "invalid_notebook_id", Message: "notebook path escapes the workspace"}
	}
	if _, err := os.Stat(filepath.Join(absDir, notebook.ManifestFileName)); err != nil {
		return "", &apperror.Error{Status: http.StatusNotFound, Code: "notebook_not_found", Message: "notebook not found"}
	}
	return absDir, nil
}

func (s *Service) Load(notebookID string) (*notebook.Notebook, *apperror.Error) {
	absDir, apiErr := s.ResolveDir(notebookID)
	if apiErr != nil {
		return nil, apiErr
	}
	if s.deps.NewLoader == nil {
		return nil, &apperror.Error{Status: http.StatusInternalServerError, Code: "notebook_service_unavailable", Message: "notebook loader is unavailable"}
	}
	loader := s.deps.NewLoader()
	if loader == nil {
		return nil, &apperror.Error{Status: http.StatusInternalServerError, Code: "notebook_service_unavailable", Message: "notebook loader is unavailable"}
	}
	nb, err := loader.Load(absDir)
	if err != nil {
		return nil, &apperror.Error{Status: http.StatusBadRequest, Code: "notebook_load_failed", Message: err.Error()}
	}
	return nb, nil
}

func (s *Service) Get(notebookID string) (model.Notebook, *apperror.Error) {
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	return s.ToModel(nb), nil
}

func (s *Service) ToModel(nb *notebook.Notebook) model.Notebook {
	metadata := ModelMetadata{}
	if s != nil && s.deps.ModelMetadata != nil {
		metadata = s.deps.ModelMetadata(nb)
	}
	return ToModel(s.WorkspaceRoot(), nb, metadata)
}

func (s *Service) newLoader() (*notebook.Loader, func()) {
	if s == nil || s.deps.NewLoader == nil {
		return nil, func() {}
	}
	return s.deps.NewLoader(), func() {}
}

func (s *Service) renameTables(sqlText, dialect string, mapping map[string]string) (string, error) {
	return sqlintelligence.RenameTables(sqlText, dialect, mapping)
}

func (s *Service) notebookVisualizationProblems(ctx context.Context, nb *notebook.Notebook) (problems, blocking []string) {
	if s == nil || s.deps.CheckVisualizations == nil {
		return nil, nil
	}
	return s.deps.CheckVisualizations(ctx, nb)
}

func (s *Service) onCellChanged(notebookID string, nb *notebook.Notebook, cellID string) {
	if s != nil && s.deps.OnCellChanged != nil {
		s.deps.OnCellChanged(notebookID, nb, cellID)
	}
}

func (s *Service) onNotebookParametersChanged(notebookID string, nb *notebook.Notebook) {
	if s != nil && s.deps.OnParametersChanged != nil {
		s.deps.OnParametersChanged(notebookID, nb)
	}
}

func (s *Service) dropCellObjects(notebookUUID, cellID string) error {
	if s == nil || s.deps.DropCellObjects == nil {
		return nil
	}
	return s.deps.DropCellObjects(notebookUUID, cellID)
}

func (s *Service) forgetCell(notebookID, notebookUUID, cellID string) {
	if s != nil && s.deps.OnCellDeleted != nil {
		s.deps.OnCellDeleted(notebookID, notebookUUID, cellID)
	}
}
