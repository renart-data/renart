"use client";

import { Dispatch, SetStateAction, useEffect, useMemo, useState } from "react";

import {
  buildConnectionFieldDefaults,
  buildConnectionSecretChanges,
  connectionSecretsReady,
  findConnectionByName,
  findEnvironmentByName,
  getFallbackEnvironmentName,
  getSelectedConnectionNameFromEnvironment,
} from "@/lib/settings-form-utils";
import {
  WorkspaceConfigConnectionType,
  WorkspaceConfigEnvironment,
  WorkspaceConfigResponse,
  WorkspaceConnectionSecretChanges,
} from "@/lib/types";

export type ConnectionMode = "edit" | "create";

export type ConnectionFormState = {
  environmentName: string;
  name: string;
  type: string;
  values: Record<string, string | number | boolean | string[]>;
  secretChanges: WorkspaceConnectionSecretChanges;
};

export function useWorkspaceConnectionForm({
  connectionTypes,
  defaultEnvironment,
  environments,
  mode,
  onCreateConnection,
  onDeleteConnection,
  onModeChange,
  onSelectedConnectionChange,
  onSelectedEnvironmentChange,
  onUpdateConnection,
  requestedConnectionType,
  requestedConnectionName,
  selectedConnectionName,
  selectedEnvironmentName,
}: {
  connectionTypes: WorkspaceConfigConnectionType[];
  defaultEnvironment?: string;
  environments: WorkspaceConfigEnvironment[];
  mode: ConnectionMode;
  onCreateConnection: (input: {
    environment_name: string;
    name: string;
    type: string;
    values: Record<string, unknown>;
    secret_changes?: WorkspaceConnectionSecretChanges;
  }) => Promise<WorkspaceConfigResponse>;
  onDeleteConnection: (input: {
    environment_name: string;
    name: string;
  }) => Promise<WorkspaceConfigResponse>;
  onModeChange: (mode: ConnectionMode) => void;
  onSelectedConnectionChange: (name: string | null) => void;
  onSelectedEnvironmentChange: (name: string | null) => void;
  onUpdateConnection: (input: {
    environment_name: string;
    current_name?: string;
    name: string;
    type: string;
    values: Record<string, unknown>;
    secret_changes?: WorkspaceConnectionSecretChanges;
  }) => Promise<WorkspaceConfigResponse>;
  requestedConnectionType?: string;
  requestedConnectionName?: string;
  selectedConnectionName?: string | null;
  selectedEnvironmentName?: string | null;
}) {
  const [connectionForm, setConnectionForm] = useState<ConnectionFormState>({
    environmentName: "",
    name: "",
    type: "",
    values: {},
    secretChanges: {},
  });

  const activeEnvironment = useMemo(
    () => findEnvironmentByName(environments, selectedEnvironmentName),
    [environments, selectedEnvironmentName],
  );

  const activeConnection = useMemo(
    () => findConnectionByName(activeEnvironment, selectedConnectionName),
    [activeEnvironment, selectedConnectionName],
  );

  const selectedConnectionType = useMemo(
    () =>
      connectionTypes.find((connectionType) => connectionType.type_name === connectionForm.type) ??
      null,
    [connectionForm.type, connectionTypes],
  );

  useEffect(() => {
    if (mode === "create") {
      const requestedType = requestedConnectionType?.trim() ?? "";
      const fallbackType =
        connectionTypes.find((connectionType) => connectionType.type_name === requestedType)
          ?.type_name ??
        connectionTypes[0]?.type_name ??
        "";
      setConnectionForm({
        environmentName: getFallbackEnvironmentName({
          defaultEnvironment,
          environments,
          selectedEnvironmentName,
        }),
        name: requestedConnectionName?.trim() ?? "",
        type: fallbackType,
        values: buildConnectionFieldDefaults({
          connectionTypes,
          typeName: fallbackType,
          existingConnection: null,
        }),
        secretChanges: buildConnectionSecretChanges(
          connectionTypes.find((connectionType) => connectionType.type_name === fallbackType),
        ),
      });
      return;
    }

    if (!activeConnection || !activeEnvironment) {
      setConnectionForm({
        environmentName: selectedEnvironmentName ?? "",
        name: "",
        type: connectionTypes[0]?.type_name ?? "",
        values: buildConnectionFieldDefaults({
          connectionTypes,
          typeName: connectionTypes[0]?.type_name ?? "",
          existingConnection: null,
        }),
        secretChanges: buildConnectionSecretChanges(connectionTypes[0]),
      });
      return;
    }

    setConnectionForm({
      environmentName: activeEnvironment.name,
      name: activeConnection.name,
      type: activeConnection.type,
      values: buildConnectionFieldDefaults({
        connectionTypes,
        typeName: activeConnection.type,
        existingConnection: activeConnection,
      }),
      secretChanges: buildConnectionSecretChanges(
        connectionTypes.find(
          (connectionType) => connectionType.type_name === activeConnection.type,
        ),
      ),
    });
  }, [
    activeConnection,
    activeEnvironment,
    connectionTypes,
    defaultEnvironment,
    environments,
    mode,
    requestedConnectionType,
    requestedConnectionName,
    selectedEnvironmentName,
  ]);

  const handleSave = async () => {
    const payload = {
      environment_name: connectionForm.environmentName,
      name: connectionForm.name.trim(),
      type: connectionForm.type,
      values: connectionForm.values,
      secret_changes: connectionForm.secretChanges,
    };

    if (mode === "create") {
      const response = await onCreateConnection(payload);
      const environment = findEnvironmentByName(
        response.environments,
        connectionForm.environmentName,
      );
      onSelectedEnvironmentChange(connectionForm.environmentName);
      onSelectedConnectionChange(
        getSelectedConnectionNameFromEnvironment(environment, connectionForm.name.trim()),
      );
      onModeChange("edit");
      return;
    }

    const response = await onUpdateConnection({
      ...payload,
      current_name: activeConnection?.name,
    });
    const environment = findEnvironmentByName(
      response.environments,
      connectionForm.environmentName,
    );
    onSelectedConnectionChange(
      getSelectedConnectionNameFromEnvironment(environment, connectionForm.name.trim()),
    );
  };

  const handleDelete = async () => {
    if (!activeConnection || !activeEnvironment) {
      return;
    }

    const response = await onDeleteConnection({
      environment_name: activeEnvironment.name,
      name: activeConnection.name,
    });
    const environment = findEnvironmentByName(response.environments, activeEnvironment.name);
    onSelectedConnectionChange(getSelectedConnectionNameFromEnvironment(environment));
  };

  return {
    activeConnection,
    activeEnvironment,
    connectionForm,
    selectedConnectionType,
    secretFieldsReady: connectionSecretsReady({
      connection: activeConnection,
      connectionType: selectedConnectionType,
      secretChanges: connectionForm.secretChanges,
    }),
    setConnectionForm: setConnectionForm as Dispatch<SetStateAction<ConnectionFormState>>,
    handleDelete,
    handleSave,
  };
}
