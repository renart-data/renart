import { buildQueryString, fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type {
  DataBrowserChildrenResponse,
  DataBrowserConnectionsResponse,
  DataBrowserObjectResponse,
  DataBrowserPreviewRequest,
  DataBrowserPreviewResponse,
  DataBrowserResolveRequest,
  DataBrowserSourceRequest,
} from "@/lib/generated/api-types";
import type { ExternalRelationImportResult } from "@/lib/api-pipelines";

export function createDataBrowserSource(
  pipelineId: string,
  request: DataBrowserSourceRequest,
  preview = false,
) {
  return fetchJSONWithBody<ExternalRelationImportResult>(
    `/api/pipelines/${encodeURIComponent(pipelineId)}/data-browser/sources${preview ? "/preview" : ""}`,
    "POST",
    request,
  );
}

// Metadata discovery must settle even when a restored connection is offline.
// Cancellation is caller-owned; the deadline is local to this request and never
// applies to running/materializing a pipeline.
async function fetchMetadata<T>(path: string, signal?: AbortSignal): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const cancel = () => controller.abort(signal?.reason);
  if (signal?.aborted) cancel();
  else signal?.addEventListener("abort", cancel, { once: true });
  const timeout = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 30_000);
  try {
    return await fetchJSON<T>(path, { cache: "no-store", signal: controller.signal });
  } catch (cause) {
    if (timedOut)
      throw new Error(
        "Data source discovery timed out. Check the connection and its project configuration, then Retry.",
        { cause },
      );
    throw cause;
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", cancel);
  }
}

export function getDataBrowserConnections(environment?: string, signal?: AbortSignal) {
  return fetchMetadata<DataBrowserConnectionsResponse>(
    `/api/data-browser/connections${buildQueryString({ environment })}`,
    signal,
  );
}

export function getDataBrowserChildren(
  options: {
    connectionId: string;
    parentId?: string;
    environment?: string;
  },
  signal?: AbortSignal,
) {
  return fetchMetadata<DataBrowserChildrenResponse>(
    `/api/data-browser/connections/${encodeURIComponent(options.connectionId)}/children${buildQueryString(
      {
        parent_id: options.parentId,
        environment: options.environment,
      },
    )}`,
    signal,
  );
}

export function getDataBrowserPrefix(
  options: { connectionId: string; prefix: string; namePrefix?: string; environment: string },
  signal?: AbortSignal,
) {
  return fetchMetadata<DataBrowserChildrenResponse>(
    `/api/data-browser/connections/${encodeURIComponent(options.connectionId)}/prefix${buildQueryString({ path: options.prefix, name_prefix: options.namePrefix, environment: options.environment })}`,
    signal,
  );
}

export function getDataBrowserObject(
  options: { objectId: string; environment?: string },
  signal?: AbortSignal,
) {
  return fetchJSON<DataBrowserObjectResponse>(
    `/api/data-browser/objects/${encodeURIComponent(options.objectId)}${buildQueryString({
      environment: options.environment,
    })}`,
    { cache: "no-store", signal },
  );
}

export function resolveDataBrowserObject(request: DataBrowserResolveRequest, signal?: AbortSignal) {
  return fetchJSONWithBody<DataBrowserObjectResponse>(
    "/api/data-browser/resolve",
    "POST",
    request,
    { cache: "no-store", signal },
  );
}

export function previewDataBrowserObject(request: DataBrowserPreviewRequest, signal?: AbortSignal) {
  return fetchJSONWithBody<DataBrowserPreviewResponse>(
    "/api/data-browser/preview",
    "POST",
    request,
    { cache: "no-store", signal },
  );
}
