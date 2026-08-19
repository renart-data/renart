import { Link } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  Braces,
  CalendarClock,
  Database,
  ExternalLink,
  Loader2,
  Package,
  Plus,
  Settings2,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useId, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { ConnectionSelect } from "@/components/app/connection-select";
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
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import {
  getPipelineConfig,
  getPipelinePythonDependencies,
  updatePipelineConfig,
  updatePipelinePythonDependencies,
} from "@/lib/api-pipelines";
import { getWorkspaceConfig } from "@/lib/api-config";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import type {
  PipelineConfigConnection,
  PipelineConfigVariable,
  UpdatePipelineConfigRequest,
  WorkspaceConfigConnection,
  WorkspaceConfigEnvironment,
} from "@/lib/types";
import { cn } from "@/lib/utils";

import { MultiValueInput } from "./multi-value-input";

const pipelineSettingsSections = [
  { id: "general", label: "General", icon: Settings2 },
  { id: "schedule", label: "Schedule", icon: CalendarClock },
  { id: "connections", label: "Connections", icon: Database },
  { id: "python", label: "Python", icon: Package },
  { id: "variables", label: "Variables", icon: Braces },
] as const;

export type PipelineSettingsSection = (typeof pipelineSettingsSections)[number]["id"];

type PipelineConfigDraft = UpdatePipelineConfigRequest;
type ReferencedConnection = { name: string; assets: string[] };

// Pipeline settings live in pipeline.yml, while Python dependencies live in the
// pipeline-root pyproject.toml. Both write through Go endpoints and SSE then
// reconciles the workspace.
export function PipelineSettingsDialog({
  open,
  onOpenChange,
  pipelineId,
  initialSection,
  highlightedVariable,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pipelineId: string;
  initialSection?: PipelineSettingsSection;
  highlightedVariable?: string;
}) {
  const [section, setSection] = useState<PipelineSettingsSection>(initialSection ?? "general");
  const [draft, setDraft] = useState<PipelineConfigDraft | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inferredDefaultConnections, setInferredDefaultConnections] = useState<
    PipelineConfigConnection[]
  >([]);
  const [referencedConnections, setReferencedConnections] = useState<ReferencedConnection[]>([]);
  const [workspaceEnvironments, setWorkspaceEnvironments] = useState<WorkspaceConfigEnvironment[]>(
    [],
  );
  const [pythonDependencies, setPythonDependencies] = useState<string[]>([]);
  const [initialPythonDependencies, setInitialPythonDependencies] = useState<string[]>([]);
  const [pythonDependencyPath, setPythonDependencyPath] = useState("");

  // Re-fetch whenever the dialog opens so the form always reflects on-disk state
  // (a code edit or CLI run may have changed pipeline.yml since last time).
  useEffect(() => {
    if (!open) return;
    setSection(initialSection ?? "general");
    setError(null);
    setLoading(true);
    setDraft(null);
    setInferredDefaultConnections([]);
    setReferencedConnections([]);
    setWorkspaceEnvironments([]);
    setPythonDependencies([]);
    setInitialPythonDependencies([]);
    setPythonDependencyPath("");
    let cancelled = false;
    Promise.allSettled([
      getPipelineConfig(pipelineId),
      getPipelinePythonDependencies(pipelineId),
      getWorkspaceConfig(),
    ])
      .then(([configResult, pythonResult, workspaceResult]) => {
        if (cancelled) return;
        const messages: string[] = [];
        if (configResult.status === "fulfilled") {
          const config = configResult.value;
          setDraft(configResponseToDraft(config));
          setInferredDefaultConnections(config.inferred_default_connections ?? []);
          setReferencedConnections(config.referenced_connections ?? []);
        } else {
          messages.push(errorMessage(configResult.reason, "Failed to load pipeline settings."));
        }
        if (pythonResult.status === "fulfilled") {
          const python = pythonResult.value;
          setPythonDependencies(python.dependencies ?? []);
          setInitialPythonDependencies(python.dependencies ?? []);
          setPythonDependencyPath(python.path);
        } else {
          messages.push(errorMessage(pythonResult.reason, "Failed to load Python dependencies."));
        }
        if (workspaceResult.status === "fulfilled") {
          setWorkspaceEnvironments(workspaceResult.value.environments ?? []);
        } else {
          messages.push(
            errorMessage(workspaceResult.reason, "Failed to load available connections."),
          );
        }
        setError(messages.length > 0 ? messages.join(" ") : null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, pipelineId, initialSection]);

  const update = useCallback(
    <K extends keyof PipelineConfigDraft>(key: K, value: PipelineConfigDraft[K]) => {
      setDraft((current) => (current ? { ...current, [key]: value } : current));
    },
    [],
  );

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      const dependenciesChanged = !sameStringArray(pythonDependencies, initialPythonDependencies);
      const [response, dependencyResponse] = await Promise.all([
        updatePipelineConfig(pipelineId, draft),
        dependenciesChanged
          ? updatePipelinePythonDependencies(pipelineId, {
              dependencies: pythonDependencies,
            })
          : Promise.resolve(null),
      ]);
      setDraft(configResponseToDraft(response));
      setInferredDefaultConnections(response.inferred_default_connections ?? []);
      setReferencedConnections(response.referenced_connections ?? []);
      if (dependencyResponse) {
        setPythonDependencies(dependencyResponse.dependencies);
        setInitialPythonDependencies(dependencyResponse.dependencies);
        setPythonDependencyPath(dependencyResponse.path);
      }
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to save pipeline settings.");
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => {
    if (!open || loading || section !== "variables" || !highlightedVariable) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const target = Array.from(
        document.querySelectorAll<HTMLElement>("[data-pipeline-variable]"),
      ).find((element) => element.dataset.pipelineVariable === highlightedVariable);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [draft, highlightedVariable, loading, open, section]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(42rem,90dvh)] min-h-0 flex-col overflow-hidden sm:max-w-3xl">
        <DialogHeader className="shrink-0">
          <DialogTitle>
            Pipeline settings{" "}
            <span className="font-mono text-xs text-muted-foreground">
              · {draft?.name || pipelineId}
            </span>
          </DialogTitle>
          <DialogDescription>
            Edit version-controlled pipeline configuration and runtime dependencies.
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={section}
          onValueChange={(value) => setSection(value as PipelineSettingsSection)}
          orientation="vertical"
          className="grid min-h-0 flex-1 grid-rows-[auto_minmax(0,1fr)] gap-4 md:grid-cols-[10.5rem_minmax(0,1fr)] md:grid-rows-1"
        >
          <div className="flex gap-2 overflow-x-auto md:hidden">
            {pipelineSettingsSections.map((item) => (
              <Button
                key={item.id}
                type="button"
                variant={section === item.id ? "secondary" : "ghost"}
                className="shrink-0 justify-start"
                onClick={() => setSection(item.id)}
              >
                {item.label}
              </Button>
            ))}
          </div>
          <TabsList
            aria-label="Pipeline settings sections"
            className="hidden h-full min-h-0 w-full self-stretch items-stretch justify-start border bg-muted/30 p-1 group-data-vertical/tabs:h-full md:flex"
          >
            {pipelineSettingsSections.map(({ id, label, icon: Icon }) => (
              <TabsTrigger key={id} value={id} className="h-8 flex-none justify-start px-2">
                <Icon data-icon="inline-start" />
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
          <ScrollArea
            className="h-full min-h-0 rounded-lg border"
            data-testid="pipeline-settings-content"
          >
            <div className="flex flex-col gap-4 p-4">
              {loading ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="size-4 animate-spin" />
                  Loading settings…
                </div>
              ) : !draft ? (
                <p className="text-sm text-muted-foreground">Pipeline settings are unavailable.</p>
              ) : (
                pipelineSettingsSections.map((item) => (
                  <TabsContent key={item.id} value={item.id} className="m-0">
                    <PipelineSettingsSectionBody
                      section={item.id}
                      draft={draft}
                      update={update}
                      inferredDefaultConnections={inferredDefaultConnections}
                      referencedConnections={referencedConnections}
                      workspaceEnvironments={workspaceEnvironments}
                      highlightedVariable={highlightedVariable}
                      pythonDependencies={pythonDependencies}
                      onPythonDependenciesChange={setPythonDependencies}
                      pythonDependencyPath={pythonDependencyPath}
                    />
                  </TabsContent>
                ))
              )}
            </div>
          </ScrollArea>
        </Tabs>
        {error ? (
          <Alert variant="destructive" className="shrink-0">
            <AlertTriangle />
            <AlertTitle>Could not load or save pipeline settings</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter className="shrink-0">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={saving || loading || !draft}>
            {saving ? (
              <>
                <Loader2 className="size-4 animate-spin" />
                Saving…
              </>
            ) : (
              "Save changes"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function configResponseToDraft(config: {
  name?: string;
  schedule?: string;
  start_date?: string;
  owner?: string;
  tags?: string[];
  domains?: string[];
  default_connections?: PipelineConfigConnection[];
  inferred_default_connections?: PipelineConfigConnection[];
  catchup?: boolean;
  metadata_push_bigquery?: boolean;
  retries?: number;
  concurrency?: number;
  max_active_steps?: number;
  notifications_slack?: PipelineConfigDraft["notifications_slack"];
  notifications_teams?: PipelineConfigDraft["notifications_teams"];
  defaults?: PipelineConfigDraft["defaults"];
  variables?: PipelineConfigVariable[];
}): PipelineConfigDraft {
  const notification = (value?: PipelineConfigDraft["notifications_slack"]) => ({
    enabled: value?.enabled ?? false,
    channel: value?.channel ?? "",
    connection: value?.connection ?? "",
    success: value?.success ?? false,
    failure: value?.failure ?? true,
  });
  return {
    name: config.name ?? "",
    schedule: config.schedule ?? "",
    start_date: config.start_date ?? "",
    owner: config.owner ?? "",
    tags: config.tags ?? [],
    domains: config.domains ?? [],
    default_connections: config.default_connections ?? [],
    catchup: config.catchup ?? false,
    metadata_push_bigquery: config.metadata_push_bigquery ?? false,
    retries: config.retries ?? 0,
    concurrency: config.concurrency ?? 0,
    max_active_steps: config.max_active_steps,
    notifications_slack: notification(config.notifications_slack),
    notifications_teams: notification(config.notifications_teams),
    defaults: config.defaults ?? {},
    variables: config.variables ?? [],
  };
}

function PipelineSettingsSectionBody({
  section,
  draft,
  update,
  inferredDefaultConnections,
  referencedConnections,
  workspaceEnvironments,
  highlightedVariable,
  pythonDependencies,
  onPythonDependenciesChange,
  pythonDependencyPath,
}: {
  section: PipelineSettingsSection;
  draft: PipelineConfigDraft;
  update: <K extends keyof PipelineConfigDraft>(key: K, value: PipelineConfigDraft[K]) => void;
  inferredDefaultConnections: PipelineConfigConnection[];
  referencedConnections: ReferencedConnection[];
  workspaceEnvironments: WorkspaceConfigEnvironment[];
  highlightedVariable?: string;
  pythonDependencies: string[];
  onPythonDependenciesChange: (value: string[]) => void;
  pythonDependencyPath: string;
}) {
  const environment = useAtomValue(selectedEnvironmentAtom);
  const environmentConnections =
    workspaceEnvironments.find((item) => item.name === environment)?.connections ??
    workspaceEnvironments[0]?.connections ??
    [];
  if (section === "general") {
    return (
      <>
        <SettingsTextField
          label="Pipeline name"
          value={draft.name}
          onChange={(value) => update("name", value)}
          placeholder="my_pipeline"
        />
        <SettingsTextField
          label="Owner"
          value={draft.owner}
          onChange={(value) => update("owner", value)}
          placeholder="team@acme.io"
        />
        <SettingsMultiValueField
          label="Tags"
          value={draft.tags}
          onChange={(value) => update("tags", value)}
          placeholder="Add tag"
        />
        <SettingsMultiValueField
          label="Domains"
          value={draft.domains}
          onChange={(value) => update("domains", value)}
          placeholder="Add domain"
        />
        <FieldGroup className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <SettingsNumberField
            label="Retries"
            value={draft.retries}
            onChange={(value) => update("retries", value ?? 0)}
            min={0}
          />
          <SettingsNumberField
            label="Overlapping pipeline runs"
            value={draft.concurrency}
            onChange={(value) => update("concurrency", value ?? 0)}
            hint="Limits how many runs of this pipeline may overlap. This is separate from parallel assets inside one run."
            min={0}
          />
          <SettingsNumberField
            className="sm:col-span-2"
            label="Maximum active steps"
            value={draft.max_active_steps}
            onChange={(value) => update("max_active_steps", value)}
            hint="Leave blank to run one asset at a time. Values above 1 let independent assets overlap; dependencies, connections, and shared targets can still serialize them."
            min={1}
            placeholder="1"
          />
        </FieldGroup>
        <SettingsToggleField
          label="Push metadata to BigQuery"
          description="Sync asset metadata to BigQuery after each run."
          checked={draft.metadata_push_bigquery}
          onChange={(value) => update("metadata_push_bigquery", value)}
        />
      </>
    );
  }
  if (section === "schedule") {
    return (
      <>
        <SettingsTextField
          label="Schedule"
          value={draft.schedule}
          onChange={(value) => update("schedule", value)}
          placeholder="@daily"
          hint="A cron expression or preset like @daily / @hourly."
        />
        <SettingsTextField
          label="Start date"
          value={draft.start_date}
          onChange={(value) => update("start_date", value)}
          placeholder="2024-01-01"
        />
        <SettingsToggleField
          label="Catchup"
          description="Backfill every schedule interval missed since the start date."
          checked={draft.catchup}
          onChange={(value) => update("catchup", value)}
        />
        <div className="grid gap-3 border-t pt-4">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            Interval defaults
          </div>
          <SettingsNumberField
            label="Rerun cooldown (seconds)"
            value={draft.defaults.rerun_cooldown}
            onChange={(value) => update("defaults", { ...draft.defaults, rerun_cooldown: value })}
          />
          <div className="grid grid-cols-2 gap-3">
            <SettingsTextField
              label="Start offset"
              value={draft.defaults.start_offset_raw ?? ""}
              onChange={(value) =>
                update("defaults", { ...draft.defaults, start_offset_raw: value || undefined })
              }
              placeholder="-1d"
            />
            <SettingsTextField
              label="End offset"
              value={draft.defaults.end_offset_raw ?? ""}
              onChange={(value) =>
                update("defaults", { ...draft.defaults, end_offset_raw: value || undefined })
              }
              placeholder="0d"
            />
          </div>
        </div>
      </>
    );
  }
  if (section === "connections") {
    return (
      <FieldGroup className="gap-3">
        <p className="text-xs text-muted-foreground">
          Default connection per platform. Assets that don&apos;t name a connection use these.
        </p>
        <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          Pipeline overrides
        </div>
        {draft.default_connections.length === 0 ? (
          <p className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
            No overrides in pipeline.yml.
          </p>
        ) : (
          draft.default_connections.map((connection, index) => (
            <div
              key={index}
              className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-end gap-2"
            >
              <PipelineConnectionSelect
                label="Platform"
                hideLabel={index > 0}
                value={connection.platform}
                options={pipelinePlatformOptions(
                  environmentConnections,
                  draft.default_connections,
                  index,
                )}
                onChange={(value) => {
                  const firstConnection = connectionNamesForPlatform(
                    environmentConnections,
                    value,
                  )[0];
                  update(
                    "default_connections",
                    replaceAt(draft.default_connections, index, {
                      platform: value,
                      name: firstConnection ?? "",
                    }),
                  );
                }}
                placeholder="Choose platform"
                valuesAreConnectionTypes
              />
              <PipelineConnectionSelect
                label="Connection"
                hideLabel={index > 0}
                value={connection.name}
                options={connectionNamesForPlatform(environmentConnections, connection.platform)}
                onChange={(value) =>
                  update(
                    "default_connections",
                    replaceAt(draft.default_connections, index, { ...connection, name: value }),
                  )
                }
                placeholder="Choose connection"
                connectionType={connection.platform}
              />
              <PipelineConnectionSettingsLink
                environment={environment}
                connection={connection.name}
              />
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Remove connection"
                onClick={() =>
                  update("default_connections", removeAt(draft.default_connections, index))
                }
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))
        )}
        <Button
          variant="outline"
          size="sm"
          disabled={
            pipelinePlatformOptions(
              environmentConnections,
              draft.default_connections,
              draft.default_connections.length,
            ).length === 0
          }
          onClick={() => {
            const platform = pipelinePlatformOptions(
              environmentConnections,
              draft.default_connections,
              draft.default_connections.length,
            )[0];
            const connection = connectionNamesForPlatform(environmentConnections, platform)[0];
            if (!platform || !connection) return;
            update("default_connections", [
              ...draft.default_connections,
              { platform, name: connection },
            ]);
          }}
        >
          <Plus className="size-3.5" />
          Add connection
        </Button>
        {environmentConnections.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            Add a project connection for this environment before creating a pipeline override.
          </p>
        ) : null}
        {referencedConnections.length > 0 ? (
          <div className="grid gap-2 border-t pt-3">
            <div>
              <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Used by assets
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                Effective source and target connections resolved across this pipeline.
              </p>
            </div>
            {referencedConnections.map((connection) => (
              <div
                key={connection.name}
                data-testid="referenced-pipeline-connection"
                className="flex min-w-0 items-center gap-3 rounded-md border bg-muted/30 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-mono text-xs">{connection.name}</div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {connection.assets.slice(0, 3).map((assetName) => (
                      <Badge key={assetName} variant="secondary" className="max-w-full font-mono">
                        <span className="truncate">{assetName}</span>
                      </Badge>
                    ))}
                    {connection.assets.length > 3 ? (
                      <Badge variant="outline">+{connection.assets.length - 3}</Badge>
                    ) : null}
                  </div>
                </div>
                <PipelineConnectionSettingsLink
                  environment={environment}
                  connection={connection.name}
                />
              </div>
            ))}
          </div>
        ) : null}
        {inferredDefaultConnections.length > 0 ? (
          <div className="grid gap-2 border-t pt-3">
            <div>
              <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Inferred defaults
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                These are inferred from asset types when no pipeline override exists.
              </p>
            </div>
            {inferredDefaultConnections.map((connection) => (
              <div
                key={`${connection.platform}:${connection.name}`}
                data-testid="inferred-default-connection"
                className="flex items-center gap-3 rounded-md border bg-muted/30 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    Platform
                  </div>
                  <div className="truncate font-mono text-xs">{connection.platform}</div>
                </div>
                <div className="min-w-0 flex-1">
                  <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    Connection
                  </div>
                  <div className="truncate font-mono text-xs">{connection.name}</div>
                </div>
                <Badge variant="outline">Inferred</Badge>
                <PipelineConnectionSettingsLink
                  environment={environment}
                  connection={connection.name}
                />
              </div>
            ))}
          </div>
        ) : null}
      </FieldGroup>
    );
  }
  if (section === "python") {
    return (
      <div className="space-y-4">
        <div>
          <p className="text-sm font-medium">Pipeline dependencies</p>
          <p className="mt-1 text-xs text-muted-foreground">
            Packages are shared by Python assets in this pipeline and installed by uv on their next
            run.
          </p>
        </div>
        <SettingsMultiValueField
          label="Packages"
          value={pythonDependencies}
          onChange={onPythonDependenciesChange}
          placeholder="Add package, for example pandas>=2"
        />
        <p className="text-xs text-muted-foreground">
          Stored in <span className="font-mono">{pythonDependencyPath || "pyproject.toml"}</span>.
          Existing pipeline-level requirements.txt dependencies are migrated on save.
        </p>
      </div>
    );
  }
  if (section === "variables") {
    return (
      <FieldGroup className="gap-3">
        <p className="text-xs text-muted-foreground">
          Pipeline variables available to assets via{" "}
          <span className="font-mono">{"{{ var.name }}"}</span>.
        </p>
        {draft.variables.map((variable, index) => (
          <div
            key={index}
            data-pipeline-variable={variable.name}
            className={cn(
              "grid gap-3 rounded-md border p-3 transition-colors",
              highlightedVariable === variable.name &&
                "border-primary bg-primary/5 ring-2 ring-primary/20",
            )}
          >
            <div className="flex items-start gap-2">
              <div className="grid min-w-0 flex-1 grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_9rem]">
                <SettingsTextField
                  label="Name"
                  value={variable.name}
                  onChange={(value) =>
                    update(
                      "variables",
                      replaceAt(draft.variables, index, { ...variable, name: value }),
                    )
                  }
                  placeholder="lookback_days"
                />
                <VariableTypeField
                  label="Type"
                  value={variable.type}
                  onChange={(value) => {
                    const next = variableWithType(variable, value);
                    update("variables", replaceAt(draft.variables, index, next));
                  }}
                />
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                className="mt-6"
                aria-label="Remove variable"
                onClick={() => update("variables", removeAt(draft.variables, index))}
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
            <VariableDefaultField
              variable={variable}
              onChange={(defaultValue) =>
                update(
                  "variables",
                  replaceAt(draft.variables, index, {
                    ...variable,
                    default_value: defaultValue,
                  }),
                )
              }
            />
            <SettingsTextField
              label="Description"
              value={variable.description ?? ""}
              onChange={(value) =>
                update(
                  "variables",
                  replaceAt(draft.variables, index, {
                    ...variable,
                    description: value || undefined,
                  }),
                )
              }
            />
          </div>
        ))}
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            update("variables", [
              ...draft.variables,
              { name: "", type: "string", default_value: "" },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add variable
        </Button>
      </FieldGroup>
    );
  }
  return null;
}

function PipelineConnectionSettingsLink({
  environment,
  connection,
}: {
  environment?: string;
  connection: string;
}) {
  const name = connection.trim();
  if (!name) return null;
  return (
    <Button asChild variant="ghost" size="icon-sm">
      <Link
        to="/project/connections"
        search={{ environment: environment || undefined, connection: name }}
        aria-label={`Open ${name} in project connection settings`}
        title={`Open ${name} in project connection settings`}
      >
        <ExternalLink />
      </Link>
    </Button>
  );
}

function SettingsTextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  className,
}: {
  label?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  hint?: string;
  className?: string;
}) {
  const id = useId();
  return (
    <Field className={cn("gap-1.5", className)}>
      {label ? (
        <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
          {label}
        </FieldLabel>
      ) : null}
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
      />
      {hint ? <FieldDescription className="text-[11px]">{hint}</FieldDescription> : null}
    </Field>
  );
}

function PipelineConnectionSelect({
  label,
  hideLabel,
  value,
  options,
  onChange,
  placeholder,
  connectionType,
  valuesAreConnectionTypes = false,
}: {
  label: string;
  hideLabel?: boolean;
  value: string;
  options: string[];
  onChange: (value: string) => void;
  placeholder: string;
  connectionType?: string;
  valuesAreConnectionTypes?: boolean;
}) {
  const id = useId();
  const unavailable = Boolean(value && !options.includes(value));
  return (
    <Field className="min-w-0 gap-1.5">
      <FieldLabel
        htmlFor={id}
        className={cn("text-xs text-muted-foreground", hideLabel && "sr-only")}
      >
        {label}
      </FieldLabel>
      <ConnectionSelect
        value={value || undefined}
        groups={[
          {
            options: [
              ...(unavailable
                ? [
                    {
                      value,
                      label: value,
                      connectionType: valuesAreConnectionTypes ? value : connectionType,
                      badge: "unavailable",
                      badgeVariant: "destructive" as const,
                      disabled: true,
                    },
                  ]
                : []),
              ...options.map((option) => ({
                value: option,
                label: option,
                connectionType: valuesAreConnectionTypes ? option : connectionType,
              })),
            ],
          },
        ]}
        onValueChange={onChange}
        id={id}
        className="w-full"
        placeholder={placeholder}
      />
      {unavailable ? (
        <FieldDescription className="text-[11px] text-destructive">
          Choose a configured {label.toLocaleLowerCase()}.
        </FieldDescription>
      ) : null}
    </Field>
  );
}

function connectionNamesForPlatform(connections: WorkspaceConfigConnection[], platform?: string) {
  if (!platform) return [];
  return Array.from(
    new Set(
      connections
        .filter((connection) => connection.type === platform)
        .map((connection) => connection.name.trim())
        .filter(Boolean),
    ),
  ).sort((left, right) => left.localeCompare(right));
}

function pipelinePlatformOptions(
  connections: WorkspaceConfigConnection[],
  defaults: PipelineConfigConnection[],
  currentIndex: number,
) {
  const used = new Set(
    defaults
      .filter((_, index) => index !== currentIndex)
      .map((connection) => connection.platform.trim())
      .filter(Boolean),
  );
  return Array.from(
    new Set(
      connections
        .map((connection) => connection.type.trim())
        .filter((platform) => platform && !used.has(platform)),
    ),
  ).sort((left, right) => left.localeCompare(right));
}

function SettingsMultiValueField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <Field className="gap-1.5">
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </FieldLabel>
      <MultiValueInput id={id} value={value} onChange={onChange} placeholder={placeholder} />
    </Field>
  );
}

const variableTypes = ["string", "integer", "number", "boolean", "array", "object"] as const;

function VariableTypeField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const id = useId();
  return (
    <Field className="gap-1.5">
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </FieldLabel>
      <Select value={value || "string"} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder="Select a type" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            {variableTypes.map((type) => (
              <SelectItem key={type} value={type}>
                {type}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </Field>
  );
}

function VariableDefaultField({
  variable,
  onChange,
}: {
  variable: PipelineConfigVariable;
  onChange: (value: unknown) => void;
}) {
  const id = useId();
  const type = variable.type || "string";
  if (type === "boolean") {
    return (
      <Field className="gap-1.5">
        <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
          Default
        </FieldLabel>
        <Select
          value={variable.default_value === true ? "true" : "false"}
          onValueChange={(value) => onChange(value === "true")}
        >
          <SelectTrigger id={id} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="true">true</SelectItem>
              <SelectItem value="false">false</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
    );
  }
  if (type === "integer" || type === "number") {
    return (
      <Field className="gap-1.5">
        <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
          Default
        </FieldLabel>
        <Input
          id={id}
          type="number"
          step={type === "integer" ? 1 : "any"}
          value={
            typeof variable.default_value === "number" || typeof variable.default_value === "string"
              ? variable.default_value
              : ""
          }
          onChange={(event) => {
            const raw = event.target.value;
            if (raw === "") {
              onChange(null);
              return;
            }
            const value = Number(raw);
            onChange(type === "integer" ? Math.trunc(value) : value);
          }}
        />
        <FieldDescription className="text-[11px]">
          Stored as a {type === "integer" ? "whole number" : "number"}, not text.
        </FieldDescription>
      </Field>
    );
  }
  if (type === "array") {
    return (
      <Field className="gap-1.5">
        <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
          Default items
        </FieldLabel>
        <MultiValueInput
          id={id}
          value={
            Array.isArray(variable.default_value)
              ? variable.default_value.map(variableValueToText)
              : []
          }
          onChange={onChange}
          placeholder="Add item"
        />
        <FieldDescription className="text-[11px]">
          Add each string item separately; commas remain part of an item.
        </FieldDescription>
      </Field>
    );
  }
  if (type === "object") {
    return <JSONVariableDefaultField value={variable.default_value} onChange={onChange} />;
  }
  return (
    <Field className="gap-1.5">
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        Default
      </FieldLabel>
      <Input
        id={id}
        value={variableValueToText(variable.default_value)}
        onChange={(event) => onChange(event.target.value)}
        placeholder="Value used when no override is supplied"
      />
    </Field>
  );
}

function JSONVariableDefaultField({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const id = useId();
  const serialized = JSON.stringify(
    value && typeof value === "object" && !Array.isArray(value) ? value : {},
    null,
    2,
  );
  const [draft, setDraft] = useState(serialized);
  const [error, setError] = useState("");

  useEffect(() => {
    setDraft(serialized);
    setError("");
  }, [serialized]);

  return (
    <Field className="gap-1.5" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        Default object
      </FieldLabel>
      <Textarea
        id={id}
        value={draft}
        aria-invalid={error ? true : undefined}
        className="min-h-24 font-mono text-xs"
        onChange={(event) => {
          const next = event.target.value;
          setDraft(next);
          try {
            const parsed = JSON.parse(next);
            if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
              setError('Enter a JSON object, for example {"region": "eu"}.');
              return;
            }
            setError("");
            onChange(parsed);
          } catch {
            setError("Enter valid JSON.");
          }
        }}
      />
      <FieldDescription className={cn("text-[11px]", error && "text-destructive")}>
        {error || "Stored as a JSON object."}
      </FieldDescription>
    </Field>
  );
}

function variableWithType(variable: PipelineConfigVariable, type: string): PipelineConfigVariable {
  let defaultValue: unknown;
  switch (type) {
    case "integer": {
      const numeric = Number(variable.default_value);
      defaultValue = Number.isFinite(numeric) ? Math.trunc(numeric) : 0;
      break;
    }
    case "number": {
      const numeric = Number(variable.default_value);
      defaultValue = Number.isFinite(numeric) ? numeric : 0;
      break;
    }
    case "boolean":
      defaultValue =
        typeof variable.default_value === "boolean"
          ? variable.default_value
          : String(variable.default_value).toLowerCase() === "true";
      break;
    case "array":
      defaultValue = Array.isArray(variable.default_value) ? variable.default_value : [];
      break;
    case "object":
      defaultValue =
        variable.default_value &&
        typeof variable.default_value === "object" &&
        !Array.isArray(variable.default_value)
          ? variable.default_value
          : {};
      break;
    default:
      defaultValue = variableValueToText(variable.default_value);
  }

  const extra = { ...(variable.extra ?? {}) };
  if (type === "array" && !extra.items) {
    extra.items = { type: "string" };
  } else if (type !== "array") {
    delete extra.items;
  }
  return {
    ...variable,
    type,
    default_value: defaultValue,
    extra: Object.keys(extra).length > 0 ? extra : undefined,
  };
}

function SettingsNumberField({
  className,
  label,
  value,
  onChange,
  hint,
  min,
  placeholder,
}: {
  className?: string;
  label: string;
  value?: number;
  onChange: (value: number | undefined) => void;
  hint?: string;
  min?: number;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <Field className={cn("gap-1.5", className)}>
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </FieldLabel>
      <Input
        id={id}
        type="number"
        min={min}
        placeholder={placeholder}
        value={value ?? ""}
        onChange={(event) => {
          const raw = event.target.value.trim();
          onChange(raw === "" ? undefined : Number(raw));
        }}
      />
      {hint ? <FieldDescription className="text-[11px]">{hint}</FieldDescription> : null}
    </Field>
  );
}

function SettingsToggleField({
  label,
  description,
  checked,
  onChange,
  compact,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  compact?: boolean;
}) {
  if (compact) {
    return (
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={checked} onCheckedChange={onChange} />
        {label}
      </label>
    );
  }
  return (
    <div className="flex items-start justify-between gap-3">
      <span>
        <span className="block text-sm font-medium">{label}</span>
        {description ? <span className="text-xs text-muted-foreground">{description}</span> : null}
      </span>
      <Switch checked={checked} onCheckedChange={onChange} />
    </div>
  );
}

function replaceAt<T>(list: T[], index: number, value: T): T[] {
  return list.map((item, itemIndex) => (itemIndex === index ? value : item));
}

function removeAt<T>(list: T[], index: number): T[] {
  return list.filter((_, itemIndex) => itemIndex !== index);
}

function variableValueToText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  return String(value);
}

function sameStringArray(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error ? cause.message : fallback;
}
