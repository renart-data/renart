import { useCallback, useEffect, useRef, useState } from "react";
import { APIError } from "@/lib/api-core";
import type { PreviewMetadata } from "@/lib/generated/api-types";
import { PreviewRequests } from "@/lib/preview";

// A display adapter, not an execution owner/cache. The caller supplies the
// authoritative base result and a separate, safe continuation endpoint.
export function useResultPreview<T extends { preview?: PreviewMetadata }>(
  key: string,
  base: T | null,
  fetch: (limit: number, signal: AbortSignal) => Promise<T>,
) {
  const requests = useRef(new PreviewRequests<T>()).current;
  const currentKey = useRef(key);
  currentKey.current = key;
  const [state, setState] = useState<{
    key: string;
    result?: T;
    loading?: boolean;
    error?: string;
    expired?: boolean;
  }>({ key });
  const current = state.key === key ? state : { key };
  const result = current.result ?? base;
  const metadata = result?.preview;
  const canLoadMore = !current.expired && Boolean(metadata?.next_limit);
  useEffect(() => () => requests.cancel(key), [key, requests]);
  const loadMore = useCallback(async () => {
    if (!canLoadMore || !metadata?.next_limit) return;
    setState((old) => ({ ...(old.key === key ? old : { key }), loading: true, error: undefined }));
    try {
      const response = await requests.run(key, metadata.next_limit, fetch);
      if (response && currentKey.current === key) setState({ key, result: response.value });
    } catch (error) {
      if (currentKey.current === key) {
        setState((old) => ({
          ...old,
          key,
          loading: false,
          error: error instanceof Error ? error.message : String(error),
          expired: error instanceof APIError && error.code === "notebook_preview_expired",
        }));
      }
    }
  }, [canLoadMore, fetch, key, metadata?.next_limit, requests]);
  const preview =
    current.expired && metadata
      ? { ...metadata, continuation: "none", reason: "expired", next_limit: 0 }
      : metadata;
  return {
    result,
    preview,
    loading: Boolean(current.loading),
    error: current.error,
    canLoadMore,
    loadMore,
  };
}
