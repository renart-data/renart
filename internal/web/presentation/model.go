package presentation

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"renart/internal/web/model"
	"renart/internal/web/workspacefs"
)

// ArtifactToModel maps a Git-authored presentation and its resolved schemas to
// the single workspace/API DTO representation.
func ArtifactToModel(
	workspaceRoot string,
	artifact *Artifact,
	datasetSchemas map[string]ResolvedSchema,
) model.PresentationArtifact {
	if artifact == nil {
		return model.PresentationArtifact{}
	}
	relPath, err := filepath.Rel(workspaceRoot, artifact.Path)
	if err != nil {
		relPath = artifact.Path
	}
	result := model.PresentationArtifact{
		ID: artifact.ID, WorkspaceID: workspacefs.EncodePathID(filepath.ToSlash(relPath)), Kind: string(artifact.Kind), Version: artifact.Version,
		Revision: artifact.Revision, Title: artifact.Title, Path: filepath.ToSlash(relPath),
		Filters:        make([]model.PresentationFilter, 0, len(artifact.Filters)),
		Visualizations: make([]model.PresentationVisualization, 0, len(artifact.Visualizations)),
		Layout:         make([]model.PresentationLayoutItem, 0, len(artifact.Layout)),
		Sections:       make([]model.PresentationSection, 0, len(artifact.Sections)),
		Problems:       FindingsToModel(artifact.Problems),
	}
	for _, id := range SortedDatasetIDs(artifact.Datasets) {
		dataset := artifact.Datasets[id]
		modelDataset := model.PresentationDataset{
			ID: id, Asset: dataset.Asset, Connection: dataset.Connection, Query: dataset.Query,
			Columns: make([]model.Column, 0, len(dataset.Columns)),
		}
		for _, column := range dataset.Columns {
			modelDataset.Columns = append(modelDataset.Columns, model.Column{
				Name: column.Name, Type: column.Type, Nullable: column.Nullable,
			})
		}
		if schema, ok := datasetSchemas[id]; ok {
			modelDataset.ResolvedColumns = make([]model.Column, 0, len(schema.Columns))
			for _, column := range schema.Columns {
				modelDataset.ResolvedColumns = append(modelDataset.ResolvedColumns, model.Column{
					Name: column.Name, Type: column.PhysicalType, Nullable: column.Nullable,
				})
			}
		}
		result.Datasets = append(result.Datasets, modelDataset)
	}
	for _, filter := range artifact.Filters {
		modelFilter := model.PresentationFilter{
			ID: filter.ID, Label: filter.Label, Type: string(filter.Type), Default: cloneJSONValue(filter.Default),
			Min: filter.Min, Max: filter.Max, Step: filter.Step,
		}
		if filter.Options != nil {
			modelFilter.Options = &model.PresentationFilterOptions{
				Values: append([]any(nil), filter.Options.Values...), Dataset: filter.Options.Dataset,
				ValueField: filter.Options.ValueField, LabelField: filter.Options.LabelField,
			}
		}
		result.Filters = append(result.Filters, modelFilter)
	}
	for _, visualization := range artifact.Visualizations {
		modelVisualization := model.PresentationVisualization{
			ID: visualization.ID, Dataset: visualization.Dataset,
			Definition:     cloneStringAnyMap(visualization.Definition),
			FilterBindings: make([]model.PresentationFilterBinding, 0, len(visualization.FilterBindings)),
		}
		for _, binding := range visualization.FilterBindings {
			modelVisualization.FilterBindings = append(modelVisualization.FilterBindings, model.PresentationFilterBinding{
				Filter: binding.Filter, Dataset: binding.Dataset, Column: binding.Column, Operator: binding.Operator,
			})
		}
		result.Visualizations = append(result.Visualizations, modelVisualization)
	}
	for _, item := range artifact.Layout {
		result.Layout = append(result.Layout, model.PresentationLayoutItem{
			Visualization: item.Visualization, X: item.X, Y: item.Y, Width: item.Width, Height: item.Height,
		})
	}
	for _, section := range artifact.Sections {
		result.Sections = append(result.Sections, model.PresentationSection{
			ID: section.ID, Title: section.Title, Markdown: section.Markdown,
			Visualization: section.Visualization, PageBreak: section.PageBreak,
		})
	}
	return result
}

// FindingsToModel maps checker findings to the public workspace/API DTO.
func FindingsToModel(findings []Finding) []model.PresentationFinding {
	result := make([]model.PresentationFinding, 0, len(findings))
	for _, finding := range findings {
		result = append(result, model.PresentationFinding{
			Code: finding.Code, Severity: finding.Severity, Message: finding.Message,
			Path: finding.Path, Field: finding.Field, PhysicalType: finding.PhysicalType,
		})
	}
	return result
}

// ArtifactFromModel maps a typed visual-editor snapshot back to the
// Git-authored presentation representation.
func ArtifactFromModel(path string, input model.PresentationArtifact) (*Artifact, error) {
	artifact := &Artifact{
		Version: input.Version, ID: input.ID, Title: input.Title, Path: path,
		Kind:           ArtifactKind(strings.ToLower(strings.TrimSpace(input.Kind))),
		Datasets:       make(map[string]DatasetDefinition, len(input.Datasets)),
		Filters:        make([]FilterDefinition, 0, len(input.Filters)),
		Visualizations: make([]ArtifactVisualization, 0, len(input.Visualizations)),
		Layout:         make([]DashboardLayoutItem, 0, len(input.Layout)),
		Sections:       make([]ReportSection, 0, len(input.Sections)),
	}
	for _, dataset := range input.Datasets {
		id := strings.TrimSpace(dataset.ID)
		if _, exists := artifact.Datasets[id]; exists {
			return nil, fmt.Errorf("dataset id %q is duplicated", id)
		}
		definition := DatasetDefinition{
			Asset: dataset.Asset, Connection: dataset.Connection, Query: dataset.Query,
			Columns: make([]DatasetColumn, 0, len(dataset.Columns)),
		}
		for _, column := range dataset.Columns {
			definition.Columns = append(definition.Columns, DatasetColumn{
				Name: column.Name, Type: column.Type, Nullable: column.Nullable,
			})
		}
		artifact.Datasets[id] = definition
	}
	for _, filter := range input.Filters {
		definition := FilterDefinition{
			ID: filter.ID, Label: filter.Label,
			Type: ParameterType(filter.Type), Default: cloneJSONValue(filter.Default),
			Min: filter.Min, Max: filter.Max, Step: filter.Step,
		}
		if filter.Options != nil {
			definition.Options = &ParameterOptions{
				Values: append([]any(nil), filter.Options.Values...), Dataset: filter.Options.Dataset,
				ValueField: filter.Options.ValueField, LabelField: filter.Options.LabelField,
			}
		}
		artifact.Filters = append(artifact.Filters, definition)
	}
	for _, visualization := range input.Visualizations {
		definition := ArtifactVisualization{
			ID: visualization.ID, Dataset: visualization.Dataset,
			Definition:     cloneStringAnyMap(visualization.Definition),
			FilterBindings: make([]FilterBinding, 0, len(visualization.FilterBindings)),
		}
		for _, binding := range visualization.FilterBindings {
			definition.FilterBindings = append(definition.FilterBindings, FilterBinding{
				Filter: binding.Filter, Dataset: binding.Dataset, Column: binding.Column, Operator: binding.Operator,
			})
		}
		artifact.Visualizations = append(artifact.Visualizations, definition)
	}
	for _, item := range input.Layout {
		artifact.Layout = append(artifact.Layout, DashboardLayoutItem{
			Visualization: item.Visualization, X: item.X, Y: item.Y, Width: item.Width, Height: item.Height,
		})
	}
	for _, section := range input.Sections {
		artifact.Sections = append(artifact.Sections, ReportSection{
			ID: section.ID, Title: section.Title, Markdown: section.Markdown,
			Visualization: section.Visualization, PageBreak: section.PageBreak,
		})
	}
	return artifact, nil
}

// SortedDatasetIDs provides deterministic traversal for schema resolution and
// API conversion.
func SortedDatasetIDs(datasets map[string]DatasetDefinition) []string {
	ids := make([]string, 0, len(datasets))
	for id := range datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return value
	}
	return result
}
