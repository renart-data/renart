import { atom } from "jotai";
import type { CSSProperties } from "react";

import {
  OnboardingDiscoveryResponse,
  OnboardingImportResponse,
  OnboardingSessionState,
} from "@/lib/types";

export type OnboardingStep = "start" | "connection-type" | "connection-config" | "import" | "quickstart" | "success";

export type OnboardingImportForm = {
  database: string;
  pipelineName: string;
  schema: string;
  pattern: string;
  disableColumns: boolean;
};

export type OnboardingRect = {
  left: number;
  top: number;
  width: number;
  height: number;
};

export type OnboardingTourState = {
  quickstartStep: number | null;
  environmentStepActive: boolean;
  overlayVisible: boolean;
  spotlightSelectors: string[];
  spotlightRect: OnboardingRect | null;
  cardStyle: CSSProperties;
};

export type OnboardingState = {
  tour: OnboardingTourState;
};

export const onboardingAtom = atom<OnboardingState>({
  tour: {
    quickstartStep: null,
    environmentStepActive: false,
    overlayVisible: false,
    spotlightSelectors: [],
    spotlightRect: null,
    cardStyle: { left: 16, top: 16, width: 360 },
  },
});

export const onboardingStepAtom = atom<OnboardingStep>("start");
export const onboardingSelectedTypeAtom = atom<string>("postgres");
export const onboardingBusyAtom = atom(false);
export const onboardingDiscoveryBusyAtom = atom(false);
export const onboardingDiscoveryErrorAtom = atom<string | null>(null);
export const onboardingDiscoveryStateAtom = atom<OnboardingDiscoveryResponse>({
  status: "ok",
  databases: [],
  tables: [],
});
export const onboardingSelectedTablesAtom = atom<string[]>([]);
export const onboardingDraftValuesAtom = atom<Record<string, string | number | boolean>>({});
export const onboardingImportFormAtom = atom<OnboardingImportForm>({
  database: "",
  pipelineName: "analytics",
  schema: "",
  pattern: "",
  disableColumns: false,
});
export const onboardingImportResultAtom = atom<OnboardingImportResponse | null>(null);

export const syncOnboardingAtomsAtom = atom(
  null,
  (get, set, session: OnboardingSessionState & { selected_type_fallback?: string }) => {
    set(onboardingStepAtom, (session.step as OnboardingStep | undefined) ?? "start");
    set(onboardingSelectedTypeAtom, session.selected_type || session.selected_type_fallback || "postgres");

    const nextDraftValues = normalizeDraftValues(session.draft_values);
    if (Object.keys(nextDraftValues).length > 0 || Object.keys(get(onboardingDraftValuesAtom)).length === 0) {
      set(onboardingDraftValuesAtom, nextDraftValues);
    }

    set(onboardingSelectedTablesAtom, session.selected_tables ?? []);
    set(onboardingImportFormAtom, {
      database: session.import_form?.database ?? "",
      pipelineName: session.import_form?.pipeline_name ?? "analytics",
      schema: session.import_form?.schema ?? "",
      pattern: session.import_form?.pattern ?? "",
      disableColumns: session.import_form?.disable_columns ?? false,
    });
    set(
      onboardingImportResultAtom,
      session.import_result
        ? {
            status: session.import_result.error ? "error" : "ok",
            output: session.import_result.output,
            error: session.import_result.error,
            pipeline_path: session.import_result.pipeline_path,
            asset_paths: session.import_result.asset_paths,
          }
        : null
    );
  }
);

function normalizeDraftValues(values?: Record<string, unknown>) {
  if (!values) {
    return {};
  }

  return Object.fromEntries(
    Object.entries(values).filter(([, value]) => value !== null && value !== undefined)
  ) as Record<string, string | number | boolean>;
}
