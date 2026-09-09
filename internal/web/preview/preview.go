// Package preview owns the response budget for interactive row samples. Query
// adapters must also apply NormalizeLimit(limit)+1 in their read-only SQL; this
// response budget is not a substitute for streaming driver memory limits.
package preview

import (
	"encoding/json"

	"github.com/google/uuid"
	"renart/internal/web/model"
)

const MaxRows = 1000
const MaxRowBytes = 2 * 1024 * 1024

func NormalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return min(limit, MaxRows)
}

// Bound consumes an adapter's limit+1 lookahead. It never counts the warehouse
// or constructs OFFSET pages. Wider samples replace, rather than append to, rows.
func Bound(rows []map[string]any, limit int, adapterHasMore bool) ([]map[string]any, *model.PreviewMetadata) {
	return BoundRows(rows, limit, adapterHasMore)
}

// BoundRows also budgets positional notebook rows without losing duplicate columns.
func BoundRows[T any](rows []T, limit int, adapterHasMore bool) ([]T, *model.PreviewMetadata) {
	limit = NormalizeLimit(limit)
	meta := &model.PreviewMetadata{
		Limit: limit, HasMore: adapterHasMore || len(rows) > limit,
		Continuation: "none", Reason: "complete", ResultID: uuid.NewString(),
	}
	bounded := make([]T, 0, min(len(rows), limit))
	bytes := 2 // JSON array delimiters; budget includes row commas.
	for _, row := range rows[:min(len(rows), limit)] {
		encoded, err := json.Marshal(row)
		if err != nil || bytes+len(encoded)+1 > MaxRowBytes {
			meta.HasMore, meta.Reason = true, "byte_limit"
			break
		}
		bounded = append(bounded, row)
		bytes += len(encoded) + 1
	}
	meta.ReturnedRows = len(bounded)
	if meta.HasMore && meta.Reason != "byte_limit" {
		if limit == MaxRows {
			meta.Reason = "row_limit"
		} else {
			meta.Continuation, meta.Reason = "replace", ""
			meta.NextLimit = min(MaxRows, limit+max(100, limit))
		}
	}
	return bounded, meta
}
