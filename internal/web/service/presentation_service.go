package service

import (
	"context"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

// PresentationDependencies collects the cross-domain adapters used by the
// presentation document and query-runtime application services.
type PresentationDependencies struct {
	WorkspaceRoot        string
	ConfigPath           string
	CurrentState         func() model.WorkspaceState
	ResolveAssetByID     func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	NewConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
	RunConnectionQuery   func(context.Context, string, string, string) ([]string, []map[string]any, error)
	PushWorkspaceUpdate  func(ctx context.Context, eventType, eventPath string)
}

// PresentationService is the compatibility facade used by HTTP and command
// adapters. Git-backed document lifecycle is owned by presentation.DocumentService;
// warehouse-backed runtime methods remain here until their execution adapters
// are extracted as a separate cohesive slice.
type PresentationService struct {
	deps      PresentationDependencies
	documents *presentation.DocumentService
}

type PresentationDocument = presentation.PresentationDocument
type CreatePresentationRequest = presentation.CreatePresentationRequest
type UpdatePresentationRequest = presentation.UpdatePresentationRequest
type ReplacePresentationRequest = presentation.ReplacePresentationRequest

func NewPresentationService(deps PresentationDependencies) *PresentationService {
	service := &PresentationService{deps: deps}
	service.documents = presentation.NewDocumentService(presentation.DocumentDependencies{
		WorkspaceRoot:       deps.WorkspaceRoot,
		Enrich:              service.enrichProblems,
		PushWorkspaceUpdate: deps.PushWorkspaceUpdate,
	})
	return service
}

func (s *PresentationService) Get(ctx context.Context, workspaceID string) (PresentationDocument, *APIError) {
	return s.documents.Get(ctx, workspaceID)
}

func (s *PresentationService) Create(ctx context.Context, request CreatePresentationRequest) (PresentationDocument, *APIError) {
	return s.documents.Create(ctx, request)
}

func (s *PresentationService) Update(
	ctx context.Context,
	workspaceID string,
	request UpdatePresentationRequest,
) (PresentationDocument, *APIError) {
	return s.documents.Update(ctx, workspaceID, request)
}

func (s *PresentationService) Replace(
	ctx context.Context,
	workspaceID string,
	request ReplacePresentationRequest,
) (PresentationDocument, *APIError) {
	return s.documents.Replace(ctx, workspaceID, request)
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
