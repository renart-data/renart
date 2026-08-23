package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

const maxPresentationDefinitionBytes = 2 << 20

type PresentationDependencies struct {
	WorkspaceRoot        string
	ConfigPath           string
	CurrentState         func() model.WorkspaceState
	ResolveAssetByID     func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	NewConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
	RunConnectionQuery   func(context.Context, string, string, string) ([]string, []map[string]any, error)
	PushWorkspaceUpdate  func(ctx context.Context, eventType, eventPath string)
}

type PresentationService struct {
	deps PresentationDependencies
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

func NewPresentationService(deps PresentationDependencies) *PresentationService {
	return &PresentationService{deps: deps}
}

func (s *PresentationService) Get(ctx context.Context, workspaceID string) (PresentationDocument, *APIError) {
	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return PresentationDocument{}, apiErr
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PresentationDocument{}, &APIError{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	artifact, err := presentation.DecodeArtifact(path, content)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	datasetSchemas := s.enrichProblems(ctx, artifact)
	return PresentationDocument{
		Artifact: presentationToModel(s.deps.WorkspaceRoot, artifact, datasetSchemas),
		Content:  string(content),
	}, nil
}

func (s *PresentationService) Create(ctx context.Context, request CreatePresentationRequest) (PresentationDocument, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kind := presentation.ArtifactKind(strings.ToLower(strings.TrimSpace(request.Kind)))
	if kind != presentation.ArtifactKindDashboard && kind != presentation.ArtifactKindReport {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_kind_invalid", Message: "kind must be dashboard or report"}
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_title_required", Message: "title is required"}
	}
	if len(title) > 160 {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_title_too_long", Message: "title cannot exceed 160 characters"}
	}

	baseID := presentationIDFromTitle(title)
	directory := "dashboards"
	suffix := ".dashboard.yml"
	if kind == presentation.ArtifactKindReport {
		directory = "reports"
		suffix = ".report.yml"
	}
	id, path := baseID, ""
	for attempt := 1; ; attempt++ {
		candidate := id
		if attempt > 1 {
			candidate = fmt.Sprintf("%s_%d", baseID, attempt)
		}
		candidatePath, err := SafeJoin(s.deps.WorkspaceRoot, filepath.Join(directory, candidate+suffix))
		if err != nil {
			return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_path_invalid", Message: err.Error()}
		}
		if _, err := os.Stat(candidatePath); os.IsNotExist(err) {
			id, path = candidate, candidatePath
			break
		} else if err != nil {
			return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
		}
	}

	artifact := presentation.Artifact{
		Version: presentation.ArtifactVersionCurrent, Kind: kind, ID: id, Title: title,
		Datasets: map[string]presentation.DatasetDefinition{},
	}
	content, err := presentation.MarshalArtifact(artifact)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
	}
	if err := writeFileAtomically(path, content, 0o644); err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_create_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-created", path)
	return s.documentFromBytes(ctx, path, content)
}

func (s *PresentationService) Update(
	ctx context.Context,
	workspaceID string,
	request UpdatePresentationRequest,
) (PresentationDocument, *APIError) {
	if len(request.Content) > maxPresentationDefinitionBytes {
		return PresentationDocument{}, &APIError{Status: http.StatusRequestEntityTooLarge, Code: "presentation_too_large", Message: "presentation definition exceeds 2 MiB"}
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
			return PresentationDocument{}, &APIError{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	current, err := presentation.DecodeArtifact(path, currentContent)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	if strings.TrimSpace(request.ExpectedRevision) == "" || request.ExpectedRevision != current.Revision {
		return PresentationDocument{}, &APIError{
			Status: http.StatusConflict, Code: "presentation_edit_conflict",
			Message: "This presentation changed after editing began. Your draft was kept; reload or reconcile the newer content before saving.",
		}
	}
	nextContent := []byte(request.Content)
	next, err := presentation.DecodeArtifact(path, nextContent)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_draft_invalid", Message: err.Error()}
	}
	if next.ID != current.ID {
		return PresentationDocument{}, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_id_immutable",
			Message: "Presentation identity cannot be changed from the definition editor.",
		}
	}
	if string(currentContent) == request.Content {
		datasetSchemas := s.enrichProblems(ctx, current)
		return PresentationDocument{Artifact: presentationToModel(s.deps.WorkspaceRoot, current, datasetSchemas), Content: request.Content}, nil
	}
	if err := writeFileAtomically(path, nextContent, 0o644); err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_update_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-updated", path)
	return s.documentFromBytes(ctx, path, nextContent)
}

// Replace applies a typed visual-editor snapshot and serializes it on the
// server. It shares the same file revision boundary as the raw Definition
// editor, so switching editor modes cannot create a second write model.
func (s *PresentationService) Replace(
	ctx context.Context,
	workspaceID string,
	request ReplacePresentationRequest,
) (PresentationDocument, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, apiErr := s.resolvePath(workspaceID)
	if apiErr != nil {
		return PresentationDocument{}, apiErr
	}
	currentContent, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PresentationDocument{}, &APIError{Status: http.StatusNotFound, Code: "presentation_not_found", Message: "presentation not found"}
		}
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_read_failed", Message: err.Error()}
	}
	current, err := presentation.DecodeArtifact(path, currentContent)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	if strings.TrimSpace(request.ExpectedRevision) == "" || request.ExpectedRevision != current.Revision {
		return PresentationDocument{}, &APIError{
			Status: http.StatusConflict, Code: "presentation_edit_conflict",
			Message: "This presentation changed after editing began. Reload it before applying visual changes.",
		}
	}
	next, err := presentationFromModel(path, request.Artifact)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if next.ID != current.ID || next.Kind != current.Kind {
		return PresentationDocument{}, &APIError{
			Status: http.StatusBadRequest, Code: "presentation_identity_immutable",
			Message: "Presentation identity and kind cannot be changed from the visual editor.",
		}
	}
	content, err := presentation.MarshalArtifact(*next)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_snapshot_invalid", Message: err.Error()}
	}
	if string(content) == string(currentContent) {
		datasetSchemas := s.enrichProblems(ctx, current)
		return PresentationDocument{Artifact: presentationToModel(s.deps.WorkspaceRoot, current, datasetSchemas), Content: string(currentContent)}, nil
	}
	if err := writeFileAtomically(path, content, 0o644); err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusInternalServerError, Code: "presentation_update_failed", Message: err.Error()}
	}
	s.pushUpdate(ctx, "presentation-updated", path)
	return s.documentFromBytes(ctx, path, content)
}

func (s *PresentationService) documentFromBytes(ctx context.Context, path string, content []byte) (PresentationDocument, *APIError) {
	artifact, err := presentation.DecodeArtifact(path, content)
	if err != nil {
		return PresentationDocument{}, &APIError{Status: http.StatusBadRequest, Code: "presentation_invalid", Message: err.Error()}
	}
	datasetSchemas := s.enrichProblems(ctx, artifact)
	return PresentationDocument{Artifact: presentationToModel(s.deps.WorkspaceRoot, artifact, datasetSchemas), Content: string(content)}, nil
}

func (s *PresentationService) enrichProblems(ctx context.Context, artifact *presentation.Artifact) map[string]presentation.ResolvedSchema {
	state := model.WorkspaceState{}
	if s.deps.CurrentState != nil {
		state = s.deps.CurrentState()
	}
	datasetSchemas, resolutionFindings := resolvePresentationDatasetSchemas(ctx, s.deps.WorkspaceRoot, state, artifact)
	artifact.Problems = append(
		(presentation.Checker{}).CheckArtifact(ctx, *artifact, datasetSchemas, presentation.CheckOptions{Strict: true}),
		resolutionFindings...,
	)
	artifact.Problems = uniquePresentationFindings(artifact.Problems)
	return datasetSchemas
}

func (s *PresentationService) resolvePath(workspaceID string) (string, *APIError) {
	relPath, err := DecodeID(strings.TrimSpace(workspaceID))
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: "invalid presentation id"}
	}
	path, err := SafeJoin(s.deps.WorkspaceRoot, relPath)
	if err != nil {
		return "", &APIError{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: err.Error()}
	}
	name := strings.ToLower(filepath.Base(path))
	if name != "dashboard.yml" && name != "report.yml" &&
		!strings.HasSuffix(name, ".dashboard.yml") && !strings.HasSuffix(name, ".report.yml") {
		return "", &APIError{Status: http.StatusBadRequest, Code: "presentation_id_invalid", Message: "presentation path has an unsupported filename"}
	}
	return path, nil
}

func (s *PresentationService) pushUpdate(ctx context.Context, eventType, path string) {
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
