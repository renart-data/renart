import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type { EnvironmentPolicy, WorkspaceRetentionSettings } from "@/lib/generated/api-types";
import {
  WorkspaceConfigResponse,
  WorkspaceConnectionSecretChanges,
  WorkspaceEnvironmentPolicyResponse,
} from "@/lib/types";

export async function getWorkspaceConfig(): Promise<WorkspaceConfigResponse> {
  return fetchJSON<WorkspaceConfigResponse>("/api/config", {
    cache: "no-store",
  });
}

export async function initializeLocalVault(passphrase: string): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>(
    "/api/config/secrets/vault/initialize",
    "POST",
    { passphrase },
  );
}

export async function unlockLocalVault(passphrase: string): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/secrets/vault/unlock", "POST", {
    passphrase,
  });
}

export async function lockLocalVault(): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/secrets/vault/lock", "POST", {});
}

export async function changeLocalVaultPassphrase(
  passphrase: string,
): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>(
    "/api/config/secrets/vault/change-passphrase",
    "POST",
    { passphrase },
  );
}

export async function updateWorkspaceProject(input: {
  name?: string;
  features?: Record<string, boolean>;
  retention?: WorkspaceRetentionSettings;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/project", "PUT", input);
}

export async function getWorkspaceEnvironmentPolicy(
  environment: string,
): Promise<WorkspaceEnvironmentPolicyResponse> {
  return fetchJSON<WorkspaceEnvironmentPolicyResponse>(
    `/api/config/environment-policies/${encodeURIComponent(environment)}`,
    { cache: "no-store" },
  );
}

export async function updateWorkspaceEnvironmentPolicy(
  environment: string,
  policy: EnvironmentPolicy,
): Promise<WorkspaceEnvironmentPolicyResponse> {
  return fetchJSONWithBody<WorkspaceEnvironmentPolicyResponse>(
    `/api/config/environment-policies/${encodeURIComponent(environment)}`,
    "PUT",
    {
      protected: policy.protected,
      deployed_only: policy.deployed_only,
      confirm_destructive: policy.confirm_destructive,
    },
  );
}

export async function createWorkspaceEnvironment(input: {
  name: string;
  schema_prefix?: string;
  set_as_default?: boolean;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/environments", "POST", input);
}

export async function updateWorkspaceEnvironment(input: {
  name: string;
  new_name?: string;
  schema_prefix?: string;
  set_as_default?: boolean;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/environments", "PUT", input);
}

export async function cloneWorkspaceEnvironment(input: {
  source_name: string;
  target_name: string;
  schema_prefix?: string;
  set_as_default?: boolean;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>(
    "/api/config/environments/clone",
    "POST",
    input,
  );
}

export async function deleteWorkspaceEnvironment(name: string): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/environments", "DELETE", { name });
}

export async function createWorkspaceConnection(input: {
  environment_name: string;
  name: string;
  type: string;
  values: Record<string, unknown>;
  secret_changes?: WorkspaceConnectionSecretChanges;
  access_mode?: "read_only" | "read_write";
  policy_revision?: string;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/connections", "POST", input);
}

export async function updateWorkspaceConnection(input: {
  environment_name: string;
  current_name?: string;
  name: string;
  type: string;
  values: Record<string, unknown>;
  secret_changes?: WorkspaceConnectionSecretChanges;
  access_mode?: "read_only" | "read_write";
  policy_revision?: string;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/connections", "PUT", input);
}

export async function deleteWorkspaceConnection(input: {
  environment_name: string;
  name: string;
}): Promise<WorkspaceConfigResponse> {
  return fetchJSONWithBody<WorkspaceConfigResponse>("/api/config/connections", "DELETE", input);
}

export async function testWorkspaceConnection(input: {
  environment_name: string;
  current_name?: string;
  name: string;
  type?: string;
  values?: Record<string, unknown>;
  secret_changes?: WorkspaceConnectionSecretChanges;
  access_mode?: "read_only" | "read_write";
  policy_revision?: string;
}): Promise<{ status: string; message?: string }> {
  return fetchJSONWithBody<{ status: string; message?: string }>(
    "/api/config/connections/test",
    "POST",
    input,
  );
}
