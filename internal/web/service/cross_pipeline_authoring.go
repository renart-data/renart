package service

import (
	"fmt"
	"sort"
	"strings"

	"renart/internal/authoringdiag"
	"renart/internal/sqllsp"
	webmodel "renart/internal/web/model"
)

const (
	crossPipelineReferenceDeclarable         = "declarable"
	crossPipelineReferenceProducerURIMissing = "producer_uri_missing"
	crossPipelineReferenceConnectionUnknown  = "connection_unknown"
	crossPipelineReferenceConnectionMismatch = "connection_mismatch"
	crossPipelineReferenceAmbiguous          = "ambiguous"

	sqlLSPActionDeclareCrossPipelineDependency = "declare-cross-pipeline-dependency"
	sqlLSPActionOpenAsset                      = "open-asset"
)

type workspaceAssetLocation struct {
	Pipeline webmodel.Pipeline
	Asset    webmodel.Asset
}

type crossPipelineAuthoringReference struct {
	Relation    sqllsp.DocumentRelation
	Consumer    workspaceAssetLocation
	Producer    workspaceAssetLocation
	Candidates  []workspaceAssetLocation
	Status      string
	HasProducer bool
}

// crossPipelineAuthoringReferences recognizes SQL that already resolves to an
// asset in a sibling pipeline but has not yet declared Bruin's workspace-wide
// URI dependency. It is intentionally an authoring observation only: runtime
// ordering and freshness continue to derive exclusively from persisted
// dependencies.
func crossPipelineAuthoringReferences(
	state webmodel.WorkspaceState,
	consumerAssetID string,
	engine *sqllsp.Engine,
	doc sqllsp.TextDocumentItem,
) []crossPipelineAuthoringReference {
	if engine == nil || strings.TrimSpace(consumerAssetID) == "" {
		return nil
	}
	consumer, ok := workspaceAssetLocationByID(state, consumerAssetID)
	if !ok {
		return nil
	}

	result := make([]crossPipelineAuthoringReference, 0)
	for _, relation := range engine.DocumentRelations(doc) {
		candidates, local := workspaceRelationCandidates(state, consumer.Pipeline.ID, relation)
		if local || len(candidates) == 0 {
			continue
		}
		if len(candidates) > 1 {
			result = append(result, crossPipelineAuthoringReference{
				Relation:   relation,
				Consumer:   consumer,
				Candidates: candidates,
				Status:     crossPipelineReferenceAmbiguous,
			})
			continue
		}

		producer := candidates[0]
		if hasDeclaredCrossPipelineDependency(consumer.Asset, producer) {
			continue
		}
		status := crossPipelineReferenceDeclarable
		consumerConnection := strings.TrimSpace(consumer.Asset.Connection)
		producerConnection := strings.TrimSpace(producer.Asset.Connection)
		switch {
		case strings.TrimSpace(producer.Asset.URI) == "":
			status = crossPipelineReferenceProducerURIMissing
		case consumerConnection == "" || producerConnection == "":
			status = crossPipelineReferenceConnectionUnknown
		case !strings.EqualFold(consumerConnection, producerConnection):
			status = crossPipelineReferenceConnectionMismatch
		}
		result = append(result, crossPipelineAuthoringReference{
			Relation:    relation,
			Consumer:    consumer,
			Producer:    producer,
			Status:      status,
			HasProducer: true,
		})
	}
	return result
}

func workspaceAssetLocationByID(state webmodel.WorkspaceState, assetID string) (workspaceAssetLocation, bool) {
	for _, candidatePipeline := range state.Pipelines {
		for _, candidateAsset := range candidatePipeline.Assets {
			if candidateAsset.ID == assetID {
				return workspaceAssetLocation{Pipeline: candidatePipeline, Asset: candidateAsset}, true
			}
		}
	}
	return workspaceAssetLocation{}, false
}

func workspaceRelationCandidates(
	state webmodel.WorkspaceState,
	consumerPipelineID string,
	relation sqllsp.DocumentRelation,
) ([]workspaceAssetLocation, bool) {
	for _, name := range []string{relation.Name, relation.ResolvedName} {
		exact := workspaceAssetsNamed(state, name)
		if local := containsPipelineAsset(exact, consumerPipelineID); local {
			return nil, true
		}
		if len(exact) > 0 {
			return sortedWorkspaceAssetLocations(exact), false
		}
	}

	if strings.TrimSpace(relation.AssetID) != "" {
		if resolved, ok := workspaceAssetLocationByID(state, relation.AssetID); ok {
			if resolved.Pipeline.ID == consumerPipelineID {
				return nil, true
			}
			return []workspaceAssetLocation{resolved}, false
		}
	}

	rawName := normalizeWorkspaceRelationName(relation.Name)
	if rawName == "" || strings.Contains(rawName, ".") {
		return nil, false
	}
	var shortMatches []workspaceAssetLocation
	for _, candidatePipeline := range state.Pipelines {
		for _, candidateAsset := range candidatePipeline.Assets {
			if shortWorkspaceRelationName(candidateAsset.Name) == rawName {
				shortMatches = appendWorkspaceAssetLocation(shortMatches, candidatePipeline, candidateAsset)
			}
		}
	}
	if local := containsPipelineAsset(shortMatches, consumerPipelineID); local {
		return nil, true
	}
	return sortedWorkspaceAssetLocations(shortMatches), false
}

func workspaceAssetsNamed(state webmodel.WorkspaceState, name string) []workspaceAssetLocation {
	normalized := normalizeWorkspaceRelationName(name)
	if normalized == "" {
		return nil
	}
	var result []workspaceAssetLocation
	for _, candidatePipeline := range state.Pipelines {
		for _, candidateAsset := range candidatePipeline.Assets {
			if normalizeWorkspaceRelationName(candidateAsset.Name) == normalized {
				result = appendWorkspaceAssetLocation(result, candidatePipeline, candidateAsset)
			}
		}
	}
	return result
}

func appendWorkspaceAssetLocation(
	locations []workspaceAssetLocation,
	pipeline webmodel.Pipeline,
	asset webmodel.Asset,
) []workspaceAssetLocation {
	for _, current := range locations {
		if current.Pipeline.ID == pipeline.ID && current.Asset.ID == asset.ID {
			return locations
		}
	}
	return append(locations, workspaceAssetLocation{Pipeline: pipeline, Asset: asset})
}

func sortedWorkspaceAssetLocations(locations []workspaceAssetLocation) []workspaceAssetLocation {
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].Pipeline.Name != locations[j].Pipeline.Name {
			return locations[i].Pipeline.Name < locations[j].Pipeline.Name
		}
		if locations[i].Asset.Name != locations[j].Asset.Name {
			return locations[i].Asset.Name < locations[j].Asset.Name
		}
		return locations[i].Asset.ID < locations[j].Asset.ID
	})
	return locations
}

func containsPipelineAsset(locations []workspaceAssetLocation, pipelineID string) bool {
	for _, location := range locations {
		if location.Pipeline.ID == pipelineID {
			return true
		}
	}
	return false
}

func normalizeWorkspaceRelationName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) >= 2 {
			switch {
			case (part[0] == '"' && part[len(part)-1] == '"') ||
				(part[0] == '`' && part[len(part)-1] == '`') ||
				(part[0] == '[' && part[len(part)-1] == ']'):
				part = part[1 : len(part)-1]
			}
		}
		parts[index] = strings.ToLower(strings.TrimSpace(part))
	}
	return strings.Join(parts, ".")
}

func shortWorkspaceRelationName(name string) string {
	normalized := normalizeWorkspaceRelationName(name)
	if index := strings.LastIndex(normalized, "."); index >= 0 {
		return normalized[index+1:]
	}
	return normalized
}

func hasDeclaredCrossPipelineDependency(consumer webmodel.Asset, producer workspaceAssetLocation) bool {
	producerURI := strings.TrimSpace(producer.Asset.URI)
	for _, dependency := range consumer.Dependencies {
		if dependency.ResolvedAssetID == producer.Asset.ID &&
			(dependency.ResolvedPipelineID == "" || dependency.ResolvedPipelineID == producer.Pipeline.ID) {
			return true
		}
		if producerURI != "" && strings.EqualFold(strings.TrimSpace(dependency.Type), "uri") &&
			strings.EqualFold(strings.TrimSpace(dependency.Value), producerURI) {
			return true
		}
	}
	return false
}

func (reference crossPipelineAuthoringReference) diagnostic() sqllsp.Diagnostic {
	code := authoringdiag.CodeCrossPipelineDependencyMissing
	message := reference.message()
	if reference.Status == crossPipelineReferenceAmbiguous {
		code = authoringdiag.CodeCrossPipelineRelationAmbiguous
	}
	return sqllsp.Diagnostic{
		Range:      reference.Relation.Range,
		Severity:   2,
		Code:       code,
		Source:     authoringdiag.SourceRenart,
		Message:    message,
		Scope:      string(authoringdiag.ScopeDocument),
		Confidence: string(authoringdiag.ConfidenceHigh),
	}
}

func (reference crossPipelineAuthoringReference) message() string {
	relation := strings.TrimSpace(reference.Relation.Name)
	if relation == "" {
		relation = strings.TrimSpace(reference.Relation.ResolvedName)
	}
	if reference.Status == crossPipelineReferenceAmbiguous {
		labels := make([]string, 0, len(reference.Candidates))
		for _, candidate := range reference.Candidates {
			labels = append(labels, fmt.Sprintf("%s in %s", candidate.Asset.Name, candidate.Pipeline.Name))
		}
		return fmt.Sprintf(
			"Relation %q matches assets in multiple sibling pipelines (%s). Qualify the SQL relation or declare the intended producer URI explicitly.",
			relation,
			strings.Join(labels, ", "),
		)
	}

	prefix := fmt.Sprintf(
		"Relation %q resolves to asset %q in sibling pipeline %q, but this asset has no declared URI dependency.",
		relation,
		reference.Producer.Asset.Name,
		reference.Producer.Pipeline.Name,
	)
	switch reference.Status {
	case crossPipelineReferenceProducerURIMissing:
		return prefix + " Declare a URI on the producer before linking the pipelines."
	case crossPipelineReferenceConnectionUnknown:
		return prefix + " Renart cannot add it safely because one of the effective connections is unknown."
	case crossPipelineReferenceConnectionMismatch:
		return fmt.Sprintf(
			"%s The producer uses connection %q while the consumer uses %q, so Renart cannot add it safely.",
			prefix,
			reference.Producer.Asset.Connection,
			reference.Consumer.Asset.Connection,
		)
	default:
		return prefix + " Add a full dependency for ordering and freshness, or a symbolic dependency for lineage only."
	}
}

func (reference crossPipelineAuthoringReference) codeActions(diagnostic sqllsp.Diagnostic) []sqllsp.CodeAction {
	if !reference.HasProducer {
		return nil
	}
	switch reference.Status {
	case crossPipelineReferenceDeclarable:
		return []sqllsp.CodeAction{
			reference.dependencyCodeAction(diagnostic, "full", "Add full cross-pipeline dependency", true),
			reference.dependencyCodeAction(diagnostic, "symbolic", "Add symbolic cross-pipeline dependency", false),
		}
	case crossPipelineReferenceProducerURIMissing:
		return []sqllsp.CodeAction{{
			Title:       "Open producer to declare URI",
			Kind:        "quickfix",
			Diagnostics: []sqllsp.Diagnostic{diagnostic},
			Action: &sqllsp.CodeActionAction{
				Type:       sqlLSPActionOpenAsset,
				PipelineID: reference.Producer.Pipeline.ID,
				AssetID:    reference.Producer.Asset.ID,
			},
		}}
	}
	return nil
}

func (reference crossPipelineAuthoringReference) dependencyCodeAction(
	diagnostic sqllsp.Diagnostic,
	mode string,
	title string,
	preferred bool,
) sqllsp.CodeAction {
	return sqllsp.CodeAction{
		Title:       title,
		Kind:        "quickfix",
		Diagnostics: []sqllsp.Diagnostic{diagnostic},
		IsPreferred: preferred,
		Action: &sqllsp.CodeActionAction{
			Type:    sqlLSPActionDeclareCrossPipelineDependency,
			AssetID: reference.Consumer.Asset.ID,
			URI:     strings.TrimSpace(reference.Producer.Asset.URI),
			Mode:    mode,
		},
	}
}

func (reference crossPipelineAuthoringReference) typeCheckResolutions() []TypeCheckResolution {
	if !reference.HasProducer {
		return nil
	}
	switch reference.Status {
	case crossPipelineReferenceDeclarable:
		producerURI := strings.TrimSpace(reference.Producer.Asset.URI)
		return []TypeCheckResolution{
			{
				ID:    "add-full-cross-pipeline-dependency",
				Title: "Add full dependency",
				Transaction: &TypeCheckResolutionTransaction{
					Type: TxDependencyManualAdd,
					Dependency: &TransactionDependency{
						URI:  producerURI,
						Mode: "full",
					},
				},
			},
			{
				ID:    "add-symbolic-cross-pipeline-dependency",
				Title: "Add symbolic dependency",
				Transaction: &TypeCheckResolutionTransaction{
					Type: TxDependencyManualAdd,
					Dependency: &TransactionDependency{
						URI:  producerURI,
						Mode: "symbolic",
					},
				},
			},
		}
	case crossPipelineReferenceProducerURIMissing:
		return []TypeCheckResolution{{
			ID:    "open-cross-pipeline-producer",
			Title: "Open producer to declare URI",
			Action: &TypeCheckResolutionAction{
				Type:       typeCheckResolutionActionOpenAsset,
				PipelineID: reference.Producer.Pipeline.ID,
				AssetID:    reference.Producer.Asset.ID,
			},
		}}
	}
	return nil
}

func (reference crossPipelineAuthoringReference) reportReference() (TypeCheckCrossPipelineReference, bool) {
	if !reference.HasProducer {
		return TypeCheckCrossPipelineReference{}, false
	}
	return TypeCheckCrossPipelineReference{
		ID:                   reference.Consumer.Asset.ID + "->" + reference.Producer.Asset.ID,
		Status:               reference.Status,
		Relation:             strings.TrimSpace(reference.Relation.Name),
		ConsumerAssetID:      reference.Consumer.Asset.ID,
		ConsumerAssetName:    reference.Consumer.Asset.Name,
		ProducerAssetID:      reference.Producer.Asset.ID,
		ProducerAssetName:    reference.Producer.Asset.Name,
		ProducerPipelineID:   reference.Producer.Pipeline.ID,
		ProducerPipelineName: reference.Producer.Pipeline.Name,
		ProducerURI:          strings.TrimSpace(reference.Producer.Asset.URI),
	}, true
}
