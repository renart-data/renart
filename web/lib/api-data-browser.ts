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

export function getDataBrowserConnections(environment?: string) {
  return fetchJSON<DataBrowserConnectionsResponse>(
    `/api/data-browser/connections${buildQueryString({ environment })}`,
    { cache: "no-store" },
  );
}

export function getDataBrowserChildren(options: {
  connectionId: string;
  parentId?: string;
  environment?: string;
}) {
  return fetchJSON<DataBrowserChildrenResponse>(
    `/api/data-browser/connections/${encodeURIComponent(options.connectionId)}/children${buildQueryString(
      {
        parent_id: options.parentId,
        environment: options.environment,
      },
    )}`,
    { cache: "no-store" },
  );
}

export function getDataBrowserObject(options: { objectId: string; environment?: string }) {
  return fetchJSON<DataBrowserObjectResponse>(
    `/api/data-browser/objects/${encodeURIComponent(options.objectId)}${buildQueryString({
      environment: options.environment,
    })}`,
    { cache: "no-store" },
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
