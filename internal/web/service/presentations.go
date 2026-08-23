package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"
	"renart/internal/sqllsp"
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
		datasetSchemas, resolutionFindings := resolvePresentationDatasetSchemas(ctx, s.workspaceRoot, *state, artifact)
		artifact.Problems = append(
			(presentation.Checker{}).CheckArtifact(
				ctx, *artifact, datasetSchemas, presentation.CheckOptions{Strict: true},
			),
			resolutionFindings...,
		)
		artifact.Problems = uniquePresentationFindings(artifact.Problems)
		state.Presentations = append(state.Presentations, presentationToModel(s.workspaceRoot, artifact, datasetSchemas))
	}
	sort.Slice(state.Presentations, func(i, j int) bool {
		return state.Presentations[i].Path < state.Presentations[j].Path
	})
}

func resolvePresentationDatasetSchemas(
	ctx context.Context,
	workspaceRoot string,
	state model.WorkspaceState,
	artifact *presentation.Artifact,
) (map[string]presentation.ResolvedSchema, []presentation.Finding) {
	schemas := make(map[string]presentation.ResolvedSchema, len(artifact.Datasets))
	findings := make([]presentation.Finding, 0)
	var authoringGraph sqllsp.CanonicalGraph
	authoringGraphReady := false
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
		if len(columns) == 0 && strings.TrimSpace(definition.Query) != "" {
			if !authoringGraphReady {
				authoringGraph = buildWorkspaceCanonicalGraph(ctx, workspaceRoot, state)
				authoringGraphReady = true
			}
			if inferred, ok := inferPresentationQuerySchema(ctx, state, authoringGraph, artifact.ID, id, definition); ok {
				columns = inferred.Columns
				schemas[id] = inferred
			}
		}
		if len(columns) > 0 {
			if _, inferred := schemas[id]; inferred {
				continue
			}
			schemas[id] = presentation.ResolvedSchema{
				Source:  presentation.DataSourceRef{Kind: "dataset", ArtifactID: artifact.ID, ComponentID: id},
				Columns: columns, Complete: true,
			}
		}
	}
	return schemas, findings
}

// inferPresentationQuerySchema derives a query dataset's output only from the
// Git-authored workspace graph. Live catalog observations deliberately stay
// outside this path: they improve Monaco input intelligence, but cannot become
// a dashboard or report's deploy-time schema contract.
func inferPresentationQuerySchema(
	ctx context.Context,
	state model.WorkspaceState,
	base sqllsp.CanonicalGraph,
	artifactID string,
	datasetID string,
	definition presentation.DatasetDefinition,
) (presentation.ResolvedSchema, bool) {
	connection := strings.TrimSpace(definition.Connection)
	queryType, ok := queryAssetTypeForConnectionType(state.Connections[connection])
	if connection == "" || !ok {
		return presentation.ResolvedSchema{}, false
	}

	uri := sqllsp.URI(
		"inmemory://renart/presentation-query-schema/" + strings.TrimSpace(artifactID) + "/" + strings.TrimSpace(datasetID) + ".sql",
	)
	dialect := sqllsp.DialectFromAssetType(string(queryType))
	inferred := sqllsp.InferOutputSchema(ctx, base, sqllsp.TextDocumentItem{
		URI: uri, LanguageID: "sql", Text: definition.Query,
	}, dialect)
	columns := make([]presentation.ResolvedColumn, 0, len(inferred.Columns))
	for _, column := range inferred.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		columns = append(columns, presentation.ResolvedColumn{
			Name: name, PhysicalType: column.Type,
			SemanticType: presentation.SemanticTypeForPhysicalType(column.Type), Nullable: column.Nullable,
		})
	}
	if len(columns) == 0 {
		return presentation.ResolvedSchema{}, false
	}
	return presentation.ResolvedSchema{
		Source:  presentation.DataSourceRef{Kind: "dataset", ArtifactID: artifactID, ComponentID: datasetID},
		Columns: columns, Complete: strings.EqualFold(strings.TrimSpace(inferred.Completeness), "complete"),
	}, true
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

func presentationToModel(
	workspaceRoot string,
	artifact *presentation.Artifact,
	datasetSchemas map[string]presentation.ResolvedSchema,
) model.PresentationArtifact {
	return presentation.ArtifactToModel(workspaceRoot, artifact, datasetSchemas)
}

func presentationFindingsToModel(findings []presentation.Finding) []model.PresentationFinding {
	return presentation.FindingsToModel(findings)
}

func presentationFromModel(path string, input model.PresentationArtifact) (*presentation.Artifact, error) {
	return presentation.ArtifactFromModel(path, input)
}

func sortedPresentationDatasetIDs(datasets map[string]presentation.DatasetDefinition) []string {
	return presentation.SortedDatasetIDs(datasets)
}
