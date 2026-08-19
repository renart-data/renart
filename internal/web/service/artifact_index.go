package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"renart/internal/web/identity"
	"renart/internal/web/model"
)

const (
	artifactKindPipelineAsset = "pipeline_asset"
	artifactKindNotebook      = "notebook"
	artifactKindDashboard     = "dashboard"
	artifactKindReport        = "report"

	componentKindCell          = "cell"
	componentKindDataset       = "dataset"
	componentKindFilter        = "filter"
	componentKindSource        = "source"
	componentKindSection       = "section"
	componentKindMarkdown      = "markdown"
	componentKindParameter     = "parameter"
	componentKindVisualization = "visualization"

	artifactCapabilityDeployable       = "deployable"
	artifactCapabilityExecutable       = "executable"
	artifactCapabilityHasSchema        = "has_schema"
	artifactCapabilityPresentation     = "presentation"
	artifactCapabilityProducesRelation = "produces_relation"
	artifactCapabilityVersioned        = "versioned"
)

// BuildArtifactIndex derives the workspace-wide artifact projection from the
// authoritative pipeline and notebook models already loaded from disk.
func BuildArtifactIndex(state model.WorkspaceState) model.ArtifactIndex {
	index := model.ArtifactIndex{
		Artifacts:    make([]model.ArtifactDescriptor, 0),
		Containment:  make([]model.ArtifactContainment, 0),
		Dependencies: make([]model.ArtifactDependency, 0),
	}
	workspaceAssetRefs := make(map[string]model.ArtifactRef)
	assetNameRefs := make(map[string][]model.ArtifactRef)
	artifactColumns := make(map[string][]model.Column)

	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			artifactID := pipelineAssetArtifactID(pipeline, asset)
			ref := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: artifactID}
			workspaceAssetRefs[asset.ID] = ref
			nameKey := strings.ToLower(strings.TrimSpace(asset.Name))
			assetNameRefs[nameKey] = appendUniqueArtifactRef(assetNameRefs[nameKey], ref)
			if uriKey := strings.ToLower(strings.TrimSpace(asset.URI)); uriKey != "" {
				assetNameRefs[uriKey] = appendUniqueArtifactRef(assetNameRefs[uriKey], ref)
			}
			artifactColumns[artifactRefKey(ref)] = cloneArtifactColumns(asset.Columns)
			index.Artifacts = append(index.Artifacts, model.ArtifactDescriptor{
				ID:           artifactID,
				Kind:         artifactKindPipelineAsset,
				WorkspaceID:  asset.ID,
				Path:         asset.Path,
				Title:        asset.Name,
				Capabilities: pipelineAssetArtifactCapabilities(asset),
				Columns:      cloneArtifactColumns(asset.Columns),
			})
		}
	}

	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			consumer := model.ArtifactRef{
				Kind: artifactKindPipelineAsset, ArtifactID: pipelineAssetArtifactID(pipeline, asset),
			}
			for _, dependency := range asset.Dependencies {
				producer, ok := workspaceAssetRefs[dependency.ResolvedAssetID]
				if !ok {
					continue
				}
				index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
					Producer: producer,
					Consumer: consumer,
				})
			}
		}
	}

	for _, notebook := range state.Notebooks {
		appendNotebookArtifact(&index, notebook, assetNameRefs)
	}
	for _, artifact := range state.Presentations {
		appendPresentationArtifact(&index, artifact, assetNameRefs, artifactColumns)
	}

	sortArtifactIndex(&index)
	index.Revision = artifactIndexRevision(index)
	return index
}

func appendUniqueArtifactRef(refs []model.ArtifactRef, candidate model.ArtifactRef) []model.ArtifactRef {
	for _, ref := range refs {
		if artifactRefKey(ref) == artifactRefKey(candidate) {
			return refs
		}
	}
	return append(refs, candidate)
}

func pipelineAssetArtifactID(pipeline model.Pipeline, asset model.Asset) string {
	if strings.TrimSpace(pipeline.UUID) != "" {
		return identity.AssetID(pipeline.UUID, asset.Name)
	}
	return fmt.Sprintf("%s:%s", pipeline.ID, asset.Name)
}

func pipelineAssetArtifactCapabilities(asset model.Asset) []string {
	assetType := strings.ToLower(strings.TrimSpace(asset.Type))
	isSource := strings.HasSuffix(assetType, ".source")
	isSensor := strings.Contains(assetType, ".sensor.") || strings.HasSuffix(assetType, ".sensor")
	capabilities := []string{artifactCapabilityDeployable, artifactCapabilityVersioned}
	if !isSource {
		capabilities = append(capabilities, artifactCapabilityExecutable)
	}
	if !isSensor {
		capabilities = append(capabilities, artifactCapabilityProducesRelation, artifactCapabilityHasSchema)
	}
	sort.Strings(capabilities)
	return capabilities
}

func appendNotebookArtifact(index *model.ArtifactIndex, notebook model.Notebook, assetNameRefs map[string][]model.ArtifactRef) {
	artifactID := strings.TrimSpace(notebook.UUID)
	if artifactID == "" {
		artifactID = notebook.ID
	}
	artifactRef := model.ArtifactRef{Kind: artifactKindNotebook, ArtifactID: artifactID}
	components := make([]model.ArtifactComponent, 0, len(notebook.Parameters)+len(notebook.Blocks))
	cellsByID := make(map[string]model.Asset, len(notebook.Cells))
	cellsByName := make(map[string]model.Asset, len(notebook.Cells))
	componentRefs := make(map[string]model.ArtifactRef, len(notebook.Parameters)+len(notebook.Blocks))
	for _, cell := range notebook.Cells {
		cellsByID[cell.CellID] = cell
		cellsByName[strings.ToLower(strings.TrimSpace(cell.Name))] = cell
	}

	for position, parameter := range notebook.Parameters {
		componentID := "parameter:" + strings.TrimSpace(parameter.ID)
		if componentID == "parameter:" {
			continue
		}
		name := strings.TrimSpace(parameter.Label)
		if name == "" {
			name = parameter.ID
		}
		component := model.ArtifactComponent{
			ID: componentID, Kind: componentKindParameter, Name: name,
			Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		}
		components = append(components, component)
		childRef := model.ArtifactRef{
			Kind: artifactKindNotebook, ArtifactID: artifactID, ComponentID: componentID,
		}
		componentRefs[componentID] = childRef
		index.Containment = append(index.Containment, model.ArtifactContainment{
			Parent: artifactRef, Child: childRef, Position: position,
		})
	}

	for blockPosition, block := range notebook.Blocks {
		position := len(notebook.Parameters) + blockPosition
		component, ok := notebookArtifactComponent(block, cellsByID)
		if !ok {
			continue
		}
		components = append(components, component)
		childRef := model.ArtifactRef{
			Kind: artifactKindNotebook, ArtifactID: artifactID, ComponentID: component.ID,
		}
		componentRefs[component.ID] = childRef
		index.Containment = append(index.Containment, model.ArtifactContainment{
			Parent: artifactRef, Child: childRef, Position: position,
		})
	}

	index.Artifacts = append(index.Artifacts, model.ArtifactDescriptor{
		ID:           artifactID,
		Kind:         artifactKindNotebook,
		WorkspaceID:  notebook.ID,
		Path:         notebook.Path,
		Title:        notebook.Title,
		Capabilities: []string{artifactCapabilityExecutable, artifactCapabilityVersioned},
		Components:   components,
	})

	for _, cell := range notebook.Cells {
		consumer, ok := componentRefs[cell.CellID]
		if !ok {
			continue
		}
		for _, upstreamName := range cell.Upstreams {
			upstream, exists := cellsByName[strings.ToLower(strings.TrimSpace(upstreamName))]
			if !exists {
				continue
			}
			producer, exists := componentRefs[upstream.CellID]
			if exists {
				index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
					Producer: producer, Consumer: consumer,
				})
			}
		}
		for _, externalRef := range cell.ExternalRefs {
			matches := assetNameRefs[strings.ToLower(strings.TrimSpace(externalRef))]
			if len(matches) != 1 {
				continue
			}
			index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
				Producer: matches[0], Consumer: consumer,
			})
		}
	}

	for _, block := range notebook.Blocks {
		if block.Visualization == nil {
			continue
		}
		producer, producerOK := componentRefs[block.Visualization.Source]
		consumer, consumerOK := componentRefs[block.ID]
		if !producerOK || !consumerOK {
			continue
		}
		index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
			Producer: producer,
			Consumer: consumer,
			Columns:  visualizationColumnUsages(block.Visualization.Definition),
		})
	}
}

func appendPresentationArtifact(
	index *model.ArtifactIndex,
	artifact model.PresentationArtifact,
	assetRefs map[string][]model.ArtifactRef,
	artifactColumns map[string][]model.Column,
) {
	kind := strings.ToLower(strings.TrimSpace(artifact.Kind))
	if kind != artifactKindDashboard && kind != artifactKindReport {
		return
	}
	artifactID := strings.TrimSpace(artifact.ID)
	if artifactID == "" {
		artifactID = artifact.Path
	}
	parent := model.ArtifactRef{Kind: kind, ArtifactID: artifactID}
	components := make([]model.ArtifactComponent, 0,
		len(artifact.Datasets)+len(artifact.Filters)+len(artifact.Visualizations)+len(artifact.Sections))
	componentRefs := make(map[string]model.ArtifactRef)
	position := 0

	for _, dataset := range artifact.Datasets {
		componentID := "dataset:" + strings.TrimSpace(dataset.ID)
		columns := cloneArtifactColumns(dataset.Columns)
		matches := assetRefs[strings.ToLower(strings.TrimSpace(dataset.Asset))]
		if len(columns) == 0 && len(matches) == 1 {
			columns = cloneArtifactColumns(artifactColumns[artifactRefKey(matches[0])])
		}
		capabilities := []string{artifactCapabilityProducesRelation, artifactCapabilityVersioned}
		if len(columns) > 0 {
			capabilities = append(capabilities, artifactCapabilityHasSchema)
			sort.Strings(capabilities)
		}
		component := model.ArtifactComponent{
			ID: componentID, Kind: componentKindDataset, Name: dataset.ID,
			Capabilities: capabilities, Columns: columns,
		}
		components = append(components, component)
		child := model.ArtifactRef{Kind: kind, ArtifactID: artifactID, ComponentID: componentID}
		componentRefs[componentID] = child
		index.Containment = append(index.Containment, model.ArtifactContainment{Parent: parent, Child: child, Position: position})
		position++
		if len(matches) == 1 {
			index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
				Producer: matches[0], Consumer: child,
			})
		}
	}

	for _, filter := range artifact.Filters {
		componentID := "filter:" + strings.TrimSpace(filter.ID)
		component := model.ArtifactComponent{
			ID: componentID, Kind: componentKindFilter,
			Name:         filterDisplayName(filter),
			Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		}
		components = append(components, component)
		child := model.ArtifactRef{Kind: kind, ArtifactID: artifactID, ComponentID: componentID}
		componentRefs[componentID] = child
		index.Containment = append(index.Containment, model.ArtifactContainment{Parent: parent, Child: child, Position: position})
		position++
	}

	for _, visualization := range artifact.Visualizations {
		componentID := "visualization:" + strings.TrimSpace(visualization.ID)
		component := model.ArtifactComponent{
			ID: componentID, Kind: componentKindVisualization, Name: visualization.ID,
			Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		}
		components = append(components, component)
		child := model.ArtifactRef{Kind: kind, ArtifactID: artifactID, ComponentID: componentID}
		componentRefs[componentID] = child
		index.Containment = append(index.Containment, model.ArtifactContainment{Parent: parent, Child: child, Position: position})
		position++
	}

	for _, section := range artifact.Sections {
		componentID := "section:" + strings.TrimSpace(section.ID)
		name := strings.TrimSpace(section.Title)
		if name == "" {
			name = section.ID
		}
		component := model.ArtifactComponent{
			ID: componentID, Kind: componentKindSection, Name: name,
			Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		}
		components = append(components, component)
		child := model.ArtifactRef{Kind: kind, ArtifactID: artifactID, ComponentID: componentID}
		componentRefs[componentID] = child
		index.Containment = append(index.Containment, model.ArtifactContainment{Parent: parent, Child: child, Position: position})
		position++
	}

	index.Artifacts = append(index.Artifacts, model.ArtifactDescriptor{
		ID: artifactID, Kind: kind, WorkspaceID: EncodeID(artifact.Path), Path: artifact.Path,
		Title:        artifact.Title,
		Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		Components:   components,
	})

	for _, filter := range artifact.Filters {
		if filter.Options == nil || strings.TrimSpace(filter.Options.Dataset) == "" {
			continue
		}
		producer, producerOK := componentRefs["dataset:"+strings.TrimSpace(filter.Options.Dataset)]
		consumer, consumerOK := componentRefs["filter:"+strings.TrimSpace(filter.ID)]
		if !producerOK || !consumerOK {
			continue
		}
		columns := []model.ArtifactColumnUsage{{Name: strings.TrimSpace(filter.Options.ValueField), Role: "options.value_field"}}
		if label := strings.TrimSpace(filter.Options.LabelField); label != "" {
			columns = append(columns, model.ArtifactColumnUsage{Name: label, Role: "options.label_field"})
		}
		index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
			Producer: producer, Consumer: consumer, Columns: columns,
		})
	}

	for _, visualization := range artifact.Visualizations {
		consumer, consumerOK := componentRefs["visualization:"+strings.TrimSpace(visualization.ID)]
		producer, producerOK := componentRefs["dataset:"+strings.TrimSpace(visualization.Dataset)]
		if consumerOK && producerOK {
			index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
				Producer: producer, Consumer: consumer,
				Columns: visualizationColumnUsages(visualization.Definition),
			})
		}
		for bindingIndex, binding := range visualization.FilterBindings {
			filterRef, filterOK := componentRefs["filter:"+strings.TrimSpace(binding.Filter)]
			if filterOK && consumerOK {
				index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
					Producer: filterRef, Consumer: consumer,
				})
			}
			datasetID := strings.TrimSpace(binding.Dataset)
			if datasetID == "" {
				datasetID = strings.TrimSpace(visualization.Dataset)
			}
			bindingProducer, bindingProducerOK := componentRefs["dataset:"+datasetID]
			if bindingProducerOK && consumerOK && strings.TrimSpace(binding.Column) != "" {
				index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
					Producer: bindingProducer, Consumer: consumer,
					Columns: []model.ArtifactColumnUsage{{
						Name: strings.TrimSpace(binding.Column),
						Role: fmt.Sprintf("filter_bindings[%d].column", bindingIndex),
					}},
				})
			}
		}
	}

	for _, section := range artifact.Sections {
		producer, producerOK := componentRefs["visualization:"+strings.TrimSpace(section.Visualization)]
		consumer, consumerOK := componentRefs["section:"+strings.TrimSpace(section.ID)]
		if producerOK && consumerOK {
			index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
				Producer: producer, Consumer: consumer,
			})
		}
	}
}

func filterDisplayName(filter model.PresentationFilter) string {
	if label := strings.TrimSpace(filter.Label); label != "" {
		return label
	}
	return filter.ID
}

func notebookArtifactComponent(block model.NotebookBlock, cellsByID map[string]model.Asset) (model.ArtifactComponent, bool) {
	if block.Cell != "" {
		cell, ok := cellsByID[block.Cell]
		if !ok {
			return model.ArtifactComponent{}, false
		}
		kind := componentKindCell
		if cell.NotebookSource != nil {
			kind = componentKindSource
		}
		return model.ArtifactComponent{
			ID: block.Cell, Kind: kind, Name: cell.Name, Path: cell.Path,
			AssetType: cell.Type,
			Capabilities: []string{
				artifactCapabilityExecutable,
				artifactCapabilityHasSchema,
				artifactCapabilityProducesRelation,
				artifactCapabilityVersioned,
			},
			Columns: cloneArtifactColumns(cell.Columns),
		}, true
	}
	if block.Visualization != nil {
		id := strings.TrimSpace(block.ID)
		if id == "" {
			id = strings.TrimSpace(block.Visualization.ID)
		}
		if id == "" {
			return model.ArtifactComponent{}, false
		}
		return model.ArtifactComponent{
			ID: id, Kind: componentKindVisualization,
			Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
		}, true
	}
	if strings.TrimSpace(block.ID) == "" {
		return model.ArtifactComponent{}, false
	}
	return model.ArtifactComponent{
		ID: block.ID, Kind: componentKindMarkdown,
		Capabilities: []string{artifactCapabilityPresentation, artifactCapabilityVersioned},
	}, true
}

func visualizationColumnUsages(definition map[string]any) []model.ArtifactColumnUsage {
	usages := make([]model.ArtifactColumnUsage, 0)
	var visit func(value any, path string)
	visit = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				nextPath := key
				if path != "" {
					nextPath = path + "." + key
				}
				if key == "field" {
					if field, ok := typed[key].(string); ok && strings.TrimSpace(field) != "" {
						usages = append(usages, model.ArtifactColumnUsage{Name: strings.TrimSpace(field), Role: nextPath})
					}
				}
				visit(typed[key], nextPath)
			}
		case []any:
			for index, item := range typed {
				visit(item, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	visit(definition, "")
	sort.Slice(usages, func(i, j int) bool {
		if usages[i].Role == usages[j].Role {
			return usages[i].Name < usages[j].Name
		}
		return usages[i].Role < usages[j].Role
	})
	return usages
}

func appendArtifactDependency(existing []model.ArtifactDependency, candidate model.ArtifactDependency) []model.ArtifactDependency {
	for index, dependency := range existing {
		if artifactRefKey(dependency.Producer) == artifactRefKey(candidate.Producer) &&
			artifactRefKey(dependency.Consumer) == artifactRefKey(candidate.Consumer) {
			existing[index].Columns = mergeArtifactColumnUsages(dependency.Columns, candidate.Columns)
			return existing
		}
	}
	candidate.Columns = mergeArtifactColumnUsages(nil, candidate.Columns)
	return append(existing, candidate)
}

func mergeArtifactColumnUsages(left, right []model.ArtifactColumnUsage) []model.ArtifactColumnUsage {
	merged := append(append([]model.ArtifactColumnUsage(nil), left...), right...)
	seen := make(map[string]bool, len(merged))
	result := make([]model.ArtifactColumnUsage, 0, len(merged))
	for _, usage := range merged {
		key := strings.ToLower(strings.TrimSpace(usage.Name)) + "\x00" + strings.TrimSpace(usage.Role)
		if strings.TrimSpace(usage.Name) == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, usage)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role == result[j].Role {
			return result[i].Name < result[j].Name
		}
		return result[i].Role < result[j].Role
	})
	return result
}

func artifactRefKey(ref model.ArtifactRef) string {
	return ref.Kind + "\x00" + ref.ArtifactID + "\x00" + ref.ComponentID
}

func sortArtifactIndex(index *model.ArtifactIndex) {
	sort.Slice(index.Artifacts, func(i, j int) bool {
		if index.Artifacts[i].Kind == index.Artifacts[j].Kind {
			return index.Artifacts[i].ID < index.Artifacts[j].ID
		}
		return index.Artifacts[i].Kind < index.Artifacts[j].Kind
	})
	sort.Slice(index.Containment, func(i, j int) bool {
		left, right := index.Containment[i], index.Containment[j]
		if artifactRefKey(left.Parent) == artifactRefKey(right.Parent) {
			if left.Position == right.Position {
				return artifactRefKey(left.Child) < artifactRefKey(right.Child)
			}
			return left.Position < right.Position
		}
		return artifactRefKey(left.Parent) < artifactRefKey(right.Parent)
	})
	sort.Slice(index.Dependencies, func(i, j int) bool {
		left, right := index.Dependencies[i], index.Dependencies[j]
		if artifactRefKey(left.Consumer) == artifactRefKey(right.Consumer) {
			return artifactRefKey(left.Producer) < artifactRefKey(right.Producer)
		}
		return artifactRefKey(left.Consumer) < artifactRefKey(right.Consumer)
	})
}

func artifactIndexRevision(index model.ArtifactIndex) string {
	index.Revision = ""
	content, _ := json.Marshal(index)
	sum := sha256.Sum256(content)
	return "v1:" + hex.EncodeToString(sum[:])
}

func cloneArtifactColumns(columns []model.Column) []model.Column {
	if len(columns) == 0 {
		return nil
	}
	return append([]model.Column(nil), columns...)
}
