import { useCallback, useEffect, useReducer } from "react";

import { getWorkspaceConfig } from "@/lib/api-config";
import {
  getPipelineConfig,
  getPipelinePythonDependencies,
  updatePipelineConfig,
  updatePipelinePythonDependencies,
} from "@/lib/api-pipelines";
import type {
  PipelineConfigResponse,
  PipelinePythonDependenciesResponse,
  UpdatePipelineConfigRequest,
  WorkspaceConfigEnvironment,
} from "@/lib/types";

export type PipelineConfigDraft = UpdatePipelineConfigRequest;
export type ReferencedPipelineConnection = PipelineConfigResponse["referenced_connections"][number];

type PipelineSettingsLoad = {
  draft: PipelineConfigDraft | null;
  inferredDefaultConnections: PipelineConfigResponse["inferred_default_connections"];
  referencedConnections: ReferencedPipelineConnection[];
  workspaceEnvironments: WorkspaceConfigEnvironment[];
  pythonDependencies: string[];
  pythonDependencyPath: string;
  errors: string[];
};

export type PipelineSettingsState = {
  draft: PipelineConfigDraft | null;
  savedDraft: PipelineConfigDraft | null;
  inferredDefaultConnections: PipelineConfigResponse["inferred_default_connections"];
  referencedConnections: ReferencedPipelineConnection[];
  workspaceEnvironments: WorkspaceConfigEnvironment[];
  pythonDependencies: string[];
  savedPythonDependencies: string[];
  pythonDependencyPath: string;
  loading: boolean;
  saving: boolean;
  error: string | null;
};

export type PipelineSettingsEvent =
  | { type: "load_started" }
  | { type: "loaded"; payload: PipelineSettingsLoad }
  | {
      type: "draft_changed";
      key: keyof PipelineConfigDraft;
      value: PipelineConfigDraft[keyof PipelineConfigDraft];
    }
  | { type: "python_dependencies_changed"; dependencies: string[] }
  | { type: "save_started" }
  | { type: "config_saved"; response: PipelineConfigResponse }
  | { type: "python_dependencies_saved"; response: PipelinePythonDependenciesResponse }
  | { type: "save_failed"; message: string }
  | { type: "save_finished" };

export const initialPipelineSettingsState: PipelineSettingsState = {
  draft: null,
  savedDraft: null,
  inferredDefaultConnections: [],
  referencedConnections: [],
  workspaceEnvironments: [],
  pythonDependencies: [],
  savedPythonDependencies: [],
  pythonDependencyPath: "",
  loading: false,
  saving: false,
  error: null,
};

export function pipelineSettingsReducer(
  state: PipelineSettingsState,
  event: PipelineSettingsEvent,
): PipelineSettingsState {
  switch (event.type) {
    case "load_started":
      return { ...initialPipelineSettingsState, loading: true };
    case "loaded":
      return {
        ...state,
        draft: event.payload.draft,
        savedDraft: event.payload.draft,
        inferredDefaultConnections: event.payload.inferredDefaultConnections,
        referencedConnections: event.payload.referencedConnections,
        workspaceEnvironments: event.payload.workspaceEnvironments,
        pythonDependencies: event.payload.pythonDependencies,
        savedPythonDependencies: event.payload.pythonDependencies,
        pythonDependencyPath: event.payload.pythonDependencyPath,
        loading: false,
        error: event.payload.errors.length > 0 ? event.payload.errors.join(" ") : null,
      };
    case "draft_changed":
      return state.draft
        ? { ...state, draft: { ...state.draft, [event.key]: event.value } }
        : state;
    case "python_dependencies_changed":
      return { ...state, pythonDependencies: event.dependencies };
    case "save_started":
      return { ...state, saving: true, error: null };
    case "config_saved": {
      const draft = pipelineConfigResponseToDraft(event.response);
      return {
        ...state,
        draft,
        savedDraft: draft,
        inferredDefaultConnections: event.response.inferred_default_connections ?? [],
        referencedConnections: event.response.referenced_connections ?? [],
      };
    }
    case "python_dependencies_saved":
      return {
        ...state,
        pythonDependencies: event.response.dependencies,
        savedPythonDependencies: event.response.dependencies,
        pythonDependencyPath: event.response.path,
      };
    case "save_failed":
      return { ...state, saving: false, error: event.message };
    case "save_finished":
      return { ...state, saving: false, error: null };
  }
}

export function pipelineConfigResponseToDraft(config: PipelineConfigResponse): PipelineConfigDraft {
  const notification = (value?: PipelineConfigDraft["notifications_slack"]) => ({
    enabled: value?.enabled ?? false,
    channel: value?.channel ?? "",
    connection: value?.connection ?? "",
    success: value?.success ?? false,
    failure: value?.failure ?? true,
  });
  return {
    name: config.name ?? "",
    schedule: config.schedule ?? "",
    start_date: config.start_date ?? "",
    owner: config.owner ?? "",
    tags: config.tags ?? [],
    domains: config.domains ?? [],
    default_connections: config.default_connections ?? [],
    catchup: config.catchup ?? false,
    metadata_push_bigquery: config.metadata_push_bigquery ?? false,
    retries: config.retries ?? 0,
    concurrency: config.concurrency ?? 0,
    max_active_steps: config.max_active_steps,
    notifications_slack: notification(config.notifications_slack),
    notifications_teams: notification(config.notifications_teams),
    defaults: config.defaults ?? {},
    variables: config.variables ?? [],
  };
}

export function pipelineSettingsDirty(state: PipelineSettingsState): boolean {
  return (
    !pipelineConfigDraftsEqual(state.draft, state.savedDraft) ||
    !sameStringArray(state.pythonDependencies, state.savedPythonDependencies)
  );
}

export function pipelineConfigDraftsEqual(
  left: PipelineConfigDraft | null,
  right: PipelineConfigDraft | null,
): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return JSON.stringify(left) === JSON.stringify(right);
}

export type PipelineSettingsValidation = {
  valid: boolean;
  pipelineName?: string;
  retries?: string;
  concurrency?: string;
  maxActiveSteps?: string;
  legacySchedule?: string;
  variables: Record<number, { name?: string; type?: string }>;
};

const variableTypes = new Set(["string", "integer", "number", "boolean", "array", "object"]);

export function validatePipelineSettings(
  draft: PipelineConfigDraft | null,
): PipelineSettingsValidation {
  const validation: PipelineSettingsValidation = { valid: Boolean(draft), variables: {} };
  if (!draft) return validation;

  if (!draft.name.trim()) validation.pipelineName = "Pipeline name is required.";
  if (!Number.isFinite(draft.retries) || draft.retries < 0) {
    validation.retries = "Retries must be zero or greater.";
  }
  if (!Number.isFinite(draft.concurrency) || draft.concurrency < 0) {
    validation.concurrency = "Overlapping runs must be zero or greater.";
  }
  if (
    draft.max_active_steps !== undefined &&
    (!Number.isInteger(draft.max_active_steps) || draft.max_active_steps < 1)
  ) {
    validation.maxActiveSteps = "Maximum active steps must be a whole number of at least one.";
  }
  if (draft.catchup && !draft.schedule.trim()) {
    validation.legacySchedule = "A Bruin schedule is required when catch-up is enabled.";
  }

  const variableNames = new Map<string, number[]>();
  draft.variables.forEach((variable, index) => {
    const name = variable.name.trim();
    if (!name) {
      validation.variables[index] = { name: "Variable name is required." };
    } else {
      const indexes = variableNames.get(name) ?? [];
      indexes.push(index);
      variableNames.set(name, indexes);
    }
    if (!variableTypes.has(variable.type.trim())) {
      validation.variables[index] = {
        ...validation.variables[index],
        type: "Choose a supported variable type.",
      };
    }
  });
  for (const indexes of variableNames.values()) {
    if (indexes.length < 2) continue;
    for (const index of indexes) {
      validation.variables[index] = {
        ...validation.variables[index],
        name: "Variable names must be unique.",
      };
    }
  }

  validation.valid =
    !validation.pipelineName &&
    !validation.retries &&
    !validation.concurrency &&
    !validation.maxActiveSteps &&
    !validation.legacySchedule &&
    Object.keys(validation.variables).length === 0;
  return validation;
}

type PipelineSettingsPersistence = {
  updateConfig: typeof updatePipelineConfig;
  updatePythonDependencies: typeof updatePipelinePythonDependencies;
};

export async function persistPipelineSettings(
  pipelineId: string,
  state: PipelineSettingsState,
  dispatch: (event: PipelineSettingsEvent) => void,
  persistence: PipelineSettingsPersistence = {
    updateConfig: updatePipelineConfig,
    updatePythonDependencies: updatePipelinePythonDependencies,
  },
): Promise<boolean> {
  if (!state.draft || !pipelineSettingsDirty(state)) return false;
  dispatch({ type: "save_started" });
  let configSaved = false;
  try {
    if (!pipelineConfigDraftsEqual(state.draft, state.savedDraft)) {
      const response = await persistence.updateConfig(pipelineId, state.draft);
      dispatch({ type: "config_saved", response });
      configSaved = true;
    }
    if (!sameStringArray(state.pythonDependencies, state.savedPythonDependencies)) {
      const response = await persistence.updatePythonDependencies(pipelineId, {
        dependencies: state.pythonDependencies,
      });
      dispatch({ type: "python_dependencies_saved", response });
    }
    dispatch({ type: "save_finished" });
    return true;
  } catch (cause) {
    const detail = cause instanceof Error ? cause.message : "Failed to save pipeline settings.";
    dispatch({
      type: "save_failed",
      message: configSaved
        ? `Pipeline configuration was saved, but Python dependencies were not: ${detail}`
        : detail,
    });
    return false;
  }
}

export function usePipelineSettings(open: boolean, pipelineId: string) {
  const [state, dispatch] = useReducer(pipelineSettingsReducer, initialPipelineSettingsState);

  useEffect(() => {
    if (!open) return;
    dispatch({ type: "load_started" });
    let cancelled = false;
    Promise.allSettled([
      getPipelineConfig(pipelineId),
      getPipelinePythonDependencies(pipelineId),
      getWorkspaceConfig(),
    ]).then(([configResult, pythonResult, workspaceResult]) => {
      if (cancelled) return;
      const errors: string[] = [];
      const config = configResult.status === "fulfilled" ? configResult.value : null;
      if (configResult.status === "rejected") {
        errors.push(errorMessage(configResult.reason, "Failed to load pipeline settings."));
      }
      const python = pythonResult.status === "fulfilled" ? pythonResult.value : null;
      if (pythonResult.status === "rejected") {
        errors.push(errorMessage(pythonResult.reason, "Failed to load Python dependencies."));
      }
      const workspace = workspaceResult.status === "fulfilled" ? workspaceResult.value : null;
      if (workspaceResult.status === "rejected") {
        errors.push(errorMessage(workspaceResult.reason, "Failed to load available connections."));
      }
      dispatch({
        type: "loaded",
        payload: {
          draft: config ? pipelineConfigResponseToDraft(config) : null,
          inferredDefaultConnections: config?.inferred_default_connections ?? [],
          referencedConnections: config?.referenced_connections ?? [],
          workspaceEnvironments: workspace?.environments ?? [],
          pythonDependencies: python?.dependencies ?? [],
          pythonDependencyPath: python?.path ?? "",
          errors,
        },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [open, pipelineId]);

  const update = useCallback(
    <K extends keyof PipelineConfigDraft>(key: K, value: PipelineConfigDraft[K]) => {
      dispatch({ type: "draft_changed", key, value });
    },
    [],
  );
  const setPythonDependencies = useCallback((dependencies: string[]) => {
    dispatch({ type: "python_dependencies_changed", dependencies });
  }, []);
  const save = useCallback(
    () => persistPipelineSettings(pipelineId, state, dispatch),
    [pipelineId, state],
  );

  return {
    ...state,
    dirty: pipelineSettingsDirty(state),
    validation: validatePipelineSettings(state.draft),
    update,
    setPythonDependencies,
    save,
  };
}

function sameStringArray(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error ? cause.message : fallback;
}
