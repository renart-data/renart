package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

// appendPresentations discovers Git-native dashboards and reports. Parse
// failures join workspace errors; structurally valid files remain visible with
// structured problems so users can open and repair them.
func (s *WorkspaceService) appendPresentations(ctx context.Context, state *model.WorkspaceState) {
	filesystem := afero.NewOsFs()
	paths, err := presentation.DiscoverArtifacts(filesystem, s.workspaceRoot)
	if err != nil {
		state.Errors = append(state.Errors, "presentation discovery failed: "+err.Error())
		return
	}
	for _, path := range paths {
		artifact, loadErr := presentation.LoadArtifact(filesystem, path)
		if loadErr != nil {
			state.Errors = append(state.Errors, path+": "+loadErr.Error())
			continue
		}
		datasetSchemas, resolutionFindings := resolvePresentationDatasetSchemas(*state, artifact)
		artifact.Problems = append(
			(presentation.Checker{}).CheckArtifact(
				ctx, *artifact, datasetSchemas, presentation.CheckOptions{Strict: true},
			),
			resolutionFindings...,
		)
		artifact.Problems = uniquePresentationFindings(artifact.Problems)
		state.Presentations = append(state.Presentations, presentationToModel(s.workspaceRoot, artifact))
	}
	sort.Slice(state.Presentations, func(i, j int) bool {
		return state.Presentations[i].Path < state.Presentations[j].Path
	})
}

func resolvePresentationDatasetSchemas(
	state model.WorkspaceState,
	artifact *presentation.Artifact,
) (map[string]presentation.ResolvedSchema, []presentation.Finding) {
	schemas := make(map[string]presentation.ResolvedSchema, len(artifact.Datasets))
	findings := make([]presentation.Finding, 0)
	for _, id := range sortedPresentationDatasetIDs(artifact.Datasets) {
		definition := artifact.Datasets[id]
		columns := make([]presentation.ResolvedColumn, 0, len(definition.Columns))
		for _, column := range definition.Columns {
			columns = append(columns, presentation.ResolvedColumn{
				Name: column.Name, PhysicalType: column.Type,
				SemanticType: presentation.SemanticTypeForPhysicalType(column.Type), Nullable: column.Nullable,
			})
		}
		assetRef := strings.TrimSpace(definition.Asset)
		if assetRef != "" {
			matches := presentationAssetMatches(state, assetRef)
			switch len(matches) {
			case 0:
				findings = append(findings, presentation.Finding{
					Code: "presentation-dataset-asset-missing", Severity: "error", Path: "datasets." + id + ".asset",
					Message: fmt.Sprintf("Asset %q could not be resolved in this workspace.", assetRef),
				})
			case 1:
				if len(matches[0].Columns) > 0 {
					columns = presentation.ColumnsFromModel(matches[0].Columns)
				}
			default:
				findings = append(findings, presentation.Finding{
					Code: "presentation-dataset-asset-ambiguous", Severity: "error", Path: "datasets." + id + ".asset",
					Message: fmt.Sprintf("Asset reference %q matches more than one workspace asset; use a unique asset URI.", assetRef),
				})
			}
		}
		if len(columns) > 0 {
			schemas[id] = presentation.ResolvedSchema{
				Source:  presentation.DataSourceRef{Kind: "dataset", ArtifactID: artifact.ID, ComponentID: id},
				Columns: columns, Complete: true,
			}
		}
	}
	return schemas, findings
}

func presentationAssetMatches(state model.WorkspaceState, reference string) []model.Asset {
	reference = strings.TrimSpace(reference)
	matches := make([]model.Asset, 0, 1)
	seen := map[string]bool{}
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			if !strings.EqualFold(strings.TrimSpace(asset.Name), reference) &&
				!strings.EqualFold(strings.TrimSpace(asset.URI), reference) {
				continue
			}
			key := asset.ID + "\x00" + asset.Name
			if !seen[key] {
				matches = append(matches, asset)
				seen[key] = true
			}
		}
	}
	return matches
}

func uniquePresentationFindings(findings []presentation.Finding) []presentation.Finding {
	seen := make(map[string]bool, len(findings))
	result := make([]presentation.Finding, 0, len(findings))
	for _, finding := range findings {
		key := finding.Code + "\x00" + finding.Path + "\x00" + finding.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, finding)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Code < result[j].Code
		}
		return result[i].Path < result[j].Path
	})
	return result
}

func presentationToModel(workspaceRoot string, artifact *presentation.Artifact) model.PresentationArtifact {
	if artifact == nil {
		return model.PresentationArtifact{}
	}
	relPath, err := filepath.Rel(workspaceRoot, artifact.Path)
	if err != nil {
		relPath = artifact.Path
	}
	result := model.PresentationArtifact{
		ID: artifact.ID, WorkspaceID: EncodeID(filepath.ToSlash(relPath)), Kind: string(artifact.Kind), Version: artifact.Version,
		Revision: artifact.Revision, Title: artifact.Title, Path: filepath.ToSlash(relPath),
		Filters:        make([]model.PresentationFilter, 0, len(artifact.Filters)),
		Visualizations: make([]model.PresentationVisualization, 0, len(artifact.Visualizations)),
		Layout:         make([]model.PresentationLayoutItem, 0, len(artifact.Layout)),
		Sections:       make([]model.PresentationSection, 0, len(artifact.Sections)),
		Problems:       make([]model.PresentationFinding, 0, len(artifact.Problems)),
	}
	for _, id := range sortedPresentationDatasetIDs(artifact.Datasets) {
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
		result.Datasets = append(result.Datasets, modelDataset)
	}
	for _, filter := range artifact.Filters {
		modelFilter := model.PresentationFilter{
			ID: filter.ID, Label: filter.Label, Type: string(filter.Type), Default: cloneJSONValue(filter.Default),
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
	for _, finding := range artifact.Problems {
		result.Problems = append(result.Problems, model.PresentationFinding{
			Code: finding.Code, Severity: finding.Severity, Message: finding.Message,
			Path: finding.Path, Field: finding.Field, PhysicalType: finding.PhysicalType,
		})
	}
	return result
}

func presentationFromModel(path string, input model.PresentationArtifact) (*presentation.Artifact, error) {
	artifact := &presentation.Artifact{
		Version: input.Version, ID: input.ID, Title: input.Title, Path: path,
		Kind:           presentation.ArtifactKind(strings.ToLower(strings.TrimSpace(input.Kind))),
		Datasets:       make(map[string]presentation.DatasetDefinition, len(input.Datasets)),
		Filters:        make([]presentation.FilterDefinition, 0, len(input.Filters)),
		Visualizations: make([]presentation.ArtifactVisualization, 0, len(input.Visualizations)),
		Layout:         make([]presentation.DashboardLayoutItem, 0, len(input.Layout)),
		Sections:       make([]presentation.ReportSection, 0, len(input.Sections)),
	}
	for _, dataset := range input.Datasets {
		id := strings.TrimSpace(dataset.ID)
		if _, exists := artifact.Datasets[id]; exists {
			return nil, fmt.Errorf("dataset id %q is duplicated", id)
		}
		definition := presentation.DatasetDefinition{
			Asset: dataset.Asset, Connection: dataset.Connection, Query: dataset.Query,
			Columns: make([]presentation.DatasetColumn, 0, len(dataset.Columns)),
		}
		for _, column := range dataset.Columns {
			definition.Columns = append(definition.Columns, presentation.DatasetColumn{
				Name: column.Name, Type: column.Type, Nullable: column.Nullable,
			})
		}
		artifact.Datasets[id] = definition
	}
	for _, filter := range input.Filters {
		definition := presentation.FilterDefinition{
			ID: filter.ID, Label: filter.Label,
			Type: presentation.ParameterType(filter.Type), Default: cloneJSONValue(filter.Default),
		}
		if filter.Options != nil {
			definition.Options = &presentation.ParameterOptions{
				Values: append([]any(nil), filter.Options.Values...), Dataset: filter.Options.Dataset,
				ValueField: filter.Options.ValueField, LabelField: filter.Options.LabelField,
			}
		}
		artifact.Filters = append(artifact.Filters, definition)
	}
	for _, visualization := range input.Visualizations {
		definition := presentation.ArtifactVisualization{
			ID: visualization.ID, Dataset: visualization.Dataset,
			Definition:     cloneStringAnyMap(visualization.Definition),
			FilterBindings: make([]presentation.FilterBinding, 0, len(visualization.FilterBindings)),
		}
		for _, binding := range visualization.FilterBindings {
			definition.FilterBindings = append(definition.FilterBindings, presentation.FilterBinding{
				Filter: binding.Filter, Dataset: binding.Dataset, Column: binding.Column, Operator: binding.Operator,
			})
		}
		artifact.Visualizations = append(artifact.Visualizations, definition)
	}
	for _, item := range input.Layout {
		artifact.Layout = append(artifact.Layout, presentation.DashboardLayoutItem{
			Visualization: item.Visualization, X: item.X, Y: item.Y, Width: item.Width, Height: item.Height,
		})
	}
	for _, section := range input.Sections {
		artifact.Sections = append(artifact.Sections, presentation.ReportSection{
			ID: section.ID, Title: section.Title, Markdown: section.Markdown,
			Visualization: section.Visualization, PageBreak: section.PageBreak,
		})
	}
	return artifact, nil
}

func sortedPresentationDatasetIDs(datasets map[string]presentation.DatasetDefinition) []string {
	ids := make([]string, 0, len(datasets))
	for id := range datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
