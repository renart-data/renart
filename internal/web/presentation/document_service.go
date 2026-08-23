package presentation

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"renart/internal/web/apperror"
	"renart/internal/web/model"
	"renart/internal/web/workspacefs"
)

const maxDefinitionBytes = 2 << 20

// DocumentDependencies are the adapters needed by the Git-backed presentation
// document application service. Schema enrichment remains injected because it
// spans workspace SQL intelligence; document identity, validation, CAS, and
// persistence belong to this domain.
type DocumentDependencies struct {
	WorkspaceRoot       string
	Enrich              func(context.Context, *Artifact) map[string]ResolvedSchema
	PushWorkspaceUpdate func(ctx context.Context, eventType, eventPath string)
}

// DocumentService owns the lifecycle of Git-authored dashboard and report
// files. It deliberately stays independent of HTTP routing and warehouse query
// execution.
type DocumentService struct {
	deps DocumentDependencies
	mu   sync.Mutex
}

// renart:web
type PresentationDocument struct {
	Artifact model.PresentationArtifact `json:"artifact"`
	Content  string                     `json:"content"`
}

// renart:web
type CreatePresentationRequest struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// renart:web
type UpdatePresentationRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Content          string `json:"content"`
}

// renart:web
type ReplacePresentationRequest struct {
	ExpectedRevision string                     `json:"expected_revision"`
	Artifact         model.PresentationArtifact `json:"artifact"`
}

func NewDocumentService(deps DocumentDependencies) *DocumentService {
	return &DocumentService{deps: deps}
}

func (s *DocumentService) Get(ctx context.Context, workspaceID string) (PresentationDocument, *apperror.Error) {
	artifact, content, apiErr := s.readArtifact(workspaceID)
	if apiErr != nil {
		return PresentationDocument{}, apiErr
	}
	datasetSchemas := s.enrich(ctx, artifact)
	return PresentationDocument{
		Artifact: ArtifactToModel(s.deps.WorkspaceRoot, artifact, datasetSchemas),
		Content:  string(content),
	}, nil
}

// LoadRuntimeArtifact loads and enriches the saved artifact used by the query
// runtime without exposing document path resolution to the service facade.
func (s *DocumentService) LoadRuntimeArtifact(ctx context.Context, workspaceID string) (*Artifact, *apperror.Error) {
	artifact, _, apiErr := s.readArtifact(workspaceID)
	if apiErr != nil {
		return nil, apiErr
	}
	s.enrich(ctx, artifact)
	return artifact, nil
}

// PreparePreview validates an unsaved typed snapshot against the saved
// document revision and returns its normalized, enriched runtime artifact.
func (s *DocumentService) PreparePreview(
	ctx context.Context,
	workspaceID string,
	expectedRevision string,
	input model.PresentationArtifact,
) (*Artifact, *apperror.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, _, apiErr := s.readArtifact(workspaceID)
	if apiErr != nil {
		return nil, apiErr
	}
	if strings.TrimSpace(expectedRevision) == "" || expectedRevision != current.Revision {
		return nil, &apperror.Error{
			Status: http.StatusConflict, Code: "presentation_preview_conflict",
			Message: "This presentation changed after preview began. Reload the latest file before running the draft.",
		}
	}
	draft, err := ArtifactFromModel(current.Path, input)
	if err != nil {
		return nil, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if draft.ID != current.ID || draft.Kind != current.Kind {
		return nil, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_identity_immutable",
			Message: "Presentation identity and kind cannot be changed while previewing a draft.",
		}
	}
	normalized, err := MarshalArtifact(*draft)
	if err != nil {
		return nil, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	draft, err = DecodeArtifact(current.Path, normalized)
	if err != nil {
		return nil, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	s.enrich(ctx, draft)
	return draft, nil
}

func (s *DocumentService) Create(ctx context.Context, request CreatePresentationRequest) (PresentationDocument, *apperror.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kind := ArtifactKind(strings.ToLower(strings.TrimSpace(request.Kind)))
	if kind != ArtifactKindDashboard && kind != ArtifactKindReport {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_kind_invalid", Message: "kind must be dashboard or report"}
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_title_required", Message: "title is required"}
	}
	if len(title) > 160 {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_title_too_long", Message: "title cannot exceed 160 characters"}
	}

	baseID := presentationIDFromTitle(title)
	directory := "dashboards"
	suffix := ".dashboard.yml"
	if kind == ArtifactKindReport {
		directory = "reports"
		suffix = ".report.yml"
	}
	id, path := baseID, ""
	for attempt := 1; ; attempt++ {
		candidate := id
		if attempt > 1 {
			candidate = fmt.Sprintf("%s_%d", baseID, attempt)
		}
		candidatePath, err := workspacefs.Join(s.deps.WorkspaceRoot, filepath.Join(directory, candidate+suffix))
		if err != nil {
			return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_path_invalid", Message: err.Error()}
		}
		if _, err := os.Stat(candidatePath); os.IsNotExist(err) {
			id, path = candidate, candidatePath
			break
		} else if err != nil {
			return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
		}
	}

	artifact := Artifact{
		Version: ArtifactVersionCurrent, Kind: kind, ID: id, Title: title,
		Datasets: map[string]DatasetDefinition{},
	}
	content, err := MarshalArtifact(artifact)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
	}
	if err := workspacefs.WriteFileAtomic(path, content, 0o644); err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-created", path)
	return s.documentFromBytes(ctx, path, content)
}

func (s *DocumentService) Update(
	ctx context.Context,
	workspaceID string,
	request UpdatePresentationRequest,
) (PresentationDocument, *apperror.Error) {
	if len(request.Content) > maxDefinitionBytes {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusRequestEntityTooLarge, Code: "presentation_too_large", Message: "presentation definition exceeds 2 MiB"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return PresentationDocument{}, apiErr
	}
	currentContent, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PresentationDocument{}, &apperror.Error{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	current, err := DecodeArtifact(path, currentContent)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	if strings.TrimSpace(request.ExpectedRevision) == "" || request.ExpectedRevision != current.Revision {
		return PresentationDocument{}, &apperror.Error{
			Status: http.StatusConflict, Code: "presentation_edit_conflict",
			Message: "This presentation changed after editing began. Your draft was kept; reload or reconcile the newer content before saving.",
		}
	}
	nextContent := []byte(request.Content)
	next, err := DecodeArtifact(path, nextContent)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_draft_invalid", Message: err.Error()}
	}
	if next.ID != current.ID {
		return PresentationDocument{}, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_id_immutable",
			Message: "Presentation identity cannot be changed from the definition editor.",
		}
	}
	if string(currentContent) == request.Content {
		datasetSchemas := s.enrich(ctx, current)
		return PresentationDocument{Artifact: ArtifactToModel(s.deps.WorkspaceRoot, current, datasetSchemas), Content: request.Content}, nil
	}
	if err := workspacefs.WriteFileAtomic(path, nextContent, 0o644); err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_update_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-updated", path)
	return s.documentFromBytes(ctx, path, nextContent)
}

// Replace applies a typed visual-editor snapshot at the same file revision
// boundary used by the raw definition editor.
func (s *DocumentService) Replace(
	ctx context.Context,
	workspaceID string,
	request ReplacePresentationRequest,
) (PresentationDocument, *apperror.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return PresentationDocument{}, apiErr
	}
	currentContent, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PresentationDocument{}, &apperror.Error{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	current, err := DecodeArtifact(path, currentContent)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	if strings.TrimSpace(request.ExpectedRevision) == "" || request.ExpectedRevision != current.Revision {
		return PresentationDocument{}, &apperror.Error{
			Status: http.StatusConflict, Code: "presentation_edit_conflict",
			Message: "This presentation changed after editing began. Reload it before applying visual changes.",
		}
	}
	next, err := ArtifactFromModel(path, request.Artifact)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if next.ID != current.ID || next.Kind != current.Kind {
		return PresentationDocument{}, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_identity_immutable",
			Message: "Presentation identity and kind cannot be changed from the visual editor.",
		}
	}
	content, err := MarshalArtifact(*next)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if string(content) == string(currentContent) {
		datasetSchemas := s.enrich(ctx, current)
		return PresentationDocument{Artifact: ArtifactToModel(s.deps.WorkspaceRoot, current, datasetSchemas), Content: string(currentContent)}, nil
	}
	if err := workspacefs.WriteFileAtomic(path, content, 0o644); err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_update_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-updated", path)
	return s.documentFromBytes(ctx, path, content)
}

func (s *DocumentService) documentFromBytes(ctx context.Context, path string, content []byte) (PresentationDocument, *apperror.Error) {
	artifact, err := DecodeArtifact(path, content)
	if err != nil {
		return PresentationDocument{}, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	datasetSchemas := s.enrich(ctx, artifact)
	return PresentationDocument{Artifact: ArtifactToModel(s.deps.WorkspaceRoot, artifact, datasetSchemas), Content: string(content)}, nil
}

func (s *DocumentService) readArtifact(workspaceID string) (*Artifact, []byte, *apperror.Error) {
	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, &apperror.Error{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return nil, nil, &apperror.Error{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	artifact, err := DecodeArtifact(path, content)
	if err != nil {
		return nil, nil, &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	return artifact, content, nil
}

func (s *DocumentService) enrich(ctx context.Context, artifact *Artifact) map[string]ResolvedSchema {
	if s.deps.Enrich != nil {
		return s.deps.Enrich(ctx, artifact)
	}
	schemas := map[string]ResolvedSchema{}
	artifact.Problems = (Checker{}).CheckArtifact(ctx, *artifact, schemas, CheckOptions{Strict: true})
	return schemas
}

func (s *DocumentService) resolvePath(workspaceID string) (string, *apperror.Error) {
	relPath, err := workspacefs.DecodePathID(strings.TrimSpace(workspaceID))
	if err != nil {
		return "", &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: "invalid presentation id"}
	}
	path, err := workspacefs.Join(s.deps.WorkspaceRoot, relPath)
	if err != nil {
		return "", &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: err.Error()}
	}
	name := strings.ToLower(filepath.Base(path))
	if name != "dashboard.yml" && name != "report.yml" &&
		!strings.HasSuffix(name, ".dashboard.yml") && !strings.HasSuffix(name, ".report.yml") {
		return "", &apperror.Error{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: "presentation path has an unsupported filename"}
	}
	return path, nil
}

func (s *DocumentService) pushUpdate(ctx context.Context, eventType, path string) {
	if s.deps.PushWorkspaceUpdate != nil {
		s.deps.PushWorkspaceUpdate(ctx, eventType, path)
	}
}

var presentationIDCleanup = regexp.MustCompile(`[^a-z0-9]+`)

func presentationIDFromTitle(title string) string {
	id := presentationIDCleanup.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "_")
	id = strings.Trim(id, "_")
	if id == "" {
		return "presentation"
	}
	if id[0] < 'a' || id[0] > 'z' {
		id = "presentation_" + id
	}
	return id
}
