import { atom } from "jotai";

import { InspectDiagnosticSnapshot } from "@/lib/inspect-diagnostics";
import { AssetInspectResponse } from "@/lib/types";

export type AssetInspectDiagnosticSnapshot = InspectDiagnosticSnapshot;

export type AssetInspectEntry = {
  result: AssetInspectResponse;
  fetchedLimit: number;
  diagnosticSnapshot?: AssetInspectDiagnosticSnapshot;
};

export type AssetInspectState = {
  scope?: string;
  byAssetId: Record<string, AssetInspectEntry>;
  loadingByAssetId: Record<string, boolean>;
  requestedLimitsByAssetId: Record<string, number>;
};

export const assetInspectAtom = atom<AssetInspectState>({
  byAssetId: {},
  loadingByAssetId: {},
  requestedLimitsByAssetId: {},
});

export const emptyAssetInspectState: AssetInspectState = {
  byAssetId: {},
  loadingByAssetId: {},
  requestedLimitsByAssetId: {},
};
