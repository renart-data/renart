package execution

import (
	"context"
	"errors"
	"strings"
	"time"

	"renart/internal/web/policy"
	"renart/internal/web/runcontext"
)

var ErrInvalidExecutionTime = errors.New("invalid execution time")

type AdmissionDependencies struct {
	EffectiveEnvironment func(string) string
	EffectiveFullRefresh func(context.Context, string, bool) bool
	PolicyFor            func(string) policy.EnvironmentPolicy
	ResolveTarget        func(string) (string, error)
	ResolveWindow        func(context.Context, RunSpec, time.Time) (TimeWindow, error)
	Now                  func() time.Time
}

type Admission struct {
	Spec          RunSpec
	ExecutionTime time.Time
	Target        string
	Window        TimeWindow
}

// Admitter owns the deterministic dispatch checks that must complete before
// inline-ledger admission or physical execution begins.
type Admitter struct {
	deps AdmissionDependencies
}

func NewAdmitter(deps AdmissionDependencies) *Admitter {
	return &Admitter{deps: deps}
}

func (a *Admitter) Admit(ctx context.Context, spec RunSpec) (Admission, error) {
	if a == nil || a.deps.ResolveTarget == nil || a.deps.ResolveWindow == nil {
		return Admission{}, errors.New("execution admission is unavailable")
	}
	contextInput := runcontext.Input{
		Start: spec.StartDate, End: spec.EndDate, FullRefresh: spec.FullRefresh,
		Backfill: spec.Backfill, SensorMode: spec.SensorMode,
	}
	normalized, err := runcontext.Normalize(contextInput)
	if err != nil {
		return Admission{}, err
	}
	if err := runcontext.ValidateDryRun(spec.DryRun, contextInput); err != nil {
		return Admission{}, err
	}
	spec.StartDate = normalized.StartString()
	spec.EndDate = normalized.EndString()
	spec.SensorMode = normalized.SensorMode
	executionTime := a.now()
	if raw := strings.TrimSpace(spec.ExecutionTime); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return Admission{}, ErrInvalidExecutionTime
		}
		executionTime = parsed.UTC()
	}
	if a.deps.EffectiveEnvironment != nil {
		spec.Environment = a.deps.EffectiveEnvironment(spec.Environment)
	} else {
		spec.Environment = strings.TrimSpace(spec.Environment)
	}
	if a.deps.EffectiveFullRefresh != nil {
		spec.FullRefresh = a.deps.EffectiveFullRefresh(ctx, spec.Environment, spec.FullRefresh)
	}
	request := RunPolicyRequest(spec)
	if a.deps.PolicyFor != nil {
		if err := policy.Check(a.deps.PolicyFor(spec.Environment), request); err != nil {
			return Admission{}, err
		}
	}
	target, err := a.deps.ResolveTarget(spec.PipelineID)
	if err != nil {
		return Admission{}, errors.New("invalid pipeline id")
	}
	if spec.SnapshotDir != "" {
		target = spec.SnapshotDir
	}
	window := TimeWindow{}
	if !spec.DryRun {
		window, err = a.deps.ResolveWindow(ctx, spec, executionTime)
		if err != nil {
			return Admission{}, err
		}
	}
	return Admission{Spec: spec, ExecutionTime: executionTime, Target: target, Window: window}, nil
}

func RunPolicyRequest(spec RunSpec) policy.RunRequest {
	return policy.RunRequest{
		Environment: spec.Environment, Interactive: !spec.Scheduled,
		SnapshotBased:        spec.SnapshotDir != "",
		Destructive:          !spec.DryRun && (spec.FullRefresh || spec.Backfill),
		ConfirmedEnvironment: strings.TrimSpace(spec.ConfirmedEnvironment),
	}
}

func (a *Admitter) now() time.Time {
	if a.deps.Now != nil {
		return a.deps.Now().UTC()
	}
	return time.Now().UTC()
}
