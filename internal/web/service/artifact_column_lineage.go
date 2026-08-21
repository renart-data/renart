package service

import (
	"sort"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"

	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

type artifactSQLDefinition struct {
	ref     model.ArtifactRef
	sql     string
	dialect golyglot.Dialect
}

type artifactLineageSource struct {
	ref           model.ArtifactRef
	relationNames []string
	columns       []model.Column
}

// enrichArtifactColumnDependencies adds only positively resolved column
// mappings to the already resolved artifact dependency graph. Relation-only
// edges stay relation-only when SQL cannot be parsed, a source is ambiguous,
// or a producer schema is unknown.
func enrichArtifactColumnDependencies(index *model.ArtifactIndex, state model.WorkspaceState) {
	if index == nil {
		return
	}

	sources := artifactLineageSources(*index)
	definitions := artifactSQLDefinitions(state)

	// An asset-backed presentation dataset is an identity projection of the
	// asset schema. Recording that mapping lets an asset column flow through the
	// dataset into filter and visualization roles.
	for _, dependency := range append([]model.ArtifactDependency(nil), index.Dependencies...) {
		if dependency.Producer.Kind != artifactKindPipelineAsset ||
			!strings.HasPrefix(dependency.Consumer.ComponentID, componentKindDataset+":") {
			continue
		}
		producer, producerOK := sources[artifactRefKey(dependency.Producer)]
		consumer, consumerOK := sources[artifactRefKey(dependency.Consumer)]
		if !producerOK || !consumerOK {
			continue
		}
		columns := identityArtifactColumnUsages(producer.columns, consumer.columns)
		if len(columns) == 0 {
			continue
		}
		index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
			Producer: dependency.Producer,
			Consumer: dependency.Consumer,
			Columns:  columns,
		})
	}

	incoming := make(map[string][]model.ArtifactDependency)
	for _, dependency := range index.Dependencies {
		key := artifactRefKey(dependency.Consumer)
		incoming[key] = append(incoming[key], dependency)
	}

	for _, definition := range definitions {
		consumerKey := artifactRefKey(definition.ref)
		consumer, ok := sources[consumerKey]
		if !ok || len(consumer.columns) == 0 {
			continue
		}
		lineageSources := make([]artifactLineageSource, 0, len(incoming[consumerKey]))
		for _, dependency := range incoming[consumerKey] {
			producer, exists := sources[artifactRefKey(dependency.Producer)]
			if !exists || len(producer.columns) == 0 || len(producer.relationNames) == 0 {
				continue
			}
			lineageSources = append(lineageSources, producer)
		}
		if len(lineageSources) == 0 {
			continue
		}

		for producerKey, columns := range sqlArtifactColumnUsages(definition, consumer.columns, lineageSources) {
			producer := sources[producerKey]
			index.Dependencies = appendArtifactDependency(index.Dependencies, model.ArtifactDependency{
				Producer: producer.ref,
				Consumer: definition.ref,
				Columns:  columns,
			})
		}
	}
}

func artifactLineageSources(index model.ArtifactIndex) map[string]artifactLineageSource {
	result := make(map[string]artifactLineageSource)
	for _, artifact := range index.Artifacts {
		parent := model.ArtifactRef{Kind: artifact.Kind, ArtifactID: artifact.ID}
		result[artifactRefKey(parent)] = artifactLineageSource{
			ref: parent, relationNames: compactArtifactRelationNames(artifact.Title),
			columns: cloneArtifactColumns(artifact.Columns),
		}
		for _, component := range artifact.Components {
			child := model.ArtifactRef{Kind: artifact.Kind, ArtifactID: artifact.ID, ComponentID: component.ID}
			result[artifactRefKey(child)] = artifactLineageSource{
				ref: child, relationNames: compactArtifactRelationNames(component.Name),
				columns: cloneArtifactColumns(component.Columns),
			}
		}
	}
	return result
}

func compactArtifactRelationNames(values ...string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := normalizeArtifactRelationName(value)
		if value == "" || key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func artifactSQLDefinitions(state model.WorkspaceState) []artifactSQLDefinition {
	result := make([]artifactSQLDefinition, 0)
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(asset.Type)), ".sql") {
				continue
			}
			sql := strings.TrimSpace(ExtractExecutableContent(asset.Content))
			if sql == "" {
				continue
			}
			result = append(result, artifactSQLDefinition{
				ref: model.ArtifactRef{
					Kind: artifactKindPipelineAsset, ArtifactID: pipelineAssetArtifactID(pipeline, asset),
				},
				sql: sql, dialect: golyglot.Dialect(sqllsp.DialectFromAssetType(asset.Type)),
			})
		}
	}
	for _, notebook := range state.Notebooks {
		artifactID := strings.TrimSpace(notebook.UUID)
		if artifactID == "" {
			artifactID = notebook.ID
		}
		for _, cell := range notebook.Cells {
			if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(cell.Type)), ".sql") {
				continue
			}
			sql := strings.TrimSpace(ExtractExecutableContent(cell.Content))
			if sql == "" {
				continue
			}
			result = append(result, artifactSQLDefinition{
				ref: model.ArtifactRef{Kind: artifactKindNotebook, ArtifactID: artifactID, ComponentID: cell.CellID},
				sql: sql, dialect: golyglot.Dialect(sqllsp.DialectFromAssetType(cell.Type)),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return artifactRefKey(result[i].ref) < artifactRefKey(result[j].ref)
	})
	return result
}

func identityArtifactColumnUsages(producer, consumer []model.Column) []model.ArtifactColumnUsage {
	consumerNames := make(map[string]string, len(consumer))
	for _, column := range consumer {
		if name := strings.TrimSpace(column.Name); name != "" {
			consumerNames[strings.ToLower(name)] = name
		}
	}
	result := make([]model.ArtifactColumnUsage, 0, len(producer))
	for _, column := range producer {
		name := strings.TrimSpace(column.Name)
		consumerName, ok := consumerNames[strings.ToLower(name)]
		if name == "" || !ok {
			continue
		}
		result = append(result, model.ArtifactColumnUsage{Name: name, ConsumerColumn: consumerName})
	}
	return mergeArtifactColumnUsages(nil, result)
}

func sqlArtifactColumnUsages(
	definition artifactSQLDefinition,
	consumerColumns []model.Column,
	sources []artifactLineageSource,
) map[string][]model.ArtifactColumnUsage {
	strict := true
	schema := golyglot.ValidationSchema{Strict: &strict, Tables: make([]golyglot.SchemaTable, 0, len(sources))}
	for _, source := range sources {
		if len(source.relationNames) == 0 {
			continue
		}
		table := golyglot.SchemaTable{Name: source.relationNames[0]}
		if len(source.relationNames) > 1 {
			table.Aliases = append([]string(nil), source.relationNames[1:]...)
		}
		for _, column := range source.columns {
			if name := strings.TrimSpace(column.Name); name != "" {
				table.Columns = append(table.Columns, golyglot.SchemaColumn{Name: name, Type: column.Type})
			}
		}
		if len(table.Columns) > 0 {
			schema.Tables = append(schema.Tables, table)
		}
	}
	if len(schema.Tables) == 0 {
		return nil
	}

	output, err := golyglot.OutputColumnsWithSchema(definition.sql, schema, definition.dialect)
	if err != nil {
		return nil
	}
	knownOutputs := make(map[string]bool, len(output.Columns))
	for _, column := range output.Columns {
		if column.Name != nil && strings.TrimSpace(*column.Name) != "" {
			knownOutputs[strings.ToLower(strings.TrimSpace(*column.Name))] = true
		}
	}
	result := make(map[string][]model.ArtifactColumnUsage)
	appendSingleSourceStarUsages(result, definition, schema, consumerColumns, sources, knownOutputs)
	for _, outputColumn := range consumerColumns {
		consumerName := strings.TrimSpace(outputColumn.Name)
		if consumerName == "" || !knownOutputs[strings.ToLower(consumerName)] {
			continue
		}
		lineage, lineageErr := golyglot.LineageWithSchema(consumerName, definition.sql, schema, definition.dialect)
		resolved := appendResolvedLineageUsages(result, lineage, consumerName, sources)
		// The schema-aware relation facts deliberately include nested CTE
		// relations. If that makes an otherwise unambiguous outer reference
		// appear ambiguous, the schema-free resolver still follows the CTE and
		// we validate its physical leaf against the known producer schema below.
		if lineageErr != nil || !resolved {
			fallback, fallbackErr := golyglot.Lineage(consumerName, definition.sql, definition.dialect)
			if fallbackErr == nil {
				appendResolvedLineageUsages(result, fallback, consumerName, sources)
			}
		}
	}
	appendDirectPredicateUsages(result, definition, sources)
	for key, columns := range result {
		result[key] = mergeArtifactColumnUsages(nil, columns)
	}
	return result
}

type artifactDirectTableBinding struct {
	name   string
	alias  string
	source artifactLineageSource
}

func appendDirectPredicateUsages(
	result map[string][]model.ArtifactColumnUsage,
	definition artifactSQLDefinition,
	sources []artifactLineageSource,
) {
	parsed, err := golyglot.ParseStrict(definition.sql, definition.dialect)
	if err != nil || len(parsed.Statements) != 1 {
		return
	}
	query, ok := parsed.Statements[0].Node.(*golyglot.SelectStmt)
	if !ok || len(query.With) != 0 || query.SetLeft != nil || query.SetRight != nil {
		return
	}
	bindings, ok := directArtifactTableBindings(query, sources)
	if !ok || len(bindings) == 0 {
		return
	}
	expressions := make([]golyglot.Expr, 0, 1+len(query.From))
	if query.Where != nil {
		expressions = append(expressions, query.Where)
	}
	for _, table := range query.From {
		for _, join := range table.Joins {
			if join.Condition != nil {
				expressions = append(expressions, join.Condition)
			}
		}
	}
	for _, expression := range expressions {
		if artifactExpressionContainsSubquery(expression) {
			continue
		}
		for _, reference := range golyglot.Columns(expression) {
			source, resolved := resolveDirectArtifactColumnReference(reference, bindings)
			if !resolved {
				continue
			}
			producerColumn, exists := artifactColumnName(source.columns, reference.Column)
			if !exists || artifactColumnAlreadyMapped(result[artifactRefKey(source.ref)], producerColumn) {
				continue
			}
			key := artifactRefKey(source.ref)
			result[key] = append(result[key], model.ArtifactColumnUsage{
				Name: producerColumn, Role: artifactColumnRoleQueryReference,
			})
		}
	}
}

func directArtifactTableBindings(
	query *golyglot.SelectStmt,
	sources []artifactLineageSource,
) ([]artifactDirectTableBinding, bool) {
	bindings := make([]artifactDirectTableBinding, 0)
	appendTable := func(item golyglot.FromItem) bool {
		table, ok := item.(*golyglot.TableName)
		if !ok || len(table.Parts) == 0 {
			return false
		}
		parts := make([]string, 0, len(table.Parts))
		for _, part := range table.Parts {
			parts = append(parts, part.Text)
		}
		name := strings.Join(parts, ".")
		source, resolved := resolveArtifactLineageSource(name, sources)
		if !resolved {
			return false
		}
		alias := ""
		if table.Alias != nil {
			alias = table.Alias.Text
		}
		bindings = append(bindings, artifactDirectTableBinding{name: name, alias: alias, source: source})
		return true
	}
	for _, table := range query.From {
		if !appendTable(table.Primary) {
			return nil, false
		}
		for _, join := range table.Joins {
			if !appendTable(join.Right) {
				return nil, false
			}
		}
	}
	return bindings, true
}

func artifactExpressionContainsSubquery(expression golyglot.Expr) bool {
	contains := false
	golyglot.Walk(expression, func(node golyglot.Node) golyglot.VisitAction {
		if _, ok := node.(*golyglot.SelectStmt); ok {
			contains = true
			return golyglot.Stop
		}
		return golyglot.VisitChildren
	})
	return contains
}

func resolveDirectArtifactColumnReference(
	reference golyglot.ColumnReference,
	bindings []artifactDirectTableBinding,
) (artifactLineageSource, bool) {
	matches := make(map[string]artifactLineageSource)
	for _, binding := range bindings {
		if reference.Table != "" {
			qualifier := normalizeArtifactRelationName(reference.Table)
			name := normalizeArtifactRelationName(binding.name)
			alias := normalizeArtifactRelationName(binding.alias)
			if qualifier != name && qualifier != normalizeArtifactRelationName(lastArtifactIdentifier(binding.name)) &&
				(alias == "" || qualifier != alias) {
				continue
			}
		}
		if _, exists := artifactColumnName(binding.source.columns, reference.Column); !exists {
			continue
		}
		matches[artifactRefKey(binding.source.ref)] = binding.source
	}
	if len(matches) != 1 {
		return artifactLineageSource{}, false
	}
	for _, match := range matches {
		return match, true
	}
	return artifactLineageSource{}, false
}

func artifactColumnAlreadyMapped(usages []model.ArtifactColumnUsage, column string) bool {
	for _, usage := range usages {
		if strings.EqualFold(strings.TrimSpace(usage.Name), strings.TrimSpace(column)) {
			return true
		}
	}
	return false
}

func appendSingleSourceStarUsages(
	result map[string][]model.ArtifactColumnUsage,
	definition artifactSQLDefinition,
	schema golyglot.ValidationSchema,
	consumerColumns []model.Column,
	sources []artifactLineageSource,
	knownOutputs map[string]bool,
) {
	analysis, err := golyglot.AnalyzeQuery(definition.sql, golyglot.AnalyzeQueryOptions{
		Dialect: definition.dialect,
		Schema:  &schema,
	})
	if err != nil || analysis.Shape != "select" || len(analysis.StarProjections) == 0 ||
		len(analysis.BaseTables) != 1 {
		return
	}
	source, resolved := resolveArtifactLineageSource(analysis.BaseTables[0].Name, sources)
	if !resolved {
		return
	}
	for _, usage := range identityArtifactColumnUsages(source.columns, consumerColumns) {
		if !knownOutputs[strings.ToLower(usage.ConsumerColumn)] {
			continue
		}
		key := artifactRefKey(source.ref)
		result[key] = append(result[key], usage)
	}
}

func appendResolvedLineageUsages(
	result map[string][]model.ArtifactColumnUsage,
	lineage golyglot.LineageNode,
	consumerName string,
	sources []artifactLineageSource,
) bool {
	resolvedAny := false
	for _, node := range lineage.Walk()[1:] {
		if len(node.Downstream) != 0 || node.SourceKind != "table" || strings.TrimSpace(node.SourceName) == "" {
			continue
		}
		source, resolved := resolveArtifactLineageSource(node.SourceName, sources)
		if !resolved {
			continue
		}
		producerColumn, exists := artifactColumnName(source.columns, lastArtifactIdentifier(node.Name))
		if !exists {
			continue
		}
		key := artifactRefKey(source.ref)
		result[key] = append(result[key], model.ArtifactColumnUsage{
			Name: producerColumn, ConsumerColumn: consumerName,
		})
		resolvedAny = true
	}
	return resolvedAny
}

func resolveArtifactLineageSource(name string, sources []artifactLineageSource) (artifactLineageSource, bool) {
	normalized := normalizeArtifactRelationName(name)
	if normalized == "" {
		return artifactLineageSource{}, false
	}
	for _, suffixOnly := range []bool{false, true} {
		matches := make(map[string]artifactLineageSource)
		for _, source := range sources {
			for _, relationName := range source.relationNames {
				candidate := normalizeArtifactRelationName(relationName)
				matchesName := candidate == normalized
				if suffixOnly {
					matchesName = strings.HasSuffix(candidate, "."+normalized) || strings.HasSuffix(normalized, "."+candidate)
				}
				if matchesName {
					matches[artifactRefKey(source.ref)] = source
					break
				}
			}
		}
		if len(matches) == 1 {
			for _, match := range matches {
				return match, true
			}
		}
		if len(matches) > 1 {
			return artifactLineageSource{}, false
		}
	}
	return artifactLineageSource{}, false
}

func normalizeArtifactRelationName(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	for index, part := range parts {
		parts[index] = strings.Trim(strings.TrimSpace(part), "`\"[]")
	}
	return strings.ToLower(strings.Join(parts, "."))
}

func lastArtifactIdentifier(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) == 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(parts[len(parts)-1]), "`\"[]")
}

func artifactColumnName(columns []model.Column, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	for _, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column.Name), requested) {
			return strings.TrimSpace(column.Name), true
		}
	}
	return "", false
}

type artifactColumnTransition struct {
	consumer       model.ArtifactRef
	consumerColumn string
	role           string
}

type artifactColumnTraversal struct {
	ref      model.ArtifactRef
	column   string
	distance int
}

func deriveBreakingColumnImpacts(dependencies []model.ArtifactDependency) []model.ArtifactColumnImpact {
	adjacency := make(map[string][]artifactColumnTransition)
	sourceNames := make(map[string]artifactColumnTraversal)
	for _, dependency := range dependencies {
		for _, usage := range dependency.Columns {
			name := strings.TrimSpace(usage.Name)
			consumerColumn := strings.TrimSpace(usage.ConsumerColumn)
			role := strings.TrimSpace(usage.Role)
			if name == "" || (consumerColumn == "" && role == "") {
				continue
			}
			key := artifactColumnTraversalKey(dependency.Producer, name)
			sourceNames[key] = artifactColumnTraversal{ref: dependency.Producer, column: name}
			adjacency[key] = appendUniqueArtifactColumnTransition(adjacency[key], artifactColumnTransition{
				consumer: dependency.Consumer, consumerColumn: consumerColumn, role: role,
			})
		}
	}

	keys := make([]string, 0, len(sourceNames))
	for key := range sourceNames {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]model.ArtifactColumnImpact, 0)
	for _, sourceKey := range keys {
		source := sourceNames[sourceKey]
		queue := []artifactColumnTraversal{source}
		visited := map[string]bool{sourceKey: true}
		impacts := make(map[string]model.ArtifactColumnImpact)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, transition := range adjacency[artifactColumnTraversalKey(current.ref, current.column)] {
				distance := current.distance + 1
				impact := model.ArtifactColumnImpact{
					Producer: source.ref, Column: source.column,
					Consumer: transition.consumer, ConsumerColumn: transition.consumerColumn,
					Role: transition.role, Distance: distance,
				}
				impactKey := artifactColumnImpactKey(impact)
				if existing, ok := impacts[impactKey]; !ok || distance < existing.Distance {
					impacts[impactKey] = impact
				}
				if transition.consumerColumn == "" {
					continue
				}
				nextKey := artifactColumnTraversalKey(transition.consumer, transition.consumerColumn)
				if visited[nextKey] {
					continue
				}
				visited[nextKey] = true
				queue = append(queue, artifactColumnTraversal{
					ref: transition.consumer, column: transition.consumerColumn, distance: distance,
				})
			}
		}
		impactKeys := make([]string, 0, len(impacts))
		for key := range impacts {
			impactKeys = append(impactKeys, key)
		}
		sort.Strings(impactKeys)
		for _, key := range impactKeys {
			result = append(result, impacts[key])
		}
	}
	return result
}

func appendUniqueArtifactColumnTransition(
	existing []artifactColumnTransition,
	candidate artifactColumnTransition,
) []artifactColumnTransition {
	for _, transition := range existing {
		if artifactRefKey(transition.consumer) == artifactRefKey(candidate.consumer) &&
			strings.EqualFold(transition.consumerColumn, candidate.consumerColumn) &&
			transition.role == candidate.role {
			return existing
		}
	}
	return append(existing, candidate)
}

func artifactColumnTraversalKey(ref model.ArtifactRef, column string) string {
	return artifactRefKey(ref) + "\x00" + strings.ToLower(strings.TrimSpace(column))
}

func artifactColumnImpactKey(impact model.ArtifactColumnImpact) string {
	return artifactRefKey(impact.Consumer) + "\x00" + strings.ToLower(impact.ConsumerColumn) + "\x00" + impact.Role
}
