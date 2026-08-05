package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	webmodel "renart/internal/web/model"
	"renart/internal/web/service/assetmeta"
)

const (
	columnSyncStatusApplied   = "applied"
	columnSyncStatusUnchanged = "unchanged"
	columnSyncStatusConflicts = "conflicts"
)

type columnSchemaAnalysis struct {
	rows             []webmodel.ColumnSchemaMergeRow
	managedColumns   []WorkspaceColumn
	candidateColumns []WorkspaceColumn
	typeSources      map[string]string
	provenanceChange bool
	hasChanges       bool
	hasConflicts     bool
}

const (
	columnSourceCodeMaterialized = "m"
	columnSourceCodeLiveResponse = "l"
)

// SyncAssetColumns automatically observes the asset definition plus any
// explicitly selected advisory sources. Additions and unknown-to-known type
// refinements are safe and are reconciled immediately. Any deletion, known
// type change, or disagreement between sources is returned for resolution
// without mutating the asset.
func (s *AssetService) SyncAssetColumns(
	ctx context.Context,
	assetID string,
	additionalSourceIDs []string,
	environment string,
) (webmodel.ColumnSchemaSyncResult, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return webmodel.ColumnSchemaSyncResult{}, badRequestError("asset_resolve_failed", err.Error())
	}

	capabilities := columnInferenceSourcesForPipelineAsset(asset, parsedPipeline)
	meta := assetmeta.ParseAsset(asset)
	selected := make(map[string]struct{}, len(additionalSourceIDs))
	explicitlySelected := make(map[string]struct{}, len(additionalSourceIDs))
	for _, sourceID := range additionalSourceIDs {
		if sourceID = strings.TrimSpace(sourceID); sourceID != "" {
			selected[sourceID] = struct{}{}
			explicitlySelected[sourceID] = struct{}{}
		}
	}

	availableObserved := make(map[string]webmodel.ColumnInferenceSource)
	for _, source := range capabilities {
		if source.Category == "observed" {
			availableObserved[source.ID] = source
		}
	}
	for sourceID := range selected {
		if _, ok := availableObserved[sourceID]; !ok {
			return webmodel.ColumnSchemaSyncResult{}, badRequestError(
				"unsupported_column_source",
				fmt.Sprintf("advisory schema source %q is not available for this asset", sourceID),
			)
		}
	}
	// Re-observe non-default sources already responsible for saved types. SQL /
	// definition provenance is implicit, so ordinary assets add no metadata and
	// no extra observation. A missing or retired capability is ignored and the
	// stored type remains usable as historical evidence when SQL is still
	// unknown.
	provenanceSelected := make(map[string]struct{})
	for _, code := range meta.ColSource {
		sourceID := columnSourceIDForCode(code)
		if _, available := availableObserved[sourceID]; !available {
			continue
		}
		selected[sourceID] = struct{}{}
		if _, explicit := explicitlySelected[sourceID]; !explicit {
			provenanceSelected[sourceID] = struct{}{}
		}
	}

	evidence := make([]SchemaEvidence, 0, len(capabilities))
	notes := make([]string, 0)
	for _, source := range capabilities {
		_, sourceSelected := selected[source.ID]
		if source.Category != "definition" && !sourceSelected {
			continue
		}
		observation, apiErr := s.observeAssetColumnSource(
			ctx,
			assetID,
			parsedPipeline,
			asset,
			source,
			environment,
		)
		if apiErr != nil {
			// API assets may intentionally omit a declarative response schema. A
			// selected live request is then the best available primary observation.
			_, liveSelected := selected[columnSourceLiveResponse]
			if source.Category == "definition" && isAPIAsset(asset) && liveSelected && apiErr.Code == "column_inference_failed" {
				notes = append(notes, "No response schema is declared; using the live request as the primary inference source.")
				continue
			}
			if _, automatic := provenanceSelected[source.ID]; automatic {
				notes = append(notes, fmt.Sprintf("Could not refresh %s provenance; keeping the last saved evidence for unresolved SQL types.", source.Label))
				continue
			}
			return webmodel.ColumnSchemaSyncResult{}, apiErr
		}
		if observation.Columns == nil {
			observation.Columns = []WorkspaceColumn{}
		}
		if source.ID == columnSourceMaterialized && s.deps.MaterializedSchemaFresh != nil {
			fresh, freshErr := s.deps.MaterializedSchemaFresh(ctx, assetID, asset.Name, environment)
			if freshErr != nil {
				notes = append(notes, "Current-table freshness could not be verified, so its schema remains advisory.")
			} else {
				observation.Fresh = &fresh
				if fresh {
					observation.AssetRevision = schemaAssetRevision(asset)
				}
			}
		}
		evidence = append(evidence, observation)
		if _, automatic := provenanceSelected[source.ID]; automatic {
			notes = append(notes, fmt.Sprintf("Included %s because it is the saved type source for one or more columns.", source.Label))
		}
	}
	if len(evidence) == 0 {
		return webmodel.ColumnSchemaSyncResult{}, badRequestError(
			"column_source_required",
			"select at least one available schema source",
		)
	}

	connectionName, _ := targetConnectionNameForAsset(asset, parsedPipeline)
	requestedScope := SchemaEvidenceScope{
		Environment: strings.TrimSpace(environment), Connection: strings.TrimSpace(connectionName), Relation: strings.TrimSpace(asset.Name),
	}
	resolvedEvidence := resolveSchemaEvidence(evidence, requestedScope)
	excludedBySource := make(map[string]SchemaEvidenceExclusion, len(resolvedEvidence.Excluded))
	for _, excluded := range resolvedEvidence.Excluded {
		excludedBySource[excluded.Evidence.Source.ID] = excluded
		notes = append(notes, fmt.Sprintf("Excluded %s as %s evidence: %s", excluded.Evidence.Source.Label, excluded.Classification, excluded.Reason))
	}
	allSnapshots := make([]webmodel.ColumnSchemaSourceSnapshot, 0, len(evidence))
	for _, item := range evidence {
		if excluded, ok := excludedBySource[item.Source.ID]; ok {
			allSnapshots = append(allSnapshots, schemaEvidenceSnapshot(item, excluded.Classification, excluded.Reason))
		} else {
			allSnapshots = append(allSnapshots, schemaEvidenceSnapshot(item, "comparable", ""))
		}
	}
	comparableSnapshots := make([]webmodel.ColumnSchemaSourceSnapshot, 0, len(resolvedEvidence.Comparable))
	for _, item := range resolvedEvidence.Comparable {
		comparableSnapshots = append(comparableSnapshots, schemaEvidenceSnapshot(item, "comparable", ""))
	}
	if len(comparableSnapshots) == 0 {
		return webmodel.ColumnSchemaSyncResult{}, badRequestError(
			"column_source_incomparable",
			"none of the selected schema observations describe the current asset output",
		)
	}

	analysis := analyzeColumnSchema(asset.Columns, meta, comparableSnapshots)
	result := webmodel.ColumnSchemaSyncResult{
		Sources:          allSnapshots,
		Rows:             analysis.rows,
		ManagedColumns:   analysis.managedColumns,
		CandidateColumns: analysis.candidateColumns,
		Columns:          PipelineColumnsToModelColumns(asset.Columns),
		Notes:            notes,
	}
	if analysis.hasConflicts {
		result.Status = columnSyncStatusConflicts
		return result, nil
	}
	if !analysis.hasChanges && !analysis.provenanceChange {
		result.Status = columnSyncStatusUnchanged
		return result, nil
	}

	reconciled, apiErr := s.reconcileAssetColumns(ctx, assetID, func(_ *pipeline.Asset, nextMeta *assetmeta.RenartMeta) ([]pipeline.Column, *APIError) {
		for column, sourceCode := range analysis.typeSources {
			nextMeta.ColSource = setColumnSource(nextMeta.ColSource, column, sourceCode)
			if sourceCode != "" {
				nextMeta.ColOwn = disownField(nextMeta.ColOwn, column, "type")
			}
		}
		return ModelColumnsToPipelineColumns(analysis.managedColumns), nil
	})
	if apiErr != nil {
		return webmodel.ColumnSchemaSyncResult{}, apiErr
	}
	result.Status = columnSyncStatusApplied
	result.Columns = reconciled.Columns
	return result, nil
}

// ApplyAssetColumnSchemaResolution applies the user's merge choices in one
// provenance-aware write. Keeping the saved type takes ownership of that field;
// choosing the definition releases ownership and uses the implicit default;
// choosing an observed source records its compact source code; removing an
// inferred candidate records a durable drop marker.
func (s *AssetService) ApplyAssetColumnSchemaResolution(
	ctx context.Context,
	assetID string,
	managedColumns []WorkspaceColumn,
	candidateColumns []WorkspaceColumn,
	expectedCurrent []WorkspaceColumn,
	resolutions []webmodel.ColumnSchemaResolution,
) (ColumnReconcileResult, *APIError) {
	candidates := ModelColumnsToPipelineColumns(candidateColumns)
	candidateByName := make(map[string]pipeline.Column, len(candidates))
	for _, candidate := range candidates {
		if key := columnNameKey(candidate.Name); key != "" {
			candidateByName[key] = candidate
		}
	}
	managedNames := make(map[string]struct{}, len(managedColumns))
	for _, managed := range managedColumns {
		if key := columnNameKey(managed.Name); key != "" {
			managedNames[key] = struct{}{}
		}
	}
	return s.reconcileAssetColumns(ctx, assetID, func(asset *pipeline.Asset, meta *assetmeta.RenartMeta) ([]pipeline.Column, *APIError) {
		if !sameColumnSchema(asset.Columns, expectedCurrent) {
			return nil, newAPIError(
				http.StatusConflict,
				"schema_sync_stale",
				"the saved schema changed while the resolver was open; sync again before applying",
			)
		}
		inferred := ModelColumnsToPipelineColumns(managedColumns)
		currentByName := make(map[string]pipeline.Column, len(asset.Columns))
		for _, column := range asset.Columns {
			if key := columnNameKey(column.Name); key != "" {
				currentByName[key] = column
			}
		}

		// Keep an existing ignore durable when its advisory source was selected.
		for _, droppedName := range meta.ColDrop {
			if candidate, ok := candidateByName[columnNameKey(droppedName)]; ok {
				inferred = upsertColumn(inferred, candidate)
			}
		}

		seen := make(map[string]struct{}, len(resolutions))
		for _, resolution := range resolutions {
			key := columnNameKey(resolution.Column)
			if key == "" {
				return nil, badRequestError("invalid_schema_resolution", "resolution column is required")
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, badRequestError("invalid_schema_resolution", fmt.Sprintf("column %q has more than one resolution", resolution.Column))
			}
			seen[key] = struct{}{}
			candidate, isCandidate := candidateByName[key]
			_, isManagedCandidate := managedNames[key]

			switch strings.TrimSpace(resolution.Action) {
			case "remove":
				if isCandidate {
					inferred = upsertColumn(inferred, candidate)
					meta.ColDrop = appendName(meta.ColDrop, candidate.Name)
					meta.ColAdd = removeName(meta.ColAdd, candidate.Name)
					meta.ColOwn = disownField(meta.ColOwn, candidate.Name, "type")
					meta.ColSource = removeColumnSource(meta.ColSource, candidate.Name)
				} else {
					asset.Columns = removeColumnByName(asset.Columns, resolution.Column)
					meta.ColAdd = removeName(meta.ColAdd, resolution.Column)
					meta.ColDrop = removeName(meta.ColDrop, resolution.Column)
					meta.ColOwn = disownField(meta.ColOwn, resolution.Column, "type")
					meta.ColSource = removeColumnSource(meta.ColSource, resolution.Column)
				}

			case "use":
				source := strings.TrimSpace(resolution.Source)
				if source == "current" {
					currentColumn, ok := currentByName[key]
					if !ok {
						return nil, badRequestError("invalid_schema_resolution", fmt.Sprintf("saved column %q is not available", resolution.Column))
					}
					if isCandidate && isManagedCandidate {
						candidate.Type = currentColumn.Type
						inferred = upsertColumn(inferred, candidate)
						meta.ColAdd = removeName(meta.ColAdd, candidate.Name)
						meta.ColDrop = removeName(meta.ColDrop, candidate.Name)
						meta.ColOwn = ownField(meta.ColOwn, candidate.Name, "type")
						meta.ColSource = removeColumnSource(meta.ColSource, candidate.Name)
					} else {
						meta.ColAdd = appendName(meta.ColAdd, currentColumn.Name)
						meta.ColDrop = removeName(meta.ColDrop, currentColumn.Name)
						meta.ColOwn = ownField(meta.ColOwn, currentColumn.Name, "type")
						meta.ColSource = removeColumnSource(meta.ColSource, currentColumn.Name)
					}
					continue
				}
				if source == "" || !isCandidate {
					return nil, badRequestError("invalid_schema_resolution", fmt.Sprintf("inferred source for column %q is not available", resolution.Column))
				}
				candidate.Type = strings.TrimSpace(resolution.Type)
				inferred = upsertColumn(inferred, candidate)
				meta.ColAdd = removeName(meta.ColAdd, candidate.Name)
				meta.ColDrop = removeName(meta.ColDrop, candidate.Name)
				if source == columnSourceDefinition {
					meta.ColOwn = disownField(meta.ColOwn, candidate.Name, "type")
					meta.ColSource = removeColumnSource(meta.ColSource, candidate.Name)
				} else {
					// An observed-source choice remains generated rather than
					// becoming a manual column/type. Update the saved value before
					// reconciliation and remember only the compact non-default source.
					asset.Columns = upsertColumnType(asset.Columns, candidate)
					meta.ColOwn = disownField(meta.ColOwn, candidate.Name, "type")
					meta.ColSource = setColumnSource(meta.ColSource, candidate.Name, columnSourceCode(source))
				}

			default:
				return nil, badRequestError("invalid_schema_resolution", fmt.Sprintf("unknown action for column %q", resolution.Column))
			}
		}
		return inferred, nil
	})
}

func upsertColumnType(columns []pipeline.Column, selected pipeline.Column) []pipeline.Column {
	for index := range columns {
		if columnNameKey(columns[index].Name) == columnNameKey(selected.Name) {
			columns[index].Type = selected.Type
			return columns
		}
	}
	return append(columns, selected)
}

func sameColumnSchema(current []pipeline.Column, expected []WorkspaceColumn) bool {
	if len(current) != len(expected) {
		return false
	}
	for index := range current {
		if columnNameKey(current[index].Name) != columnNameKey(expected[index].Name) ||
			!equivalentPipelineWorkspaceColumnType(current[index], expected[index]) {
			return false
		}
	}
	return true
}

func analyzeColumnSchema(
	current []pipeline.Column,
	meta assetmeta.RenartMeta,
	snapshots []webmodel.ColumnSchemaSourceSnapshot,
) columnSchemaAnalysis {
	analysis := columnSchemaAnalysis{
		rows:             []webmodel.ColumnSchemaMergeRow{},
		managedColumns:   []WorkspaceColumn{},
		candidateColumns: []WorkspaceColumn{},
		typeSources:      map[string]string{},
	}
	if len(snapshots) == 0 {
		return analysis
	}

	primaryIndex := 0
	for index, snapshot := range snapshots {
		if snapshot.Source.Category == "definition" {
			primaryIndex = index
			break
		}
	}

	sourceMaps := make([]map[string]WorkspaceColumn, len(snapshots))
	for index, snapshot := range snapshots {
		sourceMaps[index] = columnsByName(snapshot.Columns)
	}
	managed := append([]WorkspaceColumn(nil), snapshots[primaryIndex].Columns...)
	managedIndex := make(map[string]int, len(managed))
	managedTypeSources := make(map[string]string, len(managed))
	for index := range managed {
		key := columnNameKey(managed[index].Name)
		if key == "" {
			continue
		}
		managedIndex[key] = index
		if strings.TrimSpace(managed[index].Type) != "" {
			managedTypeSources[key] = snapshots[primaryIndex].Source.ID
		}
		if workspaceColumnType(managed[index]) == "" {
			if inferredColumn, sourceID, ok := firstKnownSourceColumn(key, snapshots, sourceMaps); ok {
				copyWorkspaceColumnType(&managed[index], inferredColumn)
				managedTypeSources[key] = sourceID
			}
		}
	}
	primaryOmitted := make(map[string]struct{})
	if snapshots[primaryIndex].Source.MayOmitColumns {
		// A sampled response is useful evidence for fields it contains, but
		// cannot prove that an existing optional field was removed. When it is
		// the fallback primary source, retain unobserved saved columns in the
		// managed projection. A complete advisory source may still refine their
		// types or surface real drift below.
		for _, currentColumn := range current {
			key := columnNameKey(currentColumn.Name)
			if key == "" {
				continue
			}
			if _, present := managedIndex[key]; present {
				continue
			}
			column, sourceID, hasSourceType := firstKnownSourceColumn(key, snapshots, sourceMaps)
			if !hasSourceType {
				column = PipelineColumnsToModelColumns([]pipeline.Column{currentColumn})[0]
				sourceID = columnSourceIDForCode(columnSourceForColumn(meta.ColSource, key))
			}
			column.Name = currentColumn.Name
			managedIndex[key] = len(managed)
			managed = append(managed, column)
			if sourceID != "" {
				managedTypeSources[key] = sourceID
			}
			primaryOmitted[key] = struct{}{}
		}
	}

	currentMap := make(map[string]pipeline.Column, len(current))
	for _, column := range current {
		if key := columnNameKey(column.Name); key != "" {
			currentMap[key] = column
		}
	}
	dropped := stringSet(meta.ColDrop)
	manual := stringSet(meta.ColAdd)

	rowOrder := make([]string, 0, len(managed)+len(current))
	rowNames := make(map[string]string)
	appendOrder := func(name string) {
		key := columnNameKey(name)
		if key == "" {
			return
		}
		if _, exists := rowNames[key]; exists {
			return
		}
		rowNames[key] = strings.TrimSpace(name)
		rowOrder = append(rowOrder, key)
	}
	for _, column := range snapshots[primaryIndex].Columns {
		appendOrder(column.Name)
	}
	for _, column := range current {
		appendOrder(column.Name)
	}
	for _, snapshot := range snapshots {
		for _, column := range snapshot.Columns {
			appendOrder(column.Name)
		}
	}

	for _, key := range rowOrder {
		currentColumn, currentPresent := currentMap[key]
		managedPosition, proposedPresent := managedIndex[key]
		var proposedColumn WorkspaceColumn
		if proposedPresent {
			proposedColumn = managed[managedPosition]
		}
		_, ignored := dropped[key]
		_, manuallyAdded := manual[key]
		ownedType := columnTypeOwned(meta, key)
		provenanceSourceID := columnSourceIDForCode(columnSourceForColumn(meta.ColSource, key))
		_, omittedByPartialPrimary := primaryOmitted[key]
		provenanceBacked := false

		// A known saved type with non-default provenance remains useful when the
		// SQL/definition source can only say "unknown". This is the central compact
		// provenance rule: absence from renart_col_src means SQL owns the type;
		// presence means an observed source supplied the last known value.
		if proposedPresent && currentPresent && strings.TrimSpace(proposedColumn.Type) == "" &&
			strings.TrimSpace(currentColumn.Type) != "" && provenanceSourceID != "" {
			copyPipelineColumnType(&proposedColumn, currentColumn)
			copyPipelineColumnType(&managed[managedPosition], currentColumn)
			managedTypeSources[key] = provenanceSourceID
			provenanceBacked = true
		}

		anySourcePresent := false
		for _, sourceMap := range sourceMaps {
			if _, ok := sourceMap[key]; ok {
				anySourcePresent = true
				break
			}
		}
		observedOnly := !proposedPresent && anySourcePresent
		if ignored && !currentPresent {
			continue
		}

		sourceTypeConflict := sourceTypesConflict(key, sourceMaps)
		if sourceTypeConflict && ownedType && currentPresent && currentMatchesAnySource(currentColumn, key, sourceMaps) {
			sourceTypeConflict = false
		}
		if sourceTypeConflict && proposedPresent && currentPresent && provenanceSourceID != "" {
			if sourceColumn, fresh := freshProvenanceSourceColumn(provenanceSourceID, key, snapshots, sourceMaps); fresh && equivalentPipelineWorkspaceColumnType(currentColumn, sourceColumn) {
				// A current materialization built from this source is stronger
				// evidence than a conflicting static inference. Keep the observed
				// type without a resolver round-trip; stale observations remain
				// advisory and still surface the conflict.
				sourceTypeConflict = false
				copyWorkspaceColumnType(&proposedColumn, sourceColumn)
				copyWorkspaceColumnType(&managed[managedPosition], sourceColumn)
				managedTypeSources[key] = provenanceSourceID
				provenanceBacked = true
			}
		}
		sourceMissing := false
		if proposedPresent {
			for index, snapshot := range snapshots {
				if index == primaryIndex || snapshot.Source.MayOmitColumns {
					continue
				}
				if _, ok := sourceMaps[index][key]; !ok {
					sourceMissing = true
					break
				}
			}
		}

		row := webmodel.ColumnSchemaMergeRow{
			Column:          rowNames[key],
			CurrentPresent:  currentPresent,
			CurrentType:     currentColumn.SQLType(),
			ProposedPresent: proposedPresent,
			ProposedType:    workspaceColumnType(proposedColumn),
		}

		switch {
		case observedOnly && manuallyAdded && currentPresent:
			row.Kind = "manual"
			row.Detail = "Kept as an explicit metadata column."
			row.ProposedPresent = true
			row.ProposedType = currentColumn.SQLType()
		case observedOnly:
			row.Kind = "observed_only"
			row.Detail = "An advisory source reports a column that the primary inference does not declare."
			row.Conflict = true
		case sourceMissing:
			row.Kind = "source_missing"
			row.Detail = "An advisory source does not report this schema column."
			row.Conflict = true
		case sourceTypeConflict:
			row.Kind = "source_conflict"
			row.Detail = "The selected schema sources report different known types."
			row.Conflict = true
		case proposedPresent && !currentPresent:
			row.Kind = "added"
			row.Detail = "New inferred column; safe to add automatically."
			analysis.hasChanges = true
		case proposedPresent && currentPresent && ownedType:
			row.Kind = "owned"
			row.Detail = "The saved type is explicitly owned and remains unchanged."
			row.ProposedType = currentColumn.SQLType()
		case proposedPresent && currentPresent && provenanceBacked && equivalentPipelineWorkspaceColumnType(currentColumn, proposedColumn):
			row.Kind = "provenance"
			if provenanceSourceID == columnSourceMaterialized {
				row.Detail = "The saved type comes from the current table; SQL does not provide stronger conflicting evidence."
			} else {
				row.Detail = "The saved type comes from a previously selected schema source; the definition does not provide a known type."
			}
		case proposedPresent && currentPresent && omittedByPartialPrimary && equivalentPipelineWorkspaceColumnType(currentColumn, proposedColumn):
			row.Kind = "partial_unobserved"
			row.Detail = "The sampled source did not include this saved column, so it was retained."
		case proposedPresent && currentPresent && equivalentPipelineWorkspaceColumnType(currentColumn, proposedColumn):
			row.Kind = "unchanged"
			row.Detail = "The inferred and saved types match."
			// Avoid cosmetic rewrites between equivalent aliases such as int32
			// and integer.
			managed[managedPosition].Type = currentColumn.Type
			row.ProposedType = currentColumn.SQLType()
		case proposedPresent && currentPresent && strings.TrimSpace(currentColumn.Type) == "" && strings.TrimSpace(proposedColumn.Type) != "":
			row.Kind = "type_filled"
			row.Detail = "The previously unknown type can be filled automatically."
			analysis.hasChanges = true
		case proposedPresent && currentPresent:
			row.Kind = "type_conflict"
			row.Detail = "Changing a known type requires an explicit choice."
			row.Conflict = true
		case currentPresent && manuallyAdded:
			row.Kind = "manual"
			row.Detail = "Kept as an explicit metadata column."
			row.ProposedPresent = true
			row.ProposedType = currentColumn.SQLType()
		case currentPresent:
			row.Kind = "removed"
			row.Detail = "The primary inference no longer reports this saved column."
			row.Conflict = true
		default:
			row.Kind = "unchanged"
			row.Detail = "No schema change."
		}

		if row.Conflict {
			analysis.hasConflicts = true
		}
		if proposedPresent && !manuallyAdded {
			desiredSource := ""
			if !ownedType {
				desiredSource = columnSourceCode(managedTypeSources[key])
			}
			analysis.typeSources[row.Column] = desiredSource
			if !strings.EqualFold(columnSourceForColumn(meta.ColSource, key), desiredSource) {
				analysis.provenanceChange = true
			}
		}
		analysis.rows = append(analysis.rows, row)
	}

	// Definition/fallback columns are the provenance-managed schema. Advisory-
	// only columns are added only after an explicit resolution. Previously
	// ignored advisory columns stay managed while selected so their drop marker
	// is not pruned by a safe reconciliation.
	analysis.managedColumns = append(analysis.managedColumns, managed...)
	for _, snapshot := range snapshots {
		for _, column := range snapshot.Columns {
			key := columnNameKey(column.Name)
			if _, exists := managedIndex[key]; exists {
				continue
			}
			if _, isDropped := dropped[key]; isDropped {
				analysis.managedColumns = append(analysis.managedColumns, column)
				managedIndex[key] = len(analysis.managedColumns) - 1
			}
		}
	}

	candidateSeen := make(map[string]struct{})
	for _, column := range analysis.managedColumns {
		key := columnNameKey(column.Name)
		if key == "" {
			continue
		}
		candidateSeen[key] = struct{}{}
		analysis.candidateColumns = append(analysis.candidateColumns, column)
	}
	for _, snapshot := range snapshots {
		for _, column := range snapshot.Columns {
			key := columnNameKey(column.Name)
			if key == "" {
				continue
			}
			if _, exists := candidateSeen[key]; exists {
				continue
			}
			candidateSeen[key] = struct{}{}
			analysis.candidateColumns = append(analysis.candidateColumns, column)
		}
	}
	return analysis
}

func columnsByName(columns []WorkspaceColumn) map[string]WorkspaceColumn {
	result := make(map[string]WorkspaceColumn, len(columns))
	for _, column := range columns {
		if key := columnNameKey(column.Name); key != "" {
			result[key] = column
		}
	}
	return result
}

func firstKnownSourceColumn(
	key string,
	snapshots []webmodel.ColumnSchemaSourceSnapshot,
	sourceMaps []map[string]WorkspaceColumn,
) (WorkspaceColumn, string, bool) {
	for index, sourceMap := range sourceMaps {
		if column, ok := sourceMap[key]; ok && workspaceColumnType(column) != "" {
			return column, snapshots[index].Source.ID, true
		}
	}
	return WorkspaceColumn{}, "", false
}

func freshProvenanceSourceColumn(
	sourceID, key string,
	snapshots []webmodel.ColumnSchemaSourceSnapshot,
	sourceMaps []map[string]WorkspaceColumn,
) (WorkspaceColumn, bool) {
	for index, snapshot := range snapshots {
		if snapshot.Source.ID != sourceID || snapshot.Fresh == nil || !*snapshot.Fresh {
			continue
		}
		if column, ok := sourceMaps[index][key]; ok && workspaceColumnType(column) != "" {
			return column, true
		}
	}
	return WorkspaceColumn{}, false
}

func sourceTypesConflict(key string, sourceMaps []map[string]WorkspaceColumn) bool {
	known := make([]WorkspaceColumn, 0, len(sourceMaps))
	for _, sourceMap := range sourceMaps {
		column, ok := sourceMap[key]
		if !ok || workspaceColumnType(column) == "" {
			continue
		}
		for _, existing := range known {
			if !equivalentWorkspaceColumnType(existing, column) {
				return true
			}
		}
		known = append(known, column)
	}
	return false
}

func currentMatchesAnySource(current pipeline.Column, key string, sourceMaps []map[string]WorkspaceColumn) bool {
	if strings.TrimSpace(current.SQLType()) == "" {
		return false
	}
	for _, sourceMap := range sourceMaps {
		if column, ok := sourceMap[key]; ok && workspaceColumnType(column) != "" && equivalentPipelineWorkspaceColumnType(current, column) {
			return true
		}
	}
	return false
}

func copyWorkspaceColumnType(target *WorkspaceColumn, source WorkspaceColumn) {
	target.Type = source.Type
	target.Precision = cloneIntPointer(source.Precision)
	target.Scale = cloneIntPointer(source.Scale)
	target.Length = cloneIntPointer(source.Length)
	target.Collation = source.Collation
}

func copyPipelineColumnType(target *WorkspaceColumn, source pipeline.Column) {
	target.Type = source.Type
	target.Precision = cloneIntPointer(source.Precision)
	target.Scale = cloneIntPointer(source.Scale)
	target.Length = cloneIntPointer(source.Length)
	target.Collation = source.Collation
}

func columnTypeOwned(meta assetmeta.RenartMeta, key string) bool {
	for column, fields := range meta.ColOwn {
		if columnNameKey(column) != key {
			continue
		}
		for _, field := range fields {
			if strings.EqualFold(strings.TrimSpace(field), "type") {
				return true
			}
		}
	}
	return false
}

func columnSourceForColumn(sources map[string]string, key string) string {
	for column, source := range sources {
		if columnNameKey(column) == key {
			return strings.TrimSpace(source)
		}
	}
	return ""
}

func columnSourceCode(sourceID string) string {
	switch strings.TrimSpace(sourceID) {
	case "", columnSourceDefinition:
		return ""
	case columnSourceMaterialized:
		return columnSourceCodeMaterialized
	case columnSourceLiveResponse:
		return columnSourceCodeLiveResponse
	default:
		// Future observed sources remain forward-compatible. Known sources use
		// one-byte codes; an unknown exception pays only for its own full ID.
		return strings.TrimSpace(sourceID)
	}
}

func columnSourceIDForCode(code string) string {
	switch strings.TrimSpace(code) {
	case columnSourceCodeMaterialized:
		return columnSourceMaterialized
	case columnSourceCodeLiveResponse:
		return columnSourceLiveResponse
	default:
		return strings.TrimSpace(code)
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if key := columnNameKey(value); key != "" {
			result[key] = struct{}{}
		}
	}
	return result
}

func columnNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
