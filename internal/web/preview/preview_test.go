package preview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedReplacement(t *testing.T) {
	for _, count := range []int{0, 99, 100, 101, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rows := make([]map[string]any, count)
			for i := range rows {
				rows[i] = map[string]any{"id": i}
			}
			result, meta := Bound(rows, 100, false)
			require.Len(t, result, min(count, 100))
			require.Equal(t, count > 100, meta.HasMore)
			require.Equal(t, len(result), meta.ReturnedRows)
			require.NotEmpty(t, meta.ResultID)
			if count > 100 {
				require.Equal(t, "replace", meta.Continuation)
				require.Equal(t, 200, meta.NextLimit)
			} else {
				require.Equal(t, "none", meta.Continuation)
				require.Equal(t, "complete", meta.Reason)
			}
		})
	}
}

func TestCeilingsDoNotOfferImpossibleContinuation(t *testing.T) {
	rows := make([]map[string]any, MaxRows+1)
	_, meta := Bound(rows, MaxRows+500, false)
	require.True(t, meta.HasMore)
	require.Equal(t, "row_limit", meta.Reason)
	require.Zero(t, meta.NextLimit)
	wide := []map[string]any{{"v": "visible"}, {"v": strings.Repeat("x", MaxRowBytes)}}
	result, meta := Bound(wide, 100, false)
	require.Len(t, result, 1)
	require.True(t, meta.HasMore)
	require.Equal(t, "byte_limit", meta.Reason)
	require.Equal(t, "none", meta.Continuation)
	require.Zero(t, meta.NextLimit)
}

func TestAdapterExhaustionAndNormalizedLimits(t *testing.T) {
	_, meta := Bound([]map[string]any{{"id": 1}}, 0, true)
	require.True(t, meta.HasMore)
	require.Equal(t, 100, meta.Limit)
	require.Equal(t, 200, meta.NextLimit)
	require.Equal(t, MaxRows, NormalizeLimit(999999))
	require.Equal(t, 100, NormalizeLimit(-1))
}
