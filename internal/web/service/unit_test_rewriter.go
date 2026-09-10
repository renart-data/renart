package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/date"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/renart-data/golyglot/pkg/golyglot"
	"renart/internal/sqlintelligence"
)

// The upstream fixture compiler and comparator are pure Go; this adapter keeps
// their SQL rewrites on Renart's native parser (no Rust/Python parser runtime).
type unitTestRewriter struct{}

func boundedUnitTestQuery(sql, dialect string) (string, error) {
	_, query, _, err := unitTestSelect(sql, dialect)
	if err != nil {
		return "", err
	}
	bound := query.Limit
	if query.Top != nil {
		bound = query.Top
	}
	if query.Fetch != nil {
		if query.Fetch.Percent || query.Fetch.WithTies {
			return "", fmt.Errorf("unit tests require a fixed row limit, not PERCENT or WITH TIES")
		}
		bound = query.Fetch.Count
	}
	if bound != nil {
		literal, ok := bound.(*golyglot.LiteralExpr)
		if !ok {
			return "", fmt.Errorf("unit tests require a constant integer row limit")
		}
		count, err := strconv.ParseUint(literal.Raw, 10, 64)
		if err != nil {
			return "", fmt.Errorf("unit tests require a nonnegative integer row limit")
		}
		if count <= 5000 {
			return sql, nil
		}
	}
	// Keep ORDER BY and WITH at the top level, including on SQL Server.
	// An oversized result is rejected by the caller, never a partial pass.
	return sqlintelligence.AddLimit(sql, 5001, dialect)
}

func unitTestSelect(sql, dialect string) (golyglot.ParseResult, *golyglot.SelectStmt, golyglot.Dialect, error) {
	d, err := golyglot.ParseDialect(dialect)
	if err != nil {
		return golyglot.ParseResult{}, nil, d, err
	}
	parsed, err := golyglot.ParseStrict(sql, d)
	if err != nil || len(parsed.Statements) != 1 {
		return golyglot.ParseResult{}, nil, d, fmt.Errorf("unit tests require one supported SELECT statement")
	}
	query, ok := parsed.Statements[0].Node.(*golyglot.SelectStmt)
	if !ok {
		return golyglot.ParseResult{}, nil, d, fmt.Errorf("unit tests require a SELECT asset; SQL scripts and DDL are not executed")
	}
	var invalid error
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		switch n := node.(type) {
		case *golyglot.SelectStmt:
			if len(n.Into) > 0 {
				invalid = fmt.Errorf("SELECT INTO is not allowed in unit tests")
			}
		case *golyglot.TableFunctionFrom:
			// Table functions can read external data without a physical table
			// reference; only these fixture-local generators are allowed.
			name := ""
			if len(n.Name) == 1 {
				name = strings.ToLower(n.Name[0].Text)
			}
			if name != "range" && name != "generate_series" && name != "unnest" {
				invalid = fmt.Errorf("mock external data as an input; table function %q is not supported in unit tests", name)
			}
		}
		return golyglot.VisitChildren
	})
	return parsed, query, d, invalid
}

func (unitTestRewriter) ExtractSelect(sql, dialect string) (string, error) {
	_, _, _, err := unitTestSelect(sql, dialect)
	return strings.TrimSuffix(strings.TrimSpace(sql), ";"), err
}
func (unitTestRewriter) UsedTables(sql, dialect string) ([]string, error) {
	return sqlintelligence.UsedTables(sql, dialect)
}
func (unitTestRewriter) RenameTables(sql, dialect string, mapping map[string]string) (string, error) {
	return sqlintelligence.RenameTables(sql, dialect, mapping)
}
func (unitTestRewriter) PrependCTEs(sql, dialect string, ctes []sqlparser.CTE) (string, error) {
	_, query, d, err := unitTestSelect(sql, dialect)
	if err != nil {
		return "", err
	}
	added := make([]golyglot.CTE, 0, len(ctes))
	for _, cte := range ctes {
		for _, old := range query.With {
			if strings.EqualFold(old.Name.Text, cte.Name) {
				return "", fmt.Errorf("fixture name collides with CTE %q", cte.Name)
			}
		}
		_, body, _, err := unitTestSelect(cte.Query, dialect)
		if err != nil {
			return "", err
		}
		added = append(added, golyglot.CTE{Name: golyglot.Identifier{Text: cte.Name}, Query: body})
	}
	query.With = append(added, query.With...)
	return golyglot.GenerateWithOptions(query, golyglot.GenerateOptions{Dialect: d, Canonical: true})
}
func (unitTestRewriter) SelectFromCTE(sql, dialect, name string) (string, error) {
	_, query, d, err := unitTestSelect(sql, dialect)
	if err != nil {
		return "", err
	}
	for i, cte := range query.With {
		if cte.Name.Text != name {
			continue
		}
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if dialect == "mysql" || dialect == "bigquery" || dialect == "clickhouse" {
			quoted = "`" + strings.ReplaceAll(name, "`", "``") + "`"
		}
		_, target, _, err := unitTestSelect("select * from "+quoted, dialect)
		if err != nil {
			return "", err
		}
		target.With = query.With[:i+1]
		return golyglot.GenerateWithOptions(target, golyglot.GenerateOptions{Dialect: d, Canonical: true})
	}
	return "", fmt.Errorf("CTE %q does not exist", name)
}
func (unitTestRewriter) FreezeTime(sql, dialect, timestamp string) (string, error) {
	t, err := date.ParseTime(timestamp)
	if err != nil {
		return "", err
	}
	parsed, _, _, err := unitTestSelect(sql, dialect)
	if err != nil {
		return "", err
	}
	var edits []golyglot.TextEdit
	clock := func(name string, span golyglot.Span) bool {
		name = strings.ToLower(name)
		typ, value := "TIMESTAMP", t.UTC().Format("2006-01-02 15:04:05.999999")
		switch name {
		case "current_date":
			typ, value = "DATE", t.UTC().Format("2006-01-02")
		case "current_timestamp", "now", "localtimestamp":
		case "current_time", "localtime":
			typ, value = "TIME", t.UTC().Format("15:04:05.999999")
		default:
			return false
		}
		if typ == "TIMESTAMP" && dialect == "tsql" {
			typ = "DATETIME2"
		}
		if typ == "TIMESTAMP" && dialect == "mysql" {
			typ = "DATETIME"
		}
		edits = append(edits, golyglot.TextEdit{Span: span, NewText: "CAST('" + value + "' AS " + typ + ")"})
		return true
	}
	golyglot.WalkResult(parsed, func(node golyglot.Node) golyglot.VisitAction {
		switch n := node.(type) {
		case *golyglot.FunctionCallExpr:
			if len(n.Name) == 1 && !n.Name[0].Quoted && len(n.Args) == 0 && clock(n.Name[0].Text, n.SourceSpan()) {
				return golyglot.SkipChildren
			}
		case *golyglot.CallExpr:
			if id, ok := n.Callee.(*golyglot.IdentifierExpr); ok && len(id.Parts) == 1 && len(n.Args) == 0 && clock(id.Parts[0].Text, n.SourceSpan()) {
				return golyglot.SkipChildren
			}
		case *golyglot.IdentifierExpr:
			if len(n.Parts) == 1 && !n.Parts[0].Quoted && !strings.EqualFold(n.Parts[0].Text, "now") {
				clock(n.Parts[0].Text, n.SourceSpan())
			}
		}
		return golyglot.VisitChildren
	})
	return parsed.ApplyEdits(edits...)
}
