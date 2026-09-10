import { useEffect, useState } from "react";
import { getDataBrowserChildren, getDataBrowserPrefix } from "@/lib/api-data-browser";
import {
  planDataBrowserSearch,
  searchRequestKey,
  type BrowserSearchBase,
  type BrowserSearchRequest,
} from "@/lib/data-browser-search";
import type { DataBrowserChildrenResponse, DataBrowserConnection } from "@/lib/generated/api-types";
import { getPinnedProjectId } from "@/lib/project-context";
import { APIError } from "@/lib/api-core";

// Disposable metadata only, bounded to this mounted browser and revision scope.
// Complete listings are shared across leaf edits. Capped S3 listings include a
// literal name prefix in the key; obsolete refinements are debounced/aborted.
export function useDataBrowserSearch(
  query: string,
  connections: DataBrowserConnection[],
  base: BrowserSearchBase | undefined,
  environment: string,
) {
  const scope = JSON.stringify([getPinnedProjectId(), environment, connections.map((c) => c.id)]);
  const [cache, setCache] = useState({
    scope,
    entries: new Map<string, DataBrowserChildrenResponse>(),
  });
  const [failure, setFailure] = useState<{
    scope: string;
    key: string;
    message: string;
    stale: boolean;
  }>();
  const [revision, setRevision] = useState(0);
  const plan = planDataBrowserSearch(
    query,
    connections,
    base,
    cache.scope === scope ? cache.entries : new Map(),
  );
  const key = query && plan.request ? searchRequestKey(plan.request) : undefined;
  const error =
    plan.error ?? (failure?.scope === scope && failure.key === key ? failure.message : undefined);

  useEffect(() => {
    if (!key) return;
    const controller = new AbortController();
    const request = JSON.parse(key) as BrowserSearchRequest;
    const timer = window.setTimeout(() => {
      const result =
        request.prefix === undefined
          ? getDataBrowserChildren({ ...request, environment }, controller.signal)
          : getDataBrowserPrefix(
              {
                connectionId: request.connectionId,
                prefix: request.prefix,
                namePrefix: request.namePrefix,
                pattern: request.pattern,
                environment,
              },
              controller.signal,
            );
      void result
        .then((response) => {
          if (controller.signal.aborted) return;
          setCache((current) => {
            const entries = new Map(current.scope === scope ? current.entries : []);
            entries.set(key, response);
            if (entries.size > 32) entries.delete(entries.keys().next().value!);
            return { scope, entries };
          });
        })
        .catch((cause) => {
          if (!controller.signal.aborted)
            setFailure({
              scope,
              key,
              message: cause instanceof Error ? cause.message : "Could not browse this path.",
              stale: cause instanceof APIError && cause.code === "data_browser_revision_stale",
            });
        });
    }, 180);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [key, scope, environment, revision]);

  return {
    ...plan,
    error,
    stale: failure?.scope === scope && failure.key === key && failure.stale,
    loading: Boolean(key && !error),
    refresh: () => {
      setCache({ scope, entries: new Map() });
      setFailure(undefined);
      setRevision((value) => value + 1);
    },
  };
}
