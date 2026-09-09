import { Link } from "@tanstack/react-router";
import { Boxes, Copy } from "lucide-react";
import { useState } from "react";
import { useAtomValue, useSetAtom } from "jotai";
import { selectedEnvironmentAtom, selectedEnvironmentOverrideAtom } from "@/lib/atoms/workspace";
import { useSettingsSource } from "./settings-source";
import { useSettingsLeaveGuard } from "./settings-leave-guard";
import { ResourceLink } from "./resource-link";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldTitle,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useWorkspaceEnvironmentForm } from "@/hooks/use-workspace-environment-form";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import type { EnvironmentPolicy } from "@/lib/generated/api-types";
import {
  SettingsStatus,
  ConfirmDeleteButton,
  PlainFieldGroup,
  PlainField,
} from "./settings-form-parts";

const emptyPolicy: EnvironmentPolicy = {
  protected: false,
  deployed_only: false,
  confirm_destructive: false,
};

function policiesEqual(left: EnvironmentPolicy, right: EnvironmentPolicy) {
  return (
    Boolean(left.protected) === Boolean(right.protected) &&
    Boolean(left.deployed_only) === Boolean(right.deployed_only) &&
    Boolean(left.confirm_destructive) === Boolean(right.confirm_destructive)
  );
}

export type EnvironmentEditorState =
  | { mode: "create" }
  | { mode: "clone"; name: string }
  | { mode: "edit"; name: string };

export function EnvironmentEditor({
  state,
  onStateChange,
  onSaved,
  settings,
}: {
  onSaved: (name: string) => void;
  state: EnvironmentEditorState;
  onStateChange: (state: EnvironmentEditorState | null) => void;
  settings: ReturnType<typeof useWorkspaceSettingsData>;
}) {
  const {
    handleCloneWorkspaceEnvironment,
    handleCreateWorkspaceEnvironment,
    handleDeleteWorkspaceEnvironment,
    handleUpdateWorkspaceEnvironment,
    handleUpdateWorkspaceEnvironmentPolicy,
    normalizedConfigEnvironments,
    workspaceConfig,
    workspaceConfigBusy,
    workspaceConfigStatusMessage,
    workspaceConfigStatusTone,
    workspaceEnvironmentPolicies,
  } = settings;
  const [snapshot] = useState(normalizedConfigEnvironments);
  const [defaultSnapshot] = useState(workspaceConfig?.default_environment);
  const source = useSettingsSource(workspaceConfig);
  const [dirty, setDirty] = useState(false);
  const mode = state.mode;
  const selectedEnvironmentName = state?.mode === "create" ? null : (state?.name ?? null);

  const {
    activeEnvironment,
    environmentForm,
    handleDelete,
    handleSave,
    setEnvironmentForm: updateForm,
  } = useWorkspaceEnvironmentForm({
    defaultEnvironment: defaultSnapshot,
    environments: snapshot,
    mode,
    onCloneEnvironment: handleCloneWorkspaceEnvironment,
    onCreateEnvironment: handleCreateWorkspaceEnvironment,
    onDeleteEnvironment: handleDeleteWorkspaceEnvironment,
    onModeChange: () => {},
    onSelectedEnvironmentChange: () => {},
    onUpdateEnvironment: handleUpdateWorkspaceEnvironment,
    selectedEnvironmentName,
  });

  const editName = state.mode === "edit" ? state.name : null;
  const [storedPolicy, setStoredPolicy] = useState(
    (editName ? workspaceEnvironmentPolicies[editName] : null) ?? emptyPolicy,
  );
  const [policyDraft, setPolicyDraft] = useState(storedPolicy);
  const [partialSave, setPartialSave] = useState(false);
  const executionEnvironment = useAtomValue(selectedEnvironmentAtom);
  const setExecutionEnvironment = useSetAtom(selectedEnvironmentOverrideAtom);
  const canSave = source.canWrite && Boolean(environmentForm.name.trim()) && !workspaceConfigBusy;
  const guard = useSettingsLeaveGuard({
    project: workspaceConfig?.project_id,
    dirty,
    busy: workspaceConfigBusy,
    canSave,
    save: () => save(false),
  });
  const setEnvironmentForm: typeof updateForm = (value) => {
    guard.protectNavigation();
    setDirty(true);
    updateForm(value);
  };
  const close = () => onStateChange(null);
  const save = async (navigateAfter = true) => {
    if (!canSave) return false;
    try {
      // Guardrails are saved before a rename, which migrates the existing policy.
      // If metadata fails, retain the draft and retry only the unfinished write.
      if (editName && !policiesEqual(policyDraft, storedPolicy)) {
        await handleUpdateWorkspaceEnvironmentPolicy(editName, policyDraft);
        setStoredPolicy(policyDraft);
        setPartialSave(true);
      }
      await handleSave();
      setPartialSave(false);
      guard.allowNavigation();
      setDirty(false);
      if (navigateAfter) onSaved(environmentForm.name.trim());
      return true;
    } catch {
      return false;
    }
  };
  const remove = async () => {
    try {
      await handleDelete();
      guard.allowNavigation();
      setDirty(false);
      close();
    } catch {
      /* Preserve the draft on API errors. */
    }
  };

  const title =
    mode === "create"
      ? "New environment"
      : mode === "clone"
        ? `Clone ${environmentForm.cloneSourceName || "environment"}`
        : activeEnvironment
          ? activeEnvironment.name
          : "Environment";
  const description =
    mode === "create"
      ? "Add an environment to this project."
      : mode === "clone"
        ? "Copy an environment including its connections and guardrails."
        : "Rename, set defaults, and adjust guardrails.";

  return (
    <section aria-label={title} className="grid min-w-0 gap-6 rounded-xl border bg-card p-4 sm:p-6">
      {guard.dialog}
      {source.notice}
      <header className="grid gap-2">
        <h1 className="flex items-center gap-2 text-lg font-medium">
          <Boxes className="size-4 text-primary" />
          {title}
        </h1>
        <p className="text-sm text-muted-foreground">{description}</p>
        {mode === "edit" && activeEnvironment ? (
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span>Editing does not switch execution.</span>
            <Button
              size="sm"
              variant="outline"
              disabled={workspaceConfigBusy || executionEnvironment === activeEnvironment.name}
              onClick={() => setExecutionEnvironment(activeEnvironment.name)}
            >
              {executionEnvironment === activeEnvironment.name
                ? "In use for execution"
                : "Use for execution"}
            </Button>
          </div>
        ) : null}
      </header>
      <div className="min-w-0">
        <PlainFieldGroup>
          {partialSave && workspaceConfigStatusTone === "error" ? (
            <Alert>
              <AlertTitle>Guardrails saved</AlertTitle>
              <AlertDescription>
                The remaining environment changes could not be saved. Your draft is still here;
                retry to finish.
              </AlertDescription>
            </Alert>
          ) : null}
          {workspaceConfigStatusTone === "error" ? (
            <SettingsStatus
              message={workspaceConfigStatusMessage}
              tone={workspaceConfigStatusTone}
            />
          ) : null}
          {mode === "clone" ? (
            <PlainField>
              <Label>Source</Label>
              <Select
                value={environmentForm.cloneSourceName || undefined}
                onValueChange={(value) => {
                  const source = normalizedConfigEnvironments.find(
                    (environment) => environment.name === value,
                  );
                  setEnvironmentForm((current) => ({
                    ...current,
                    cloneSourceName: value,
                    schemaPrefix: source?.schema_prefix ?? current.schemaPrefix,
                  }));
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Select source" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {normalizedConfigEnvironments.map((environment) => (
                      <SelectItem key={environment.name} value={environment.name}>
                        {environment.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </PlainField>
          ) : null}
          <PlainField>
            <Label htmlFor="environment-name">Name</Label>
            <Input
              id="environment-name"
              disabled={workspaceConfigBusy}
              value={environmentForm.name}
              onChange={(event) =>
                setEnvironmentForm((current) => ({ ...current, name: event.target.value }))
              }
              placeholder="prod"
            />
          </PlainField>
          <PlainField>
            <Label htmlFor="environment-schema-prefix">Schema prefix</Label>
            <Input
              id="environment-schema-prefix"
              disabled={workspaceConfigBusy}
              value={environmentForm.schemaPrefix}
              onChange={(event) =>
                setEnvironmentForm((current) => ({
                  ...current,
                  schemaPrefix: event.target.value,
                }))
              }
              placeholder="analytics_"
            />
            <p className="text-xs text-muted-foreground">
              Prepended to schema names at runtime. For example,
              <span className="font-mono"> dev_</span> turns
              <span className="font-mono"> analytics.orders</span> into
              <span className="font-mono"> dev_analytics.orders</span> while the asset name stays
              unchanged.
            </p>
          </PlainField>
          <Field orientation="horizontal">
            <FieldContent>
              <FieldTitle>Default environment</FieldTitle>
              <FieldDescription>
                Use this environment when no explicit environment is selected.
              </FieldDescription>
            </FieldContent>
            <Switch
              aria-label="Default environment"
              disabled={workspaceConfigBusy}
              checked={environmentForm.setAsDefault}
              onCheckedChange={(checked) =>
                setEnvironmentForm((current) => ({ ...current, setAsDefault: checked }))
              }
            />
          </Field>
          {mode === "edit" ? (
            <div className="grid gap-3 border-t pt-4">
              <div>
                <h3 className="text-sm font-medium">Execution policy</h3>
                <p className="text-sm text-muted-foreground">
                  renart-only guardrails stored in .renart/environments.yml, applied on save.
                </p>
              </div>
              <EnvironmentPolicyFields
                policy={policyDraft}
                disabled={workspaceConfigBusy}
                onChange={(value) => {
                  guard.protectNavigation();
                  setDirty(true);
                  setPolicyDraft(value);
                }}
              />
            </div>
          ) : null}
          {mode === "edit" && activeEnvironment ? (
            <div className="grid gap-2 border-t pt-4">
              <h3 className="text-sm font-medium">Connections</h3>
              {activeEnvironment.connections.length === 0 ? (
                <p className="text-sm text-muted-foreground">No connections in this environment.</p>
              ) : (
                <div className="flex flex-wrap gap-2">
                  {activeEnvironment.connections.map((connection) => (
                    <ResourceLink
                      key={connection.name}
                      target={{ kind: "connection", connection: connection.name }}
                      environment={activeEnvironment.name}
                    >
                      {connection.name}
                    </ResourceLink>
                  ))}
                </div>
              )}
              <p className="text-xs text-muted-foreground">
                Manage them in the{" "}
                <Link
                  to="/project/connections"
                  search={(search) => ({
                    ...search,
                    environment: activeEnvironment.name,
                    action: undefined,
                    detail: undefined,
                  })}
                  className="underline underline-offset-2"
                >
                  Connections
                </Link>{" "}
                tab.
              </p>
            </div>
          ) : null}
        </PlainFieldGroup>
      </div>
      <footer className="sticky bottom-0 border-t bg-card py-3">
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            {mode === "edit" && activeEnvironment ? (
              <>
                <ConfirmDeleteButton
                  disabled={workspaceConfigBusy || !source.canWrite}
                  label="Delete"
                  onConfirm={() => void remove()}
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={workspaceConfigBusy}
                  onClick={() => onStateChange({ mode: "clone", name: activeEnvironment.name })}
                >
                  <Copy data-icon="inline-start" />
                  Clone
                </Button>
              </>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <Button size="sm" variant="outline" disabled={workspaceConfigBusy} onClick={close}>
              Cancel
            </Button>
            <Button size="sm" disabled={!canSave} onClick={() => void save()}>
              {mode === "create"
                ? "Create environment"
                : mode === "clone"
                  ? "Clone environment"
                  : "Save changes"}
            </Button>
          </div>
        </div>
      </footer>
    </section>
  );
}

function EnvironmentPolicyFields({
  policy,
  disabled,
  onChange,
}: {
  policy: EnvironmentPolicy;
  disabled: boolean;
  onChange: (policy: EnvironmentPolicy) => void;
}) {
  return (
    <FieldGroup>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Protected</FieldTitle>
          <FieldDescription>Disable interactive execution for this environment.</FieldDescription>
        </FieldContent>
        <Switch
          aria-label="Protected"
          disabled={disabled}
          checked={policy.protected}
          onCheckedChange={(checked) => onChange({ ...policy, protected: checked })}
        />
      </Field>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Deployed only</FieldTitle>
          <FieldDescription>Only run deployed snapshots for this environment.</FieldDescription>
        </FieldContent>
        <Switch
          aria-label="Deployed only"
          disabled={disabled}
          checked={policy.deployed_only}
          onCheckedChange={(checked) => onChange({ ...policy, deployed_only: checked })}
        />
      </Field>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>Confirm destructive operations</FieldTitle>
          <FieldDescription>
            Require typing the environment name before destructive runs.
          </FieldDescription>
        </FieldContent>
        <Switch
          aria-label="Confirm destructive operations"
          disabled={disabled}
          checked={policy.confirm_destructive}
          onCheckedChange={(checked) => onChange({ ...policy, confirm_destructive: checked })}
        />
      </Field>
    </FieldGroup>
  );
}
