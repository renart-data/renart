package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/telemetry"
)

func TestUsagePipelineReportsOneOutcomeAndExcludesDryRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want telemetry.Outcome
		dry  bool
	}{
		{"success", nil, telemetry.Success, false},
		{"failure", errors.New("private warehouse password detail"), telemetry.Failed, false},
		{"cancel", context.Canceled, telemetry.Cancelled, false},
		{"dry", nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observed []telemetry.Observation
			svc := NewExecutionService(ExecutionDependencies{Executor: &stubExecutionExecutor{runPipelineErr: tc.err}, RecordUsage: func(o telemetry.Observation) { observed = append(observed, o) }})
			result := svc.MaterializePipelineRun(WithExecutionOrigin(context.Background(), webscheduler.RunTriggerCLI), PipelineRunSpec{PipelineID: EncodeID("private/pipeline.yml"), DryRun: tc.dry}, nil, nil)
			if tc.dry {
				require.Empty(t, observed)
				return
			}
			require.Len(t, observed, 1, "result=%+v", result)
			require.Equal(t, tc.want, observed[0].Outcome)
			require.Equal(t, telemetry.CLI, observed[0].Trigger)
			require.Equal(t, telemetry.Pipeline, observed[0].Surface)
		})
	}
}

func TestUsageCollectorFailureDoesNotReplacePipelineOutcome(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer receiver.Close()
	client := telemetry.New(telemetry.Options{Version: "v0.5.8", Endpoint: receiver.URL, ConfigPath: filepath.Join(t.TempDir(), "telemetry.json"), Getenv: func(string) string { return "" }})
	defer client.Close()
	_, err := client.Update(telemetry.UpdateRequest{AcknowledgeNotice: true})
	require.NoError(t, err)
	for _, runErr := range []error{nil, errors.New("original pipeline failure")} {
		svc := NewExecutionService(ExecutionDependencies{Executor: &stubExecutionExecutor{runPipelineErr: runErr}, RecordUsage: client.Record})
		result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{PipelineID: EncodeID("private/pipeline.yml")}, nil, nil)
		client.Flush(context.Background())
		if runErr == nil {
			require.Equal(t, "ok", result.Status)
			require.Empty(t, result.Error)
		} else {
			require.Equal(t, "error", result.Status)
			require.Equal(t, runErr.Error(), result.Error)
		}
	}
}
