package scheduler

import (
	"context"
	"errors"
	"fmt"

	"log/slog"
	"renart/internal/web/secretstore"
	"strings"
)

func (s *Service) reconcileScheduleDeclarations(ctx context.Context) error {
	if s == nil || s.declarations == nil {
		return nil
	}
	desired, err := s.declarations.List()
	if err != nil {
		return err
	}
	rows, err := s.store.ListEnvSchedules(ctx)
	if err != nil {
		return err
	}
	existing := make(map[string]EnvSchedule, len(rows))
	for _, row := range rows {
		existing[scheduleDeclarationKey(row.PipelineUUID, row.Environment)] = row
	}
	desiredKeys := make(map[string]struct{}, len(desired))

	for _, item := range desired {
		key := scheduleDeclarationKey(item.PipelineUUID, item.Environment)
		desiredKeys[key] = struct{}{}
		declaration := normalizeScheduleDeclaration(item.Declaration)
		row, found := existing[key]
		if !found {
			row = EnvSchedule{
				PipelineUUID: item.PipelineUUID,
				Environment:  item.Environment,
			}
		}
		row.Cron = declaration.Cron
		row.Timezone = declaration.Timezone
		row.CatchupPolicy = declaration.CatchupPolicy
		row.Vars = cloneScheduleVariables(declaration.Variables)
		row.SecretRefs = cloneScheduleSecretRefs(declaration.SecretRefs)
		row.DeclarationManaged = true
		row.ArchivedReason = ""
		row.Status = ScheduleStatusActive
		if declaration.Paused || strings.TrimSpace(row.SnapshotVersionID) == "" {
			row.Status = ScheduleStatusPaused
		}

		_, pipelineExists := s.resolveRef(ctx, item.PipelineUUID)
		if !pipelineExists {
			row.Status = ScheduleStatusArchived
			row.ArchivedReason = ArchivedReasonMissing
		} else if row.CatchupPolicy == CatchupBackfill && s.pipelineIntervalAware != nil &&
			!s.pipelineIntervalAware(ctx, row.PipelineUUID) {
			row.Status = ScheduleStatusPaused
			slog.Warn("pausing declared schedule because backfill is not supported",
				"pipeline_uuid", row.PipelineUUID,
				"environment", row.Environment,
			)
		}
		if row.Status == ScheduleStatusActive {
			if err := s.validateEnvScheduleVariables(ctx, row); err != nil {
				row.Status = ScheduleStatusPaused
				slog.Warn("pausing declared schedule with unavailable or invalid variables",
					"pipeline_uuid", row.PipelineUUID,
					"environment", row.Environment,
					"error", err,
				)
			}
		}
		if err := s.store.UpsertEnvSchedule(ctx, row); err != nil {
			return fmt.Errorf("reconcile declared schedule %s/%s: %w", item.PipelineUUID, item.Environment, err)
		}
	}

	for _, row := range rows {
		if !row.DeclarationManaged {
			continue
		}
		if _, ok := desiredKeys[scheduleDeclarationKey(row.PipelineUUID, row.Environment)]; ok {
			continue
		}
		if row.Status == ScheduleStatusArchived && row.ArchivedReason == ArchivedReasonDeclarationMissing {
			continue
		}
		if err := s.store.SetEnvScheduleStatus(
			ctx,
			row.PipelineUUID,
			row.Environment,
			ScheduleStatusArchived,
			ArchivedReasonDeclarationMissing,
		); err != nil {
			return fmt.Errorf("archive removed schedule declaration %s/%s: %w", row.PipelineUUID, row.Environment, err)
		}
	}
	return nil
}

func (s *Service) resolveScheduleVariables(
	ctx context.Context,
	environment string,
	variables map[string]any,
	secretRefs map[string]string,
) (map[string]any, error) {
	if err := validateScheduleVariableOverridesShape(variables); err != nil {
		return nil, err
	}
	for name, reference := range secretRefs {
		if err := validateScheduleVariableName(name); err != nil {
			return nil, err
		}
		if _, duplicate := variables[name]; duplicate {
			return nil, fmt.Errorf("schedule variable %q has both a value and a secret reference", name)
		}
		if err := validateScheduleSecretReference(reference); err != nil {
			return nil, fmt.Errorf("schedule variable %q: %w", name, err)
		}
	}
	if len(secretRefs) == 0 {
		return cloneScheduleVariables(variables), nil
	}
	if s.resolveScheduleSecrets == nil {
		return nil, errors.New("schedule secret resolution is unavailable")
	}
	resolvedSecrets, err := s.resolveScheduleSecrets(ctx, environment, cloneScheduleSecretRefs(secretRefs))
	if err != nil {
		return nil, err
	}
	if len(resolvedSecrets) != len(secretRefs) {
		return nil, errors.New("schedule secret resolver returned an incomplete result")
	}
	resolved := cloneScheduleVariables(variables)
	if resolved == nil {
		resolved = make(map[string]any, len(resolvedSecrets))
	}
	for name := range secretRefs {
		value, ok := resolvedSecrets[name]
		if !ok {
			return nil, fmt.Errorf("schedule secret resolver omitted variable %q", name)
		}
		resolved[name] = value
	}
	if err := validateScheduleVariableOverridesShape(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// ResolveEnvScheduleVariables is used by the explicit Run-pinned action. The
// returned values exist only for the request; callers must retain the row's
// SecretRefs separately in the private RunSpec.
func (s *Service) ResolveEnvScheduleVariables(ctx context.Context, row EnvSchedule) (map[string]any, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeScheduleValidation)
	return s.resolveScheduleVariables(ctx, row.Environment, row.Vars, row.SecretRefs)
}

func (s *Service) validateEnvScheduleVariables(ctx context.Context, row EnvSchedule) error {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeScheduleValidation)
	resolved, err := s.resolveScheduleVariables(ctx, row.Environment, row.Vars, row.SecretRefs)
	if err != nil {
		return err
	}
	return s.validateScheduleVariableOverrides(ctx, row.PipelineUUID, row.SnapshotVersionID, resolved)
}

// resolveRunSpecForExecution turns durable references into an ephemeral
// execution context. The returned spec must never be written back to the run
// ledger: it can contain resolved secret values.
func (s *Service) resolveRunSpecForExecution(
	ctx context.Context,
	spec runSpecV1,
	retainedPlan PipelineRunPlan,
	hasRetainedPlan bool,
) (runSpecV1, error) {
	resolved, err := s.resolveScheduleVariables(
		secretstore.WithPurpose(ctx, secretstore.PurposeScheduledRun),
		spec.Requested.Environment,
		spec.Requested.Variables,
		spec.Requested.VariableReferences,
	)
	if err != nil {
		return runSpecV1{}, fmt.Errorf("resolve run variable references: %w", err)
	}

	if len(spec.Requested.VariableReferences) > 0 && hasRetainedPlan {
		if s.planScheduledRun == nil {
			return runSpecV1{}, errors.New("reviewed-plan verification is unavailable for referenced variables")
		}
		if spec.Requested.Start == nil || spec.Requested.End == nil || spec.Requested.ExecutionTime == nil {
			return runSpecV1{}, errors.New("reviewed plan is missing its execution window")
		}
		frozenProducerDeployments, err := producerDeploymentsFromPlan(retainedPlan)
		if err != nil {
			return runSpecV1{}, fmt.Errorf("load reviewed producer deployments: %w", err)
		}
		replanned, err := s.planScheduledRun(ctx, ScheduledRunPlanRequest{
			PipelineID:                spec.Pipeline.ID,
			PipelineUUID:              spec.Pipeline.UUID,
			Environment:               spec.Requested.Environment,
			SnapshotVersionID:         spec.Source.SnapshotVersionID,
			Start:                     spec.Requested.Start.UTC(),
			End:                       spec.Requested.End.UTC(),
			ExecutionTime:             spec.Requested.ExecutionTime.UTC(),
			VariableOverrides:         resolved,
			FrozenProducerDeployments: frozenProducerDeployments,
		})
		if err != nil {
			return runSpecV1{}, fmt.Errorf("verify referenced variables against reviewed plan: %w", err)
		}
		bindingRun := applyRunSpec(PipelineRun{}, spec)
		if err := validateRunPlanAdmissionBinding(bindingRun, spec, replanned.Plan); err != nil {
			return runSpecV1{}, fmt.Errorf("referenced variables no longer match the reviewed plan: %w", err)
		}
		if strings.TrimSpace(replanned.Plan.PlanID) != strings.TrimSpace(retainedPlan.PlanID) {
			return runSpecV1{}, errors.New("referenced variables no longer produce the reviewed plan")
		}
	}

	executionSpec := spec
	executionSpec.Requested.Variables = resolved
	executionSpec.Requested.VariableReferences = nil
	if err := executionSpec.validate(); err != nil {
		return runSpecV1{}, err
	}
	return executionSpec, nil
}

func cloneScheduleVariables(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}

func cloneScheduleSecretRefs(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
