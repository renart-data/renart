import { buildQueryString, fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type {
  DataBrowserChildrenResponse,
  DataBrowserConnectionsResponse,
  DataBrowserObjectResponse,
  DataBrowserPreviewRequest,
  DataBrowserPreviewResponse,
} from "@/lib/generated/api-types";

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

export function previewDataBrowserObject(request: DataBrowserPreviewRequest) {
  return fetchJSONWithBody<DataBrowserPreviewResponse>(
    "/api/data-browser/preview",
    "POST",
    request,
    { cache: "no-store" },
  );
}
