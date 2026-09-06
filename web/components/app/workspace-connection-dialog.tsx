"use client";

import { useMemo, useState } from "react";
import { CheckCircle2, Plug, TestTube2 } from "lucide-react";

import { WorkspaceConnectionFormFields } from "@/components/workspace-connection-form-fields";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Spinner } from "@/components/ui/spinner";
import { useWorkspaceConnectionForm } from "@/hooks/use-workspace-connection-form";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { testWorkspaceConnection } from "@/lib/api-config";
import { useIngestrEnabled, visibleConnectionTypes } from "@/lib/features";
import {
  buildConnectionFieldDefaults,
  buildConnectionSecretChanges,
} from "@/lib/settings-form-utils";
import type { WorkspaceConfigConnectionType } from "@/lib/types";

export function WorkspaceConnectionDialog({
  open,
  onOpenChange,
  environment,
  connectionTypes: availableConnectionTypes,
  requestedConnectionType,
  requestedConnectionName,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  environment: string;
  connectionTypes: WorkspaceConfigConnectionType[];
  requestedConnectionType?: string;
  requestedConnectionName?: string;
  onCreated: (connectionName: string) => void | Promise<void>;
}) {
  const settings = useWorkspaceSettingsData();
  const ingestrEnabled = useIngestrEnabled(settings.workspaceConfig);
  const connectionTypes = useMemo(
    () => visibleConnectionTypes(availableConnectionTypes, ingestrEnabled),
    [availableConnectionTypes, ingestrEnabled],
  );
  const [validateBusy, setValidateBusy] = useState(false);
  const [validateMessage, setValidateMessage] = useState<string | null>(null);
  const [validateTone, setValidateTone] = useState<"error" | "success" | null>(null);
  const [saveError, setSaveError] = useState("");

  const form = useWorkspaceConnectionForm({
    connectionTypes,
    defaultEnvironment: environment,
    environments: settings.normalizedConfigEnvironments,
    mode: "create",
    onCreateConnection: settings.handleCreateWorkspaceConnection,
    onDeleteConnection: settings.handleDeleteWorkspaceConnection,
    onModeChange: () => {},
    onSelectedConnectionChange: () => {},
    onSelectedEnvironmentChange: () => {},
    onUpdateConnection: settings.handleUpdateWorkspaceConnection,
    requestedConnectionType: connectionTypes.some(
      (type) => type.type_name === requestedConnectionType,
    )
      ? requestedConnectionType
      : undefined,
    requestedConnectionName,
    selectedConnectionName: null,
    selectedEnvironmentName: environment,
  });

  const canValidate = Boolean(
    form.connectionForm.environmentName &&
    form.connectionForm.name.trim() &&
    connectionTypes.some((type) => type.type_name === form.connectionForm.type) &&
    form.secretFieldsReady,
  );

  const validate = async () => {
    if (!canValidate) return;
    setValidateBusy(true);
    setValidateMessage(null);
    setValidateTone(null);
    try {
      const response = await testWorkspaceConnection({
        environment_name: environment,
        name: form.connectionForm.name.trim(),
        type: form.connectionForm.type,
        values: form.connectionForm.values,
        secret_changes: form.connectionForm.secretChanges,
      });
      setValidateMessage(response.message ?? "Connection validated.");
      setValidateTone("success");
    } catch (cause) {
      setValidateMessage(cause instanceof Error ? cause.message : "Connection validation failed.");
      setValidateTone("error");
    } finally {
      setValidateBusy(false);
    }
  };

  const save = async () => {
    if (!canValidate) return;
    setSaveError("");
    try {
      const connectionName = form.connectionForm.name.trim();
      await form.handleSave();
      await onCreated(connectionName);
      onOpenChange(false);
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : "Could not create the connection.");
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90dvh] min-w-0 flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Plug className="size-4 text-primary" />
            New connection
          </DialogTitle>
          <DialogDescription>
            Add a compatible connection to <span className="font-mono">{environment}</span>.
            Sensitive values are write-only and never returned to the browser.
          </DialogDescription>
        </DialogHeader>
        <ScrollArea
          className="min-h-0 min-w-0 flex-1"
          viewportClassName="p-1 [&>div]:!block [&>div]:w-full"
        >
          <div className="grid min-w-0 gap-4">
            <WorkspaceConnectionFormFields
              busy={settings.workspaceConfigBusy}
              canValidate={canValidate}
              connectionForm={form.connectionForm}
              connectionTypes={connectionTypes}
              environments={settings.normalizedConfigEnvironments}
              mode="create"
              selectedConnectionType={form.selectedConnectionType}
              localVaultState={settings.workspaceConfig?.secret_vault.state}
              secretFields={form.activeConnection?.secret_fields}
              selectedEnvironment={environment}
              environmentDisabled
              validateBusy={validateBusy}
              validateMessage={validateMessage}
              validateTone={validateTone}
              showActions={false}
              onEnvironmentChange={() => {}}
              onFieldValueChange={(fieldName, value) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  values: { ...current.values, [fieldName]: value },
                }))
              }
              onNameChange={(name) => form.setConnectionForm((current) => ({ ...current, name }))}
              onSecretChange={(fieldName, change) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  secretChanges: { ...current.secretChanges, [fieldName]: change },
                }))
              }
              onSave={() => void save()}
              onTypeChange={(type) =>
                form.setConnectionForm((current) => ({
                  ...current,
                  type,
                  values: buildConnectionFieldDefaults({
                    connectionTypes,
                    typeName: type,
                    existingConnection: null,
                  }),
                  secretChanges: buildConnectionSecretChanges(
                    connectionTypes.find((connectionType) => connectionType.type_name === type),
                  ),
                }))
              }
              onValidate={() => void validate()}
            />
            {saveError ? (
              <Alert variant="destructive">
                <AlertTitle>Could not create connection</AlertTitle>
                <AlertDescription>{saveError}</AlertDescription>
              </Alert>
            ) : null}
          </div>
        </ScrollArea>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={settings.workspaceConfigBusy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => void validate()}
            disabled={!canValidate || validateBusy || settings.workspaceConfigBusy}
          >
            {validateBusy ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <TestTube2 data-icon="inline-start" />
            )}
            Verify
          </Button>
          <Button
            type="button"
            onClick={() => void save()}
            disabled={!canValidate || settings.workspaceConfigBusy}
          >
            {settings.workspaceConfigBusy ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <CheckCircle2 data-icon="inline-start" />
            )}
            Create connection
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
