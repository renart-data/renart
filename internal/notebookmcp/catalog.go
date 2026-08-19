package notebookmcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

const (
	defaultCatalogResults = 20
	maxCatalogResults     = 50
	maxCatalogColumns     = 64
	maxCatalogLineage     = 12
	defaultSourceRows     = 10_000
)

type rankedCatalogMatch struct {
	match CatalogMatch
	score int
}

func (s *Server) searchCatalog(ctx context.Context, _ *mcp.CallToolRequest, input CatalogSearchInput) (*mcp.CallToolResult, CatalogSearchOutput, error) {
	state, err := s.backend.Workspace(ctx)
	if err != nil {
		return nil, CatalogSearchOutput{}, err
	}
	index := state.ArtifactIndex
	if strings.TrimSpace(index.Revision) == "" {
		index = service.BuildArtifactIndex(state)
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultCatalogResults
	}
	if limit > maxCatalogResults {
		limit = maxCatalogResults
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	kinds := normalizedCatalogKinds(input.Kinds)
	assets := workspaceAssetsByID(state)
	connections := workspaceConnectionTypes(state)
	notebookCells := workspaceNotebookCells(state)

	matches := make([]CatalogMatch, 0, len(index.Artifacts)*2)
	lineageRefs := make(map[string]CatalogLineageRef, len(index.Artifacts)*2)
	for _, artifact := range index.Artifacts {
		match := catalogArtifactMatch(artifact, assets[artifact.WorkspaceID], connections)
		matches = append(matches, match)
		lineageRefs[catalogRefKey(model.ArtifactRef{Kind: artifact.Kind, ArtifactID: artifact.ID})] = catalogLineageRef(match)
		for _, component := range artifact.Components {
			cell := notebookCells[artifact.WorkspaceID+"\x00"+component.ID]
			match := catalogComponentMatch(artifact, component, cell, connections)
			matches = append(matches, match)
			lineageRefs[catalogRefKey(model.ArtifactRef{
				Kind: artifact.Kind, ArtifactID: artifact.ID, ComponentID: component.ID,
			})] = catalogLineageRef(match)
		}
	}
	catalogAttachLineage(matches, index.Dependencies, lineageRefs)

	ranked := make([]rankedCatalogMatch, 0, len(matches))
	for _, match := range matches {
		if !catalogKindAllowed(match, kinds) {
			continue
		}
		if score := catalogMatchScore(match, query); score > 0 {
			ranked = append(ranked, rankedCatalogMatch{match: match, score: score})
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].match.DataSourceEligible != ranked[j].match.DataSourceEligible {
			return ranked[i].match.DataSourceEligible
		}
		left := strings.ToLower(ranked[i].match.Title)
		right := strings.ToLower(ranked[j].match.Title)
		if left != right {
			return left < right
		}
		return ranked[i].match.ID < ranked[j].match.ID
	})

	truncated := len(ranked) > limit
	if truncated {
		ranked = ranked[:limit]
	}
	results := make([]CatalogMatch, 0, len(ranked))
	for _, candidate := range ranked {
		results = append(results, candidate.match)
	}
	revision := strings.TrimSpace(index.Revision)
	if revision == "" {
		revision = fmt.Sprintf("workspace:%d", state.Revision)
	}
	return nil, CatalogSearchOutput{
		SchemaVersion: SchemaVersion,
		Revision:      revision,
		Query:         strings.TrimSpace(input.Query),
		Matches:       results,
		Truncated:     truncated,
	}, nil
}

func catalogArtifactMatch(
	artifact model.ArtifactDescriptor,
	asset model.Asset,
	connectionTypes map[string]string,
) CatalogMatch {
	match := CatalogMatch{
		ID:           artifact.ID,
		Kind:         artifact.Kind,
		ArtifactKind: artifact.Kind,
		ArtifactID:   artifact.ID,
		WorkspaceID:  artifact.WorkspaceID,
		Title:        artifact.Title,
		Capabilities: append([]string(nil), artifact.Capabilities...),
		Columns:      catalogColumns(artifact.Columns),
		ColumnCount:  len(artifact.Columns),
	}
	if artifact.Kind != "pipeline_asset" || strings.TrimSpace(asset.ID) == "" {
		return match
	}
	match.Connection = strings.TrimSpace(asset.Connection)
	match.ConnectionType = connectionTypes[strings.ToLower(match.Connection)]
	match.AssetType = strings.TrimSpace(asset.Type)
	match.Relation = strings.TrimSpace(asset.Name)
	match.Description = strings.TrimSpace(asset.Description)
	match.Tags = catalogStrings(asset.Tags)
	match.Materialization = catalogMaterialization(asset)
	match.DataSourceEligible = hasCatalogCapability(artifact.Capabilities, "produces_relation")
	if match.DataSourceEligible {
		approvalRequired := catalogSourceApprovalRequired(match.Connection, match.ConnectionType, match.AssetType)
		match.SuggestedSource = &CatalogSourceSuggestion{
			OperationKind:    service.NotebookOperationCellCreate,
			Language:         "sql",
			Connection:       match.Connection,
			AssetType:        match.AssetType,
			Content:          "select *\nfrom " + match.Relation,
			SnapshotMode:     "sample",
			RowLimit:         defaultSourceRows,
			ApprovalRequired: approvalRequired,
		}
	}
	return match
}

func catalogComponentMatch(
	artifact model.ArtifactDescriptor,
	component model.ArtifactComponent,
	cell model.Asset,
	connectionTypes map[string]string,
) CatalogMatch {
	title := strings.TrimSpace(component.Name)
	if title == "" {
		title = component.ID
	}
	match := CatalogMatch{
		ID:           artifact.ID + "#" + component.ID,
		Kind:         component.Kind,
		ArtifactKind: artifact.Kind,
		ArtifactID:   artifact.ID,
		ComponentID:  component.ID,
		WorkspaceID:  artifact.WorkspaceID,
		Title:        title,
		ParentTitle:  artifact.Title,
		Capabilities: append([]string(nil), component.Capabilities...),
		AssetType:    component.AssetType,
		Columns:      catalogColumns(component.Columns),
		ColumnCount:  len(component.Columns),
	}
	if strings.TrimSpace(cell.CellID) != "" {
		match.Connection = strings.TrimSpace(cell.Connection)
		match.ConnectionType = connectionTypes[strings.ToLower(match.Connection)]
		if match.AssetType == "" {
			match.AssetType = strings.TrimSpace(cell.Type)
		}
	}
	return match
}

func workspaceAssetsByID(state model.WorkspaceState) map[string]model.Asset {
	result := make(map[string]model.Asset)
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			result[asset.ID] = asset
		}
	}
	return result
}

func workspaceNotebookCells(state model.WorkspaceState) map[string]model.Asset {
	result := make(map[string]model.Asset)
	for _, notebook := range state.Notebooks {
		for _, cell := range notebook.Cells {
			result[notebook.ID+"\x00"+cell.CellID] = cell
		}
	}
	return result
}

func workspaceConnectionTypes(state model.WorkspaceState) map[string]string {
	result := make(map[string]string, len(state.Connections)+len(state.QueryConnections))
	for name, connectionType := range state.Connections {
		result[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(connectionType)
	}
	for _, connection := range state.QueryConnections {
		result[strings.ToLower(strings.TrimSpace(connection.Name))] = strings.TrimSpace(connection.ConnectionType)
	}
	return result
}

func catalogColumns(columns []model.Column) []ResultColumn {
	limit := min(len(columns), maxCatalogColumns)
	result := make([]ResultColumn, 0, limit)
	for index := 0; index < limit; index++ {
		result = append(result, ResultColumn{Name: columns[index].Name, Type: columns[index].Type})
	}
	return result
}

func catalogStrings(values []string) []string {
	result := catalogOrderedStrings(values)
	if len(result) == 0 {
		return nil
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func catalogOrderedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func catalogMaterialization(asset model.Asset) *CatalogMaterialization {
	materialization := CatalogMaterialization{
		Type:            strings.TrimSpace(asset.MaterializationType),
		Strategy:        strings.TrimSpace(asset.MaterializationStrategy),
		IncrementalKey:  strings.TrimSpace(asset.IncrementalKey),
		PartitionBy:     strings.TrimSpace(asset.PartitionBy),
		ClusterBy:       catalogOrderedStrings(asset.ClusterBy),
		TimeGranularity: strings.TrimSpace(asset.TimeGranularity),
	}
	if materialization.Type == "" && materialization.Strategy == "" &&
		materialization.IncrementalKey == "" && materialization.PartitionBy == "" &&
		len(materialization.ClusterBy) == 0 && materialization.TimeGranularity == "" {
		return nil
	}
	return &materialization
}

func catalogLineageRef(match CatalogMatch) CatalogLineageRef {
	return CatalogLineageRef{
		ID:          match.ID,
		Kind:        match.Kind,
		ArtifactID:  match.ArtifactID,
		ComponentID: match.ComponentID,
		Title:       match.Title,
		ParentTitle: match.ParentTitle,
		Relation:    match.Relation,
	}
}

func catalogRefKey(ref model.ArtifactRef) string {
	return ref.Kind + "\x00" + ref.ArtifactID + "\x00" + ref.ComponentID
}

func catalogAttachLineage(
	matches []CatalogMatch,
	dependencies []model.ArtifactDependency,
	refs map[string]CatalogLineageRef,
) {
	upstreams := make(map[string][]CatalogLineageRef)
	downstreams := make(map[string][]CatalogLineageRef)
	for _, dependency := range dependencies {
		producerKey := catalogRefKey(dependency.Producer)
		consumerKey := catalogRefKey(dependency.Consumer)
		producer, producerOK := refs[producerKey]
		consumer, consumerOK := refs[consumerKey]
		if !producerOK || !consumerOK {
			continue
		}
		upstreams[consumerKey] = appendCatalogLineageRef(upstreams[consumerKey], producer)
		downstreams[producerKey] = appendCatalogLineageRef(downstreams[producerKey], consumer)
	}
	for index := range matches {
		key := catalogRefKey(model.ArtifactRef{
			Kind: matches[index].ArtifactKind, ArtifactID: matches[index].ArtifactID,
			ComponentID: matches[index].ComponentID,
		})
		upstreamRefs := sortedCatalogLineageRefs(upstreams[key])
		downstreamRefs := sortedCatalogLineageRefs(downstreams[key])
		matches[index].UpstreamCount = len(upstreamRefs)
		matches[index].DownstreamCount = len(downstreamRefs)
		if len(upstreamRefs) > maxCatalogLineage {
			upstreamRefs = upstreamRefs[:maxCatalogLineage]
			matches[index].LineageTruncated = true
		}
		if len(downstreamRefs) > maxCatalogLineage {
			downstreamRefs = downstreamRefs[:maxCatalogLineage]
			matches[index].LineageTruncated = true
		}
		matches[index].Upstreams = upstreamRefs
		matches[index].Downstreams = downstreamRefs
	}
}

func appendCatalogLineageRef(existing []CatalogLineageRef, candidate CatalogLineageRef) []CatalogLineageRef {
	for _, ref := range existing {
		if ref.ID == candidate.ID {
			return existing
		}
	}
	return append(existing, candidate)
}

func sortedCatalogLineageRefs(refs []CatalogLineageRef) []CatalogLineageRef {
	if len(refs) == 0 {
		return nil
	}
	result := append([]CatalogLineageRef(nil), refs...)
	sort.Slice(result, func(i, j int) bool {
		left := strings.ToLower(result[i].Title)
		right := strings.ToLower(result[j].Title)
		if left != right {
			return left < right
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func normalizedCatalogKinds(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
			result[normalized] = true
		}
	}
	return result
}

func catalogKindAllowed(match CatalogMatch, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[strings.ToLower(match.Kind)] || allowed[strings.ToLower(match.ArtifactKind)]
}

func catalogMatchScore(match CatalogMatch, query string) int {
	if query == "" {
		if match.DataSourceEligible {
			return 20
		}
		return 10
	}
	primary := []string{match.Title, match.Relation}
	secondary := []string{
		match.ParentTitle, match.Connection, match.ConnectionType, match.AssetType,
		match.Description, strings.Join(match.Tags, " "),
		strings.Join(match.Capabilities, " "), match.Kind, match.ArtifactKind,
	}
	if match.Materialization != nil {
		secondary = append(secondary,
			match.Materialization.Type,
			match.Materialization.Strategy,
			match.Materialization.IncrementalKey,
			match.Materialization.TimeGranularity,
		)
	}
	for _, ref := range append(append([]CatalogLineageRef(nil), match.Upstreams...), match.Downstreams...) {
		secondary = append(secondary, ref.Title, ref.ParentTitle, ref.Relation)
	}
	best := 0
	for _, value := range primary {
		best = max(best, scoreCatalogText(value, query, 1000, 800, 600))
	}
	for _, value := range secondary {
		best = max(best, scoreCatalogText(value, query, 500, 400, 300))
	}
	for _, column := range match.Columns {
		best = max(best, scoreCatalogText(column.Name, query, 450, 350, 250))
	}
	return best
}

func scoreCatalogText(value, query string, exact, prefix, contains int) int {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 0
	}
	if value == query {
		return exact
	}
	if strings.HasPrefix(value, query) {
		return prefix
	}
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if strings.HasPrefix(field, query) {
			return prefix - 25
		}
	}
	if strings.Contains(value, query) {
		return contains
	}
	return 0
}

func hasCatalogCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), wanted) {
			return true
		}
	}
	return false
}

func catalogSourceApprovalRequired(connection, connectionType, assetType string) bool {
	if strings.TrimSpace(connection) == "" {
		return false
	}
	connectionType = strings.ToLower(strings.TrimSpace(connectionType))
	assetType = strings.ToLower(strings.TrimSpace(assetType))
	return connectionType != "duckdb" && !strings.HasPrefix(assetType, "duckdb.")
}
