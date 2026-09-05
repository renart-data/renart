"use client";

import { useRef, useState } from "react";
import { CheckCircle2, LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ConnectionSelect } from "@/components/app/connection-select";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
  useComboboxAnchor,
} from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Field, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { ConnectionFormState, ConnectionMode } from "@/hooks/use-workspace-connection-form";
import type {
  WorkspaceConfigConnectionType,
  WorkspaceConfigEnvironment,
  WorkspaceConfigSecretField,
  WorkspaceConnectionSecretChange,
} from "@/lib/types";

export function WorkspaceConnectionFormFields({
  busy,
  focusedField,
  canValidate,
  connectionForm,
  connectionTypes,
  environments,
  mode,
  selectedConnectionType,
  localVaultState,
  secretFields,
  selectedEnvironment,
  environmentDisabled = false,
  typeDisabled = false,
  showEnvironmentSelector = true,
  validateBusy,
  validateMessage,
  validateTone,
  showActions = true,
  onEnvironmentChange,
  onFieldValueChange,
  onNameChange,
  onSecretChange,
  onSave,
  onTypeChange,
  onValidate,
}: {
  busy: boolean;
  focusedField?: string;
  canValidate: boolean;
  connectionForm: ConnectionFormState;
  connectionTypes: WorkspaceConfigConnectionType[];
  environments: WorkspaceConfigEnvironment[];
  mode: ConnectionMode;
  selectedConnectionType: WorkspaceConfigConnectionType | null;
  localVaultState?: string;
  secretFields?: Record<string, WorkspaceConfigSecretField>;
  selectedEnvironment?: string | null;
  environmentDisabled?: boolean;
  typeDisabled?: boolean;
  showEnvironmentSelector?: boolean;
  validateBusy: boolean;
  validateMessage: string | null;
  validateTone: "error" | "success" | null;
  showActions?: boolean;
  onEnvironmentChange: (value: string) => void;
  onFieldValueChange: (fieldName: string, value: string | number | boolean | string[]) => void;
  onNameChange: (value: string) => void;
  onSecretChange: (fieldName: string, change: WorkspaceConnectionSecretChange) => void;
  onSave: () => void;
  onTypeChange: (value: string) => void;
  onValidate: () => void;
}) {
  const lastFocus = useRef<string | undefined>(undefined);
  const focusField = (name: string) => (element: HTMLDivElement | null) => {
    if (!element || name !== focusedField || lastFocus.current === name) return;
    lastFocus.current = name;
    requestAnimationFrame(() => {
      if (!element.isConnected) return;
      const input = element.querySelector<HTMLElement>('input,button,[role="combobox"]');
      input?.focus({ preventScroll: true });
      element.scrollIntoView({ block: "nearest" });
    });
  };
  return (
    <FieldGroup>
      {showEnvironmentSelector ? (
        <Field>
          <FieldLabel htmlFor="workspace-connection-environment">Environment</FieldLabel>
          <Select
            value={connectionForm.environmentName || selectedEnvironment || undefined}
            onValueChange={onEnvironmentChange}
            disabled={environmentDisabled}
          >
            <SelectTrigger id="workspace-connection-environment" className="w-full">
              <SelectValue placeholder="Select environment" />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {environments.map((environment) => (
                  <SelectItem key={environment.name} value={environment.name}>
                    {environment.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="workspace-connection-name">Name</FieldLabel>
          <Input
            id="workspace-connection-name"
            value={connectionForm.name}
            onChange={(event) => onNameChange(event.target.value)}
            placeholder="postgres-default"
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="workspace-connection-type">Type</FieldLabel>
          <ConnectionSelect
            value={connectionForm.type || undefined}
            groups={[
              {
                label: "Connection types",
                options: connectionTypes.map((connectionType) => ({
                  value: connectionType.type_name,
                  label: connectionType.type_name,
                  connectionType: connectionType.type_name,
                })),
              },
            ]}
            onValueChange={onTypeChange}
            disabled={typeDisabled}
            id="workspace-connection-type"
            className="w-full"
            placeholder="Select connection type"
          />
        </Field>
      </div>

      <FieldSet>
        <FieldLegend>Connection values</FieldLegend>
        <div className="overflow-hidden rounded-lg border">
          {selectedConnectionType?.fields.map((field) => {
            const fieldValue = connectionForm.values[field.name];
            if (field.is_sensitive || field.is_sensitive_file) {
              const change = connectionForm.secretChanges[field.name] ?? { action: "keep" };
              const descriptor = secretFields?.[field.name];
              const display = secretFieldDisplay(change, descriptor);
              const storageMode = secretStorageMode(change, descriptor);
              const help = secretFieldHelp(
                field.is_sensitive_file,
                storageMode,
                change,
                descriptor,
              );
              const environmentName = secretEnvironmentName(change, descriptor);
              const inputValue =
                change.action === "clear"
                  ? ""
                  : storageMode === "env"
                    ? environmentName
                    : change.action === "replace"
                      ? (change.value ?? "")
                      : "";
              const environmentNameInvalid =
                storageMode === "env" &&
                environmentName.length > 0 &&
                !validEnvironmentName(environmentName);
              return (
                <div
                  key={field.name}
                  ref={focusField(field.name)}
                  data-focused-field={field.name === focusedField || undefined}
                  className="grid border-t first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)]"
                >
                  <div className="flex min-w-0 items-center justify-between gap-2 bg-muted/30 px-4 py-2">
                    <span
                      className="truncate text-xs text-muted-foreground"
                      style={{
                        fontFamily: '"Geist Mono", ui-monospace, SFMono-Regular, monospace',
                      }}
                    >
                      {field.name}
                    </span>
                    <Badge variant={display.variant} size="xs">
                      {display.label}
                    </Badge>
                  </div>
                  <div className="grid gap-1 px-4 py-2 transition-colors focus-within:bg-primary/5">
                    <div className="flex min-w-0 items-center gap-2">
                      <Input
                        aria-label={field.name}
                        aria-invalid={environmentNameInvalid || undefined}
                        type={
                          storageMode === "env" || field.is_sensitive_file ? "text" : "password"
                        }
                        autoComplete={
                          storageMode === "local" || storageMode === "local-vault"
                            ? "new-password"
                            : "off"
                        }
                        value={inputValue}
                        onChange={(event) => {
                          const value = event.target.value;
                          if (storageMode === "env") {
                            onSecretChange(field.name, {
                              action: "replace",
                              binding: { ref: `env:${value}` },
                            });
                            return;
                          }
                          onSecretChange(
                            field.name,
                            value
                              ? {
                                  action: "replace",
                                  value,
                                  binding: field.is_sensitive_file
                                    ? undefined
                                    : { provider: storageMode },
                                }
                              : { action: "keep" },
                          );
                        }}
                        placeholder={
                          change.action === "clear"
                            ? "Will be removed when saved"
                            : storageMode === "env"
                              ? "Environment variable name"
                              : descriptor?.status === "configured"
                                ? "Leave blank to keep current value"
                                : field.is_sensitive_file
                                  ? "Enter a credential file path"
                                  : "Enter a value"
                        }
                        className="h-7 min-w-0 font-mono text-xs"
                      />
                      {descriptor?.status === "configured" ? (
                        <Button
                          type="button"
                          size="xs"
                          variant={change.action === "clear" ? "outline" : "ghost"}
                          onClick={() =>
                            onSecretChange(
                              field.name,
                              change.action === "clear" ? { action: "keep" } : { action: "clear" },
                            )
                          }
                        >
                          {change.action === "clear" ? "Keep" : "Clear"}
                        </Button>
                      ) : null}
                    </div>
                    <div className="flex min-w-0 flex-col items-start gap-1">
                      <ToggleGroup
                        type="single"
                        variant="outline"
                        size="sm"
                        spacing={0}
                        className="grid w-full grid-cols-1 sm:w-auto sm:grid-cols-3"
                        value={storageMode}
                        aria-label={`${field.name} secret source`}
                        onValueChange={(nextMode) => {
                          if (!nextMode || nextMode === storageMode) {
                            return;
                          }
                          onSecretChange(
                            field.name,
                            nextMode === "env"
                              ? { action: "replace", binding: { ref: "env:" } }
                              : {
                                  action: "replace",
                                  value: "",
                                  binding: field.is_sensitive_file
                                    ? undefined
                                    : {
                                        provider:
                                          nextMode === "local-vault" ? "local-vault" : "local",
                                      },
                                },
                          );
                        }}
                      >
                        <ToggleGroupItem value="local">
                          {field.is_sensitive_file ? "File path" : "Credential store"}
                        </ToggleGroupItem>
                        {!field.is_sensitive_file ? (
                          <ToggleGroupItem
                            value="local-vault"
                            disabled={localVaultState !== "unlocked"}
                          >
                            Encrypted vault
                          </ToggleGroupItem>
                        ) : null}
                        <ToggleGroupItem value="env">Environment</ToggleGroupItem>
                      </ToggleGroup>
                      <p className="min-w-0 text-[0.6875rem] leading-relaxed text-muted-foreground">
                        {help}
                      </p>
                    </div>
                    {environmentNameInvalid ? (
                      <p className="text-[0.6875rem] leading-relaxed text-destructive">
                        Use a valid environment variable name, such as WAREHOUSE_PASSWORD.
                      </p>
                    ) : null}
                    {descriptor?.message && descriptor.message !== help ? (
                      <p className="text-[0.6875rem] leading-relaxed text-muted-foreground">
                        {descriptor.message}
                      </p>
                    ) : null}
                  </div>
                </div>
              );
            }
            if (field.type === "bool") {
              return (
                <div
                  key={field.name}
                  ref={focusField(field.name)}
                  data-focused-field={field.name === focusedField || undefined}
                  className="flex items-center justify-between gap-4 border-t px-4 py-3 first:border-t-0"
                >
                  <div>
                    <div className="font-medium">{field.name}</div>
                    <div className="text-xs text-muted-foreground">
                      {field.is_required ? "Required" : "Optional"}
                    </div>
                  </div>
                  <Switch
                    checked={Boolean(fieldValue)}
                    onCheckedChange={(checked) => onFieldValueChange(field.name, checked)}
                  />
                </div>
              );
            }

            if (field.type === "string_array") {
              const values = Array.isArray(fieldValue) ? fieldValue : [];
              return (
                <div
                  key={field.name}
                  ref={focusField(field.name)}
                  data-focused-field={field.name === focusedField || undefined}
                  className="grid border-t first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)]"
                >
                  <div
                    className="bg-muted/30 px-4 py-2 text-xs text-muted-foreground"
                    style={{ fontFamily: '"Geist Mono", ui-monospace, SFMono-Regular, monospace' }}
                  >
                    {field.name}
                  </div>
                  <div className="px-4 py-1.5 transition-colors focus-within:bg-emerald-500/10 dark:focus-within:bg-emerald-500/15">
                    <StringArrayCombobox
                      value={values}
                      suggestions={field.default_value?.split(",") ?? []}
                      placeholder="Add values..."
                      onChange={(nextValues) => onFieldValueChange(field.name, nextValues)}
                    />
                  </div>
                </div>
              );
            }

            return (
              <div
                key={field.name}
                ref={focusField(field.name)}
                data-focused-field={field.name === focusedField || undefined}
                className="grid border-t first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)]"
              >
                <div
                  className="bg-muted/30 px-4 py-2 text-xs text-muted-foreground"
                  style={{ fontFamily: '"Geist Mono", ui-monospace, SFMono-Regular, monospace' }}
                >
                  {field.name}
                </div>
                <div className="px-4 py-1.5 transition-colors focus-within:bg-emerald-500/10 dark:focus-within:bg-emerald-500/15">
                  <Input
                    aria-label={field.name}
                    type={field.type === "int" ? "number" : "text"}
                    value={
                      fieldValue === undefined || fieldValue === null
                        ? ""
                        : Array.isArray(fieldValue)
                          ? fieldValue.join(", ")
                          : String(fieldValue)
                    }
                    onChange={(event) =>
                      onFieldValueChange(
                        field.name,
                        field.type === "string_array"
                          ? event.target.value
                              .split(",")
                              .map((item) => item.trim())
                              .filter(Boolean)
                          : field.type === "int"
                            ? event.target.value
                            : event.target.value,
                      )
                    }
                    placeholder={
                      field.default_value || (field.is_required ? "Required" : "Optional")
                    }
                    className="h-6 border-0 bg-transparent px-0 text-xs shadow-none focus-visible:ring-0"
                    style={{ fontFamily: '"Geist Mono", ui-monospace, SFMono-Regular, monospace' }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      </FieldSet>

      {validateMessage ? (
        <Alert variant={validateTone === "error" ? "destructive" : "default"}>
          <AlertTitle>
            {validateTone === "error"
              ? "Connection validation failed"
              : "Connection validation succeeded"}
          </AlertTitle>
          <AlertDescription className="whitespace-pre-wrap">{validateMessage}</AlertDescription>
        </Alert>
      ) : null}

      {showActions ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
          <Button
            type="button"
            variant="outline"
            className="w-full sm:w-auto"
            onClick={onValidate}
            disabled={busy || validateBusy || !canValidate}
          >
            {validateBusy ? (
              <LoaderCircle className="mr-1 inline size-3 animate-spin" />
            ) : (
              <CheckCircle2 className="mr-1 inline size-3" />
            )}
            Verify Connection
          </Button>
          <Button
            className="w-full sm:w-auto"
            type="button"
            onClick={onSave}
            disabled={busy || !canValidate}
          >
            {mode === "create" ? "Create Connection" : "Save Changes"}
          </Button>
        </div>
      ) : null}
    </FieldGroup>
  );
}

function StringArrayCombobox({
  value,
  suggestions,
  placeholder,
  onChange,
}: {
  value: string[];
  suggestions: string[];
  placeholder: string;
  onChange: (value: string[]) => void;
}) {
  const anchor = useComboboxAnchor();
  const [draft, setDraft] = useState("");
  const normalizedValue = compactUnique(value);
  const draftAsItem = draft.trim();
  const items = compactUnique([
    ...normalizedValue,
    ...suggestions,
    ...(draftAsItem ? [draftAsItem] : []),
  ]);

  const commitDraft = () => {
    const additions = splitCommaSeparated(draft);
    if (additions.length === 0) {
      return;
    }
    onChange(compactUnique([...normalizedValue, ...additions]));
    setDraft("");
  };

  const toggleValue = (item: string) => {
    const key = item.toLowerCase();
    if (normalizedValue.some((current) => current.toLowerCase() === key)) {
      onChange(normalizedValue.filter((current) => current.toLowerCase() !== key));
      setDraft("");
      return;
    }
    onChange(compactUnique([...normalizedValue, item]));
    setDraft("");
  };

  return (
    <Combobox
      multiple
      autoHighlight
      items={items}
      value={normalizedValue}
      onValueChange={(nextValue) =>
        onChange(Array.isArray(nextValue) ? compactUnique(nextValue as string[]) : [])
      }
    >
      <ComboboxChips
        ref={anchor}
        className="min-h-7 w-full border-0 bg-transparent px-0 py-0 shadow-none focus-within:ring-0"
      >
        <ComboboxValue>
          {(values) => (
            <>
              {(values as string[]).map((item) => (
                <ComboboxChip key={item}>{item}</ComboboxChip>
              ))}
              <ComboboxChipsInput
                value={draft}
                placeholder={placeholder}
                onChange={(event) => setDraft(event.target.value)}
                onBlur={commitDraft}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === ",") {
                    event.preventDefault();
                    commitDraft();
                  }
                }}
              />
            </>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>No values yet.</ComboboxEmpty>
        <ComboboxList>
          {(item) => (
            <ComboboxItem key={item} value={item} onClick={() => toggleValue(item)}>
              {item}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}

function splitCommaSeparated(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function compactUnique(values: string[]) {
  const result: string[] = [];
  const seen = new Set<string>();
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed) {
      continue;
    }
    const key = trimmed.toLowerCase();
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    result.push(trimmed);
  }
  return result;
}

function secretFieldDisplay(
  change: WorkspaceConnectionSecretChange,
  descriptor?: WorkspaceConfigSecretField,
): {
  label: string;
  variant: "secondary" | "destructive" | "outline" | "muted";
} {
  if (change.action === "replace") {
    return {
      label: change.binding?.ref?.startsWith("env:") ? "Environment ref" : "New value",
      variant: "secondary",
    };
  }
  if (change.action === "clear") {
    return { label: "Will clear", variant: "destructive" };
  }
  switch (descriptor?.status) {
    case "configured":
      return {
        label:
          descriptor.provider === "local"
            ? "Configured · Local"
            : descriptor.provider === "local-vault"
              ? "Configured · Vault"
              : descriptor.provider === "env"
                ? "Configured · Env"
                : "Configured",
        variant: "outline",
      };
    case "unavailable":
      return { label: "Unavailable", variant: "destructive" };
    case "permission_required":
      return { label: "Permission required", variant: "destructive" };
    default:
      return { label: "Missing", variant: "muted" };
  }
}

function secretFieldHelp(
  isSensitiveFile: boolean,
  storageMode: SecretStorageMode,
  change: WorkspaceConnectionSecretChange,
  descriptor?: WorkspaceConfigSecretField,
) {
  if (storageMode === "env") {
    return "Only the environment variable name is saved. Renart resolves its value when the connection is used.";
  }
  if (storageMode === "local-vault") {
    if (descriptor?.message) {
      return descriptor.message;
    }
    return change.action === "replace"
      ? "Encrypted outside this Git repository when you save. The vault stays unlocked only for this Renart session."
      : "Stored in the passphrase-protected local vault outside this Git repository.";
  }
  if (descriptor?.message) {
    return descriptor.message;
  }
  if (isSensitiveFile) {
    return "Write-only credential file path. Renart never returns the current path to the browser.";
  }
  if (change.action === "replace") {
    return "Saved in your system credential store when you save. Renart never returns the value to the browser.";
  }
  if (descriptor?.provider === "local") {
    return descriptor.reference
      ? `System credential store · ${descriptor.reference}`
      : "Stored in your system credential store.";
  }
  if (descriptor?.provider === "env") {
    return descriptor.reference
      ? `Provided by ${descriptor.reference}. Entering a replacement moves this field to your system credential store.`
      : "Provided by the process environment.";
  }
  return "Write-only. Entering a replacement moves this field to your system credential store.";
}

type SecretStorageMode = "local" | "local-vault" | "env";

function secretStorageMode(
  change: WorkspaceConnectionSecretChange,
  descriptor?: WorkspaceConfigSecretField,
): SecretStorageMode {
  if (change.binding?.ref?.startsWith("env:")) {
    return "env";
  }
  if (
    change.binding?.provider === "local-vault" ||
    change.binding?.ref?.startsWith("local-vault:")
  ) {
    return "local-vault";
  }
  if (change.binding?.provider === "local") {
    return "local";
  }
  if (change.action === "keep" && descriptor?.provider === "env") {
    return "env";
  }
  if (change.action === "keep" && descriptor?.provider === "local-vault") {
    return "local-vault";
  }
  return "local";
}

function secretEnvironmentName(
  change: WorkspaceConnectionSecretChange,
  descriptor?: WorkspaceConfigSecretField,
) {
  const reference =
    change.binding?.ref ??
    (change.action === "keep" && descriptor?.provider === "env" ? descriptor.reference : undefined);
  return reference?.startsWith("env:") ? reference.slice("env:".length) : "";
}

function validEnvironmentName(value: string) {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(value);
}
