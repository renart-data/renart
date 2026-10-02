import {
  ArrowLeft,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  Import,
  LoaderCircle,
  PlugZap,
  Search,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { WorkspaceConnectionFormFields } from "@/components/workspace-connection-form-fields";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { ConnectionFormState } from "@/hooks/use-workspace-connection-form";
import { createWorkspaceConnection, updateWorkspaceConnection } from "@/lib/api-config";
import { importOnboardingDatabase, previewOnboardingDiscovery } from "@/lib/api-onboarding";
import type { SqlDiscoveryTable } from "@/lib/generated/api-types";
import {
  buildConnectionFieldDefaults,
  buildConnectionSecretChanges,
  connectionSecretsReady,
} from "@/lib/settings-form-utils";
import type {
  OnboardingDiscoveryResponse,
  OnboardingImportResponse,
  WorkspaceConfigResponse,
} from "@/lib/types";
import { cn } from "@/lib/utils";

import { ConnectionSelect } from "./connection-select";
import {
  connectionTypeProductName,
  isDocumentedSQLConnectionType,
  normalizeConnectionType,
} from "./connection-type-icon";

export type ConnectDataStep = "connect" | "tables";

export type ConnectDataResult = {
  connectionName: string;
  pipelinePath: string;
  tables: string[];
  response: OnboardingImportResponse;
};

// Connect a database, test it, pick tables and import them as source assets.
// The welcome screen and the in-workspace "Connect your data" sheet share it.
export function ConnectDataFlow({
  config,
  environmentName,
  defaultPipelineName = "analytics",
  pipelineNames = [],
  onStepChange,
  onSelectionChange,
  onCancel,
  cancelLabel = "Back",
  onImported,
}: {
  config: WorkspaceConfigResponse;
  environmentName: string;
  defaultPipelineName?: string;
  // Existing pipelines the tables can be added to.
  pipelineNames?: string[];
  onStepChange?: (step: ConnectDataStep) => void;
  // The tables currently selected and their connection, for a preview.
  onSelectionChange?: (tables: SqlDiscoveryTable[], connectionName: string) => void;
  onCancel?: () => void;
  cancelLabel?: string;
  onImported: (result: ConnectDataResult) => void;
}) {
  const [step, setStep] = useState<ConnectDataStep>("connect");
  const [showAllTypes, setShowAllTypes] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [discovery, setDiscovery] = useState<OnboardingDiscoveryResponse | null>(null);
  const [database, setDatabase] = useState("");
  const [connectionName, setConnectionName] = useState("");

  const sqlTypes = useMemo(
    () => config.connection_types.filter((type) => type.category === "warehouse"),
    [config.connection_types],
  );
  const documentedTypes = useMemo(
    () =>
      sqlTypes
        .filter((type) => isDocumentedSQLConnectionType(type.type_name))
        .sort((left, right) =>
          connectionTypeProductName(left.type_name).localeCompare(
            connectionTypeProductName(right.type_name),
          ),
        ),
    [sqlTypes],
  );
  const otherTypes = useMemo(
    () => sqlTypes.filter((type) => !isDocumentedSQLConnectionType(type.type_name)),
    [sqlTypes],
  );

  // The same form state Settings edits; this flow saves it only after a
  // successful test.
  const [connectionForm, setConnectionForm] = useState<ConnectionFormState>({
    environmentName,
    name: "",
    type: "",
    values: {},
    secretChanges: {},
    accessMode: "read_write",
    policyRevision: config.connection_policy_revision,
  });
  const selectedConnectionType =
    sqlTypes.find((type) => type.type_name === connectionForm.type) ?? null;
  const typeChosen = connectionForm.type !== "";

  const goTo = (next: ConnectDataStep) => {
    setStep(next);
    setError(null);
    onStepChange?.(next);
  };

  const chooseType = (typeName: string) => {
    const name = `${normalizeConnectionType(typeName)}-default`;
    setConnectionName(name);
    setDiscovery(null);
    setDatabase("");
    setError(null);
    setConnectionForm((current) => ({
      ...current,
      environmentName,
      name,
      type: typeName,
      values: buildConnectionFieldDefaults({
        connectionTypes: sqlTypes,
        typeName,
        existingConnection: null,
      }),
      secretChanges: buildConnectionSecretChanges(
        sqlTypes.find((candidate) => candidate.type_name === typeName),
      ),
    }));
  };

  const secretsReady = connectionSecretsReady({
    connection: null,
    connectionType: selectedConnectionType,
    secretChanges: connectionForm.secretChanges,
  });

  const test = async (selectedDatabase?: string) => {
    setBusy(true);
    setError(null);
    try {
      const response = await previewOnboardingDiscovery({
        environment_name: environmentName,
        type: connectionForm.type,
        values: connectionForm.values,
        secret_changes: connectionForm.secretChanges,
        database: selectedDatabase,
      });
      if (response.status === "error") {
        setDiscovery(null);
        setError(response.error || "Renart could not connect with these settings.");
        return;
      }
      setDiscovery(response);
      setDatabase(selectedDatabase ?? response.selected_database ?? "");
    } catch (testError) {
      setDiscovery(null);
      setError(testError instanceof Error ? testError.message : "Could not connect.");
    } finally {
      setBusy(false);
    }
  };

  // Discovery only previews the connection. Importing reads the saved project
  // config, so save it before picking tables.
  const saveAndContinue = async () => {
    setBusy(true);
    setError(null);
    try {
      const values = { ...connectionForm.values };
      if (database && connectionForm.type !== "duckdb" && !values.database) {
        values.database = database;
      }
      const input = {
        environment_name: environmentName,
        name: connectionForm.name.trim() || connectionName,
        type: connectionForm.type,
        values,
        secret_changes: connectionForm.secretChanges,
        access_mode: connectionForm.accessMode ?? "read_write",
        policy_revision: config.connection_policy_revision,
      };
      try {
        await createWorkspaceConnection(input);
      } catch (createError) {
        const message = createError instanceof Error ? createError.message : "";
        if (!/already exists/i.test(message)) throw createError;
        await updateWorkspaceConnection({ ...input, current_name: input.name });
      }
      setConnectionName(input.name);
      goTo("tables");
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : "The connection could not be saved.",
      );
    } finally {
      setBusy(false);
    }
  };

  const tables = discovery?.tables ?? [];
  const needsDatabase = Boolean(discovery && !database && discovery.databases.length > 0);
  const connected = Boolean(discovery) && !needsDatabase;

  return (
    <>
      {step === "connect" ? (
        <>
          <div className="grid gap-1.5">
            <Label htmlFor="connect-data-type">Database</Label>
            <ConnectionSelect
              id="connect-data-type"
              value={connectionForm.type || undefined}
              placeholder="Choose your database or warehouse"
              className="w-full"
              groups={[
                {
                  options: documentedTypes.map((type) => ({
                    value: type.type_name,
                    label: connectionTypeProductName(type.type_name),
                    connectionType: type.type_name,
                    detail: type.type_name,
                  })),
                },
                ...(showAllTypes
                  ? [
                      {
                        label: "More connection types",
                        options: otherTypes.map((type) => ({
                          value: type.type_name,
                          label: connectionTypeProductName(type.type_name),
                          connectionType: type.type_name,
                          detail: type.type_name,
                        })),
                      },
                    ]
                  : []),
              ]}
              onValueChange={chooseType}
            />
            {otherTypes.length > 0 ? (
              <label className="flex w-fit items-center gap-2 text-xs text-muted-foreground">
                <Switch size="sm" checked={showAllTypes} onCheckedChange={setShowAllTypes} />
                Show connection types without documented support
              </label>
            ) : null}
          </div>

          {typeChosen ? (
            <div className="rounded-xl border bg-background p-4">
              <WorkspaceConnectionFormFields
                busy={busy}
                canValidate={secretsReady}
                compactSections
                connectionForm={connectionForm}
                connectionTypes={sqlTypes}
                environments={config.environments}
                mode="create"
                selectedConnectionType={selectedConnectionType}
                localVaultState={config.secret_vault?.state}
                showEnvironmentSelector={false}
                showTypeSelector={false}
                showActions={false}
                validateBusy={false}
                validateMessage={null}
                validateTone={null}
                onAccessModeChange={(accessMode) =>
                  setConnectionForm((current) => ({ ...current, accessMode }))
                }
                onEnvironmentChange={() => {}}
                onFieldValueChange={(fieldName, value) => {
                  setDiscovery(null);
                  setConnectionForm((current) => ({
                    ...current,
                    values: { ...current.values, [fieldName]: value },
                  }));
                }}
                onNameChange={(name) => setConnectionForm((current) => ({ ...current, name }))}
                onSecretChange={(fieldName, change) => {
                  setDiscovery(null);
                  setConnectionForm((current) => ({
                    ...current,
                    secretChanges: { ...current.secretChanges, [fieldName]: change },
                  }));
                }}
                onSave={() => {}}
                onTypeChange={chooseType}
                onValidate={() => void test()}
              />
            </div>
          ) : null}

          {connected && discovery ? (
            <div
              role="status"
              className="flex items-center gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2 text-sm motion-safe:animate-in motion-safe:fade-in motion-safe:duration-200"
            >
              <CheckCircle2 className="size-4 shrink-0 text-primary" />
              <span className="min-w-0">
                <span className="font-medium">Connected</span>
                <span className="text-muted-foreground">
                  {" · "}
                  {discovery.databases.length > 1
                    ? `${discovery.databases.length} databases · `
                    : ""}
                  {tables.length} {tables.length === 1 ? "table" : "tables"}
                  {database ? ` in ${database}` : ""}
                </span>
              </span>
            </div>
          ) : null}

          {needsDatabase && discovery ? (
            <div className="grid gap-1.5">
              <Label htmlFor="connect-data-database">Database</Label>
              <Select value={database} onValueChange={(value) => void test(value)}>
                <SelectTrigger id="connect-data-database">
                  <SelectValue
                    placeholder={`Connected · choose one of ${discovery.databases.length} databases`}
                  />
                </SelectTrigger>
                <SelectContent>
                  {discovery.databases.map((name) => (
                    <SelectItem key={name} value={name}>
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ) : null}

          {error ? <ConnectError title="Could not connect" message={error} /> : null}

          <div className="flex items-center justify-between gap-2">
            {onCancel ? (
              <Button variant="ghost" disabled={busy} onClick={onCancel}>
                <ArrowLeft data-icon="inline-start" />
                {cancelLabel}
              </Button>
            ) : (
              <span />
            )}
            {connected ? (
              <Button disabled={busy} onClick={() => void saveAndContinue()}>
                {busy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <ChevronRight data-icon="inline-start" />
                )}
                Choose tables
              </Button>
            ) : (
              <Button
                disabled={busy || !typeChosen || !secretsReady || needsDatabase}
                onClick={() => void test()}
              >
                {busy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <PlugZap data-icon="inline-start" />
                )}
                Test connection
              </Button>
            )}
          </div>
        </>
      ) : (
        <TablePicker
          tables={tables}
          database={database}
          connectionName={connectionName}
          environmentName={environmentName}
          defaultPipelineName={defaultPipelineName}
          pipelineNames={pipelineNames}
          onBack={() => goTo("connect")}
          onSelectionChange={onSelectionChange}
          onImported={onImported}
        />
      )}
    </>
  );
}

type SchemaGroup = { schema: string; tables: SqlDiscoveryTable[] };

export function groupTablesBySchema(tables: SqlDiscoveryTable[]): SchemaGroup[] {
  const groups = new Map<string, SqlDiscoveryTable[]>();
  for (const table of tables) {
    const schema =
      table.schema_name?.trim() ||
      (table.name.includes(".") ? table.name.slice(0, table.name.lastIndexOf(".")) : "") ||
      "default";
    groups.set(schema, [...(groups.get(schema) ?? []), table]);
  }
  return [...groups.entries()]
    .map(([schema, schemaTables]) => ({
      schema,
      tables: [...schemaTables].sort((left, right) =>
        (left.short_name || left.name).localeCompare(right.short_name || right.name),
      ),
    }))
    .sort((left, right) => left.schema.localeCompare(right.schema));
}

function TablePicker({
  tables,
  database,
  connectionName,
  environmentName,
  defaultPipelineName,
  pipelineNames,
  onBack,
  onSelectionChange,
  onImported,
}: {
  tables: SqlDiscoveryTable[];
  database: string;
  connectionName: string;
  environmentName: string;
  defaultPipelineName: string;
  pipelineNames: string[];
  onBack: () => void;
  onSelectionChange?: (tables: SqlDiscoveryTable[], connectionName: string) => void;
  onImported: (result: ConnectDataResult) => void;
}) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState("");
  const [pipelineName, setPipelineName] = useState(defaultPipelineName);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const groups = useMemo(() => groupTablesBySchema(tables), [tables]);
  const query = filter.trim().toLowerCase();
  const visibleGroups = useMemo(
    () =>
      groups
        .map((group) => ({
          ...group,
          tables: query
            ? group.tables.filter((table) => table.name.toLowerCase().includes(query))
            : group.tables,
        }))
        .filter((group) => group.tables.length > 0),
    [groups, query],
  );

  useEffect(() => {
    onSelectionChange?.(
      tables.filter((table) => selected.has(table.name)),
      connectionName,
    );
  }, [connectionName, onSelectionChange, selected, tables]);

  const toggle = (names: string[], checked: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      for (const name of names) {
        if (checked) next.add(name);
        else next.delete(name);
      }
      return next;
    });

  const importTables = async () => {
    setBusy(true);
    setError(null);
    const pipeline = pipelineName.trim() || defaultPipelineName;
    try {
      const response = await importOnboardingDatabase({
        connection_name: connectionName,
        environment_name: environmentName,
        pipeline_name: pipeline,
        tables: [...selected],
        create_if_missing: true,
      });
      if (response.status === "error") {
        setError(response.error || "The tables could not be imported.");
        return;
      }
      onImported({
        connectionName,
        pipelinePath: response.pipeline_path ?? pipeline,
        tables: [...selected],
        response,
      });
    } catch (importError) {
      setError(importError instanceof Error ? importError.message : "Import failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="grid gap-1.5">
        <Label htmlFor="connect-data-pipeline">Pipeline</Label>
        <Input
          id="connect-data-pipeline"
          value={pipelineName}
          list={pipelineNames.length > 0 ? "connect-data-pipelines" : undefined}
          onChange={(event) => setPipelineName(event.target.value)}
        />
        {pipelineNames.length > 0 ? (
          <datalist id="connect-data-pipelines">
            {pipelineNames.map((name) => (
              <option key={name} value={name} />
            ))}
          </datalist>
        ) : null}
        <p className="text-xs text-muted-foreground">
          Each table becomes a source asset in this pipeline. A new name creates the pipeline.
        </p>
      </div>

      <div className="grid gap-2">
        <div className="flex items-center justify-between gap-2">
          <Label>
            Tables{database ? ` in ${database}` : ""}{" "}
            <span className="font-normal text-muted-foreground">
              ({selected.size} of {tables.length} selected)
            </span>
          </Label>
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Filter tables"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter tables..."
            className="pl-8"
          />
        </div>
        <ScrollArea className="h-72 rounded-lg border bg-background" viewportClassName="max-h-72">
          <div className="flex flex-col">
            {visibleGroups.map((group) => {
              const names = group.tables.map((table) => table.name);
              const selectedCount = names.filter((name) => selected.has(name)).length;
              return (
                <div key={group.schema} role="group" aria-label={`Schema ${group.schema}`}>
                  <label className="sticky top-0 z-10 flex cursor-pointer items-center gap-2 border-b bg-muted/80 px-3 py-1.5 text-xs font-medium backdrop-blur">
                    <Checkbox
                      aria-label={`Select all tables in ${group.schema}`}
                      checked={
                        selectedCount === 0
                          ? false
                          : selectedCount === names.length
                            ? true
                            : "indeterminate"
                      }
                      onCheckedChange={(checked) => toggle(names, checked === true)}
                    />
                    <span className="min-w-0 flex-1 truncate font-mono">{group.schema}</span>
                    <span className="shrink-0 font-normal text-muted-foreground">
                      {selectedCount > 0 ? `${selectedCount} of ` : ""}
                      {names.length}
                    </span>
                  </label>
                  {group.tables.map((table) => (
                    <label
                      key={table.name}
                      className={cn(
                        "flex cursor-pointer items-center gap-2 border-b py-1.5 pr-3 pl-8 text-sm last:border-b-0 hover:bg-muted/50",
                        selected.has(table.name) && "bg-primary/5",
                      )}
                    >
                      <Checkbox
                        aria-label={table.name}
                        checked={selected.has(table.name)}
                        onCheckedChange={(checked) => toggle([table.name], checked === true)}
                      />
                      <span
                        className="min-w-0 flex-1 truncate font-mono text-xs"
                        title={table.name}
                      >
                        {table.short_name || table.name}
                      </span>
                    </label>
                  ))}
                </div>
              );
            })}
            {tables.length === 0 ? (
              <p className="px-3 py-4 text-sm text-muted-foreground">
                No tables found in this database.
              </p>
            ) : visibleGroups.length === 0 ? (
              <p className="px-3 py-4 text-sm text-muted-foreground">No tables match the filter.</p>
            ) : null}
          </div>
        </ScrollArea>
      </div>

      {error ? <ConnectError title="Import failed" message={error} /> : null}

      <div className="flex items-center justify-between gap-2">
        <Button variant="ghost" disabled={busy} onClick={onBack}>
          <ArrowLeft data-icon="inline-start" />
          Back
        </Button>
        <Button
          disabled={busy || selected.size === 0 || !pipelineName.trim()}
          onClick={() => void importTables()}
        >
          {busy ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <Import data-icon="inline-start" />
          )}
          Import {selected.size} {selected.size === 1 ? "table" : "tables"}
        </Button>
      </div>
    </>
  );
}

function ConnectError({ title, message }: { title: string; message: string }) {
  return (
    <Alert variant="destructive">
      <CircleAlert />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="whitespace-pre-wrap">{message}</AlertDescription>
    </Alert>
  );
}
