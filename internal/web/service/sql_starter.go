package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// sqlStarterSource is one upstream asset a generated query reads.
type sqlStarterSource struct {
	Name    string
	Columns []pipeline.Column
}

// sqlStarterStyle carries the project conventions a generated query follows.
type sqlStarterStyle struct {
	Lowercase bool
	Dialect   string
}

func (s sqlStarterStyle) kw(keyword string) string {
	if s.Lowercase {
		return strings.ToLower(keyword)
	}
	return keyword
}

var (
	sqlLineCommentPattern  = regexp.MustCompile(`--[^\n]*`)
	sqlBlockCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)
	sqlUpperKeywordPattern = regexp.MustCompile(`\b(SELECT|FROM|WHERE|JOIN)\b`)
	sqlLowerKeywordPattern = regexp.MustCompile(`\b(select|from|where|join)\b`)
	simpleSQLIdentifier    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// projectSQLStarterStyle reads the keyword case the pipeline's SQL assets
// already use. Uppercase wins ties and pipelines without SQL, matching the
// project templates.
func projectSQLStarterStyle(pp *pipeline.Pipeline, assetType string) sqlStarterStyle {
	dialect, _ := AssetTypeToDialect(pipeline.AssetType(assetType))
	upper, lower := 0, 0
	if pp != nil {
		for _, asset := range pp.Assets {
			if asset == nil || !asset.IsSQLAsset() {
				continue
			}
			code := sqlBlockCommentPattern.ReplaceAllString(asset.ExecutableFile.Content, "")
			code = sqlLineCommentPattern.ReplaceAllString(code, "")
			upper += len(sqlUpperKeywordPattern.FindAllStringIndex(code, -1))
			lower += len(sqlLowerKeywordPattern.FindAllStringIndex(code, -1))
		}
	}
	return sqlStarterStyle{Lowercase: lower > upper, Dialect: dialect}
}

// downstreamSQLStarterFor writes the starter query over sources with the
// columns their definitions declare or imply.
func downstreamSQLStarterFor(ctx context.Context, pp *pipeline.Pipeline, assetType string, sources []*pipeline.Asset) string {
	resolver := newAssetDefinitionSchemaResolver(pp)
	starterSources := make([]sqlStarterSource, 0, len(sources))
	for _, source := range sources {
		starterSources = append(starterSources, sqlStarterSource{Name: source.Name, Columns: resolver.Available(ctx, source)})
	}
	return downstreamSQLStarter(starterSources, projectSQLStarterStyle(pp, assetType))
}

// downstreamSQLStarter is the query of a new SQL asset that reads sources.
// One source selects its known columns, or * when none are known. Several
// sources become a join skeleton on the columns they share.
func downstreamSQLStarter(sources []sqlStarterSource, style sqlStarterStyle) string {
	if len(sources) == 0 {
		return ""
	}
	if len(sources) == 1 {
		source := sources[0]
		var b strings.Builder
		b.WriteString(style.kw("SELECT"))
		if len(source.Columns) == 0 {
			b.WriteString(" *\n")
		} else {
			b.WriteString("\n")
			for i, column := range source.Columns {
				b.WriteString("    " + quoteStarterIdentifier(column.Name, style.Dialect))
				if i < len(source.Columns)-1 {
					b.WriteString(",")
				}
				b.WriteString("\n")
			}
		}
		b.WriteString(style.kw("FROM") + " " + source.Name + "\n")
		return b.String()
	}

	aliases := starterAliases(sources, nil)
	base := sqlStarterRelation{Reference: sources[0].Name, Alias: aliases[0], Columns: sources[0].Columns}
	joined := make([]sqlStarterRelation, 0, len(sources)-1)
	for i, source := range sources[1:] {
		joined = append(joined, sqlStarterRelation{Reference: source.Name, Alias: aliases[i+1], Columns: source.Columns})
	}
	return renderSQLJoin(base, joined, false, style)
}

// joinedSQLQuery wraps an existing query as a CTE and joins source to it, so
// the asset reads the new upstream while its own logic stays intact.
func joinedSQLQuery(targetName, query string, targetColumns []pipeline.Column, source sqlStarterSource, style sqlStarterStyle) string {
	body := strings.TrimRight(strings.ReplaceAll(query, "\r\n", "\n"), " \t\n;")
	aliases := starterAliases([]sqlStarterSource{{Name: targetName}, source}, nil)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s %s (\n", style.kw("WITH"), aliases[0], style.kw("AS")))
	for _, line := range strings.Split(strings.Trim(body, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	b.WriteString(")\n\n")
	base := sqlStarterRelation{Reference: aliases[0], Alias: aliases[0], Columns: targetColumns, AllColumns: true}
	b.WriteString(renderSQLJoin(base, []sqlStarterRelation{{Reference: source.Name, Alias: aliases[1], Columns: source.Columns}}, true, style))
	return b.String()
}

type sqlStarterRelation struct {
	Reference string
	Alias     string
	Columns   []pipeline.Column
	// AllColumns selects alias.* for this relation even when its columns are
	// known; they are still used to find join keys and skip duplicates.
	AllColumns bool
}

func renderSQLJoin(base sqlStarterRelation, joined []sqlStarterRelation, baseIsCTE bool, style sqlStarterStyle) string {
	type selectItem struct{ alias, column string }
	var items []selectItem
	selected := map[string]bool{}
	addRelation := func(relation sqlStarterRelation) {
		if relation.AllColumns || len(relation.Columns) == 0 {
			items = append(items, selectItem{alias: relation.Alias, column: "*"})
			for _, column := range relation.Columns {
				selected[strings.ToLower(column.Name)] = true
			}
			return
		}
		for _, column := range relation.Columns {
			key := strings.ToLower(column.Name)
			if selected[key] {
				continue
			}
			selected[key] = true
			items = append(items, selectItem{alias: relation.Alias, column: column.Name})
		}
	}

	type joinClause struct {
		relation sqlStarterRelation
		on       []string // left alias, left column, right column
	}
	clauses := make([]joinClause, 0, len(joined))
	earlier := []sqlStarterRelation{base}
	addRelation(base)
	for _, relation := range joined {
		clause := joinClause{relation: relation}
		if left, column, ok := starterJoinKey(earlier, relation); ok {
			clause.on = []string{left.Alias, column, starterColumnName(relation.Columns, column)}
		}
		clauses = append(clauses, clause)
		earlier = append(earlier, relation)
		addRelation(relation)
	}

	var b strings.Builder
	b.WriteString(style.kw("SELECT") + "\n")
	for i, item := range items {
		column := item.column
		if column != "*" {
			column = quoteStarterIdentifier(column, style.Dialect)
		}
		b.WriteString("    " + item.alias + "." + column)
		if i < len(items)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	if baseIsCTE {
		b.WriteString(style.kw("FROM") + " " + base.Reference + "\n")
	} else {
		b.WriteString(fmt.Sprintf("%s %s %s %s\n", style.kw("FROM"), base.Reference, style.kw("AS"), base.Alias))
	}
	for _, clause := range clauses {
		if clause.on == nil {
			b.WriteString(fmt.Sprintf("-- No shared column with %s: replace the CROSS JOIN with a join condition.\n", clause.relation.Alias))
			b.WriteString(fmt.Sprintf("%s %s %s %s\n", style.kw("CROSS JOIN"), clause.relation.Reference, style.kw("AS"), clause.relation.Alias))
			continue
		}
		b.WriteString(fmt.Sprintf("%s %s %s %s\n", style.kw("LEFT JOIN"), clause.relation.Reference, style.kw("AS"), clause.relation.Alias))
		b.WriteString(fmt.Sprintf("    %s %s.%s = %s.%s\n",
			style.kw("ON"),
			clause.on[0], quoteStarterIdentifier(clause.on[1], style.Dialect),
			clause.relation.Alias, quoteStarterIdentifier(clause.on[2], style.Dialect),
		))
	}
	return b.String()
}

// starterJoinKey picks the column relation shares with an earlier relation:
// its primary key first, then an id-like column, then any shared column.
func starterJoinKey(earlier []sqlStarterRelation, relation sqlStarterRelation) (sqlStarterRelation, string, bool) {
	type candidate struct {
		left   sqlStarterRelation
		column string
		rank   int
	}
	var best *candidate
	for _, column := range relation.Columns {
		key := strings.ToLower(column.Name)
		for _, left := range earlier {
			leftColumn := starterColumnName(left.Columns, column.Name)
			if leftColumn == "" {
				continue
			}
			rank := 3
			switch {
			case column.PrimaryKey || starterColumnIsPrimaryKey(left.Columns, column.Name):
				rank = 0
			case key == "id" || strings.HasSuffix(key, "_id"):
				rank = 1
			}
			if best == nil || rank < best.rank {
				best = &candidate{left: left, column: leftColumn, rank: rank}
			}
			break
		}
	}
	if best == nil {
		return sqlStarterRelation{}, "", false
	}
	return best.left, best.column, true
}

func starterColumnName(columns []pipeline.Column, name string) string {
	for _, column := range columns {
		if strings.EqualFold(column.Name, name) {
			return column.Name
		}
	}
	return ""
}

func starterColumnIsPrimaryKey(columns []pipeline.Column, name string) bool {
	for _, column := range columns {
		if strings.EqualFold(column.Name, name) {
			return column.PrimaryKey
		}
	}
	return false
}

// starterAliases names each source by the last part of its asset name, kept
// unique and clear of reserved words.
func starterAliases(sources []sqlStarterSource, taken map[string]bool) []string {
	if taken == nil {
		taken = map[string]bool{}
	}
	aliases := make([]string, len(sources))
	for i, source := range sources {
		leaf := source.Name
		if dot := strings.LastIndex(leaf, "."); dot >= 0 {
			leaf = leaf[dot+1:]
		}
		alias := strings.ToLower(strings.Trim(nonIdentifierCharacters.ReplaceAllString(leaf, "_"), "_"))
		if alias == "" || (alias[0] >= '0' && alias[0] <= '9') {
			alias = "t_" + alias
		}
		if starterReservedWords[alias] {
			alias += "_src"
		}
		candidate := alias
		for n := 2; taken[candidate]; n++ {
			candidate = fmt.Sprintf("%s_%d", alias, n)
		}
		taken[candidate] = true
		aliases[i] = candidate
	}
	return aliases
}

var nonIdentifierCharacters = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// quoteStarterIdentifier leaves plain names bare, as hand-written SQL does,
// and quotes the rest in the dialect's style.
func quoteStarterIdentifier(name, dialect string) string {
	caseSensitive := dialect == "postgres"
	if simpleSQLIdentifier.MatchString(name) && !starterReservedWords[strings.ToLower(name)] &&
		(!caseSensitive || name == strings.ToLower(name)) {
		return name
	}
	switch dialect {
	case "bigquery", "mysql", "databricks", "clickhouse", "doris":
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case "tsql", "fabric":
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
}

var starterReservedWords = map[string]bool{
	"all": true, "and": true, "as": true, "asc": true, "between": true, "by": true,
	"case": true, "check": true, "column": true, "create": true, "cross": true,
	"current_date": true, "current_time": true, "current_timestamp": true,
	"current_user": true, "default": true, "desc": true, "distinct": true,
	"else": true, "end": true, "except": true, "false": true, "for": true,
	"from": true, "full": true, "grant": true, "group": true, "having": true,
	"in": true, "inner": true, "intersect": true, "into": true, "is": true,
	"join": true, "left": true, "like": true, "limit": true, "natural": true,
	"not": true, "null": true, "offset": true, "on": true, "or": true,
	"order": true, "outer": true, "primary": true, "references": true,
	"right": true, "select": true, "table": true, "then": true, "to": true,
	"true": true, "union": true, "unique": true, "user": true, "using": true,
	"when": true, "where": true, "window": true, "with": true,
}
