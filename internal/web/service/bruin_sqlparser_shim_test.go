//go:build cgo && (linux || darwin)

package service

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/stretchr/testify/require"
)

func TestBruinRustSQLParserLinkShimFailsClosed(t *testing.T) {
	t.Parallel()

	parser, err := sqlparser.NewRustSQLParser(false)
	require.NoError(t, err)

	_, err = parser.UsedTables("select * from should_not_be_parsed", "duckdb")
	require.ErrorContains(t, err, "Bruin RustSQLParser is disabled; Renart uses native Golyglot")
}
