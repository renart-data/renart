import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useMemo, useRef, useState } from "react";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { useWorkspaceConnectionForm } from "@/hooks/use-workspace-connection-form";
import { WorkspaceConnectionFormFields } from "@/components/workspace-connection-form-fields";
import { ScrollArea } from "@/components/ui/scroll-area";
import { testWorkspaceConnection } from "@/lib/api-config";
import type { ConnectionTarget, ResourceSearch } from "@/lib/resource-navigation";

export function ResourceConnectionDetail({
  target,
  environment,
}: {
  target: ConnectionTarget;
  environment: string;
}) {
  const settings = useWorkspaceSettingsData();
  const config = settings.workspaceConfig;
  const connection = config?.environments
    .find((e) => e.name === environment)
    ?.connections.filter((c) => c.name === target.connection);
  if (!config)
    return (
      <p role="status" className="p-3">
        Loading connection…
      </p>
    );
  if (connection?.length !== 1)
    return (
      <p role="alert" className="p-3">
        The linked connection is missing or ambiguous in this environment.
      </p>
    );
  return (
    <ConnectionEditor
      key={`${environment}:${target.connection}`}
      target={target}
      environment={environment}
      settings={settings}
    />
  );
}

// Uses the settings form/controller; URL state carries identity only. Secrets
// remain write-only and Verify/Save are explicit commands, never navigation.
function ConnectionEditor({
  target,
  environment,
  settings,
}: {
  target: ConnectionTarget;
  environment: string;
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [tone, setTone] = useState<"error" | "success" | null>(null);
  // Keep the loaded form snapshot stable while editing; unrelated config
  // refreshes must not overwrite a local draft.
  const [config, setConfig] = useState(settings.workspaceConfig!);
  const navigate = useNavigate();
  const acceptingSaved = useRef(false);
  const environments = useMemo(() => config.environments, [config]);
  const form = useWorkspaceConnectionForm({
    connectionTypes: config.connection_types,
    defaultEnvironment: config.default_environment,
    environments,
    mode: "edit",
    onCreateConnection: settings.handleCreateWorkspaceConnection,
    onDeleteConnection: settings.handleDeleteWorkspaceConnection,
    onUpdateConnection: settings.handleUpdateWorkspaceConnection,
    onModeChange: () => {},
    onSelectedConnectionChange: () => {},
    onSelectedEnvironmentChange: () => {},
    selectedConnectionName: target.connection,
    selectedEnvironmentName: environment,
  });
  useBlocker({
    shouldBlockFn: ({ current, next }) => {
      if (acceptingSaved.current) return false;
      const a = (current.search as ResourceSearch).detail;
      const b = (next.search as ResourceSearch).detail;
      if (JSON.stringify(a) === JSON.stringify(b)) return false;
      if (busy) return true;
      return dirty && !window.confirm("Discard unsaved connection changes?");
    },
    enableBeforeUnload: dirty || busy,
  });
  const change: typeof form.setConnectionForm = (value) => {
    setDirty(true);
    form.setConnectionForm(value);
  };
  const execute = async (verify: boolean) => {
    setBusy(true);
    setMessage(null);
    try {
      if (verify) {
        const result = await testWorkspaceConnection({
          environment_name: environment,
          current_name: form.activeConnection?.name,
          name: form.connectionForm.name,
          type: form.connectionForm.type,
          values: form.connectionForm.values,
          secret_changes: form.connectionForm.secretChanges,
        });
        setMessage(result.message ?? "Connection validated.");
      } else {
        const saved = await form.handleSave();
        acceptingSaved.current = true;
        const name = form.connectionForm.name.trim();
        setConfig(saved);
        setDirty(false);
        setMessage("Connection saved.");
        if (name !== target.connection)
          await navigate({
            to: ".",
            replace: true,
            search: (s) => ({
              ...s,
              detail: { v: 1, environment, target: { ...target, connection: name } },
            }),
          });
      }
      setTone("success");
    } catch (error) {
      setTone("error");
      setMessage(error instanceof Error ? error.message : "Connection operation failed.");
    } finally {
      acceptingSaved.current = false;
      setBusy(false);
    }
  };
  const fieldExists =
    !target.field || form.selectedConnectionType?.fields.some((f) => f.name === target.field);
  return (
    <ScrollArea className="min-h-0 flex-1">
      <div className="space-y-3 p-3" data-testid="routed-connection">
        <p className="text-xs text-muted-foreground">
          Current configuration · {environment}. Leave secrets blank to keep them.
        </p>
        {!fieldExists ? (
          <p role="alert" className="text-sm">
            The linked field no longer exists. No other field has been selected.
          </p>
        ) : null}
        <WorkspaceConnectionFormFields
          busy={busy}
          canValidate={form.secretFieldsReady}
          connectionForm={form.connectionForm}
          connectionTypes={config.connection_types}
          environments={environments}
          mode="edit"
          selectedConnectionType={form.selectedConnectionType}
          localVaultState={config.secret_vault.state}
          secretFields={form.activeConnection?.secret_fields}
          selectedEnvironment={environment}
          environmentDisabled
          typeDisabled
          showEnvironmentSelector={false}
          validateBusy={busy}
          validateMessage={message}
          validateTone={tone}
          focusedField={fieldExists ? target.field : undefined}
          onEnvironmentChange={() => {}}
          onTypeChange={() => {}}
          onNameChange={(value) => change((c) => ({ ...c, name: value }))}
          onFieldValueChange={(field, value) =>
            change((c) => ({ ...c, values: { ...c.values, [field]: value } }))
          }
          onSecretChange={(field, value) =>
            change((c) => ({ ...c, secretChanges: { ...c.secretChanges, [field]: value } }))
          }
          onSave={() => void execute(false)}
          onValidate={() => void execute(true)}
        />
        {dirty ? <p className="text-xs text-muted-foreground">Unsaved changes</p> : null}
        {message ? (
          <p role="status" className="text-sm">
            {message}
          </p>
        ) : null}
      </div>
    </ScrollArea>
  );
}
