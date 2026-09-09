package sqlnamespace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuoteReferencePreservesIndividualIdentifiers(t *testing.T) {
	for _, engine := range []string{"starrocks", "doris", "trino", "databricks", "duckdb", "motherduck"} {
		parts := []string{"Lake.Catalog", "sales", "Order.Items", "a\"b`c"}
		reference := Reference(engine, parts...)
		decoded, err := Parts(reference)
		require.NoError(t, err)
		require.Equal(t, parts, decoded)
		quoted, err := QuoteReference(engine, reference)
		require.NoError(t, err)
		decoded, err = Parts(quoted)
		require.NoError(t, err)
		require.Equal(t, parts, decoded)
	}
	for _, invalid := range []string{"", "cat..table", "cat.", `cat."unfinished`, `"cat"unexpected.table`, "cat.\x00table"} {
		_, err := Parts(invalid)
		require.Error(t, err, invalid)
	}
}
