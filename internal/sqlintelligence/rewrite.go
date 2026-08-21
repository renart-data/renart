package sqlintelligence

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

// IsReadOnlySingleQuery reports whether SQL is exactly one read-only SELECT.
// It deliberately fails closed: syntax that Golyglot cannot parse as a typed
// SELECT is not safe enough for Renart's inspect and Python-query paths.
func IsReadOnlySingleQuery(sql, dialectName string) (bool, error) {
	dialect, err := rewriteDialect(dialectName)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(sql) == "" {
		return false, fmt.Errorf("cannot parse an empty query")
	}
	parsed, err := golyglot.ParseStrict(sql, dialect)
	if err != nil {
		return false, fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	if len(parsed.Statements) != 1 {
		return false, nil
	}
	query, ok := parsed.Statements[0].Node.(*golyglot.SelectStmt)
	if !ok {
		return false, nil
	}
	// SELECT INTO creates a relation on engines that support it.
	if len(query.Into) > 0 {
		return false, nil
	}
	return true, nil
}

// RenameTables applies relation renames as source-span edits. Unchanged SQL,
// including comments, whitespace, quoting, and keyword case, is retained byte
// for byte. When the destination leaf changes, the source leaf is kept as an
// alias so existing leaf-qualified columns continue to resolve.
func RenameTables(sql, dialectName string, mapping map[string]string) (string, error) {
	if len(mapping) == 0 {
		return sql, nil
	}
	dialect, err := rewriteDialect(dialectName)
	if err != nil {
		return "", err
	}
	parsed, err := golyglot.ParseStrict(sql, dialect)
	if err != nil {
		return "", fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	mappings, err := prepareTableMappings(mapping, dialect)
	if err != nil {
		return "", err
	}

	edits := make([]golyglot.TextEdit, 0)
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		switch value := node.(type) {
		case *golyglot.TableName:
			entry := matchingTableMapping(value.Parts, mappings)
			if entry == nil || len(value.Parts) == 0 {
				return golyglot.VisitChildren
			}
			span := golyglot.Span{Start: value.Parts[0].Span.Start, End: value.Parts[len(value.Parts)-1].Span.End}
			edits = append(edits, golyglot.TextEdit{Span: span, NewText: entry.destination})
			if value.Alias == nil && !identifierEqual(value.Parts[len(value.Parts)-1], entry.destinationParts[len(entry.destinationParts)-1]) {
				leaf, ok := parsed.SourceSlice(value.Parts[len(value.Parts)-1].Span)
				if ok {
					edits = append(edits, golyglot.TextEdit{Span: golyglot.Span{Start: span.End, End: span.End}, NewText: " AS " + leaf})
				}
			}
		case *golyglot.IdentifierExpr:
			// schema.table.column (and catalog.schema.table.column) must
			// continue resolving through the source leaf alias after a rename.
			if len(value.Parts) < 3 {
				return golyglot.VisitChildren
			}
			qualifier := value.Parts[:len(value.Parts)-1]
			entry := matchingColumnQualifier(qualifier, mappings)
			if entry == nil {
				return golyglot.VisitChildren
			}
			leaf := qualifier[len(qualifier)-1]
			leafText, ok := parsed.SourceSlice(leaf.Span)
			if !ok {
				return golyglot.VisitChildren
			}
			edits = append(edits, golyglot.TextEdit{
				Span:    golyglot.Span{Start: qualifier[0].Span.Start, End: leaf.Span.End},
				NewText: leafText,
			})
		}
		return golyglot.VisitChildren
	})

	return parsed.ApplyEdits(edits...)
}

// QualifyColumn qualifies every unqualified reference to column with one
// validated relation alias. It edits identifier spans only; aliases, comments,
// literals, and formatting are left untouched.
func QualifyColumn(sql, dialectName, column, qualifier string) (string, error) {
	dialect, err := rewriteDialect(dialectName)
	if err != nil {
		return "", err
	}
	qualifier, err = singleIdentifier(qualifier, dialect)
	if err != nil {
		return "", fmt.Errorf("invalid column qualifier: %w", err)
	}
	column = strings.Trim(strings.TrimSpace(column), "`\"")
	column = strings.TrimSuffix(strings.TrimPrefix(column, "["), "]")
	if column == "" {
		return "", fmt.Errorf("column is empty")
	}
	parsed, err := golyglot.ParseStrict(sql, dialect)
	if err != nil {
		return "", fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	edits := make([]golyglot.TextEdit, 0)
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		identifier, ok := node.(*golyglot.IdentifierExpr)
		if !ok || len(identifier.Parts) != 1 || !identifierMatches(identifier.Parts[0], column) {
			return golyglot.VisitChildren
		}
		source, ok := parsed.SourceSlice(identifier.SourceSpan())
		if ok {
			edits = append(edits, golyglot.TextEdit{Span: identifier.SourceSpan(), NewText: qualifier + "." + source})
		}
		return golyglot.VisitChildren
	})
	if len(edits) == 0 {
		return "", fmt.Errorf("unqualified column %q was not found", column)
	}
	return parsed.ApplyEdits(edits...)
}

// AliasRelation adds or replaces the alias of one relation occurrence and
// rewrites that occurrence's qualified column references to the new alias. It
// fails when the relation occurs more than once because choosing an occurrence
// would otherwise require a source offset rather than a semantic name.
func AliasRelation(sql, dialectName, relation, alias string) (string, error) {
	dialect, err := rewriteDialect(dialectName)
	if err != nil {
		return "", err
	}
	alias, err = singleIdentifier(alias, dialect)
	if err != nil {
		return "", fmt.Errorf("invalid relation alias: %w", err)
	}
	sourceParts := splitRelationName(relation)
	if len(sourceParts) == 0 {
		return "", fmt.Errorf("relation is empty")
	}
	parsed, err := golyglot.ParseStrict(sql, dialect)
	if err != nil {
		return "", fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	var matches []*golyglot.TableName
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		if table, ok := node.(*golyglot.TableName); ok && identifiersMatchStrings(table.Parts, sourceParts) {
			matches = append(matches, table)
		}
		return golyglot.VisitChildren
	})
	if len(matches) == 0 {
		return "", fmt.Errorf("relation %q was not found", relation)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("relation %q occurs %d times; a semantic alias edit requires one occurrence", relation, len(matches))
	}
	table := matches[0]
	partsSpan := golyglot.Span{Start: table.Parts[0].Span.Start, End: table.Parts[len(table.Parts)-1].Span.End}
	edits := make([]golyglot.TextEdit, 0, 2)
	oldQualifiers := [][]string{sourceParts, {sourceParts[len(sourceParts)-1]}}
	if table.Alias != nil {
		oldQualifiers = append(oldQualifiers, []string{table.Alias.Text})
		edits = append(edits, golyglot.TextEdit{Span: table.Alias.Span, NewText: alias})
	} else {
		edits = append(edits, golyglot.TextEdit{
			Span: golyglot.Span{Start: partsSpan.End, End: partsSpan.End}, NewText: " AS " + alias,
		})
	}
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		identifier, ok := node.(*golyglot.IdentifierExpr)
		if !ok || len(identifier.Parts) < 2 {
			return golyglot.VisitChildren
		}
		qualifierParts := identifier.Parts[:len(identifier.Parts)-1]
		for _, oldQualifier := range oldQualifiers {
			if !identifiersMatchStrings(qualifierParts, oldQualifier) {
				continue
			}
			edits = append(edits, golyglot.TextEdit{
				Span:    golyglot.Span{Start: qualifierParts[0].Span.Start, End: qualifierParts[len(qualifierParts)-1].Span.End},
				NewText: alias,
			})
			break
		}
		return golyglot.VisitChildren
	})
	return parsed.ApplyEdits(edits...)
}

// AddLimit adds or replaces the row limit on one SELECT while retaining all
// unrelated source text. The syntax emitted for a missing limit follows the
// target dialect (TOP for T-SQL/Fabric, FETCH FIRST for Oracle, LIMIT elsewhere).
func AddLimit(sql string, limit int, dialectName string) (string, error) {
	if limit < 0 {
		return "", fmt.Errorf("limit must not be negative")
	}
	dialect, err := rewriteDialect(dialectName)
	if err != nil {
		return "", err
	}
	parsed, err := golyglot.ParseStrict(sql, dialect)
	if err != nil {
		return "", fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	if len(parsed.Statements) != 1 {
		return "", fmt.Errorf("expected one SQL statement, got %d", len(parsed.Statements))
	}
	query, ok := parsed.Statements[0].Node.(*golyglot.SelectStmt)
	if !ok || len(query.Into) > 0 {
		return "", fmt.Errorf("row limits can only be added to one read-only SELECT")
	}
	value := strconv.Itoa(limit)
	for _, expression := range []golyglot.Expr{query.Top, query.Limit} {
		if expression != nil && expression.SourceSpan().Valid(len(sql)) && !expression.SourceSpan().Empty() {
			return parsed.ApplyEdits(golyglot.TextEdit{Span: expression.SourceSpan(), NewText: value})
		}
	}
	if query.Fetch != nil && query.Fetch.Count != nil {
		return parsed.ApplyEdits(golyglot.TextEdit{Span: query.Fetch.Count.SourceSpan(), NewText: value})
	}

	if dialect == golyglot.DialectTSQL {
		position, positionErr := topInsertionPosition(parsed, query)
		if positionErr != nil {
			return "", positionErr
		}
		return parsed.ApplyEdits(golyglot.TextEdit{
			Span:    golyglot.Span{Start: position, End: position},
			NewText: " TOP (" + value + ")",
		})
	}

	position := query.SourceSpan().End
	clause := " LIMIT " + value
	if dialect == golyglot.DialectOracle {
		clause = " FETCH FIRST " + value + " ROWS ONLY"
	}
	return parsed.ApplyEdits(golyglot.TextEdit{
		Span:    golyglot.Span{Start: position, End: position},
		NewText: clause,
	})
}

type tableMapping struct {
	source           []string
	destination      string
	destinationParts []golyglot.Identifier
}

func prepareTableMappings(mapping map[string]string, dialect golyglot.Dialect) ([]tableMapping, error) {
	entries := make([]tableMapping, 0, len(mapping))
	for source, destination := range mapping {
		sourceParts := splitRelationName(source)
		if len(sourceParts) == 0 {
			return nil, fmt.Errorf("table rename source %q is empty", source)
		}
		destination = strings.TrimSpace(destination)
		destinationParts, err := parseDestinationName(destination, dialect)
		if err != nil {
			return nil, fmt.Errorf("invalid table rename destination %q: %w", destination, err)
		}
		entries = append(entries, tableMapping{
			source: sourceParts, destination: destination, destinationParts: destinationParts,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].source) != len(entries[j].source) {
			return len(entries[i].source) > len(entries[j].source)
		}
		return strings.Join(entries[i].source, ".") < strings.Join(entries[j].source, ".")
	})
	return entries, nil
}

func parseDestinationName(name string, dialect golyglot.Dialect) ([]golyglot.Identifier, error) {
	if name == "" {
		return nil, fmt.Errorf("destination is empty")
	}
	prefix := "SELECT * FROM "
	parsed, err := golyglot.ParseStrict(prefix+name, dialect)
	if err != nil {
		return nil, err
	}
	if len(parsed.Statements) != 1 {
		return nil, fmt.Errorf("destination must be one relation name")
	}
	var tables []*golyglot.TableName
	golyglot.Walk(parsed.Statements[0].Node, func(node golyglot.Node) golyglot.VisitAction {
		if table, ok := node.(*golyglot.TableName); ok {
			tables = append(tables, table)
		}
		return golyglot.VisitChildren
	})
	if len(tables) != 1 || len(tables[0].Parts) == 0 || tables[0].Alias != nil || len(tables[0].Columns) > 0 || tables[0].Sample != nil || tables[0].Hint != "" || tables[0].Tail != "" {
		return nil, fmt.Errorf("destination must be one unaliased relation name")
	}
	parts := tables[0].Parts
	span := golyglot.Span{Start: parts[0].Span.Start, End: parts[len(parts)-1].Span.End}
	if span.Start != len(prefix) || span.End != len(prefix)+len(name) {
		return nil, fmt.Errorf("destination contains unsupported SQL")
	}
	return append([]golyglot.Identifier(nil), parts...), nil
}

func splitRelationName(name string) []string {
	rawParts := strings.Split(strings.TrimSpace(name), ".")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "`\"")
		part = strings.TrimPrefix(part, "[")
		part = strings.TrimSuffix(part, "]")
		if part == "" {
			return nil
		}
		parts = append(parts, part)
	}
	return parts
}

func matchingTableMapping(parts []golyglot.Identifier, mappings []tableMapping) *tableMapping {
	for index := range mappings {
		entry := &mappings[index]
		if identifiersMatchStrings(parts, entry.source) {
			return entry
		}
	}
	return nil
}

func matchingColumnQualifier(parts []golyglot.Identifier, mappings []tableMapping) *tableMapping {
	for index := range mappings {
		entry := &mappings[index]
		if len(parts) > len(entry.source) {
			continue
		}
		// A partially-qualified column may omit a source catalog/schema, but
		// every qualifier it does provide must be a suffix of the source name.
		sourceSuffix := entry.source[len(entry.source)-len(parts):]
		if identifiersMatchStrings(parts, sourceSuffix) {
			return entry
		}
	}
	return nil
}

func identifiersMatchStrings(identifiers []golyglot.Identifier, parts []string) bool {
	if len(identifiers) != len(parts) {
		return false
	}
	for index, identifier := range identifiers {
		if identifier.Quoted {
			if identifier.Text != parts[index] {
				return false
			}
			continue
		}
		if !strings.EqualFold(identifier.Text, parts[index]) {
			return false
		}
	}
	return true
}

func identifierEqual(left, right golyglot.Identifier) bool {
	if left.Quoted || right.Quoted {
		return left.Text == right.Text
	}
	return strings.EqualFold(left.Text, right.Text)
}

func identifierMatches(identifier golyglot.Identifier, text string) bool {
	if identifier.Quoted {
		return identifier.Text == text
	}
	return strings.EqualFold(identifier.Text, text)
}

func singleIdentifier(value string, dialect golyglot.Dialect) (string, error) {
	value = strings.TrimSpace(value)
	parts, err := parseDestinationName(value, dialect)
	if err != nil || len(parts) != 1 {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("expected one identifier")
	}
	return value, nil
}

func topInsertionPosition(parsed golyglot.ParseResult, query *golyglot.SelectStmt) (int, error) {
	projectionStart := query.SourceSpan().End
	for _, item := range query.Projections {
		if span := item.Expr.SourceSpan(); span.Valid(len(parsed.SQL)) && span.Start < projectionStart {
			projectionStart = span.Start
		}
	}
	selectIndex := -1
	for index, token := range parsed.Tokens {
		if token.Span.Start < query.SourceSpan().Start || token.Span.End > projectionStart {
			continue
		}
		if token.IsWord("SELECT") {
			selectIndex = index
		}
	}
	if selectIndex < 0 {
		return 0, fmt.Errorf("could not locate the outer SELECT keyword")
	}
	position := parsed.Tokens[selectIndex].Span.End
	for index := selectIndex + 1; index < len(parsed.Tokens); index++ {
		token := parsed.Tokens[index]
		if token.Span.Start >= projectionStart {
			break
		}
		if token.IsWord("DISTINCT") || token.IsWord("ALL") || token.IsWord("UNIQUE") {
			position = token.Span.End
		}
	}
	return position, nil
}

func rewriteDialect(name string) (golyglot.Dialect, error) {
	dialect, err := golyglot.ParseDialect(name)
	if err != nil {
		return "", err
	}
	// Fabric Warehouse SQL uses the T-SQL SELECT/TOP surface. Golyglot keeps
	// Fabric as a distinct transpilation target, so execution rewrites parse it
	// through the T-SQL grammar explicitly.
	if dialect == golyglot.DialectFabric {
		return golyglot.DialectTSQL, nil
	}
	return dialect, nil
}
