package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/policy"
)

func TestAdmitterNormalizesAndBindsRunBeforeExecution(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	admitter := NewAdmitter(AdmissionDependencies{
		EffectiveEnvironment: func(string) string { return "production" },
		EffectiveFullRefresh: func(_ context.Context, _ string, requested bool) bool { return requested },
		ResolveTarget:        func(string) (string, error) { return "pipelines/orders", nil },
		ResolveWindow: func(_ context.Context, spec RunSpec, executionTime time.Time) (TimeWindow, error) {
			assert.Equal(t, "production", spec.Environment)
			assert.Equal(t, now, executionTime)
			return TimeWindow{Start: now.Add(-time.Hour), End: now}, nil
		},
		Now: func() time.Time { return now },
	})

	admission, err := admitter.Admit(context.Background(), RunSpec{
		PipelineID: "pipeline-id", FullRefresh: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "production", admission.Spec.Environment)
	assert.Equal(t, "pipelines/orders", admission.Target)
	assert.Equal(t, now.Add(-time.Hour), admission.Window.Start)
}

func TestAdmitterEnforcesEnvironmentPolicyBeforeResolvingWindow(t *testing.T) {
	windowCalled := false
	admitter := NewAdmitter(AdmissionDependencies{
		EffectiveEnvironment: func(string) string { return "production" },
		PolicyFor: func(string) policy.EnvironmentPolicy {
			return policy.EnvironmentPolicy{Protected: true}
		},
		ResolveTarget: func(string) (string, error) { return "pipeline", nil },
		ResolveWindow: func(context.Context, RunSpec, time.Time) (TimeWindow, error) {
			windowCalled = true
			return TimeWindow{}, nil
		},
	})

	_, err := admitter.Admit(context.Background(), RunSpec{PipelineID: "pipeline-id"})
	require.Error(t, err)
	assert.False(t, windowCalled)
}
