import { Link, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  ArrowRight,
  Braces,
  CalendarClock,
  Database,
  ExternalLink,
  Gauge,
  Package,
  Plus,
  Settings2,
  Trash2,
  Wrench,
} from "lucide-react";
import { useCallback, useEffect, useId, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { ConnectionSelect } from "@/components/app/connection-select";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
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
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { useIsMobile } from "@/hooks/use-mobile";
import {
  usePipelineSettings,
  type PipelineConfigDraft,
  type PipelineSettingsValidation,
  type ReferencedPipelineConnection,
} from "@/hooks/use-pipeline-settings";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import type {
  PipelineConfigConnection,
  PipelineConfigVariable,
  WorkspaceConfigConnection,
  WorkspaceConfigEnvironment,
} from "@/lib/types";
import { cn } from "@/lib/utils";

import { MultiValueInput } from "./multi-value-input";

const pipelineSettingsSections = [
  {
    id: "general",
    label: "General",
    icon: Settings2,
    description: "Identity and catalog metadata stored with the pipeline.",
  },
  {
    id: "execution",
    label: "Execution",
    icon: Gauge,
    description: "Run limits and the default data interval used by manual and scheduled runs.",
  },
  {
    id: "connections",
    label: "Connections",
    icon: Database,
    description: "Pipeline-wide connection defaults and the connections used by its assets.",
  },
  {
    id: "variables",
    label: "Variables",
    icon: Braces,
    description: "Typed values that assets can reference from SQL, YAML, and Python.",
  },
  {
    id: "python",
    label: "Python",
    icon: Package,
    description: "Packages shared by the pipeline's Python assets.",
  },
  {
    id: "advanced",
    label: "Advanced",
    icon: Wrench,
    description: "Bruin compatibility settings and platform-specific metadata behavior.",
  },
] as const;

export type PipelineSettingsSection = (typeof pipelineSettingsSections)[number]["id"];

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
  const settings = usePipelineSettings(open, pipelineId);
  const isMobile = useIsMobile();
  const navigate = useNavigate();
  const [pendingDiscardAction, setPendingDiscardAction] = useState<"close" | "schedules" | null>(
    null,
  );
  const [invalidEditors, setInvalidEditors] = useState<Set<string>>(() => new Set());

  const setEditorValidity = useCallback((key: string, valid: boolean) => {
    setInvalidEditors((current) => {
      const next = new Set(current);
      if (valid) next.delete(key);
      else next.add(key);
      if (next.size === current.size && [...next].every((value) => current.has(value))) {
        return current;
      }
      return next;
    });
  }, []);

  useEffect(() => {
    if (!open) return;
    setSection(initialSection ?? "general");
    setInvalidEditors(new Set());
    setPendingDiscardAction(null);
  }, [open, pipelineId, initialSection]);

  useEffect(() => {
    if (!open || settings.loading || section !== "variables" || !highlightedVariable) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const target = Array.from(
        document.querySelectorAll<HTMLElement>("[data-pipeline-variable]"),
      ).find((element) => element.dataset.pipelineVariable === highlightedVariable);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [highlightedVariable, open, section, settings.draft, settings.loading]);

  const requestClose = () => {
    if (settings.dirty || invalidEditors.size > 0) {
      setPendingDiscardAction("close");
      return;
    }
    onOpenChange(false);
  };
  const scheduleSearch = { pipeline: settings.draft?.name || pipelineId };
  const confirmDiscard = () => {
    const action = pendingDiscardAction;
    setPendingDiscardAction(null);
    onOpenChange(false);
    if (action === "schedules") {
      void navigate({ to: "/schedules", search: scheduleSearch });
    }
  };
  const save = async () => {
    if (invalidEditors.size > 0 || !settings.validation.valid) return;
    if (await settings.save()) onOpenChange(false);
  };

  const hasUnsavedChanges = settings.dirty || invalidEditors.size > 0;
  const saveDisabled =
    settings.saving ||
    settings.loading ||
    !settings.draft ||
    !settings.dirty ||
    !settings.validation.valid ||
    invalidEditors.size > 0;

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          if (nextOpen) onOpenChange(true);
          else requestClose();
        }}
      >
        <DialogContent className="grid h-[min(46rem,calc(100dvh-2rem))] min-h-0 min-w-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle className="flex min-w-0 items-center gap-2">
              <Settings2 className="shrink-0 text-primary" />
              <span className="truncate">Pipeline settings</span>
              {hasUnsavedChanges ? <Badge variant="secondary">Unsaved</Badge> : null}
            </DialogTitle>
            <DialogDescription className="min-w-0 truncate">
              Version-controlled settings for{" "}
              <span className="font-mono">{settings.draft?.name || pipelineId}</span>.
            </DialogDescription>
          </DialogHeader>

          <Tabs
            value={section}
            onValueChange={(value) => setSection(value as PipelineSettingsSection)}
            orientation={isMobile ? "horizontal" : "vertical"}
            className="grid min-h-0 min-w-0 grid-rows-[auto_minmax(0,1fr)] gap-0 md:grid-cols-[11.5rem_minmax(0,1fr)] md:grid-rows-1"
          >
            <ScrollArea
              className="min-w-0 border-b md:h-full md:border-b-0"
              data-testid="pipeline-settings-navigation"
            >
              <TabsList
                variant="line"
                aria-label="Pipeline settings sections"
                className="h-10 w-max min-w-full justify-start rounded-none px-1 md:h-full md:min-h-full md:w-full md:min-w-0 md:items-stretch md:justify-start md:px-2 md:py-3"
              >
                {pipelineSettingsSections.map(({ id, label, icon: Icon }) => (
                  <TabsTrigger key={id} value={id} className="h-9 shrink-0 px-2.5 md:flex-none">
                    <Icon data-icon="inline-start" />
                    {label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </ScrollArea>

            <ScrollArea
              className="min-h-0 min-w-0 md:border-l"
              data-testid="pipeline-settings-content"
            >
              <div className="flex min-w-0 flex-col gap-5 px-1 py-4 sm:px-5">
                {settings.loading ? (
                  <PipelineSettingsSkeleton />
                ) : !settings.draft ? (
                  <Empty className="min-h-52 border">
                    <EmptyHeader>
                      <EmptyMedia variant="icon">
                        <AlertTriangle />
                      </EmptyMedia>
                      <EmptyTitle>Pipeline settings are unavailable</EmptyTitle>
                      <EmptyDescription>
                        Check the pipeline file and try opening settings again.
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                ) : (
                  <>
                    {settings.error ? (
                      <Alert variant="destructive">
                        <AlertTriangle />
                        <AlertTitle>Some settings could not be loaded or saved</AlertTitle>
                        <AlertDescription>{settings.error}</AlertDescription>
                      </Alert>
                    ) : null}
                    {pipelineSettingsSections.map((item) => (
                      <TabsContent key={item.id} value={item.id} className="m-0 min-w-0">
                        <PipelineSettingsSectionHeader section={item} />
                        <PipelineSettingsSectionBody
                          section={item.id}
                          draft={settings.draft!}
                          update={settings.update}
                          validation={settings.validation}
                          inferredDefaultConnections={settings.inferredDefaultConnections}
                          referencedConnections={settings.referencedConnections}
                          workspaceEnvironments={settings.workspaceEnvironments}
                          highlightedVariable={highlightedVariable}
                          pythonDependencies={settings.pythonDependencies}
                          onPythonDependenciesChange={settings.setPythonDependencies}
                          pythonDependencyPath={settings.pythonDependencyPath}
                          onEditorValidityChange={setEditorValidity}
                          onManageSchedules={() => {
                            if (hasUnsavedChanges) {
                              setPendingDiscardAction("schedules");
                              return;
                            }
                            onOpenChange(false);
                            void navigate({ to: "/schedules", search: scheduleSearch });
                          }}
                        />
                      </TabsContent>
                    ))}
                  </>
                )}
              </div>
            </ScrollArea>
          </Tabs>

          <DialogFooter className="border-t pt-4">
            <Button variant="outline" onClick={requestClose} disabled={settings.saving}>
              Cancel
            </Button>
            <Button onClick={() => void save()} disabled={saveDisabled}>
              {settings.saving ? <Spinner data-icon="inline-start" /> : null}
              {settings.saving ? "Saving…" : "Save changes"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={pendingDiscardAction !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPendingDiscardAction(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard unsaved pipeline settings?</AlertDialogTitle>
            <AlertDialogDescription>
              Your changes have not been written to the pipeline files.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep editing</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={confirmDiscard}>
              Discard changes
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function PipelineSettingsSkeleton() {
  return (
    <div className="flex flex-col gap-5" aria-label="Loading pipeline settings">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-5 w-36" />
        <Skeleton className="h-3 w-72 max-w-full" />
      </div>
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-9 w-32" />
    </div>
  );
}

function PipelineSettingsSectionHeader({
  section,
}: {
  section: (typeof pipelineSettingsSections)[number];
}) {
  const Icon = section.icon;
  return (
    <div className="mb-5 flex min-w-0 items-start gap-3">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
        <Icon />
      </div>
      <div className="min-w-0">
        <h3 className="text-sm font-semibold">{section.label}</h3>
        <p className="mt-0.5 text-xs/relaxed text-muted-foreground">{section.description}</p>
      </div>
    </div>
  );
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
  validation,
  onEditorValidityChange,
  onManageSchedules,
}: {
  section: PipelineSettingsSection;
  draft: PipelineConfigDraft;
  update: <K extends keyof PipelineConfigDraft>(key: K, value: PipelineConfigDraft[K]) => void;
  inferredDefaultConnections: PipelineConfigConnection[];
  referencedConnections: ReferencedPipelineConnection[];
  workspaceEnvironments: WorkspaceConfigEnvironment[];
  highlightedVariable?: string;
  pythonDependencies: string[];
  onPythonDependenciesChange: (value: string[]) => void;
  pythonDependencyPath: string;
  validation: PipelineSettingsValidation;
  onEditorValidityChange: (key: string, valid: boolean) => void;
  onManageSchedules: () => void;
}) {
  const environment = useAtomValue(selectedEnvironmentAtom);
  const workspaceEnvironment =
    workspaceEnvironments.find((item) => item.name === environment) ?? workspaceEnvironments[0];
  const effectiveEnvironment = workspaceEnvironment?.name ?? environment ?? "default";
  const environmentConnections = workspaceEnvironment?.connections ?? [];
  if (section === "general") {
    return (
      <FieldSet>
        <FieldLegend>Identity</FieldLegend>
        <FieldGroup>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <SettingsTextField
              label="Pipeline name"
              value={draft.name}
              onChange={(value) => update("name", value)}
              placeholder="my_pipeline"
              error={validation.pipelineName}
            />
            <SettingsTextField
              label="Owner"
              value={draft.owner}
              onChange={(value) => update("owner", value)}
              placeholder="team@acme.io"
            />
          </div>
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
        </FieldGroup>
      </FieldSet>
    );
  }
  if (section === "execution") {
    return (
      <FieldGroup>
        <Alert>
          <CalendarClock />
          <AlertTitle>Schedules are managed per environment</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between">
            <span>
              Renart schedules pin a deployment and keep production execution separate from these
              pipeline defaults.
            </span>
            <Button type="button" variant="outline" size="sm" onClick={onManageSchedules}>
              Manage schedules
              <ArrowRight data-icon="inline-end" />
            </Button>
          </AlertDescription>
        </Alert>

        <FieldSet>
          <FieldLegend>Run behavior</FieldLegend>
          <FieldGroup>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <SettingsNumberField
                label="Retries"
                value={draft.retries}
                onChange={(value) => update("retries", value ?? 0)}
                min={0}
                error={validation.retries}
              />
              <SettingsNumberField
                label="Overlapping pipeline runs"
                value={draft.concurrency}
                onChange={(value) => update("concurrency", value ?? 0)}
                hint="Limits how many runs of this pipeline may overlap."
                min={0}
                error={validation.concurrency}
              />
            </div>
            <SettingsNumberField
              label="Maximum active steps"
              value={draft.max_active_steps}
              onChange={(value) => update("max_active_steps", value)}
              hint="Leave blank to run one asset at a time. Higher values let independent assets overlap."
              min={1}
              placeholder="1"
              error={validation.maxActiveSteps}
            />
          </FieldGroup>
        </FieldSet>

        <FieldSet>
          <FieldLegend>Default data interval</FieldLegend>
          <FieldGroup>
            <SettingsTextField
              label="Start date"
              value={draft.start_date}
              onChange={(value) => update("start_date", value)}
              placeholder="2024-01-01"
              hint="Default beginning of the pipeline's data interval."
            />
            <SettingsNumberField
              label="Rerun cooldown (seconds)"
              value={draft.defaults.rerun_cooldown}
              onChange={(value) => update("defaults", { ...draft.defaults, rerun_cooldown: value })}
              min={0}
            />
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
          </FieldGroup>
        </FieldSet>
      </FieldGroup>
    );
  }
  if (section === "connections") {
    return (
      <FieldGroup>
        <Alert>
          <Database />
          <AlertTitle>Connection choices reflect {effectiveEnvironment}</AlertTitle>
          <AlertDescription>
            Pipeline defaults are stored in Git. The available connection names come from the
            selected workspace environment so you can verify them before saving.
          </AlertDescription>
        </Alert>
        <FieldSet>
          <FieldLegend>Pipeline overrides</FieldLegend>
          <FieldDescription>
            Assets that do not name a connection use the pipeline default for their platform.
          </FieldDescription>
          {draft.default_connections.length === 0 ? (
            <Empty className="min-h-36 border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Database />
                </EmptyMedia>
                <EmptyTitle>No pipeline overrides</EmptyTitle>
                <EmptyDescription>
                  Assets currently use their explicit connections or inferred platform defaults.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <FieldGroup>
              {draft.default_connections.map((connection, index) => (
                <div
                  key={index}
                  className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-end gap-2"
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
                    options={connectionNamesForPlatform(
                      environmentConnections,
                      connection.platform,
                    )}
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
                    environment={effectiveEnvironment}
                    connection={connection.name}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Remove connection"
                    onClick={() =>
                      update("default_connections", removeAt(draft.default_connections, index))
                    }
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
            </FieldGroup>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="w-fit"
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
            <Plus data-icon="inline-start" />
            Add connection
          </Button>
          {environmentConnections.length === 0 ? (
            <FieldDescription className="text-destructive">
              Add a project connection for this environment before creating a pipeline override.
            </FieldDescription>
          ) : null}
        </FieldSet>
        {referencedConnections.length > 0 ? (
          <FieldSet>
            <div>
              <FieldLegend>Used by assets</FieldLegend>
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
                  environment={effectiveEnvironment}
                  connection={connection.name}
                />
              </div>
            ))}
          </FieldSet>
        ) : null}
        {inferredDefaultConnections.length > 0 ? (
          <FieldSet>
            <div>
              <FieldLegend>Inferred defaults</FieldLegend>
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
                  environment={effectiveEnvironment}
                  connection={connection.name}
                />
              </div>
            ))}
          </FieldSet>
        ) : null}
      </FieldGroup>
    );
  }
  if (section === "python") {
    return (
      <FieldSet>
        <FieldLegend>Pipeline dependencies</FieldLegend>
        <FieldDescription>
          Packages are shared by Python assets in this pipeline and installed by uv on their next
          run.
        </FieldDescription>
        <FieldGroup>
          <SettingsMultiValueField
            label="Packages"
            value={pythonDependencies}
            onChange={onPythonDependenciesChange}
            placeholder="Add package, for example pandas>=2"
          />
          <FieldDescription>
            Stored in <span className="font-mono">{pythonDependencyPath || "pyproject.toml"}</span>.
            Existing pipeline-level requirements.txt dependencies are migrated on save.
          </FieldDescription>
        </FieldGroup>
      </FieldSet>
    );
  }
  if (section === "variables") {
    return (
      <FieldSet>
        <FieldLegend>Declared variables</FieldLegend>
        <FieldDescription>
          Pipeline variables are available to assets through{" "}
          <span className="font-mono">{"{{ var.name }}"}</span>.
        </FieldDescription>
        <FieldGroup>
          {draft.variables.length === 0 ? (
            <Empty className="min-h-36 border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Braces />
                </EmptyMedia>
                <EmptyTitle>No pipeline variables</EmptyTitle>
                <EmptyDescription>
                  Add a typed value when multiple assets need the same configurable input.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : null}
          {draft.variables.map((variable, index) => (
            <div
              key={index}
              data-pipeline-variable={variable.name}
              className={cn(
                "grid min-w-0 gap-4 rounded-lg border bg-card p-4 transition-colors",
                highlightedVariable === variable.name &&
                  "border-primary bg-primary/5 ring-2 ring-primary/20",
              )}
            >
              <div className="flex min-w-0 items-start gap-2">
                <div className="grid min-w-0 flex-1 grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_9rem]">
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
                    error={validation.variables[index]?.name}
                  />
                  <VariableTypeField
                    label="Type"
                    value={variable.type}
                    error={validation.variables[index]?.type}
                    onChange={(value) => {
                      const next = variableWithType(variable, value);
                      update("variables", replaceAt(draft.variables, index, next));
                    }}
                  />
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="mt-6"
                  aria-label={`Remove variable ${variable.name || index + 1}`}
                  onClick={() => update("variables", removeAt(draft.variables, index))}
                >
                  <Trash2 />
                </Button>
              </div>
              <VariableDefaultField
                variable={variable}
                validationKey={`variable-${index}-default`}
                onValidityChange={onEditorValidityChange}
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
            type="button"
            variant="outline"
            size="sm"
            className="w-fit"
            onClick={() =>
              update("variables", [
                ...draft.variables,
                { name: "", type: "string", default_value: "" },
              ])
            }
          >
            <Plus data-icon="inline-start" />
            Add variable
          </Button>
        </FieldGroup>
      </FieldSet>
    );
  }
  if (section === "advanced") {
    return (
      <FieldGroup>
        <Alert>
          <AlertTriangle />
          <AlertTitle>Bruin CLI schedule compatibility</AlertTitle>
          <AlertDescription>
            The schedule below is stored in pipeline.yml for Bruin CLI workflows. It does not create
            or update a Renart environment schedule.
          </AlertDescription>
        </Alert>
        <FieldSet>
          <FieldLegend>Legacy schedule</FieldLegend>
          <FieldGroup>
            <SettingsTextField
              label="Bruin schedule"
              value={draft.schedule}
              onChange={(value) => update("schedule", value)}
              placeholder="@daily"
              hint="A cron expression or preset such as @daily or @hourly."
              error={validation.legacySchedule}
            />
            <SettingsToggleField
              label="Catch up missed intervals"
              description="Backfill every missed Bruin schedule interval since the start date."
              checked={draft.catchup}
              onChange={(value) => update("catchup", value)}
            />
          </FieldGroup>
        </FieldSet>
        <FieldSet>
          <FieldLegend>Warehouse metadata</FieldLegend>
          <SettingsToggleField
            label="Push metadata to BigQuery"
            description="Sync asset metadata to BigQuery after each run."
            checked={draft.metadata_push_bigquery}
            onChange={(value) => update("metadata_push_bigquery", value)}
          />
        </FieldSet>
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
  error,
  className,
}: {
  label?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  hint?: string;
  error?: string;
  className?: string;
}) {
  const id = useId();
  return (
    <Field className={cn("gap-1.5", className)} data-invalid={error ? true : undefined}>
      {label ? (
        <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
          {label}
        </FieldLabel>
      ) : null}
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        aria-invalid={error ? true : undefined}
        onChange={(event) => onChange(event.target.value)}
      />
      {hint ? <FieldDescription className="text-[11px]">{hint}</FieldDescription> : null}
      <FieldError>{error}</FieldError>
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
  error,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  error?: string;
}) {
  const id = useId();
  return (
    <Field className="gap-1.5" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </FieldLabel>
      <Select value={value || "string"} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-full" aria-invalid={error ? true : undefined}>
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
      <FieldError>{error}</FieldError>
    </Field>
  );
}

function VariableDefaultField({
  variable,
  onChange,
  validationKey,
  onValidityChange,
}: {
  variable: PipelineConfigVariable;
  onChange: (value: unknown) => void;
  validationKey: string;
  onValidityChange: (key: string, valid: boolean) => void;
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
    return (
      <JSONVariableDefaultField
        value={variable.default_value}
        onChange={onChange}
        validationKey={validationKey}
        onValidityChange={onValidityChange}
      />
    );
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
  validationKey,
  onValidityChange,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
  validationKey: string;
  onValidityChange: (key: string, valid: boolean) => void;
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
    onValidityChange(validationKey, true);
    return () => onValidityChange(validationKey, true);
  }, [onValidityChange, serialized, validationKey]);

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
              onValidityChange(validationKey, false);
              return;
            }
            setError("");
            onValidityChange(validationKey, true);
            onChange(parsed);
          } catch {
            setError("Enter valid JSON.");
            onValidityChange(validationKey, false);
          }
        }}
      />
      {error ? <FieldError>{error}</FieldError> : null}
      {!error ? <FieldDescription>Stored as a JSON object.</FieldDescription> : null}
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
  error,
}: {
  className?: string;
  label: string;
  value?: number;
  onChange: (value: number | undefined) => void;
  hint?: string;
  min?: number;
  placeholder?: string;
  error?: string;
}) {
  const id = useId();
  return (
    <Field className={cn("gap-1.5", className)} data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </FieldLabel>
      <Input
        id={id}
        type="number"
        min={min}
        placeholder={placeholder}
        value={value ?? ""}
        aria-invalid={error ? true : undefined}
        onChange={(event) => {
          const raw = event.target.value.trim();
          onChange(raw === "" ? undefined : Number(raw));
        }}
      />
      {hint ? <FieldDescription className="text-[11px]">{hint}</FieldDescription> : null}
      <FieldError>{error}</FieldError>
    </Field>
  );
}

function SettingsToggleField({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  const id = useId();
  return (
    <Field orientation="horizontal">
      <div className="min-w-0 flex-1">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {description ? <FieldDescription>{description}</FieldDescription> : null}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </Field>
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
