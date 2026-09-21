package sqllsp

import (
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

func completionInsideLiteralOrComment(sql string, offset int, dialectName string) bool {
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		dialect = golyglot.DialectGeneric
	}
	tokens, _, err := golyglot.Tokenize(sql[:min(max(offset, 0), len(sql))], dialect)
	if err != nil {
		literal, _, comment := quoteContextAtOffset(sql, offset)
		return literal || comment
	}
	for _, token := range tokens {
		if token.Span.End != offset {
			continue
		}
		switch token.Kind {
		case golyglot.TokenUnterminatedString, golyglot.TokenUnterminatedComment:
			return true
		case golyglot.TokenComment:
			return !strings.HasSuffix(token.Text, "*/")
		}
	}
	return false
}

func completionStatementRange(sql string, offset int, dialectName string) byteRange {
	result := byteRange{start: 0, end: len(sql)}
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		dialect = golyglot.DialectGeneric
	}
	tokens, _, err := golyglot.Tokenize(sql, dialect)
	if err != nil {
		return result
	}
	for _, token := range tokens {
		if token.Kind != golyglot.TokenPunctuation || token.Text != ";" {
			continue
		}
		if token.Span.End <= offset {
			result.start = token.Span.End
		} else {
			result.end = token.Span.Start
			break
		}
	}
	return result
}

// Completion analyzes a document's dialect without mutating the shared engine.
type dialectScopeResolver struct {
	scopeResolver
	dialect string
}

func (a sqlAnalysis) identifierKey(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	quoted := len(identifier) > 1 && (identifier[0] == '"' || identifier[0] == '`')
	if quoted {
		if decoded, end := readIdentifier(identifier, 0); end == len(identifier) {
			identifier = decoded
		}
	}
	switch a.dialect {
	case "duckdb", "sqlite", "tsql", "":
		return strings.ToLower(identifier)
	}
	if quoted {
		return identifier
	}
	if a.dialect == "snowflake" || a.dialect == "oracle" {
		return strings.ToUpper(identifier)
	}
	return strings.ToLower(identifier)
}

// visibleCTEsAt walks lexical WITH scopes, not the flat full-document CTE map.
// A body sees earlier siblings (and itself under RECURSIVE), never its consumer
// or later non-recursive siblings. Inner WITH declarations shadow outer ones.
func visibleCTEsAt(sql string, offset int, scopes []byteRange, resolver dialectScopeResolver, baseOffset int) sqlAnalysis {
	visible := sqlAnalysis{dialect: resolver.dialect, aliases: map[string]aliasRef{}, ctes: map[string]aliasRef{}}
	for _, scope := range scopes {
		for _, cte := range extractCTEDefs(sql[scope.start:scope.end]) {
			start := scope.start + cte.bodyStart
			end := start + len(cte.body)
			if offset < start {
				break
			}
			inside := offset <= end
			if !inside || cte.recursive {
				addCTEDef(&visible, cte, resolver, baseOffset+scope.start)
			}
			if inside {
				break
			}
		}
	}
	return visible
}

func addCTEDef(analysis *sqlAnalysis, cte cteBody, resolver scopeResolver, baseOffset int) {
	key := analysis.identifierKey(cte.identifier)
	ref := aliasRef{alias: cte.identifier, name: cte.identifier, kind: "cte", start: baseOffset + cte.nameStart, end: baseOffset + cte.nameEnd}
	if cte.recursive {
		// Bind declared names before analyzing the recursive arm. Unknown types
		// stay unknown; name completion does not require executing the recursion.
		ref.columns, ref.columnRanges = applyColumnAliases(nil, nil, cte.columnAliases, baseOffset)
		analysis.ctes[key] = ref
	}
	absoluteBodyStart := baseOffset + cte.bodyStart
	body := analyzeSQLWithParent(cte.body, resolver, analysis, absoluteBodyStart)
	ref.columns, ref.columnRanges = outputColumnsWithRanges(cte.body, body, resolver, absoluteBodyStart)
	ref.columns, ref.columnRanges = applyColumnAliases(ref.columns, ref.columnRanges, cte.columnAliases, baseOffset)
	analysis.ctes[key] = ref
}

func applyColumnAliases(columns []ColumnInfo, ranges map[string]byteRange, aliases []subqueryColumnAlias, baseOffset int) ([]ColumnInfo, map[string]byteRange) {
	if len(aliases) == 0 {
		return columns, ranges
	}
	renamed := make([]ColumnInfo, 0, len(aliases))
	ranges = make(map[string]byteRange, len(aliases))
	for index, alias := range aliases {
		column := ColumnInfo{Name: alias.name}
		if index < len(columns) {
			column = columns[index]
			column.Name = alias.name
		}
		renamed = append(renamed, column)
		ranges[strings.ToLower(alias.name)] = byteRange{start: baseOffset + alias.start, end: baseOffset + alias.end}
	}
	return renamed, ranges
}

func (e *Engine) scopedRelationCompletions(analysis sqlAnalysis) []CompletionItem {
	items := make([]CompletionItem, 0, len(analysis.ctes)+len(e.graph.Relations))
	for _, cte := range analysis.ctes {
		items = append(items, CompletionItem{Label: cte.alias, InsertText: cte.alias, Kind: completionKindReference, Detail: "CTE", SortText: "0" + strings.ToLower(cte.alias)})
	}
	for _, item := range e.relationCompletions() {
		if _, shadowed := analysis.ctes[analysis.identifierKey(item.Label)]; !shadowed {
			items = append(items, item)
		}
	}
	sortCompletionItems(items)
	return items
}
