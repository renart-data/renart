import { useAtom, useAtomValue } from "jotai";
import { useCallback, useEffect } from "react";
import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type { UsageAnalyticsStatus, UpdateUsageAnalyticsRequest } from "@/lib/generated/api-types";
import { workspaceConnectionSequenceAtom } from "@/lib/atoms/workspace";
import {
  usageAnalyticsStatusAtom,
  usageAnalyticsRevisionAtom,
  usageAnalyticsBusyAtom,
  usageAnalyticsErrorAtom,
  usageAnalyticsSaveErrorAtom,
} from "@/lib/atoms/domains/usage-analytics";

let pending: Promise<UsageAnalyticsStatus> | null = null;
let mutationEpoch = 0;

export function useUsageAnalytics() {
  const [data, setData] = useAtom(usageAnalyticsStatusAtom);
  const [error, setError] = useAtom(usageAnalyticsErrorAtom);
  const [saveError, setSaveError] = useAtom(usageAnalyticsSaveErrorAtom);
  const [busy, setBusy] = useAtom(usageAnalyticsBusyAtom);
  const revision = useAtomValue(usageAnalyticsRevisionAtom);
  const connection = useAtomValue(workspaceConnectionSequenceAtom);
  const reload = useCallback(async () => {
    // A settings event may arrive while an older GET is in flight. Wait for it,
    // then read again so joining that stale request cannot consume the refresh.
    if (pending) {
      try {
        await pending;
      } catch {
        // The fresh read below owns the current error state.
      }
    }
    const epoch = mutationEpoch;
    if (!pending)
      pending = fetchJSON<UsageAnalyticsStatus>("/api/telemetry", { cache: "no-store" }).finally(
        () => {
          pending = null;
        },
      );
    try {
      const result = await pending;
      if (epoch === mutationEpoch) {
        setData(result);
        setError(null);
      }
    } catch (error) {
      if (epoch === mutationEpoch)
        setError(error instanceof Error ? error.message : "Could not load usage settings.");
    }
  }, [setData, setError]);
  useEffect(() => {
    void reload();
  }, [reload, revision, connection]);
  useEffect(() => {
    const focus = () => void reload();
    window.addEventListener("focus", focus);
    return () => window.removeEventListener("focus", focus);
  }, [reload]);
  const update = async (input: UpdateUsageAnalyticsRequest) => {
    mutationEpoch++;
    setBusy(true);
    setSaveError(null);
    try {
      const result = await fetchJSONWithBody<UsageAnalyticsStatus>("/api/telemetry", "PUT", input);
      mutationEpoch++;
      setData(result);
      setError(null);
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : "Could not save usage settings.");
    } finally {
      setBusy(false);
    }
  };
  return { data, error, saveError, busy, update };
}
