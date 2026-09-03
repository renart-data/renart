import { describe, expect, it } from "vitest";

import type { DeployStatus } from "@/lib/api-deploy";
import type { EnvSchedule } from "@/lib/api-env-schedules";
import type { PipelineRun, WebPipeline } from "@/lib/types";

import { buildRunOverviewModel } from "./use-run-overview-model";

const now = Date.parse("2026-09-02T12:00:00.000Z");

function pipeline(id: string, name = id): WebPipeline {
  return { id, uuid: `${id}-uuid`, name, path: `${id}/pipeline.yml`, assets: [] };
}

function schedule(overrides: Partial<EnvSchedule> = {}): EnvSchedule {
  return {
    pipeline_uuid: "analytics-uuid",
    pipeline_id: "analytics",
    pipeline_name: "analytics",
    environment: "production",
    snapshot_version_id: "version-1",
    snapshot_ordinal: 1,
    cron: "0 * * * *",
    timezone: "UTC",
    declaration_managed: true,
    catchup_policy: "skip",
    status: "active",
    next_run_at: "2026-09-02T13:00:00.000Z",
    created_at: "2026-09-01T00:00:00.000Z",
    updated_at: "2026-09-01T00:00:00.000Z",
    ...overrides,
  };
}

function run(overrides: Partial<PipelineRun> = {}): PipelineRun {
  return {
    id: "run-1",
    pipeline_id: "analytics",
    pipeline: "analytics",
    environment: "production",
    trigger: "schedule",
    status: "success",
    started_at: "2026-09-02T11:00:00.000Z",
    finished_at: "2026-09-02T11:05:00.000Z",
    ...overrides,
  };
}

function deployment(overrides: Partial<DeployStatus> = {}): DeployStatus {
  return {
    has_snapshot: true,
    executable: true,
    in_sync: true,
    dependency_manifest_in_sync: true,
    version_id: "version-1",
    ordinal: 1,
    source_merkle: "merkle",
    snapshot_count: 1,
    ...overrides,
  };
}

describe("buildRunOverviewModel", () => {
  it("combines projections, actual durations, and manual runs without conflating them", () => {
    const manual = run({
      id: "manual-run",
      trigger: "manual",
      started_at: "2026-09-02T11:30:00.000Z",
      finished_at: "2026-09-02T11:45:00.000Z",
    });
    const model = buildRunOverviewModel({
      pipelines: [pipeline("analytics")],
      schedules: [schedule()],
      runs: [run(), manual],
      deployments: { analytics: deployment() },
      environment: "production",
      bucket: "6hr",
      density: "regular",
      now,
    });

    expect(model.timelineRows).toHaveLength(1);
    expect(model.timelineRows[0]?.runs.map((item) => item.id)).toContain("manual-run");
    expect(model.timelineRows[0]?.projections).toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "persisted" })]),
    );
    expect(model.runsToday).toBe(2);
    expect(model.nextRunAt).toBe("2026-09-02T13:00:00.000Z");
  });

  it("scopes the model to the selected pipeline and environment", () => {
    const model = buildRunOverviewModel({
      pipelines: [pipeline("analytics"), pipeline("billing")],
      schedules: [
        schedule(),
        schedule({
          pipeline_uuid: "billing-uuid",
          pipeline_id: "billing",
          pipeline_name: "billing",
        }),
        schedule({ environment: "staging" }),
      ],
      runs: [
        run(),
        run({ id: "billing-run", pipeline_id: "billing", pipeline: "billing" }),
        run({ id: "staging-run", environment: "staging" }),
      ],
      deployments: { analytics: deployment(), billing: deployment() },
      selectedPipelineId: "billing",
      environment: "production",
      bucket: "24hr",
      density: "compact",
      now,
    });

    expect(model.selectedPipeline?.id).toBe("billing");
    expect(model.timelineRows.map((row) => row.pipeline.id)).toEqual(["billing"]);
    expect(model.timelineRows[0]?.runs.map((item) => item.id)).toEqual(["billing-run"]);
  });

  it("keeps actual runs on their owning pipeline row in the workspace view", () => {
    const model = buildRunOverviewModel({
      pipelines: [pipeline("analytics"), pipeline("billing")],
      schedules: [
        schedule(),
        schedule({
          pipeline_uuid: "billing-uuid",
          pipeline_id: "billing",
          pipeline_name: "billing",
        }),
      ],
      runs: [
        run({ id: "analytics-run" }),
        run({ id: "billing-run", pipeline_id: "billing", pipeline: "billing" }),
      ],
      deployments: { analytics: deployment(), billing: deployment() },
      environment: "production",
      bucket: "24hr",
      density: "regular",
      now,
    });

    expect(
      Object.fromEntries(
        model.timelineRows.map((row) => [row.pipeline.id, row.runs.map((item) => item.id)]),
      ),
    ).toEqual({
      analytics: ["analytics-run"],
      billing: ["billing-run"],
    });
  });

  it("surfaces failed runs, unpinned schedules, and deployment drift separately", () => {
    const model = buildRunOverviewModel({
      pipelines: [pipeline("analytics")],
      schedules: [schedule({ snapshot_version_id: undefined })],
      runs: [run({ status: "failed", error: "warehouse unavailable" })],
      deployments: {
        analytics: deployment({
          in_sync: false,
          changed_files: ["assets/orders.sql"],
          version_id: "version-2",
        }),
      },
      environment: "production",
      bucket: "12hr",
      density: "regular",
      now,
    });

    expect(model.attention.map((item) => item.title)).toEqual(
      expect.arrayContaining(["analytics failed", "analytics needs a deployment"]),
    );
    expect(model.readiness).toEqual([
      expect.objectContaining({ title: "analytics has workspace changes" }),
    ]);
    expect(model.pipelines[0]).toEqual(expect.objectContaining({ health: "failed" }));
  });
});
