"use client";

import { atom, useAtom } from "jotai";
import { useCallback, useEffect, useMemo } from "react";

import {
  changeLocalVaultPassphrase,
  cloneWorkspaceEnvironment,
  createWorkspaceConnection,
  createWorkspaceEnvironment,
  deleteWorkspaceConnection,
  deleteWorkspaceEnvironment,
  getWorkspaceConfig,
  getWorkspaceEnvironmentPolicy,
  initializeLocalVault,
  lockLocalVault,
  unlockLocalVault,
  updateWorkspaceEnvironmentPolicy,
  updateWorkspaceConnection,
  updateWorkspaceEnvironment,
  updateWorkspaceProject,
} from "@/lib/api-config";
import type { EnvironmentPolicy, WorkspaceRetentionSettings } from "@/lib/generated/api-types";
import { WorkspaceConfigResponse, type WorkspaceConnectionSecretChanges } from "@/lib/types";

const workspaceConfigAtom = atom<WorkspaceConfigResponse | null>(null);
const workspaceEnvironmentPoliciesAtom = atom<Record<string, EnvironmentPolicy>>({});
const workspaceConfigLoadingAtom = atom(false);
const workspaceConfigBusyAtom = atom(false);
const workspaceConfigStatusMessageAtom = atom<string | null>(null);
const workspaceConfigStatusToneAtom = atom<"error" | "success" | null>(null);

let workspaceConfigLoadPromise: Promise<void> | null = null;

export function useWorkspaceSettingsData() {
  const [workspaceConfig, setWorkspaceConfig] = useAtom(workspaceConfigAtom);
  const [workspaceEnvironmentPolicies, setWorkspaceEnvironmentPolicies] = useAtom(
    workspaceEnvironmentPoliciesAtom,
  );
  const [workspaceConfigLoading, setWorkspaceConfigLoading] = useAtom(workspaceConfigLoadingAtom);
  const [workspaceConfigBusy, setWorkspaceConfigBusy] = useAtom(workspaceConfigBusyAtom);
  const [workspaceConfigStatusMessage, setWorkspaceConfigStatusMessage] = useAtom(
    workspaceConfigStatusMessageAtom,
  );
  const [workspaceConfigStatusTone, setWorkspaceConfigStatusTone] = useAtom(
    workspaceConfigStatusToneAtom,
  );

  const loadWorkspaceConfig = useCallback(async () => {
    if (workspaceConfigLoadPromise) {
      await workspaceConfigLoadPromise;
      return;
    }

    setWorkspaceConfigLoading(true);
    workspaceConfigLoadPromise = (async () => {
      try {
        const response = await getWorkspaceConfig();
        setWorkspaceConfig(response);
        setWorkspaceConfigStatusMessage(null);
        setWorkspaceConfigStatusTone(null);
      } catch (error) {
        setWorkspaceConfigStatusMessage(
          error instanceof Error ? error.message : "Failed to load workspace config.",
        );
        setWorkspaceConfigStatusTone("error");
      } finally {
        setWorkspaceConfigLoading(false);
        workspaceConfigLoadPromise = null;
      }
    })();

    await workspaceConfigLoadPromise;
  }, [
    setWorkspaceConfig,
    setWorkspaceConfigLoading,
    setWorkspaceConfigStatusMessage,
    setWorkspaceConfigStatusTone,
  ]);

  useEffect(() => {
    if (!workspaceConfig && !workspaceConfigLoading) {
      void loadWorkspaceConfig();
    }
  }, [loadWorkspaceConfig, workspaceConfig, workspaceConfigLoading]);

  const normalizedConfigEnvironments = useMemo(
    () =>
      [...(workspaceConfig?.environments ?? [])].sort((left, right) =>
        left.name.localeCompare(right.name),
      ),
    [workspaceConfig],
  );

  const fallbackConfigEnvironment = useMemo(
    () =>
      workspaceConfig?.selected_environment ||
      workspaceConfig?.default_environment ||
      normalizedConfigEnvironments[0]?.name ||
      null,
    [normalizedConfigEnvironments, workspaceConfig],
  );

  const runWorkspaceConfigMutation = useCallback(
    async (operation: () => Promise<WorkspaceConfigResponse>, successMessage: string) => {
      setWorkspaceConfigBusy(true);
      setWorkspaceConfigStatusMessage(null);
      setWorkspaceConfigStatusTone(null);

      try {
        const response = await operation();
        setWorkspaceConfig(response);
        setWorkspaceConfigStatusMessage(successMessage);
        setWorkspaceConfigStatusTone("success");
        return response;
      } catch (error) {
        setWorkspaceConfigStatusMessage(
          error instanceof Error ? error.message : "Workspace config update failed.",
        );
        setWorkspaceConfigStatusTone("error");
        throw error;
      } finally {
        setWorkspaceConfigBusy(false);
      }
    },
    [],
  );

  const handleUpdateWorkspaceProject = useCallback(
    (input: {
      name?: string;
      features?: Record<string, boolean>;
      retention?: WorkspaceRetentionSettings;
    }) =>
      runWorkspaceConfigMutation(
        () => updateWorkspaceProject(input),
        input.name
          ? `Project renamed to "${input.name}".`
          : input.retention
            ? "Retention settings updated."
            : "Project settings updated.",
      ),
    [runWorkspaceConfigMutation],
  );

  const handleCreateWorkspaceEnvironment = useCallback(
    (input: { name: string; schema_prefix?: string; set_as_default?: boolean }) =>
      runWorkspaceConfigMutation(
        () => createWorkspaceEnvironment(input),
        `Environment "${input.name}" created.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleUpdateWorkspaceEnvironment = useCallback(
    (input: {
      name: string;
      new_name?: string;
      schema_prefix?: string;
      set_as_default?: boolean;
    }) =>
      runWorkspaceConfigMutation(
        () => updateWorkspaceEnvironment(input),
        `Environment "${input.new_name || input.name}" saved.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleCloneWorkspaceEnvironment = useCallback(
    (input: {
      source_name: string;
      target_name: string;
      schema_prefix?: string;
      set_as_default?: boolean;
    }) =>
      runWorkspaceConfigMutation(
        () => cloneWorkspaceEnvironment(input),
        `Environment "${input.target_name}" cloned.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleDeleteWorkspaceEnvironment = useCallback(
    (name: string) =>
      runWorkspaceConfigMutation(
        () => deleteWorkspaceEnvironment(name),
        `Environment "${name}" deleted.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleCreateWorkspaceConnection = useCallback(
    (input: {
      environment_name: string;
      name: string;
      type: string;
      values: Record<string, unknown>;
      secret_changes?: WorkspaceConnectionSecretChanges;
      access_mode?: "read_only" | "read_write";
      policy_revision?: string;
    }) =>
      runWorkspaceConfigMutation(
        () => createWorkspaceConnection(input),
        `Connection "${input.name}" created.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleUpdateWorkspaceConnection = useCallback(
    (input: {
      environment_name: string;
      current_name?: string;
      name: string;
      type: string;
      values: Record<string, unknown>;
      secret_changes?: WorkspaceConnectionSecretChanges;
      access_mode?: "read_only" | "read_write";
      policy_revision?: string;
    }) =>
      runWorkspaceConfigMutation(
        () => updateWorkspaceConnection(input),
        `Connection "${input.name}" saved.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleDeleteWorkspaceConnection = useCallback(
    (input: { environment_name: string; name: string }) =>
      runWorkspaceConfigMutation(
        () => deleteWorkspaceConnection(input),
        `Connection "${input.name}" deleted.`,
      ),
    [runWorkspaceConfigMutation],
  );

  const handleInitializeLocalVault = useCallback(
    (passphrase: string) =>
      runWorkspaceConfigMutation(
        () => initializeLocalVault(passphrase),
        "Encrypted vault set up and unlocked for this Renart session.",
      ),
    [runWorkspaceConfigMutation],
  );

  const handleUnlockLocalVault = useCallback(
    (passphrase: string) =>
      runWorkspaceConfigMutation(
        () => unlockLocalVault(passphrase),
        "Encrypted vault unlocked for this Renart session.",
      ),
    [runWorkspaceConfigMutation],
  );

  const handleLockLocalVault = useCallback(
    () => runWorkspaceConfigMutation(() => lockLocalVault(), "Encrypted vault locked."),
    [runWorkspaceConfigMutation],
  );

  const handleChangeLocalVaultPassphrase = useCallback(
    (passphrase: string) =>
      runWorkspaceConfigMutation(
        () => changeLocalVaultPassphrase(passphrase),
        "Encrypted vault passphrase changed.",
      ),
    [runWorkspaceConfigMutation],
  );

  const loadWorkspaceEnvironmentPolicy = useCallback(
    async (environment: string) => {
      const response = await getWorkspaceEnvironmentPolicy(environment);
      setWorkspaceEnvironmentPolicies((current) => ({
        ...current,
        [response.environment]: response.policy,
      }));
      return response.policy;
    },
    [setWorkspaceEnvironmentPolicies],
  );

  const handleUpdateWorkspaceEnvironmentPolicy = useCallback(
    async (environment: string, policy: EnvironmentPolicy) => {
      setWorkspaceConfigBusy(true);
      setWorkspaceConfigStatusMessage(null);
      setWorkspaceConfigStatusTone(null);
      try {
        const response = await updateWorkspaceEnvironmentPolicy(environment, policy);
        setWorkspaceEnvironmentPolicies((current) => ({
          ...current,
          [response.environment]: response.policy,
        }));
        setWorkspaceConfigStatusMessage(`Policy for "${environment}" saved.`);
        setWorkspaceConfigStatusTone("success");
        return response.policy;
      } catch (error) {
        setWorkspaceConfigStatusMessage(
          error instanceof Error ? error.message : "Environment policy update failed.",
        );
        setWorkspaceConfigStatusTone("error");
        throw error;
      } finally {
        setWorkspaceConfigBusy(false);
      }
    },
    [
      setWorkspaceConfigBusy,
      setWorkspaceConfigStatusMessage,
      setWorkspaceConfigStatusTone,
      setWorkspaceEnvironmentPolicies,
    ],
  );

  return {
    fallbackConfigEnvironment,
    handleChangeLocalVaultPassphrase,
    handleCloneWorkspaceEnvironment,
    handleCreateWorkspaceConnection,
    handleCreateWorkspaceEnvironment,
    handleDeleteWorkspaceConnection,
    handleDeleteWorkspaceEnvironment,
    handleInitializeLocalVault,
    handleLockLocalVault,
    handleUpdateWorkspaceConnection,
    handleUpdateWorkspaceEnvironment,
    handleUpdateWorkspaceEnvironmentPolicy,
    handleUpdateWorkspaceProject,
    handleUnlockLocalVault,
    loadWorkspaceEnvironmentPolicy,
    loadWorkspaceConfig,
    normalizedConfigEnvironments,
    workspaceEnvironmentPolicies,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigLoading,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
  };
}
