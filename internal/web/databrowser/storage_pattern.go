package databrowser

import (
	"context"

	"renart/internal/web/apperror"
)

// StoragePattern prepares a source selector, not a snapshot of the capped
// search results. No files are listed, read, or written as part of the handoff.
func (s *Service) StoragePattern(ctx context.Context, connectionID, pattern, environment string) (ObjectResponse, *apperror.Error) {
	scope, apiErr := s.resolveScope(ctx, connectionID, environment)
	if apiErr != nil {
		return ObjectResponse{}, apiErr
	}
	if scope.ref.SourceKind != "storage" {
		return ObjectResponse{}, badRequest("data_browser_pattern_unsupported", "Choose a storage connection for a wildcard source.")
	}
	pattern, err := NormalizeStoragePattern(pattern)
	if err != nil {
		return ObjectResponse{}, badRequest("data_browser_pattern_invalid", err.Error())
	}
	ref := scope.ref
	ref.Kind, ref.Path = "storage_pattern", pattern
	return s.Object(ctx, encodeRef(ref), environment)
}

func (s *Service) storagePatternObject(ctx context.Context, ref objectRef, object Object) (ObjectResponse, *apperror.Error) {
	pattern, err := NormalizeStoragePattern(ref.Path)
	if err != nil {
		return ObjectResponse{}, badRequest("data_browser_pattern_invalid", err.Error())
	}
	if s.deps.StoragePatternReference == nil {
		return ObjectResponse{}, badRequest("data_browser_pattern_unsupported", "This storage connection does not support Load patterns.")
	}
	reference, err := s.deps.StoragePatternReference(ctx, ref.Connection, pattern, ref.Environment)
	if err != nil {
		return ObjectResponse{}, internalError("data_browser_pattern_failed", err)
	}
	object.Name, object.Kind, object.ReferenceText = pattern, "pattern", reference
	object.Capabilities = Capabilities{LoadSource: true}
	object.Warning = "The Load reads files matching this pattern when it runs, not just the matches currently listed. Patterns cannot be destinations."
	return ObjectResponse{Status: "ok", Object: object}, nil
}
