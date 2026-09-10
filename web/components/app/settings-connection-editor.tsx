import { CheckCircle2, LoaderCircle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useSettingsSource } from "./settings-source";
import { useSettingsLeaveGuard } from "./settings-leave-guard";
import { useResourceNavigation } from "@/hooks/use-resource-navigation";
import { useNavigationArrival } from "@/hooks/use-navigation-arrival";
import { WorkspaceConnectionFormFields } from "@/components/workspace-connection-form-fields";
import { ConnectionAccessPreview } from "@/components/app/connection-access-preview";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useWorkspaceConnectionForm } from "@/hooks/use-workspace-connection-form";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { testWorkspaceConnection } from "@/lib/api-config";
import { useIngestrEnabled, visibleConnectionTypes } from "@/lib/features";
import {
  buildConnectionFieldDefaults,
  buildConnectionSecretChanges,
} from "@/lib/settings-form-utils";
import { ConfirmDeleteButton, SettingsEditorHeader } from "./settings-form-parts";

export type ConnectionEditorState =
  | { mode: "create"; environment: string | null }
  | { mode: "edit"; environment: string; connection: string };

export function ConnectionEditor({
  focusedField,
  state,
  onSaved,
  onClose,
  settings,
}: {
  focusedField?: string;
  state: ConnectionEditorState;
  onSaved: (environment: string, connection: string) => void;
  onClose: () => void;
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  const {
    handleCreateWorkspaceConnection,
    handleDeleteWorkspaceConnection,
    handleUpdateWorkspaceConnection,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
  } = settings;
  const resource = useResourceNavigation();
  const arrival = useNavigationArrival(resource.detail);
  const mode = state?.mode ?? "edit";
  const [dirty, setDirty] = useState(false);
  const [snapshot] = useState(normalizedConfigEnvironments);
  const [configSnapshot] = useState(workspaceConfig);
  const source = useSettingsSource(configSnapshot);

  const ingestrEnabled = useIngestrEnabled(configSnapshot);
  // Stable identity matters: this array is an effect dependency inside
  // useWorkspaceConnectionForm, and a fresh [] per render loops the effect.
  // Ingestr/SaaS source types are hidden unless the feature is on, but the
  // type of the connection being edited always stays selectable.
  const editedConnectionType =
    state?.mode === "edit"
      ? configSnapshot?.environments
          ?.find((environment) => environment.name === state.environment)
          ?.connections.find((connection) => connection.name === state.connection)?.type
      : undefined;
  const connectionTypes = useMemo(() => {
    const all = configSnapshot?.connection_types ?? [];
    const visible = visibleConnectionTypes(all, ingestrEnabled);
    if (editedConnectionType && !visible.some((type) => type.type_name === editedConnectionType)) {
      const edited = all.find((type) => type.type_name === editedConnectionType);
      if (edited) {
        return [...visible, edited];
      }
    }
    return visible;
  }, [editedConnectionType, ingestrEnabled, configSnapshot?.connection_types]);
  const [validateBusy, setValidateBusy] = useState(false);
  const [validateMessage, setValidateMessage] = useState<string | null>(null);
  const [validateTone, setValidateTone] = useState<"error" | "success" | null>(null);

  useEffect(() => {
    setValidateMessage(null);
    setValidateTone(null);
  }, [state]);

  const form = useWorkspaceConnectionForm({
    policyRevision: configSnapshot?.connection_policy_revision,
    connectionTypes: connectionTypes,
    defaultEnvironment: configSnapshot?.default_environment,
    environments: snapshot,
    mode,
    onCreateConnection: handleCreateWorkspaceConnection,
    onDeleteConnection: handleDeleteWorkspaceConnection,
    onModeChange: () => {},
    onSelectedConnectionChange: () => {},
    onSelectedEnvironmentChange: () => {},
    onUpdateConnection: handleUpdateWorkspaceConnection,
    selectedConnectionName: state?.mode === "edit" ? state.connection : null,
    selectedEnvironmentName: state?.environment ?? null,
  });

  const canSave =
    source.canWrite &&
    Boolean(
      form.connectionForm.environmentName &&
      form.connectionForm.name.trim() &&
      form.connectionForm.type &&
      form.secretFieldsReady,
    ) &&
    !workspaceConfigBusy &&
    !validateBusy;
  const guard = useSettingsLeaveGuard({
    project: configSnapshot?.project_id,
    dirty,
    busy: workspaceConfigBusy || validateBusy,
    canSave,
    save: () => save(false),
  });
  const changeForm: typeof form.setConnectionForm = (value) => {
    guard.protectNavigation();
    setDirty(true);
    setValidateMessage(null);
    setValidateTone(null);
    form.setConnectionForm(value);
  };
  const validateConnection = async () => {
    setValidateBusy(true);
    setValidateMessage(null);
    setValidateTone(null);
    try {
      const response = await testWorkspaceConnection({
        environment_name: form.connectionForm.environmentName,
        current_name: form.activeConnection?.name,
        name: form.connectionForm.name,
        type: form.connectionForm.type,
        values: form.connectionForm.values,
        secret_changes: form.connectionForm.secretChanges,
        access_mode: form.connectionForm.accessMode,
      });
      setValidateMessage(response.message ?? "Connection validated.");
      setValidateTone("success");
    } catch (error) {
      setValidateMessage(error instanceof Error ? error.message : "Connection validation failed.");
      setValidateTone("error");
    } finally {
      setValidateBusy(false);
    }
  };

  const save = async (navigateAfter = true) => {
    if (!canSave) return false;
    try {
      await form.handleSave();
      guard.allowNavigation();
      setDirty(false);
      if (navigateAfter)
        onSaved(form.connectionForm.environmentName, form.connectionForm.name.trim());
      return true;
    } catch {
      return false;
    }
  };
  const remove = async () => {
    try {
      await form.handleDelete();
      guard.allowNavigation();
      setDirty(false);
      onClose();
    } catch {
      /* Keep the draft and show the API error. */
    }
  };
  if (
    state.mode === "edit" &&
    !snapshot.some(
      (environment) =>
        environment.name === state.environment &&
        environment.connections.some((connection) => connection.name === state.connection),
    )
  )
    return (
      <Alert variant="destructive">
        <AlertTitle>Connection not found</AlertTitle>
        <AlertDescription>
          The linked connection is no longer available in this environment.
        </AlertDescription>
      </Alert>
    );

  return (
    <section
      aria-label={
        mode === "create"
          ? "New connection"
          : state.mode === "edit"
            ? state.connection
            : "Connection"
      }
      className="mx-auto grid w-full min-w-0 max-w-3xl gap-6 p-4 sm:p-6"
    >
      {guard.dialog}
      {source.notice}
      <SettingsEditorHeader
        title={mode === "create" ? "New connection" : (form.activeConnection?.name ?? "Connection")}
        description={
          mode === "create"
            ? "Sensitive values are write-only and scoped to this environment."
            : `${form.connectionForm.type} · ${state.environment}`
        }
      />
      <div className="grid min-w-0 gap-4">
        {focusedField &&
        focusedField !== "access_mode" &&
        !form.selectedConnectionType?.fields.some((f) => f.name === focusedField) ? (
          <p role="alert">The linked field no longer exists.</p>
        ) : null}
        {state?.mode === "edit" && form.connectionForm.accessMode === "read_only" ? (
          <ConnectionAccessPreview
            environment={form.connectionForm.environmentName}
            connection={state.connection}
          />
        ) : null}
        <WorkspaceConnectionFormFields
          compactSections
          focusedField={focusedField}
          focusToken={arrival}
          onFieldFocus={(field) => {
            if (state?.mode === "edit" && field !== focusedField)
              void resource.reflect(
                { kind: "connection", connection: state.connection, field },
                state.environment,
              );
          }}
          busy={workspaceConfigBusy}
          canValidate={Boolean(
            form.connectionForm.environmentName &&
            form.connectionForm.name.trim() &&
            form.connectionForm.type &&
            form.secretFieldsReady,
          )}
          connectionForm={form.connectionForm}
          connectionTypes={connectionTypes}
          environments={normalizedConfigEnvironments}
          mode={mode}
          selectedConnectionType={form.selectedConnectionType}
          localVaultState={workspaceConfig?.secret_vault.state}
          secretFields={form.activeConnection?.secret_fields}
          selectedEnvironment={state?.environment ?? null}
          showEnvironmentSelector={mode === "create"}
          environmentDisabled={mode === "edit"}
          typeDisabled={mode === "edit"}
          validateBusy={validateBusy}
          validateMessage={validateMessage}
          validateTone={validateTone}
          showActions={false}
          onAccessModeChange={(accessMode) => changeForm((current) => ({ ...current, accessMode }))}
          onEnvironmentChange={(value) =>
            changeForm((current) => ({ ...current, environmentName: value }))
          }
          onFieldValueChange={(fieldName, value) =>
            changeForm((current) => ({
              ...current,
              values: { ...current.values, [fieldName]: value },
            }))
          }
          onNameChange={(value) => changeForm((current) => ({ ...current, name: value }))}
          onSecretChange={(fieldName, change) =>
            changeForm((current) => ({
              ...current,
              secretChanges: { ...current.secretChanges, [fieldName]: change },
            }))
          }
          onSave={() => void save()}
          onTypeChange={(value) =>
            changeForm((current) => ({
              ...current,
              type: value,
              values: buildConnectionFieldDefaults({
                connectionTypes: connectionTypes,
                existingConnection: null,
                previousValues: current.values,
                typeName: value,
              }),
              secretChanges: buildConnectionSecretChanges(
                connectionTypes.find((connectionType) => connectionType.type_name === value),
              ),
            }))
          }
          onValidate={() => void validateConnection()}
        />
      </div>
      <footer className="sticky bottom-0 border-t bg-card py-3">
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            {mode === "edit" && form.activeConnection ? (
              <ConfirmDeleteButton
                disabled={workspaceConfigBusy || !source.canWrite}
                label="Delete"
                onConfirm={() => void remove()}
              />
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={
                workspaceConfigBusy ||
                validateBusy ||
                !form.connectionForm.name.trim() ||
                !form.secretFieldsReady
              }
              onClick={() => void validateConnection()}
            >
              {validateBusy ? (
                <LoaderCircle data-icon="inline-start" className="animate-spin" />
              ) : (
                <CheckCircle2 data-icon="inline-start" />
              )}
              Verify
            </Button>
            <Button size="sm" disabled={!canSave} onClick={() => void save()}>
              {mode === "create" ? "Create connection" : "Save changes"}
            </Button>
          </div>
        </div>
      </footer>
    </section>
  );
}
