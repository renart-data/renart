import { atom } from "jotai";

export const DATA_BROWSER_MIME = "application/x-renart-data-browser";

// Disposable same-window interaction state. Only the nonce enters DataTransfer:
// no credentials, SQL, file paths, or reusable cross-project authority.
export type DataBrowserTransfer = {
  kind: "table" | "connection";
  id: string;
  label: string;
  token: string;
  pipelineId: string;
  projectId: string | null;
  environment: string;
  method: "drag" | "choose";
};

export const dataBrowserTransferAtom = atom<DataBrowserTransfer | null>(null);

export function acceptsDataBrowserTransfer(
  transfer: DataBrowserTransfer | null,
  pipelineId: string | undefined,
  projectId: string | null,
  environment: string,
): transfer is DataBrowserTransfer {
  return Boolean(
    transfer &&
    pipelineId &&
    transfer.pipelineId === pipelineId &&
    transfer.projectId === projectId &&
    transfer.environment === environment,
  );
}

export function matchesDataBrowserTransfer(transfer: DataBrowserTransfer | null, token: string) {
  return Boolean(transfer?.method === "drag" && token && transfer.token === token);
}

export function canLoadDataBrowserConnection(
  asset: { connection?: string; readOnly?: boolean; kind: string },
  destination: string,
  sources: string[],
  destinations: string[],
) {
  return Boolean(
    !asset.readOnly &&
    !["sensor", "unittest", "asset"].includes(asset.kind) &&
    asset.connection &&
    asset.connection !== destination &&
    sources.includes(asset.connection) &&
    destinations.includes(destination),
  );
}
