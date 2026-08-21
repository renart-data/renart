package bruincompat

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/renart-data/golyglot/pkg/golyglot"
)

type DeclareHoister struct{}

func NewDeclareHoister() *DeclareHoister { return &DeclareHoister{} }

func (h *DeclareHoister) HoistDeclares(query string, assetType pipeline.AssetType) (string, error) {
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return query, err
	}
	if strings.TrimSpace(query) == "" {
		return query, nil
	}

	positions, err := topLevelSemicolons(context.Background(), query, dialect)
	if err != nil {
		return query, err
	}

	slices := make([]string, 0, len(positions)+1)
	previous := 0
	for _, position := range positions {
		slices = append(slices, query[previous:position])
		previous = position + 1
	}
	if previous < len(query) {
		slices = append(slices, query[previous:])
	}

	declares := make([]string, 0)
	rest := make([]string, 0)
	sawNonDeclare := false
	needsReorder := false
	for _, statement := range slices {
		trimmed := strings.TrimSpace(statement)
		if trimmed == "" {
			continue
		}
		isDeclare, err := isDeclareStatement(context.Background(), trimmed, dialect)
		if err != nil {
			return query, err
		}
		if isDeclare {
			declares = append(declares, trimmed)
			needsReorder = needsReorder || sawNonDeclare
			continue
		}
		rest = append(rest, trimmed)
		sawNonDeclare = true
	}

	if len(declares) == 0 || !needsReorder {
		return query, nil
	}
	return strings.Join(append(declares, rest...), ";\n") + ";", nil
}

func (h *DeclareHoister) HoistDeclaresList(queries []string, assetType pipeline.AssetType) ([]string, error) {
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return queries, err
	}
	if len(queries) == 0 {
		return queries, nil
	}

	declares := make([]string, 0)
	rest := make([]string, 0)
	sawNonDeclare := false
	needsReorder := false
	for _, query := range queries {
		isDeclare, err := isDeclareStatement(context.Background(), strings.TrimSpace(query), dialect)
		if err != nil {
			return queries, err
		}
		if isDeclare {
			declares = append(declares, query)
			needsReorder = needsReorder || sawNonDeclare
			continue
		}
		rest = append(rest, query)
		sawNonDeclare = true
	}
	if len(declares) == 0 || !needsReorder {
		return queries, nil
	}
	return append(declares, rest...), nil
}

func topLevelSemicolons(ctx context.Context, query, dialect string) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nativeDialect, err := golyglot.ParseDialect(dialect)
	if err != nil {
		return nil, err
	}
	tokens, diagnostics, err := golyglot.Tokenize(query, nativeDialect)
	if err != nil {
		return nil, fmt.Errorf("tokenize SQL for DECLARE hoisting: %w", err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == golyglot.SeverityError {
			return nil, fmt.Errorf("Golyglot SQL tokenization failed: %s", diagnostic.Message)
		}
	}

	parenDepth := 0
	beginEndDepth := 0
	caseDepth := 0
	positions := make([]int, 0)
	for index, current := range tokens {
		switch {
		case current.Text == "(":
			parenDepth++
		case current.Text == ")":
			if parenDepth > 0 {
				parenDepth--
			}
		case current.IsWord("CASE"):
			caseDepth++
		case current.IsWord("BEGIN"):
			nextIsTransaction := nextSignificantTokenIs(tokens, index+1, "TRANSACTION")
			if !nextIsTransaction {
				beginEndDepth++
			}
		case current.IsWord("END"):
			if caseDepth > 0 {
				caseDepth--
			} else if beginEndDepth > 0 {
				beginEndDepth--
			}
		case current.Text == ";":
			if parenDepth == 0 && beginEndDepth == 0 {
				positions = append(positions, current.Span.Start)
			}
		}
	}
	return positions, nil
}

func nextSignificantTokenIs(tokens []golyglot.Token, start int, word string) bool {
	for index := start; index < len(tokens); index++ {
		if tokens[index].Kind == golyglot.TokenComment {
			continue
		}
		return tokens[index].IsWord(word)
	}
	return false
}

func isDeclareStatement(ctx context.Context, statement, dialect string) (bool, error) {
	if !strings.Contains(strings.ToLower(statement), "declare") {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	nativeDialect, err := golyglot.ParseDialect(dialect)
	if err != nil {
		return false, err
	}
	parsed, err := golyglot.ParseStrict(statement, nativeDialect)
	if err != nil {
		return false, nil
	}
	if len(parsed.Statements) != 1 {
		return false, nil
	}
	switch node := parsed.Statements[0].Node.(type) {
	case *golyglot.CommandStmt:
		return strings.EqualFold(node.Keyword, "DECLARE"), nil
	case *golyglot.RawStmt:
		return strings.EqualFold(node.Keyword, "DECLARE"), nil
	default:
		for _, token := range parsed.Tokens {
			if token.Kind == golyglot.TokenComment {
				continue
			}
			return token.IsWord("DECLARE"), nil
		}
		return false, nil
	}
}
