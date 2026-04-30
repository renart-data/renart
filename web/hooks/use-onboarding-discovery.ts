"use client";

import { useCallback, useEffect } from "react";
import { useNavigate } from "@tanstack/react-router";

import { previewOnboardingDiscovery, testWorkspaceConnection, updateOnboardingState } from "@/lib/api";
import { OnboardingDiscoveryResponse, OnboardingImportFormState } from "@/lib/types";

type Params = {
  step: string;
  selectedType: string;
  connectionName: string;
  defaultEnvironment: string;
  draftValues: Record<string, string | number | boolean>;
  importForm: OnboardingImportFormState & {
    pipelineName: string;
    disableColumns: boolean;
  };
  selectedTables: string[];
  discoveryBusy: boolean;
  discoveryState: OnboardingDiscoveryResponse;
  setDiscoveryBusy: (value: boolean) => void;
  setDiscoveryError: (value: string | null) => void;
  setDiscoveryState: (value: OnboardingDiscoveryResponse) => void;
  setDraftValues: (value: Record<string, string | number | boolean>) => void;
  setImportForm: (value: { database: string; pipelineName: string; schema: string; pattern: string; disableColumns: boolean } | ((current: { database: string; pipelineName: string; schema: string; pattern: string; disableColumns: boolean }) => { database: string; pipelineName: string; schema: string; pattern: string; disableColumns: boolean })) => void;
  setSelectedTables: (value: string[]) => void;
  setStep: (value: "start" | "connection-type" | "connection-config" | "import" | "quickstart" | "success") => void;
};

export function useOnboardingDiscovery({
  step,
  selectedType,
  connectionName,
  defaultEnvironment,
  draftValues,
  importForm,
  selectedTables,
  discoveryBusy,
  discoveryState,
  setDiscoveryBusy,
  setDiscoveryError,
  setDiscoveryState,
  setDraftValues,
  setImportForm,
  setSelectedTables,
  setStep,
}: Params) {
  const navigate = useNavigate();

  useEffect(() => {
    if (step !== "import" || discoveryBusy || discoveryState.databases.length > 0) {
      return;
    }

    const loadSavedDiscovery = async () => {
      setDiscoveryBusy(true);
      setDiscoveryError(null);
      try {
        const resumeDatabase =
          selectedType === "duckdb"
            ? importForm.database
            : selectedTables.length > 0
              ? importForm.database
              : undefined;

        const response = await previewOnboardingDiscovery({
          environment_name: defaultEnvironment,
          type: selectedType,
          values: draftValues,
          database: resumeDatabase,
        });
        setDiscoveryState(response);
        if ((response.tables ?? []).length > 0 && selectedTables.length === 0) {
          setSelectedTables(response.tables.map((table) => table.name));
        }
      } catch (error) {
        setDiscoveryError(
          error instanceof Error ? error.message : "Failed to inspect available objects."
        );
      } finally {
        setDiscoveryBusy(false);
      }
    };

    void loadSavedDiscovery();
  }, [defaultEnvironment, discoveryBusy, discoveryState.databases.length, draftValues, importForm.database, selectedTables.length, selectedType, setDiscoveryBusy, setDiscoveryError, setDiscoveryState, setSelectedTables, step]);

  const runDiscovery = useCallback(
    async (values: Record<string, string | number | boolean>, database?: string) => {
      setDiscoveryBusy(true);
      setDiscoveryError(null);
      try {
        await testWorkspaceConnection({
          environment_name: defaultEnvironment,
          name: connectionName,
          type: selectedType,
          values,
        });

        const response = await previewOnboardingDiscovery({
          environment_name: defaultEnvironment,
          type: selectedType,
          values,
          database,
        });

        const nextDatabase =
          selectedType === "duckdb"
            ? response.selected_database ?? database ?? importForm.database ?? ""
            : database ?? importForm.database ?? "";
        const nextSchema =
          importForm.schema || response.tables.find((table) => table.schema_name)?.schema_name || "";
        const nextSelectedTables = (response.tables ?? []).map((table) => table.name);

        setDiscoveryState(response);
        setDraftValues(values);
        setImportForm((current) => ({
          ...current,
          database: nextDatabase,
          schema: nextSchema,
        }));
        setSelectedTables(nextSelectedTables);
        setStep("import");

        await updateOnboardingState({
          active: true,
          step: "import",
          selected_type: selectedType,
          environment_name: defaultEnvironment,
          draft_values: values,
          import_form: {
            database: nextDatabase,
            pipeline_name: importForm.pipelineName,
            schema: nextSchema,
            pattern: importForm.pattern,
            disable_columns: importForm.disableColumns,
          },
          selected_tables: nextSelectedTables,
          import_result: null,
        });
        void navigate({
          to: "/onboarding/import",
          replace: true,
        });
      } catch (error) {
        setDiscoveryState({ status: "ok", databases: [], tables: [] });
        setDiscoveryError(error instanceof Error ? error.message : "Connection validation failed.");
      } finally {
        setDiscoveryBusy(false);
      }
    },
    [connectionName, defaultEnvironment, importForm.database, importForm.disableColumns, importForm.pattern, importForm.pipelineName, importForm.schema, navigate, selectedType, setDiscoveryBusy, setDiscoveryError, setDiscoveryState, setDraftValues, setImportForm, setSelectedTables, setStep]
  );

  return { runDiscovery };
}
