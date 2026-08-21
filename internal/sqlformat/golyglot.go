package sqlformat

import (
	"context"
	"errors"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

const DialectGeneric = "generic"

// Format parses and pretty-prints SQL through the in-process, pure-Go
// Golyglot engine. It intentionally keeps the former context-aware Renart API
// while avoiding a runtime, ABI, or module initialization boundary.
func Format(ctx context.Context, sql, dialectName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		return "", err
	}
	statements, err := golyglot.Format(sql, dialect)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(statements) == 0 {
		return "", errors.New("Golyglot returned no formatted SQL")
	}
	return statements[0], nil
}
