package sqlintelligence

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

// UsedTables returns the input relations in a SQL script. Its compatibility
// policy matches Bruin's dependency parser: DML write targets are excluded,
// CREATE TABLE targets are retained, CTE aliases and table functions are not
// dependencies, and a preceding T-SQL USE qualifies later references.
func UsedTables(query, dialect string) ([]string, error) {
	return UsedTablesContext(context.Background(), query, dialect)
}

func UsedTablesContext(ctx context.Context, query, dialectName string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		return nil, err
	}
	parsed, err := golyglot.ParseStrict(query, dialect)
	if err != nil {
		return nil, fmt.Errorf("Golyglot SQL parse failed: %w", err)
	}
	seen := make(map[string]struct{})
	currentDatabase := ""
	for _, statement := range parsed.Statements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		statementSQL := nativeSourceSlice(query, statement.Span)
		switch node := statement.Node.(type) {
		case *golyglot.CommandStmt:
			if strings.EqualFold(node.Keyword, "USE") {
				currentDatabase = usedTablesCommandArgument(statementSQL, "USE")
			}
		case *golyglot.SelectStmt:
			collectGolyglotSelectTables(seen, node, dialect, currentDatabase)
		case *golyglot.InsertStmt:
			if node.Query != nil {
				collectGolyglotSelectTables(seen, node.Query, dialect, currentDatabase)
			}
		case *golyglot.UpdateStmt:
			collectTokenTableReferences(seen, statementSQL, dialect, currentDatabase)
		case *golyglot.DeleteStmt:
			// Bruin excludes both DELETE targets and USING relations.
		case *golyglot.CreateTableStmt:
			addGolyglotTableName(seen, golyglotTableParts(node.Name, dialect, currentDatabase))
			collectTokenTableReferences(seen, statementSQL, dialect, currentDatabase)
		case *golyglot.RawStmt:
			if strings.EqualFold(node.Keyword, "USE") {
				currentDatabase = usedTablesCommandArgument(statementSQL, "USE")
			}
		}
	}
	tables := make([]string, 0, len(seen))
	for table := range seen {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables, nil
}

func collectGolyglotSelectTables(seen map[string]struct{}, query *golyglot.SelectStmt, dialect golyglot.Dialect, currentDatabase string) {
	if query == nil {
		return
	}
	cteAliases := make(map[string]struct{})
	golyglot.Walk(query, func(node golyglot.Node) golyglot.VisitAction {
		selectStmt, ok := node.(*golyglot.SelectStmt)
		if !ok {
			return golyglot.VisitChildren
		}
		for _, cte := range selectStmt.With {
			cteAliases[strings.ToLower(cte.Name.Text)] = struct{}{}
		}
		return golyglot.VisitChildren
	})
	golyglot.Walk(query, func(node golyglot.Node) golyglot.VisitAction {
		if function, ok := node.(*golyglot.TableFunctionFrom); ok {
			if dialect == golyglot.DialectTSQL && golyglotLooksLikeTSQLTableHint(function) {
				addGolyglotTableName(seen, golyglotTableParts(function.Name, dialect, currentDatabase))
			}
			return golyglot.VisitChildren
		}
		table, ok := node.(*golyglot.TableName)
		if !ok || len(table.Parts) == 0 {
			return golyglot.VisitChildren
		}
		if len(table.Parts) == 1 {
			if _, isCTE := cteAliases[strings.ToLower(table.Parts[0].Text)]; isCTE {
				return golyglot.VisitChildren
			}
		}
		addGolyglotTableName(seen, golyglotTableParts(table.Parts, dialect, currentDatabase))
		return golyglot.VisitChildren
	})
}

func golyglotLooksLikeTSQLTableHint(function *golyglot.TableFunctionFrom) bool {
	if function == nil || len(function.Name) == 0 || len(function.Args) == 0 || len(function.Args) > 2 {
		return false
	}
	for _, argument := range function.Args {
		if _, ok := argument.(*golyglot.IdentifierExpr); !ok {
			return false
		}
	}
	return true
}

func golyglotTableParts(identifiers []golyglot.Identifier, dialect golyglot.Dialect, currentDatabase string) []string {
	parts := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		parts[index] = identifier.Text
	}
	if dialect == golyglot.DialectTSQL && currentDatabase != "" {
		switch len(parts) {
		case 1:
			parts = []string{currentDatabase, "dbo", parts[0]}
		case 2:
			parts = []string{currentDatabase, parts[0], parts[1]}
		}
	}
	return parts
}

func addGolyglotTableName(seen map[string]struct{}, parts []string) {
	name := strings.Join(parts, ".")
	if name != "" {
		seen[name] = struct{}{}
	}
}

func usedTablesCommandArgument(statement, keyword string) string {
	trimmed := strings.TrimSpace(statement)
	if len(trimmed) < len(keyword) || !strings.EqualFold(trimmed[:len(keyword)], keyword) {
		return ""
	}
	value := strings.TrimSpace(strings.TrimSuffix(trimmed[len(keyword):], ";"))
	return strings.Trim(value, "`\"[]")
}

// collectTokenTableReferences covers statement tails that Golyglot preserves
// losslessly but does not model yet, notably UPDATE ... FROM and CREATE TABLE
// ... AS SELECT. Only FROM/JOIN references are considered, so write targets and
// expressions cannot become dependencies.
func collectTokenTableReferences(seen map[string]struct{}, sql string, dialect golyglot.Dialect, currentDatabase string) {
	tokens, diagnostics, err := golyglot.Tokenize(sql, dialect)
	if err != nil || hasGolyglotErrorDiagnostic(diagnostics) {
		return
	}
	for index := 0; index < len(tokens); index++ {
		if !tokens[index].IsWord("FROM") && !tokens[index].IsWord("JOIN") {
			continue
		}
		cursor := index + 1
		for cursor < len(tokens) && (tokens[cursor].IsWord("LATERAL") || tokens[cursor].IsWord("ONLY")) {
			cursor++
		}
		if cursor >= len(tokens) || tokens[cursor].Text == "(" || !golyglotIdentifierToken(tokens[cursor]) {
			continue
		}
		parts := []string{trimSQLIdentifier(tokens[cursor].Text)}
		cursor++
		for cursor+1 < len(tokens) && tokens[cursor].Text == "." && golyglotIdentifierToken(tokens[cursor+1]) {
			parts = append(parts, trimSQLIdentifier(tokens[cursor+1].Text))
			cursor += 2
		}
		if cursor < len(tokens) && tokens[cursor].Text == "(" {
			continue
		}
		if dialect == golyglot.DialectTSQL && currentDatabase != "" {
			switch len(parts) {
			case 1:
				parts = []string{currentDatabase, "dbo", parts[0]}
			case 2:
				parts = []string{currentDatabase, parts[0], parts[1]}
			}
		}
		addGolyglotTableName(seen, parts)
	}
}

func hasGolyglotErrorDiagnostic(diagnostics []golyglot.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == golyglot.SeverityError {
			return true
		}
	}
	return false
}

func golyglotIdentifierToken(token golyglot.Token) bool {
	return token.Kind == golyglot.TokenIdentifier || token.Kind == golyglot.TokenQuotedIdentifier || token.Kind == golyglot.TokenKeyword
}

func trimSQLIdentifier(value string) string {
	return strings.Trim(value, "`\"[]")
}
