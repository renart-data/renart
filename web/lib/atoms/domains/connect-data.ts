import { atom } from "jotai";
import { atomWithStorage, createJSONStorage } from "jotai/utils";

// Opens the in-project "Connect your data" sheet. Kept apart from the sheet so
// pages that only open it don't load the connect flow.
export const openConnectDataAtom = atom(false);

// What the last import created, so the pipeline canvas can offer a next step.
// Kept for the tab in session storage: an import from the welcome screen into
// a new project reaches the canvas through a document load.
export type ConnectDataHandoff = {
  pipelineId: string;
  connectionName: string;
  tables: string[];
};
export const connectDataHandoffAtom = atomWithStorage<ConnectDataHandoff | null>(
  "renart.connect-data.handoff",
  null,
  createJSONStorage(() => window.sessionStorage),
  { getOnInit: true },
);
