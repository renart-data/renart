package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"renart/internal/web/preview"
)

func TestInspectPreviewLookaheadAndPayloadBudget(t *testing.T) {
	for _, count := range []int{0, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			executor := &stubExecutionExecutor{runWithRetry: func(_ context.Context, req QueryAssetRequest, _ int, _ time.Duration) ([]byte, error, int) {
				require.Equal(t, "3", req.Limit)
				rows := make([]map[string]any, count)
				for i := range rows {
					rows[i] = map[string]any{"id": i}
				}
				output, err := json.Marshal(QueryRowsEnvelope{Columns: []string{"id"}, Rows: rows})
				return output, err, 1
			}}
			svc := NewExecutionService(ExecutionDependencies{Executor: executor, ParseQueryOutput: ParseQueryJSONOutput})
			result := svc.InspectAsset(context.Background(), EncodeID("assets/orders.sql"), "2", "default", "", "")
			require.Equal(t, "ok", result.Status)
			require.Len(t, result.Rows, min(count, 2))
			require.Equal(t, count > 2, result.Preview.HasMore)
			require.Empty(t, executor.runAssetRequests)
			require.Empty(t, executor.runPipelineReqs)
		})
	}
	executor := &stubExecutionExecutor{runWithRetry: func(_ context.Context, req QueryAssetRequest, _ int, _ time.Duration) ([]byte, error, int) {
		require.Equal(t, "1001", req.Limit)
		output, err := json.Marshal(QueryRowsEnvelope{Columns: []string{"v"}, Rows: []map[string]any{{"v": strings.Repeat("x", preview.MaxRowBytes)}}})
		return output, err, 1
	}}
	result := NewExecutionService(ExecutionDependencies{Executor: executor, ParseQueryOutput: ParseQueryJSONOutput}).InspectAsset(context.Background(), EncodeID("assets/orders.sql"), "999999", "", "", "")
	require.Empty(t, result.Rows)
	require.Equal(t, "byte_limit", result.Preview.Reason)
	require.Less(t, len(result.RawOutput), 100, "raw output must not bypass the payload budget")
}
