"use client";

import AnsiToHtml from "ansi-to-html";
import { useAtom, useAtomValue, useSetAtom } from "jotai";
import { useCallback, useMemo, useRef, useState } from "react";

import {
  assetResultsAtom,
  changedAssetIdsAtom,
  enrichedSelectedAssetAtom,
  materializingAssetIdsAtom,
} from "@/lib/atoms/domains/results";
import {
  pipelineAtom,
  resolvedSelectedAssetAtom,
  selectedExecutionTimeWindowAtom,
  selectedEnvironmentAtom,
} from "@/lib/atoms/domains/workspace";
import { useAssetInspect } from "@/hooks/use-asset-inspect";
import { useSchedulerRunEvents } from "@/hooks/use-scheduler-run-events";
import { materializeAssetStream } from "@/lib/api-assets-inspect";
import { materializePipelineStream } from "@/lib/api-pipelines";
import {
  assetResultsReducer,
  createMaterializeEntry,
  deriveAssetResults,
  terminalSchedulerRun,
  type MaterializeHistoryEntry,
  type TerminalSchedulerRun,
} from "@/lib/asset-results-model";
import {
  activePipelineRunConflict,
  getRun,
  triggerPipelineRun,
  type PipelineRunSource,
} from "@/lib/api-scheduler";
import { buildStalePipelineStream } from "@/lib/api-staleness";
import type { StreamAssetEvent } from "@/lib/api-streams";
import { MaterializeScope, labelForMaterializeScope } from "@/lib/materialize-scope";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import { AssetInspectResponse, type PipelineRun, WebAsset } from "@/lib/types";

let nextMaterializeHistoryId = 0;
const maxRememberedTerminalSchedulerRuns = 128;

function rememberTerminalSchedulerRun(
  cache: Map<string, TerminalSchedulerRun>,
  terminal: TerminalSchedulerRun,
) {
  const existing = cache.get(terminal.runId);
  const remembered = {
    ...existing,
    ...terminal,
    output: terminal.output ?? existing?.output,
  };
  // Refresh insertion order so the bounded cache retains recently observed
  // terminal runs, including runs reconciled by a canonical HTTP response.
  cache.delete(terminal.runId);
  cache.set(terminal.runId, remembered);
  while (cache.size > maxRememberedTerminalSchedulerRuns) {
    const oldestRunId = cache.keys().next().value;
    if (!oldestRunId) break;
    cache.delete(oldestRunId);
  }
  return remembered;
}

function createMaterializeHistoryId() {
  nextMaterializeHistoryId += 1;
  return `materialize-${Date.now()}-${nextMaterializeHistoryId}`;
}

export function resolveScopedMaterializingAssetIds(
  assets: WebAsset[],
  assetId: string,
  scope: MaterializeScope,
) {
  const selected = assets.find((candidate) => candidate.id === assetId) ?? null;
  if (!selected || scope === "asset") {
    return [assetId];
  }

  const assetByName = new Map(assets.map((candidate) => [candidate.name, candidate]));
  const downstreamByName = new Map<string, WebAsset[]>();
  for (const candidate of assets) {
    for (const upstreamName of candidate.upstreams ?? []) {
      const downstream = downstreamByName.get(upstreamName) ?? [];
      downstream.push(candidate);
      downstreamByName.set(upstreamName, downstream);
    }
  }

  const selectedNames = new Set<string>();
  const queue = [selected.name];
  while (queue.length > 0) {
    const name = queue.shift();
    if (!name || selectedNames.has(name)) {
      continue;
    }
    selectedNames.add(name);

    const current = assetByName.get(name);
    if (!current) {
      continue;
    }

    if (scope === "asset_with_upstreams" || scope === "asset_with_upstreams_and_downstreams") {
      for (const upstreamName of current.upstreams ?? []) {
        if (assetByName.has(upstreamName)) {
          queue.push(upstreamName);
        }
      }
    }

    if (scope === "asset_with_downstreams" || scope === "asset_with_upstreams_and_downstreams") {
      for (const downstream of downstreamByName.get(name) ?? []) {
        queue.push(downstream.name);
      }
    }
  }

  const ids = assets
    .filter((candidate) => selectedNames.has(candidate.name))
    .map((candidate) => candidate.id);
  return ids.length > 0 ? ids : [assetId];
}

export function useAssetResults() {
  const [results, setResults] = useAtom(assetResultsAtom);
  const setChangedAssetIds = useSetAtom(changedAssetIdsAtom);
  const [pipelineMaterializeLoading, setPipelineMaterializeLoading] = useState(false);
  const [assetMaterializeLoading, setAssetMaterializeLoading] = useState(false);
  const [materializingAssetIds, setMaterializingAssetIds] = useAtom(materializingAssetIdsAtom);
  const asset = useAtomValue(enrichedSelectedAssetAtom);
  const pipeline = useAtomValue(pipelineAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const pipelineId = pipeline?.id ?? null;
  const selectedAssetId = useAtomValue(resolvedSelectedAssetAtom);
  const selectedExecutionTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const terminalSchedulerRuns = useRef(new Map<string, TerminalSchedulerRun>());
  const inspectAssets = useMemo(() => (asset ? [asset] : []), [asset]);
  const {
    inspectAssetById,
    inspectByAssetId,
    inspectDiagnosticSnapshotByAssetId,
    inspectLoadingByAssetId,
    canLoadMoreByAssetId,
    loadMorePreviewRows,
  } = useAssetInspect(inspectAssets);
  const { materializeHistory } = results;

  const inspectResult = selectedAssetId ? (inspectByAssetId[selectedAssetId] ?? null) : null;
  const inspectLoading = selectedAssetId
    ? (inspectLoadingByAssetId[selectedAssetId] ?? false)
    : false;
  const canLoadMoreInspectRows = selectedAssetId
    ? Boolean(canLoadMoreByAssetId[selectedAssetId])
    : false;

  const effectiveMaterializeLoading = assetMaterializeLoading || pipelineMaterializeLoading;

  const setResultTab = (tab: "inspect" | "materialize") =>
    setResults((previous) => assetResultsReducer(previous, { type: "result_tab_selected", tab }));

  const selectMaterializeEntry = (entryId: string) => {
    setResults((previous) =>
      assetResultsReducer(previous, { type: "materialize_entry_selected", entryId }),
    );
  };

  const hasInspectData = Boolean(inspectResult);
  const { selectedMaterializeEntry, hasResultData, effectiveResultTab } = deriveAssetResults(
    results,
    {
      hasInspectData,
      inspectLoading,
      materializeLoading: effectiveMaterializeLoading,
    },
  );

  const ansiConverter = useMemo(() => new AnsiToHtml({ escapeXML: true }), []);
  const materializeOutputHtml = useMemo(() => {
    const normalized = (selectedMaterializeEntry?.output ?? "").replace(/\r\n/g, "\n");
    return ansiConverter.toHtml(normalized);
  }, [ansiConverter, selectedMaterializeEntry?.output]);

  const loadMoreInspectRows = () => {
    if (selectedAssetId) {
      loadMorePreviewRows(selectedAssetId);
    }
  };

  const upsertMaterializeEntry = (
    entryId: string,
    updater: (previous: MaterializeHistoryEntry | null) => MaterializeHistoryEntry,
  ) => {
    setResults((previous) => {
      const existingEntry =
        previous.materializeHistory.find((entry) => entry.id === entryId) ?? null;
      return assetResultsReducer(previous, {
        type: "materialize_entry_upserted",
        entry: updater(existingEntry),
      });
    });
  };

  const applyTerminalSchedulerRun = useCallback(
    (terminal: TerminalSchedulerRun) => {
      setResults((previous) =>
        assetResultsReducer(previous, {
          type: "terminal_run_applied",
          terminal,
          observedAt: Date.now(),
        }),
      );
    },
    [setResults],
  );

  const reconcileTerminalSchedulerRun = useCallback(
    async (runId: string) => {
      try {
        const response = await getRun(runId);
        if (response.status !== "ok") return;
        const terminal = terminalSchedulerRun(
          response.run,
          (response.logs ?? []).map((log) => log.line).join(""),
        );
        // A non-terminal response may have been captured before a finish event.
        // It must never overwrite newer live output or a terminal cache entry.
        if (!terminal) return;
        const remembered = rememberTerminalSchedulerRun(terminalSchedulerRuns.current, terminal);
        applyTerminalSchedulerRun(remembered);
      } catch {
        // Live events still provide truthful terminal status. Association with
        // a trigger response retries this reconciliation for fast runs.
      }
    },
    [applyTerminalSchedulerRun],
  );

  useSchedulerRunEvents((schedulerRunEvent) => {
    if (schedulerRunEvent.type === "run.unit") {
      return;
    }

    const eventRunId =
      schedulerRunEvent.type === "run.log" || schedulerRunEvent.type === "run.step"
        ? schedulerRunEvent.run.run_id
        : schedulerRunEvent.run.id;
    if (schedulerRunEvent.type === "run.finished") {
      const terminal = terminalSchedulerRun(schedulerRunEvent.run);
      if (terminal) {
        const remembered = rememberTerminalSchedulerRun(terminalSchedulerRuns.current, terminal);
        applyTerminalSchedulerRun(remembered);
        // The event carries terminal state but not the canonical stored log.
        // Keep the result for later association even when this run finished
        // before its trigger response gave the history entry a run ID.
        void reconcileTerminalSchedulerRun(eventRunId);
      }
      return;
    }
    if (terminalSchedulerRuns.current.has(eventRunId)) {
      return;
    }

    setResults((previous) =>
      assetResultsReducer(previous, {
        type: "scheduler_run_observed",
        event: schedulerRunEvent,
        observedAt: Date.now(),
      }),
    );
  });

  const runInspectForAsset = useCallback(
    async (assetId: string, contentSnapshot?: string) => {
      try {
        const result = await inspectAssetById(assetId, {
          force: true,
          limit: 200,
          contentSnapshot,
          timeWindow: selectedExecutionTimeWindow ?? undefined,
        });
        if (result.rows.length > 0 || result.error) {
          setResultTab("inspect");
        }
        return result;
      } catch (error) {
        const failure: AssetInspectResponse = {
          status: "error",
          columns: [],
          rows: [],
          raw_output: "",
          operation: { type: "inspect" },
          error: String(error),
        };
        setResultTab("inspect");
        return failure;
      }
    },
    [inspectAssetById, selectedExecutionTimeWindow],
  );

  const runMaterializeForAsset = useCallback(
    async (
      assetId: string,
      scope: MaterializeScope = "asset",
      refresh?: () => Promise<void> | void,
      overrides?: {
        assetName?: string;
        timeWindow?: { start: string; end: string } | null;
        fullRefresh?: boolean;
        backfill?: boolean;
        confirmedEnvironment?: string;
      },
    ) => {
      const entryId = createMaterializeHistoryId();
      const startedAt = Date.now();
      // Resolve the asset being built rather than assuming it is the selected one:
      // the stale-build flow materializes assets other than the active selection,
      // and the entry must carry the correct name, label and time window.
      const targetAssetName =
        overrides?.assetName ??
        pipeline?.assets.find((candidate) => candidate.id === assetId)?.name ??
        asset?.name ??
        null;
      const entryTimeWindow =
        overrides?.timeWindow !== undefined ? overrides.timeWindow : selectedExecutionTimeWindow;
      const actionLabel = overrides?.backfill
        ? "Backfill"
        : overrides?.fullRefresh
          ? "Full refresh"
          : labelForMaterializeScope(scope);
      const materializeLabel = targetAssetName ? `${actionLabel}: ${targetAssetName}` : actionLabel;
      const scopedMaterializingIds = resolveScopedMaterializingAssetIds(
        pipeline?.assets ?? [],
        assetId,
        scope,
      );

      setAssetMaterializeLoading(true);
      setMaterializingAssetIds(
        (previous: Set<string>) => new Set([...previous, ...scopedMaterializingIds]),
      );
      upsertMaterializeEntry(entryId, () => ({
        ...createMaterializeEntry({
          id: entryId,
          kind: "asset",
          label: materializeLabel,
          assetId,
          assetName: targetAssetName,
          pipelineId: pipelineId ?? null,
          pipelineName: pipeline?.name ?? null,
          loading: true,
          createdAt: startedAt,
          timeWindow: entryTimeWindow,
        }),
      }));

      try {
        await awaitWorkspaceSaves();
        const result = await materializeAssetStream(
          assetId,
          {
            onChunk: (chunk) => {
              upsertMaterializeEntry(entryId, (previous) => ({
                ...(previous ??
                  createMaterializeEntry({
                    id: entryId,
                    kind: "asset",
                    label: materializeLabel,
                    assetId,
                    assetName: targetAssetName,
                    pipelineId: pipelineId ?? null,
                    pipelineName: pipeline?.name ?? null,
                    loading: true,
                    createdAt: startedAt,
                    timeWindow: entryTimeWindow,
                  })),
                output: (previous?.output ?? "") + chunk,
                loading: true,
                updatedAt: Date.now(),
              }));
            },
          },
          {
            environment: selectedEnvironment,
            scope,
            timeWindow: entryTimeWindow ?? undefined,
            fullRefresh: overrides?.fullRefresh,
            backfill: overrides?.backfill,
            confirmedEnvironment: overrides?.confirmedEnvironment,
          },
        );
        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ??
            createMaterializeEntry({
              id: entryId,
              kind: "asset",
              label: materializeLabel,
              assetId,
              assetName: targetAssetName,
              pipelineId: pipelineId ?? null,
              pipelineName: pipeline?.name ?? null,
              loading: true,
              createdAt: startedAt,
              timeWindow: entryTimeWindow,
            })),
          output: result.output ?? previous?.output ?? "",
          status: result.status ?? "error",
          error: result.error ?? "",
          warnings: result.warnings ?? [],
          loading: false,
          updatedAt: Date.now(),
        }));

        const affectedIds = result.changed_asset_ids;
        if (affectedIds && affectedIds.length > 0) {
          setChangedAssetIds((prev: Set<string>) => {
            const next = new Set(prev);
            for (const id of affectedIds) {
              next.add(id);
            }
            return next;
          });
        } else {
          setChangedAssetIds((prev: Set<string>) => new Set([...prev, assetId]));
        }

        return result;
      } catch (error) {
        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ??
            createMaterializeEntry({
              id: entryId,
              kind: "asset",
              label: materializeLabel,
              assetId,
              assetName: targetAssetName,
              pipelineId: pipelineId ?? null,
              pipelineName: pipeline?.name ?? null,
              loading: true,
              createdAt: startedAt,
              timeWindow: entryTimeWindow,
            })),
          output: (previous?.output ?? "") + (previous?.output ? "\n" : "") + String(error),
          status: "error",
          error: String(error),
          loading: false,
          updatedAt: Date.now(),
        }));
        return null;
      } finally {
        setAssetMaterializeLoading(false);
        setMaterializingAssetIds((previous: Set<string>) => {
          const next = new Set(previous);
          for (const id of scopedMaterializingIds) {
            next.delete(id);
          }
          return next;
        });
        if (refresh) {
          await refresh();
        }
      }
    },
    [
      asset?.name,
      pipeline,
      pipelineId,
      selectedEnvironment,
      selectedExecutionTimeWindow,
      setChangedAssetIds,
      setResultTab,
    ],
  );

  const runMaterializePipeline = useCallback(
    async (
      pipelineId: string,
      refresh?: () => Promise<void> | void,
      options?: {
        dryRun?: boolean;
        fullRefresh?: boolean;
        backfill?: boolean;
        confirmedEnvironment?: string;
        sensorMode?: "once" | "wait" | "skip";
        source?: PipelineRunSource;
      },
    ) => {
      const entryId = createMaterializeHistoryId();
      const startedAt = Date.now();
      const pipelineMaterializingIds = pipeline?.assets.map((current) => current.id) ?? [];
      const entryTimeWindow = options?.dryRun ? null : selectedExecutionTimeWindow;

      setPipelineMaterializeLoading(true);
      if (!options?.dryRun) {
        setMaterializingAssetIds(
          (previous: Set<string>) => new Set([...previous, ...pipelineMaterializingIds]),
        );
      }
      upsertMaterializeEntry(entryId, () => ({
        ...createMaterializeEntry({
          id: entryId,
          kind: "pipeline",
          label: pipeline?.name
            ? `${options?.dryRun ? "Dry run" : "Pipeline"}: ${pipeline.name}`
            : options?.dryRun
              ? "Pipeline dry run"
              : "Pipeline materialize",
          pipelineId,
          pipelineName: pipeline?.name ?? null,
          loading: true,
          createdAt: startedAt,
          timeWindow: entryTimeWindow,
        }),
      }));

      try {
        await awaitWorkspaceSaves();
        if (!options?.dryRun) {
          if (!options?.source) {
            throw new Error("A pipeline run source is required.");
          }
          const response = await triggerPipelineRun(pipelineId, {
            ...options.source,
            environment: selectedEnvironment,
            start: selectedExecutionTimeWindow?.start,
            end: selectedExecutionTimeWindow?.end,
            full_refresh: options?.fullRefresh,
            backfill: options?.backfill,
            confirmed_environment: options?.confirmedEnvironment,
            sensor_mode: options?.sensorMode,
          });
          const run = response.run;
          const responseTerminal = terminalSchedulerRun(run);
          const rememberedTerminal = responseTerminal
            ? rememberTerminalSchedulerRun(terminalSchedulerRuns.current, responseTerminal)
            : terminalSchedulerRuns.current.get(run.id);
          upsertMaterializeEntry(entryId, (previous) => ({
            ...(previous ??
              createMaterializeEntry({
                id: entryId,
                kind: "pipeline",
                label: pipeline?.name ? `Pipeline: ${pipeline.name}` : "Pipeline materialize",
                pipelineId,
                pipelineName: pipeline?.name ?? null,
                runId: run.id,
                loading: true,
                createdAt: startedAt,
                timeWindow: entryTimeWindow,
              })),
            runId: run.id,
            output: rememberedTerminal?.output ?? previous?.output ?? "",
            status: rememberedTerminal?.status ?? null,
            error: rememberedTerminal?.error ?? "",
            loading: !rememberedTerminal,
            updatedAt: Date.now(),
          }));
          // Usually this observes a queued/running run and becomes a no-op. For
          // a run that completed before trigger correlation, it supplies the
          // final stored logs even if the earlier finish-event request failed.
          void reconcileTerminalSchedulerRun(run.id);
          return { status: "ok", output: "", error: "", changed_asset_ids: [] };
        }

        const result = await materializePipelineStream(
          pipelineId,
          {
            onChunk: (chunk) => {
              upsertMaterializeEntry(entryId, (previous) => ({
                ...(previous ??
                  createMaterializeEntry({
                    id: entryId,
                    kind: "pipeline",
                    label: pipeline?.name
                      ? `${options?.dryRun ? "Dry run" : "Pipeline"}: ${pipeline.name}`
                      : options?.dryRun
                        ? "Pipeline dry run"
                        : "Pipeline materialize",
                    pipelineId,
                    pipelineName: pipeline?.name ?? null,
                    loading: true,
                    createdAt: startedAt,
                    timeWindow: entryTimeWindow,
                  })),
                output: (previous?.output ?? "") + chunk,
                loading: true,
                updatedAt: Date.now(),
              }));
            },
          },
          {
            environment: selectedEnvironment,
            // Deliberately omit timeWindow: the validation-only executor does
            // not consume it, and the server rejects unsupported dry-run context.
            dryRun: options?.dryRun,
          },
        );

        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ??
            createMaterializeEntry({
              id: entryId,
              kind: "pipeline",
              label: pipeline?.name
                ? `${options?.dryRun ? "Dry run" : "Pipeline"}: ${pipeline.name}`
                : options?.dryRun
                  ? "Pipeline dry run"
                  : "Pipeline materialize",
              pipelineId,
              pipelineName: pipeline?.name ?? null,
              loading: true,
              createdAt: startedAt,
              timeWindow: entryTimeWindow,
            })),
          output: result.output ?? previous?.output ?? "",
          status: result.status ?? "error",
          error: result.error ?? "",
          warnings: result.warnings ?? [],
          loading: false,
          updatedAt: Date.now(),
        }));

        const affectedIds = result.changed_asset_ids ?? [];
        if (affectedIds.length > 0) {
          setChangedAssetIds((prev: Set<string>) => {
            const next = new Set(prev);
            for (const id of affectedIds) {
              next.add(id);
            }
            return next;
          });
        }

        return result;
      } catch (error) {
        const conflict = activePipelineRunConflict(error);
        if (conflict) {
          upsertMaterializeEntry(entryId, (previous) => ({
            ...(previous ??
              createMaterializeEntry({
                id: entryId,
                kind: "pipeline",
                label: pipeline?.name ? `Pipeline: ${pipeline.name}` : "Pipeline materialize",
                pipelineId,
                pipelineName: pipeline?.name ?? null,
                createdAt: startedAt,
                timeWindow: entryTimeWindow,
              })),
            runId: conflict.activeRunId,
            output: `Run ${conflict.activeRunId} is already queued or running for this pipeline. Open the active run to follow its progress.`,
            status: null,
            error: "",
            loading: false,
            updatedAt: Date.now(),
          }));
          return null;
        }
        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ??
            createMaterializeEntry({
              id: entryId,
              kind: "pipeline",
              label: pipeline?.name
                ? `${options?.dryRun ? "Dry run" : "Pipeline"}: ${pipeline.name}`
                : options?.dryRun
                  ? "Pipeline dry run"
                  : "Pipeline materialize",
              pipelineId,
              pipelineName: pipeline?.name ?? null,
              loading: true,
              createdAt: startedAt,
              timeWindow: entryTimeWindow,
            })),
          output: (previous?.output ?? "") + (previous?.output ? "\n" : "") + String(error),
          status: "error",
          error: String(error),
          loading: false,
          updatedAt: Date.now(),
        }));
        return null;
      } finally {
        setPipelineMaterializeLoading(false);
        setMaterializingAssetIds((previous: Set<string>) => {
          const next = new Set(previous);
          for (const id of pipelineMaterializingIds) {
            next.delete(id);
          }
          return next;
        });
        if (!options?.dryRun && refresh) {
          await refresh();
        }
      }
    },
    [
      pipeline,
      reconcileTerminalSchedulerRun,
      selectedEnvironment,
      selectedExecutionTimeWindow,
      setChangedAssetIds,
    ],
  );

  const trackConfirmedPipelineRun = useCallback(
    (run: PipelineRun, timeWindow?: { start: string; end: string } | null) => {
      const entryId = createMaterializeHistoryId();
      const startedAt = Date.now();
      const responseTerminal = terminalSchedulerRun(run);
      const rememberedTerminal = responseTerminal
        ? rememberTerminalSchedulerRun(terminalSchedulerRuns.current, responseTerminal)
        : terminalSchedulerRuns.current.get(run.id);
      upsertMaterializeEntry(entryId, () =>
        createMaterializeEntry({
          id: entryId,
          kind: "pipeline",
          label: pipeline?.name ? `Pipeline: ${pipeline.name}` : "Pipeline materialize",
          pipelineId: run.pipeline_id || pipelineId,
          pipelineName: run.pipeline || pipeline?.name || null,
          runId: run.id,
          output: rememberedTerminal?.output ?? "",
          status: rememberedTerminal?.status ?? null,
          error: rememberedTerminal?.error ?? "",
          loading: !rememberedTerminal,
          createdAt: startedAt,
          timeWindow: timeWindow ?? selectedExecutionTimeWindow,
        }),
      );
      void reconcileTerminalSchedulerRun(run.id);
      return entryId;
    },
    [pipeline, pipelineId, reconcileTerminalSchedulerRun, selectedExecutionTimeWindow],
  );

  // runBuildStale delegates the whole "build stale assets" operation to the
  // server: one streamed run in dependency order, one history entry, one log.
  const runBuildStale = useCallback(
    async (
      targetPipelineId: string,
      options?: {
        assetIds?: string[];
        onAssetEvent?: (event: StreamAssetEvent) => void;
      },
    ) => {
      const entryId = createMaterializeHistoryId();
      const startedAt = Date.now();
      const label = pipeline?.name ? `Build stale: ${pipeline.name}` : "Build stale assets";
      const staleMaterializingIds = options?.assetIds ?? [];
      const baseEntry = () =>
        createMaterializeEntry({
          id: entryId,
          kind: "batch",
          label,
          pipelineId: targetPipelineId,
          pipelineName: pipeline?.name ?? null,
          loading: true,
          createdAt: startedAt,
          timeWindow: selectedExecutionTimeWindow,
        });

      setPipelineMaterializeLoading(true);
      setMaterializingAssetIds(
        (previous: Set<string>) => new Set([...previous, ...staleMaterializingIds]),
      );
      upsertMaterializeEntry(entryId, () => ({ ...baseEntry() }));

      try {
        await awaitWorkspaceSaves();
        if (!selectedEnvironment) {
          throw new Error("Select an environment before building stale assets.");
        }
        const result = await buildStalePipelineStream(
          targetPipelineId,
          {
            onChunk: (chunk) => {
              upsertMaterializeEntry(entryId, (previous) => ({
                ...(previous ?? baseEntry()),
                output: (previous?.output ?? "") + chunk,
                loading: true,
                updatedAt: Date.now(),
              }));
            },
            onAssetEvent: options?.onAssetEvent,
          },
          {
            environment: selectedEnvironment,
            start: selectedExecutionTimeWindow?.start,
            end: selectedExecutionTimeWindow?.end,
          },
        );

        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ?? baseEntry()),
          output: result.output ?? previous?.output ?? "",
          status: result.status ?? "error",
          error: result.error ?? "",
          warnings: result.warnings ?? [],
          loading: false,
          updatedAt: Date.now(),
        }));

        const affectedIds = result.changed_asset_ids ?? [];
        if (affectedIds.length > 0) {
          setChangedAssetIds((prev: Set<string>) => {
            const next = new Set(prev);
            for (const id of affectedIds) {
              next.add(id);
            }
            return next;
          });
        }

        return result;
      } catch (error) {
        upsertMaterializeEntry(entryId, (previous) => ({
          ...(previous ?? baseEntry()),
          output: (previous?.output ?? "") + (previous?.output ? "\n" : "") + String(error),
          status: "error",
          error: String(error),
          loading: false,
          updatedAt: Date.now(),
        }));
        return null;
      } finally {
        setPipelineMaterializeLoading(false);
        setMaterializingAssetIds((previous: Set<string>) => {
          const next = new Set(previous);
          for (const id of staleMaterializingIds) {
            next.delete(id);
          }
          return next;
        });
      }
    },
    [pipeline, selectedEnvironment, selectedExecutionTimeWindow, setChangedAssetIds],
  );

  const setMaterializeBatchResult = (
    output: string,
    status: "ok" | "error",
    errorMessage: string,
  ) => {
    const entryId = createMaterializeHistoryId();
    const now = Date.now();

    setResults((previous) =>
      assetResultsReducer(previous, {
        type: "materialize_entry_prepended",
        entry: createMaterializeEntry({
          id: entryId,
          kind: "batch",
          label: "Tutorial materialize",
          assetId: selectedAssetId,
          assetName: asset?.name ?? null,
          pipelineId: pipelineId ?? null,
          pipelineName: pipeline?.name ?? null,
          output,
          status,
          error: errorMessage,
          loading: false,
          createdAt: now,
        }),
      }),
    );
  };

  const clearResultsAfterDelete = () => {
    setResults((previous) =>
      assetResultsReducer(previous, {
        type: "asset_results_removed",
        assetId: selectedAssetId,
      }),
    );
  };

  return {
    inspectResult,
    inspectDiagnosticSnapshotByAssetId,
    inspectLoadingByAssetId,
    inspectLoading,
    materializeLoading: effectiveMaterializeLoading,
    materializingAssetIds,
    pipelineMaterializeLoading,
    hasInspectData,
    hasMaterializeData: true,
    hasResultData,
    effectiveResultTab,
    materializeOutputHtml,
    selectedMaterializeEntry,
    materializeHistory,
    canLoadMoreInspectRows,
    loadMoreInspectRows,
    setResultTab,
    selectMaterializeEntry,
    runInspectForAsset,
    runMaterializeForAsset,
    runMaterializePipeline,
    trackConfirmedPipelineRun,
    runBuildStale,
    setMaterializeBatchResult,
    clearResultsAfterDelete,
  };
}
