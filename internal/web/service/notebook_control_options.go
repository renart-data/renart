package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
)

const notebookControlOptionLimit = 1000

// RefreshControlOptions reads a bounded option snapshot from an existing local
// notebook result. It never runs the producer or queries a source warehouse.
func (s *NotebookService) RefreshControlOptions(
	ctx context.Context,
	notebookID string,
	controlID string,
) (model.PresentationDatasetResult, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.PresentationDatasetResult{}, apiErr
	}
	definition, producer, apiErr := notebookControlOptionSource(nb, controlID)
	if apiErr != nil {
		return model.PresentationDatasetResult{}, apiErr
	}

	s.hydrateRuntime(nb)
	runtime := s.runtimes.get(nb.UUID)
	runtime.mu.Lock()
	producerResult, hasResult := runtime.results[producer.ID]
	producerStale := runtime.stale[producer.ID]
	producerRunning := slices.Contains(runtime.runningCellsLocked(), producer.ID)
	runtime.mu.Unlock()
	if producerRunning {
		return model.PresentationDatasetResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_control_options_running",
			Message: fmt.Sprintf("Wait for %s to finish before refreshing options for %s.", producer.Asset.Name, definition.ID),
		}
	}
	if producerStale {
		return model.PresentationDatasetResult{}, &APIError{
			Status: http.StatusConflict,
			Code:   "notebook_control_options_stale",
			Message: fmt.Sprintf(
				"Run %s before refreshing options for %s; its local result is stale.",
				producer.Asset.Name,
				definition.ID,
			),
		}
	}
	if !hasResult || producerResult.Status != notebook.CellRunOK {
		return model.PresentationDatasetResult{}, &APIError{
			Status: http.StatusConflict,
			Code:   "notebook_control_options_unavailable",
			Message: fmt.Sprintf(
				"Run %s before refreshing options for %s.",
				producer.Asset.Name,
				definition.ID,
			),
		}
	}

	startedAt := time.Now()
	options, err := s.store.ReadCellOptions(
		ctx,
		nb.UUID,
		producer.ID,
		definition.Options.ValueField,
		definition.Options.LabelField,
		notebookControlOptionLimit,
	)
	if err != nil {
		if errors.Is(err, notebook.ErrCellResultUnavailable) {
			return model.PresentationDatasetResult{}, &APIError{
				Status: http.StatusConflict,
				Code:   "notebook_control_options_unavailable",
				Message: fmt.Sprintf(
					"Run %s before refreshing options for %s.",
					producer.Asset.Name,
					definition.ID,
				),
			}
		}
		return model.PresentationDatasetResult{}, &APIError{
			Status: http.StatusUnprocessableEntity,
			Code:   "notebook_control_options_failed",
			Message: fmt.Sprintf(
				"Could not read options for %s from %s: %s",
				definition.ID,
				producer.Asset.Name,
				err.Error(),
			),
		}
	}

	// A cell edit or another run can race the session read. Never return the old
	// relation as a current snapshot when runtime ownership changed meanwhile.
	runtime.mu.Lock()
	currentResult, stillPresent := runtime.results[producer.ID]
	changed := runtime.stale[producer.ID] || !stillPresent || currentResult.Status != notebook.CellRunOK ||
		currentResult.Fingerprint != producerResult.Fingerprint ||
		slices.Contains(runtime.runningCellsLocked(), producer.ID)
	runtime.mu.Unlock()
	if changed {
		return model.PresentationDatasetResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_control_options_changed",
			Message: fmt.Sprintf("%s changed while its options were loading; refresh again.", producer.Asset.Name),
		}
	}

	return model.PresentationDatasetResult{
		Dataset: producer.ID, Status: "ok", Columns: options.Columns, ColumnTypes: options.ColumnTypes,
		Rows: options.Rows, TotalRows: options.TotalRows, Truncated: options.Truncated,
		DurationMS: time.Since(startedAt).Milliseconds(),
	}, nil
}

func notebookControlOptionSource(
	nb *notebook.Notebook,
	controlID string,
) (presentation.ParameterDefinition, *notebook.Cell, *APIError) {
	controlID = strings.TrimSpace(controlID)
	var definition *presentation.ParameterDefinition
	for index := range nb.Parameters {
		if strings.EqualFold(strings.TrimSpace(nb.Parameters[index].ID), controlID) {
			definition = &nb.Parameters[index]
			break
		}
	}
	if definition == nil {
		return presentation.ParameterDefinition{}, nil, &APIError{
			Status: http.StatusNotFound, Code: "notebook_control_not_found",
			Message: fmt.Sprintf("Control %q was not found.", controlID),
		}
	}
	typeName := presentation.ParameterType(strings.ToLower(strings.TrimSpace(string(definition.Type))))
	if typeName != presentation.ParameterTypeSelect && typeName != presentation.ParameterTypeMultiSelect {
		return presentation.ParameterDefinition{}, nil, &APIError{
			Status: http.StatusUnprocessableEntity, Code: "notebook_control_options_unsupported",
			Message: fmt.Sprintf("Control %s does not use selectable options.", definition.ID),
		}
	}
	if definition.Options == nil || strings.TrimSpace(definition.Options.Dataset) == "" ||
		strings.TrimSpace(definition.Options.ValueField) == "" {
		return presentation.ParameterDefinition{}, nil, &APIError{
			Status: http.StatusUnprocessableEntity, Code: "notebook_control_options_not_configured",
			Message: fmt.Sprintf("Control %s does not have a complete dataset option source.", definition.ID),
		}
	}
	dataset := strings.TrimSpace(definition.Options.Dataset)
	producer := nb.CellByID(dataset)
	if producer == nil {
		producer = nb.CellByName(dataset)
	}
	if producer == nil {
		return presentation.ParameterDefinition{}, nil, &APIError{
			Status: http.StatusUnprocessableEntity, Code: "notebook_control_option_dataset_missing",
			Message: fmt.Sprintf("Option dataset %q does not match a notebook cell.", dataset),
		}
	}
	return *definition, producer, nil
}
