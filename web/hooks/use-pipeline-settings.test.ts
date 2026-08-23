import { describe, expect, it, vi } from "vitest";

import type { PipelineConfigResponse, PipelinePythonDependenciesResponse } from "@/lib/types";

import {
  initialPipelineSettingsState,
  persistPipelineSettings,
  pipelineConfigResponseToDraft,
  pipelineSettingsDirty,
  pipelineSettingsReducer,
  validatePipelineSettings,
  type PipelineSettingsEvent,
  type PipelineSettingsState,
} from "./use-pipeline-settings";

function configResponse(overrides: Partial<PipelineConfigResponse> = {}): PipelineConfigResponse {
  return {
    status: "ok",
    id: "pipeline-id",
    path: "analytics",
    name: "analytics",
    tags: [],
    domains: [],
    default_connections: [],
    inferred_default_connections: [],
    referenced_connections: [],
    catchup: false,
    metadata_push_bigquery: false,
    retries: 0,
    concurrency: 1,
    notifications_slack: {
      enabled: false,
      success: false,
      failure: true,
    },
    notifications_teams: {
      enabled: false,
      success: false,
      failure: true,
    },
    defaults: {},
    variables: [],
    yaml: "name: analytics\n",
    ...overrides,
  };
}

function loadedState(response = configResponse()): PipelineSettingsState {
  const draft = pipelineConfigResponseToDraft(response);
  return pipelineSettingsReducer(initialPipelineSettingsState, {
    type: "loaded",
    payload: {
      draft,
      inferredDefaultConnections: [],
      referencedConnections: [],
      workspaceEnvironments: [],
      pythonDependencies: [],
      pythonDependencyPath: "analytics/pyproject.toml",
      errors: [],
    },
  });
}

describe("pipeline settings state", () => {
  it("tracks normalized config and Python dependency changes independently", () => {
    let state = loadedState();
    expect(pipelineSettingsDirty(state)).toBe(false);

    state = pipelineSettingsReducer(state, {
      type: "draft_changed",
      key: "owner",
      value: "data@example.com",
    });
    expect(pipelineSettingsDirty(state)).toBe(true);

    state = pipelineSettingsReducer(state, {
      type: "config_saved",
      response: configResponse({ owner: "data@example.com" }),
    });
    expect(pipelineSettingsDirty(state)).toBe(false);

    state = pipelineSettingsReducer(state, {
      type: "python_dependencies_changed",
      dependencies: ["polars>=1"],
    });
    expect(pipelineSettingsDirty(state)).toBe(true);
  });

  it("rejects incomplete and duplicate variable declarations before saving", () => {
    const draft = pipelineConfigResponseToDraft(
      configResponse({
        name: "",
        variables: [
          { name: "region", type: "string", default_value: "eu" },
          { name: "region", type: "mystery", default_value: "us" },
          { name: "", type: "integer", default_value: 1 },
        ],
      }),
    );
    const validation = validatePipelineSettings(draft);

    expect(validation.valid).toBe(false);
    expect(validation.pipelineName).toBe("Pipeline name is required.");
    expect(validation.variables[0]?.name).toBe("Variable names must be unique.");
    expect(validation.variables[1]).toEqual({
      name: "Variable names must be unique.",
      type: "Choose a supported variable type.",
    });
    expect(validation.variables[2]?.name).toBe("Variable name is required.");
  });

  it("keeps only the failed dependency portion dirty after a partial save", async () => {
    let state = loadedState();
    state = pipelineSettingsReducer(state, {
      type: "draft_changed",
      key: "owner",
      value: "data@example.com",
    });
    state = pipelineSettingsReducer(state, {
      type: "python_dependencies_changed",
      dependencies: ["polars>=1"],
    });

    const events: PipelineSettingsEvent[] = [];
    const dispatch = (event: PipelineSettingsEvent) => {
      events.push(event);
      state = pipelineSettingsReducer(state, event);
    };
    const updateConfig = vi.fn(async () => configResponse({ owner: "data@example.com" }));
    const updatePythonDependencies = vi.fn(async () => {
      throw new Error("pyproject.toml is read-only");
    });

    await expect(
      persistPipelineSettings("pipeline-id", state, dispatch, {
        updateConfig,
        updatePythonDependencies,
      }),
    ).resolves.toBe(false);

    expect(updateConfig).toHaveBeenCalledBefore(updatePythonDependencies);
    expect(state.error).toContain("Pipeline configuration was saved");
    expect(state.savedDraft?.owner).toBe("data@example.com");
    expect(state.savedPythonDependencies).toEqual([]);
    expect(pipelineSettingsDirty(state)).toBe(true);
  });

  it("skips unchanged config when only Python dependencies changed", async () => {
    let state = loadedState();
    state = pipelineSettingsReducer(state, {
      type: "python_dependencies_changed",
      dependencies: ["polars>=1"],
    });

    const response: PipelinePythonDependenciesResponse = {
      status: "ok",
      pipeline_id: "pipeline-id",
      path: "analytics/pyproject.toml",
      dependencies: ["polars>=1"],
    };
    const updateConfig = vi.fn(async () => configResponse());
    const updatePythonDependencies = vi.fn(async () => response);
    const dispatch = (event: PipelineSettingsEvent) => {
      state = pipelineSettingsReducer(state, event);
    };

    await expect(
      persistPipelineSettings("pipeline-id", state, dispatch, {
        updateConfig,
        updatePythonDependencies,
      }),
    ).resolves.toBe(true);
    expect(updateConfig).not.toHaveBeenCalled();
    expect(updatePythonDependencies).toHaveBeenCalledOnce();
    expect(pipelineSettingsDirty(state)).toBe(false);
  });
});
