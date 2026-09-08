import type { DataBrowserObject } from "./generated/api-types";

export type StorageLoadDraft = {
  connection?: string;
  sourceConnection?: string;
  sourceTable?: string;
  destinationObject?: string;
};

// A reviewed authoring suggestion, never an execution command. The object was
// resolved server-side immediately before placement; asset creation still
// applies the current connection roles and read-only policy on confirmation.
export function storageLoadDraft(
  object: Pick<DataBrowserObject, "connection_name" | "reference_text" | "kind" | "capabilities">,
  upstream?: { name: string; connection: string },
): StorageLoadDraft | null {
  if (upstream) {
    if (!object.capabilities.load_destination) return null;
    const file = encodeURIComponent(`${upstream.name.split(".").at(-1) || "output"}.parquet`);
    return {
      connection: object.connection_name,
      sourceConnection: upstream.connection,
      destinationObject: object.reference_text + (object.kind === "prefix" ? file : ""),
    };
  }
  if (!object.capabilities.load_source) return null;
  return {
    sourceConnection: object.connection_name,
    sourceTable: object.reference_text + (object.kind === "prefix" ? "*" : ""),
  };
}
