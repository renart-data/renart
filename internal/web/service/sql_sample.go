package service

import (
	"fmt"
	"strings"
)

// Callers must validate a single read-only SELECT first. The wrapper preserves
// authored LIMIT/TOP/FETCH instead of silently increasing its result semantics.
func wrapReadOnlySampleQuery(query, connectionType, alias string, limit int) string {
	query = strings.TrimRight(strings.TrimSpace(query), "; \n\r\t")
	switch normalizeConnectionType(connectionType) {
	case "mssql", "synapse", "fabric":
		return fmt.Sprintf("SELECT TOP (%d) * FROM (\n%s\n) AS %s", limit, query, alias)
	case "oracle":
		return fmt.Sprintf("SELECT * FROM (\n%s\n) %s\nFETCH FIRST %d ROWS ONLY", query, alias, limit)
	default:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS %s\nLIMIT %d", query, alias, limit)
	}
}
