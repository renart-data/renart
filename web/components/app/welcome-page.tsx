import { Link } from "@tanstack/react-router";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Database,
  FileCode,
  FolderPlus,
  Import,
  LoaderCircle,
  Play,
  Rocket,
  Sparkles,
  XCircle,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { AnsiOutput } from "@/components/ansi-output";
import { DirectoryPickerDialog } from "@/components/app/directory-picker-dialog";
import { TemplateCatalog } from "@/components/app/template-catalog";
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { useFollowOutputScroll } from "@/hooks/use-follow-output-scroll";
import {
  createWorkspaceConnection,
  getWorkspaceConfig,
  updateWorkspaceConnection,
} from "@/lib/api-config";
import { importOnboardingDatabase, previewOnboardingDiscovery } from "@/lib/api-onboarding";
import {
  browseProjectDirs,
  createProject,
  getProjectTemplates,
  listProjects,
} from "@/lib/api-projects";
import { buildStalePipelineStream } from "@/lib/api-staleness";
import type { StreamAssetEvent } from "@/lib/api-streams";
import { getWorkspace } from "@/lib/api-workspace";
import { splitConnectionDraftValues } from "@/lib/settings-form-utils";
import type { CreateProjectResponse, ProjectTemplateInfo } from "@/lib/generated/api-types";
import type {
  OnboardingDiscoveryResponse,
  OnboardingImportResponse,
  WorkspaceConfigConnectionType,
} from "@/lib/types";
import { pinProject } from "@/lib/project-context";
import { cn } from "@/lib/utils";

type WelcomeIntent = "demo" | "import" | "empty";

type WelcomeStage =
  | "choose"
  | "target"
  | "creating"
  | "materializing"
  | "import-connect"
  | "import-tables"
  | "done";

const IMPORT_ENVIRONMENT = "default";

// The welcome flow is the first-run onboarding screen and the "New project"
// wizard behind the project switcher. It scaffolds a template via the
// process-level create-project endpoint, pins the new project to this tab,
// and bootstraps demos with the build-stale stream (all assets of a fresh
// pipeline are never_built, so that one run materializes everything with
// per-asset progress).
export function WelcomePage({ forceNew = false }: { forceNew?: boolean }) {
  useWorkspaceTheme();

  const [templates, setTemplates] = useState<ProjectTemplateInfo[]>([]);
  const [workspaceEmpty, setWorkspaceEmpty] = useState<boolean | null>(null);
  const [bootstrapMode, setBootstrapMode] = useState<boolean | null>(null);
  const [workspacePath, setWorkspacePath] = useState("");
  const [connectionTypes, setConnectionTypes] = useState<WorkspaceConfigConnectionType[]>([]);

  const [stage, setStage] = useState<WelcomeStage>("choose");
  const [intent, setIntent] = useState<WelcomeIntent>("demo");
  const [demoId, setDemoId] = useState("");
  const [projectName, setProjectName] = useState("");
  const [parentDir, setParentDir] = useState("");
  const [parentDirLoading, setParentDirLoading] = useState(true);
  const [directoryPickerOpen, setDirectoryPickerOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreateProjectResponse | null>(null);
  const [tickedFiles, setTickedFiles] = useState(0);

  const [assetEvents, setAssetEvents] = useState<Record<string, StreamAssetEvent>>({});
  const [runProgress, setRunProgress] = useState<{ step: number; total: number } | null>(null);
  const [runLog, setRunLog] = useState("");
  const [runState, setRunState] = useState<"idle" | "running" | "done" | "error">("idle");
  const runLogScroll = useFollowOutputScroll(runLog, created?.pipeline_id);

  const [connectionType, setConnectionType] = useState("");
  const [connectionValues, setConnectionValues] = useState<Record<string, string | boolean>>({});
  const [connectionSecretModes, setConnectionSecretModes] = useState<
    Record<string, "local" | "env">
  >({});
  const [discovery, setDiscovery] = useState<OnboardingDiscoveryResponse | null>(null);
  const [selectedDatabase, setSelectedDatabase] = useState("");
  const [selectedTables, setSelectedTables] = useState<Set<string>>(new Set());
  const [tableFilter, setTableFilter] = useState("");
  const [pipelineName, setPipelineName] = useState("analytics");
  const [importResult, setImportResult] = useState<OnboardingImportResponse | null>(null);

  useEffect(() => {
    void getProjectTemplates()
      .then((response) => {
        setTemplates(response.templates);
        const firstDemo = response.templates.find((template) => template.id.startsWith("demo:"));
        setDemoId((current) => current || firstDemo?.id || "");
      })
      .catch(() => setTemplates([]));
    void listProjects()
      .then((response) => setBootstrapMode(response.bootstrap))
      .catch(() => setBootstrapMode(true));
    void getWorkspace()
      .then((workspace) => setWorkspaceEmpty(workspace.pipelines.length === 0))
      .catch(() => setWorkspaceEmpty(false));
    void getWorkspaceConfig()
      .then((config) => {
        setWorkspacePath(config.workspace_path ?? "");
        setConnectionTypes(config.connection_types.filter((type) => type.category === "warehouse"));
      })
      .catch(() => {});
    void browseProjectDirs(undefined, "create")
      .then((response) => setParentDir((current) => current || response.path))
      .catch(() => {})
      .finally(() => setParentDirLoading(false));
  }, []);

  // Scaffolding into the open (empty) workspace is the first-run default;
  // "New project" from the switcher always creates a fresh directory.
  const inPlace = !forceNew && bootstrapMode === false && workspaceEmpty === true;
  const demoTemplates = useMemo(
    () => templates.filter((template) => template.id.startsWith("demo:")),
    [templates],
  );
  const demoCatalogItems = useMemo(
    () =>
      demoTemplates.map((template) => ({
        id: template.id,
        title: template.title,
        description: template.description,
        category: template.category,
        offline: template.offline,
        features: template.features,
        assetNames: template.asset_names,
      })),
    [demoTemplates],
  );
  const selectedTemplateId = intent === "demo" ? demoId : intent === "import" ? "bare" : "empty";

  const pickIntent = (nextIntent: WelcomeIntent) => {
    setIntent(nextIntent);
    setError(null);
    if (nextIntent === "import" && inPlace) {
      // The open workspace already is the project; connect straight away.
      setStage("import-connect");
      return;
    }
    if (!projectName) {
      setProjectName(nextIntent === "demo" ? demoSuggestedName(demoId) : "analytics");
    }
    setStage("target");
  };

  const handleCreate = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await createProject({
        template: selectedTemplateId,
        ...(inPlace
          ? { path: workspacePath }
          : { name: projectName.trim(), parent_dir: parentDir.trim() || undefined }),
      });
      if (!inPlace) {
        // Route every follow-up call (workspace, discovery, import, run)
        // onto the new project's API mount for this tab.
        pinProject(response.project.default ? null : response.project.id);
      }
      setCreated(response);
      setTickedFiles(0);
      setStage("creating");
    } catch (createError) {
      setError(
        createError instanceof Error ? createError.message : "Failed to create the project.",
      );
    } finally {
      setBusy(false);
    }
  }, [inPlace, parentDir, projectName, selectedTemplateId, workspacePath]);

  // The "creating..." checklist ticks files one by one, then hands over to
  // the intent's next step.
  useEffect(() => {
    if (stage !== "creating" || !created) return;
    if (tickedFiles < created.files.length) {
      const timer = window.setTimeout(() => setTickedFiles((count) => count + 1), 120);
      return () => window.clearTimeout(timer);
    }
    const timer = window.setTimeout(() => {
      if (intent === "demo") {
        setStage("materializing");
      } else if (intent === "import") {
        setStage("import-connect");
      } else {
        setStage("done");
      }
    }, 500);
    return () => window.clearTimeout(timer);
  }, [created, intent, stage, tickedFiles]);

  const startMaterialize = useCallback(async () => {
    if (!created) return;
    setRunState("running");
    setAssetEvents({});
    setRunProgress(null);
    setRunLog("");
    setError(null);
    try {
      // Project creation pins the new runtime before this stage starts. Resolve
      // its selected environment explicitly so the run facts and the Build
      // area's staleness selection use the same identity.
      const workspace = await getWorkspace();
      const environment = workspace.selected_environment.trim();
      if (!environment) {
        throw new Error("The project has no selected environment for its first build.");
      }
      const payload = await buildStalePipelineStream(
        created.pipeline_id,
        {
          onChunk: (chunk) => setRunLog((log) => (log + chunk).slice(-20000)),
          onAssetEvent: (event) => {
            if (event.asset_name) {
              setAssetEvents((events) => ({ ...events, [event.asset_name as string]: event }));
            }
            if (event.step && event.total) {
              setRunProgress({ step: event.step, total: event.total });
            }
          },
        },
        { environment },
      );
      if (payload?.status === "ok") {
        setRunState("done");
        setStage("done");
      } else {
        setRunState("error");
        setError(
          payload?.error ||
            "The first run failed. You can retry or open the workspace and run assets individually.",
        );
      }
    } catch (runError) {
      setRunState("error");
      setError(runError instanceof Error ? runError.message : "The first run failed.");
    }
  }, [created]);

  useEffect(() => {
    if (stage === "materializing" && runState === "idle") {
      void startMaterialize();
    }
  }, [runState, stage, startMaterialize]);

  const runDiscovery = useCallback(
    async (database?: string) => {
      setBusy(true);
      setError(null);
      try {
        const values: Record<string, unknown> = { ...connectionValues };
        const connectionTypeDef = connectionTypes.find((type) => type.type_name === connectionType);
        const draft = splitConnectionDraftValues(connectionTypeDef, values, connectionSecretModes);
        const response = await previewOnboardingDiscovery({
          environment_name: IMPORT_ENVIRONMENT,
          type: connectionType,
          values: draft.values,
          secret_changes: draft.secretChanges,
          database,
        });
        if (response.status === "error") {
          setError(response.error || "Could not connect.");
          return;
        }
        setDiscovery(response);
        setSelectedDatabase(database ?? response.selected_database ?? "");
        setSelectedTables(new Set());
        if (database ?? response.selected_database) {
          // Discovery only previews the connection; the import step reads the
          // saved workspace config, so persist the connection now.
          await persistImportConnection(
            connectionTypeDef,
            values,
            connectionSecretModes,
            database ?? response.selected_database ?? "",
          );
          setStage("import-tables");
        }
      } catch (discoveryError) {
        setError(discoveryError instanceof Error ? discoveryError.message : "Could not connect.");
      } finally {
        setBusy(false);
      }
    },
    [connectionSecretModes, connectionType, connectionValues],
  );

  const handleImport = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await importOnboardingDatabase({
        connection_name: `${connectionType}-default`,
        environment_name: IMPORT_ENVIRONMENT,
        pipeline_name: pipelineName.trim() || "analytics",
        tables: [...selectedTables],
        create_if_missing: true,
      });
      if (response.status === "error") {
        setError(response.error || "Import failed.");
        return;
      }
      setImportResult(response);
      setStage("done");
    } catch (importError) {
      setError(importError instanceof Error ? importError.message : "Import failed.");
    } finally {
      setBusy(false);
    }
  }, [connectionType, pipelineName, selectedTables]);

  const openWorkspace = () => window.location.assign("/");

  return (
    <div className="flex min-h-dvh flex-col items-center overflow-auto bg-muted/40 px-4 py-10 text-foreground">
      <div
        className={cn(
          "w-full",
          stage === "target" && intent === "demo" ? "max-w-3xl" : "max-w-2xl",
        )}
      >
        <header className="mb-8 flex flex-col items-center gap-2 text-center">
          <img src="/icons/icon.svg" alt="" aria-hidden className="size-12 rounded-xl" />
          <h1 className="text-2xl font-semibold tracking-tight">
            {stage === "choose" ? "Welcome to renart" : stageTitle(stage, intent)}
          </h1>
          <p className="max-w-md text-sm text-muted-foreground">
            {stageSubtitle(stage, inPlace, workspacePath)}
          </p>
        </header>

        {error && stage !== "target" ? (
          <Alert variant="destructive" className="mb-4">
            <CircleAlert />
            <AlertTitle>Something went wrong</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {stage === "choose" && workspaceEmpty === null ? (
          <div className="flex justify-center py-10">
            <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : null}

        {stage === "choose" && workspaceEmpty !== null ? (
          <div className="grid gap-3">
            <OptionCard
              icon={Sparkles}
              title="Start from a demo"
              description="A small working pipeline with sample data — materialized on creation so you can explore a live workspace."
              onClick={() => pickIntent("demo")}
            />
            <OptionCard
              icon={Import}
              title="Import existing tables"
              description="Connect a database, pick tables, and generate source assets to build on."
              onClick={() => pickIntent("import")}
            />
            <OptionCard
              icon={FolderPlus}
              title="Start empty"
              description="A minimal pipeline with one example SQL asset against local DuckDB."
              onClick={() => pickIntent("empty")}
            />
            {workspaceEmpty === false ? (
              <Button variant="ghost" size="sm" className="mx-auto mt-2" asChild>
                <Link to="/">
                  <ArrowLeft data-icon="inline-start" />
                  Back to workspace
                </Link>
              </Button>
            ) : null}
          </div>
        ) : null}

        {stage === "target" ? (
          <div className="grid gap-4 rounded-xl border bg-background p-5">
            {intent === "demo" ? (
              <div className="grid gap-2">
                <Label>Demo</Label>
                <TemplateCatalog
                  items={demoCatalogItems}
                  selectedId={demoId}
                  ariaLabel="Demo pipeline"
                  onSelect={(template) => {
                    setError(null);
                    setDemoId(template.id);
                    setProjectName((current) =>
                      !current || current === demoSuggestedName(demoId)
                        ? demoSuggestedName(template.id)
                        : current,
                    );
                  }}
                />
              </div>
            ) : null}

            {inPlace ? (
              <div className="grid gap-1.5">
                <Label>Project</Label>
                <div
                  className="truncate rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-xs"
                  title={workspacePath}
                >
                  {workspacePath || "current workspace"}
                </div>
                <p className="text-xs text-muted-foreground">
                  Files are created in the workspace this server was started on.
                </p>
              </div>
            ) : (
              <>
                <div className="grid gap-1.5">
                  <Label htmlFor="welcome-project-name">Project name</Label>
                  <Input
                    id="welcome-project-name"
                    value={projectName}
                    onChange={(event) => {
                      setProjectName(event.target.value);
                      setError(null);
                    }}
                    placeholder={intent === "demo" ? demoSuggestedName(demoId) : "analytics"}
                    autoFocus
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="welcome-project-location">Location</Label>
                  <Button
                    id="welcome-project-location"
                    type="button"
                    variant="outline"
                    className="min-w-0 justify-start"
                    aria-label="Choose project location"
                    title={parentDir || undefined}
                    disabled={parentDirLoading}
                    onClick={() => setDirectoryPickerOpen(true)}
                  >
                    {parentDirLoading ? (
                      <LoaderCircle data-icon="inline-start" className="animate-spin" />
                    ) : (
                      <FolderPlus data-icon="inline-start" />
                    )}
                    <span className="truncate font-mono text-xs">
                      {parentDirLoading
                        ? "Loading suggested location..."
                        : parentDir || "Choose a directory"}
                    </span>
                  </Button>
                  <p className="text-xs text-muted-foreground">
                    A new {projectName.trim() || "project"} directory with its own git repository is
                    created inside this folder.
                  </p>
                </div>
              </>
            )}

            {error ? (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>Project could not be created</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}

            <div className="flex justify-between">
              <Button
                variant="ghost"
                onClick={() => {
                  setError(null);
                  setStage("choose");
                }}
                disabled={busy}
              >
                <ArrowLeft data-icon="inline-start" />
                Back
              </Button>
              <Button
                onClick={() => void handleCreate()}
                disabled={
                  busy ||
                  (!inPlace && (!projectName.trim() || parentDirLoading)) ||
                  (intent === "demo" && !demoId)
                }
              >
                {busy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <Rocket data-icon="inline-start" />
                )}
                Create project
              </Button>
            </div>
          </div>
        ) : null}

        {stage === "creating" && created ? (
          <div className="grid gap-2 rounded-xl border bg-background p-5">
            {created.files.map((file, index) => (
              <div
                key={file}
                className={cn(
                  "flex items-center gap-2 font-mono text-xs transition-opacity",
                  index < tickedFiles ? "opacity-100" : "opacity-30",
                )}
              >
                {index < tickedFiles ? (
                  <CheckCircle2 className="size-3.5 shrink-0 text-primary" />
                ) : (
                  <FileCode className="size-3.5 shrink-0 text-muted-foreground" />
                )}
                {file}
              </div>
            ))}
            {created.git_initialized && tickedFiles >= created.files.length ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <CheckCircle2 className="size-3.5 shrink-0 text-primary" />
                git repository initialized with an initial commit
              </div>
            ) : null}
          </div>
        ) : null}

        {stage === "materializing" && created ? (
          <div className="grid gap-4 rounded-xl border bg-background p-5">
            <div className="grid gap-2">
              <div className="flex items-center justify-between text-sm">
                <span className="flex items-center gap-2 font-medium">
                  {runState === "running" ? (
                    <LoaderCircle className="size-4 animate-spin text-primary" />
                  ) : runState === "error" ? (
                    <XCircle className="size-4 text-destructive" />
                  ) : (
                    <CheckCircle2 className="size-4 text-primary" />
                  )}
                  {runState === "running"
                    ? "Materializing demo assets..."
                    : runState === "error"
                      ? "First run failed"
                      : "Demo materialized"}
                </span>
                {runProgress ? (
                  <span className="text-xs text-muted-foreground">
                    {Math.min(runProgress.step, runProgress.total)} / {runProgress.total}
                  </span>
                ) : null}
              </div>
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                  className={cn(
                    "h-full rounded-full transition-all duration-300",
                    runState === "error" ? "bg-destructive" : "bg-primary",
                  )}
                  style={{
                    width: runProgress
                      ? `${Math.round((Math.min(runProgress.step, runProgress.total) / Math.max(runProgress.total, 1)) * 100)}%`
                      : "5%",
                  }}
                />
              </div>
            </div>
            <div className="grid gap-1.5">
              {Object.values(assetEvents).map((event) => (
                <div key={event.asset_name} className="flex items-center gap-2 text-xs">
                  <AssetStatusIcon status={event.status} />
                  <span className="font-mono">{event.asset_name}</span>
                </div>
              ))}
            </div>
            {runLog ? (
              <ScrollArea
                className="h-64 rounded-md border bg-zinc-950"
                viewportClassName="max-h-64"
                viewportRef={runLogScroll.viewportRef}
                onViewportScroll={runLogScroll.onViewportScroll}
              >
                <AnsiOutput
                  output={runLog}
                  className="whitespace-pre-wrap p-3 font-mono text-[11px] leading-relaxed text-zinc-300"
                />
              </ScrollArea>
            ) : null}
            {runState === "error" ? (
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={openWorkspace}>
                  Open workspace anyway
                </Button>
                <Button onClick={() => void startMaterialize()}>
                  <Play data-icon="inline-start" />
                  Retry run
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}

        {stage === "import-connect" ? (
          <div className="grid gap-4 rounded-xl border bg-background p-5">
            <div className="grid gap-1.5">
              <Label>Database type</Label>
              <Select
                value={connectionType}
                onValueChange={(value) => {
                  setConnectionType(value);
                  setConnectionValues(
                    defaultConnectionValues(
                      connectionTypes.find((type) => type.type_name === value),
                    ),
                  );
                  setConnectionSecretModes(
                    defaultConnectionSecretModes(
                      connectionTypes.find((type) => type.type_name === value),
                    ),
                  );
                  setDiscovery(null);
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select a database type" />
                </SelectTrigger>
                <SelectContent>
                  {connectionTypes.map((type) => (
                    <SelectItem key={type.type_name} value={type.type_name}>
                      {type.type_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {connectionTypes
              .find((type) => type.type_name === connectionType)
              ?.fields.map((field) => (
                <div key={field.name} className="grid gap-1.5">
                  {field.type === "bool" ? (
                    <label className="flex items-center justify-between gap-2 text-sm">
                      <span className="font-medium">{field.name}</span>
                      <Switch
                        checked={connectionValues[field.name] === true}
                        onCheckedChange={(checked) =>
                          setConnectionValues((values) => ({ ...values, [field.name]: checked }))
                        }
                      />
                    </label>
                  ) : (
                    <>
                      <Label htmlFor={`welcome-field-${field.name}`}>
                        {field.name}
                        {field.is_required ? <span className="text-destructive"> *</span> : null}
                      </Label>
                      <Input
                        id={`welcome-field-${field.name}`}
                        type={
                          field.type === "int"
                            ? "number"
                            : field.is_sensitive &&
                                !field.is_sensitive_file &&
                                connectionSecretModes[field.name] !== "env"
                              ? "password"
                              : "text"
                        }
                        value={String(connectionValues[field.name] ?? "")}
                        onChange={(event) =>
                          setConnectionValues((values) => ({
                            ...values,
                            [field.name]: event.target.value,
                          }))
                        }
                        placeholder={
                          connectionSecretModes[field.name] === "env"
                            ? "Environment variable name"
                            : field.default_value || undefined
                        }
                      />
                      {field.is_sensitive || field.is_sensitive_file ? (
                        <div className="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center">
                          <ToggleGroup
                            type="single"
                            variant="outline"
                            size="sm"
                            spacing={0}
                            value={connectionSecretModes[field.name] ?? "local"}
                            aria-label={`${field.name} secret source`}
                            onValueChange={(nextMode) => {
                              if (nextMode !== "local" && nextMode !== "env") {
                                return;
                              }
                              setConnectionSecretModes((modes) => ({
                                ...modes,
                                [field.name]: nextMode,
                              }));
                              setConnectionValues((values) => ({
                                ...values,
                                [field.name]: "",
                              }));
                            }}
                          >
                            <ToggleGroupItem value="local">
                              {field.is_sensitive_file ? "File path" : "Credential store"}
                            </ToggleGroupItem>
                            <ToggleGroupItem value="env">Environment</ToggleGroupItem>
                          </ToggleGroup>
                          <p className="text-xs text-muted-foreground">
                            {connectionSecretModes[field.name] === "env"
                              ? "Only the variable name is saved; its value stays in the environment."
                              : field.is_sensitive_file
                                ? "The credential file path is kept write-only."
                                : "The value is saved in your operating system credential store."}
                          </p>
                        </div>
                      ) : null}
                    </>
                  )}
                </div>
              ))}
            {discovery && !selectedDatabase && discovery.databases.length > 0 ? (
              <div className="grid gap-1.5">
                <Label>Database</Label>
                <Select
                  value={selectedDatabase}
                  onValueChange={(value) => void runDiscovery(value)}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select a database" />
                  </SelectTrigger>
                  <SelectContent>
                    {discovery.databases.map((database) => (
                      <SelectItem key={database} value={database}>
                        {database}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : null}
            <div className="flex justify-between">
              {created ? (
                <span />
              ) : (
                <Button variant="ghost" disabled={busy} onClick={() => setStage("choose")}>
                  <ArrowLeft data-icon="inline-start" />
                  Back
                </Button>
              )}
              <Button disabled={busy || !connectionType} onClick={() => void runDiscovery()}>
                {busy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <Database data-icon="inline-start" />
                )}
                Connect
              </Button>
            </div>
          </div>
        ) : null}

        {stage === "import-tables" && discovery ? (
          <div className="grid gap-4 rounded-xl border bg-background p-5">
            <div className="grid gap-1.5">
              <Label htmlFor="welcome-pipeline-name">Pipeline name</Label>
              <Input
                id="welcome-pipeline-name"
                value={pipelineName}
                onChange={(event) => setPipelineName(event.target.value)}
              />
            </div>
            <div className="grid gap-1.5">
              <div className="flex items-center justify-between">
                <Label>Tables ({selectedTables.size} selected)</Label>
                <button
                  type="button"
                  className="text-xs text-primary hover:underline"
                  onClick={() =>
                    setSelectedTables((current) =>
                      current.size === discovery.tables.length
                        ? new Set()
                        : new Set(discovery.tables.map((table) => table.name)),
                    )
                  }
                >
                  {selectedTables.size === discovery.tables.length ? "Clear all" : "Select all"}
                </button>
              </div>
              <Input
                value={tableFilter}
                onChange={(event) => setTableFilter(event.target.value)}
                placeholder="Filter tables..."
              />
              <ScrollArea className="h-56 rounded-md border" viewportClassName="max-h-56">
                <div className="flex flex-col">
                  {discovery.tables
                    .filter((table) => table.name.toLowerCase().includes(tableFilter.toLowerCase()))
                    .map((table) => (
                      <label
                        key={table.name}
                        className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-sm last:border-b-0 hover:bg-muted/50"
                      >
                        <Checkbox
                          checked={selectedTables.has(table.name)}
                          onCheckedChange={(checked) =>
                            setSelectedTables((current) => {
                              const next = new Set(current);
                              if (checked) {
                                next.add(table.name);
                              } else {
                                next.delete(table.name);
                              }
                              return next;
                            })
                          }
                        />
                        <span className="min-w-0 flex-1 truncate font-mono text-xs">
                          {table.name}
                        </span>
                      </label>
                    ))}
                  {discovery.tables.length === 0 ? (
                    <p className="px-3 py-4 text-sm text-muted-foreground">
                      No tables found in this database.
                    </p>
                  ) : null}
                </div>
              </ScrollArea>
            </div>
            <div className="flex justify-between">
              <Button variant="ghost" disabled={busy} onClick={() => setStage("import-connect")}>
                <ArrowLeft data-icon="inline-start" />
                Back
              </Button>
              <Button
                disabled={busy || selectedTables.size === 0 || !pipelineName.trim()}
                onClick={() => void handleImport()}
              >
                {busy ? (
                  <LoaderCircle data-icon="inline-start" className="animate-spin" />
                ) : (
                  <Import data-icon="inline-start" />
                )}
                Import {selectedTables.size} table{selectedTables.size === 1 ? "" : "s"}
              </Button>
            </div>
          </div>
        ) : null}

        {stage === "done" ? (
          <div className="grid gap-4 rounded-xl border bg-background p-5 text-center">
            <CheckCircle2 className="mx-auto size-10 text-primary" />
            <div>
              <p className="text-sm font-medium">
                {intent === "demo"
                  ? "Your demo workspace is ready — all assets are materialized."
                  : intent === "import"
                    ? `Imported ${importResult?.asset_paths?.length ?? 0} source asset${(importResult?.asset_paths?.length ?? 0) === 1 ? "" : "s"} into ${importResult?.pipeline_path ?? pipelineName}.`
                    : "Your project is ready."}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                Every file is plain YAML/SQL in your git repository — edit visually or in code.
              </p>
            </div>
            <Button className="mx-auto" onClick={openWorkspace}>
              Open workspace
              <ArrowRight data-icon="inline-end" />
            </Button>
          </div>
        ) : null}
      </div>
      {!inPlace ? (
        <DirectoryPickerDialog
          open={directoryPickerOpen}
          onOpenChange={setDirectoryPickerOpen}
          initialPath={parentDir}
          browsePurpose="create"
          title="Choose project location"
          description="Choose the folder that will contain the new project directory."
          confirmLabel="Use this directory"
          allowCreate
          onSelect={(path) => {
            setParentDir(path);
            setError(null);
          }}
        />
      ) : null}
    </div>
  );
}

function OptionCard({
  icon: Icon,
  title,
  description,
  onClick,
}: {
  icon: typeof Sparkles;
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="group flex items-start gap-4 rounded-xl border bg-background p-5 text-left transition-colors hover:border-primary/50 hover:bg-muted/30"
    >
      <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
        <Icon className="size-5" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold">{title}</div>
        <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
      </div>
      <ArrowRight className="mt-2 size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
    </button>
  );
}

function AssetStatusIcon({ status }: { status?: string }) {
  if (status === "succeeded") {
    return <CheckCircle2 className="size-3.5 shrink-0 text-primary" />;
  }
  if (status === "failed") {
    return <XCircle className="size-3.5 shrink-0 text-destructive" />;
  }
  if (status === "skipped") {
    return <CircleAlert className="size-3.5 shrink-0 text-muted-foreground" />;
  }
  return <LoaderCircle className="size-3.5 shrink-0 animate-spin text-muted-foreground" />;
}

function stageTitle(stage: WelcomeStage, intent: WelcomeIntent) {
  switch (stage) {
    case "target":
      return intent === "demo"
        ? "Pick a demo"
        : intent === "import"
          ? "Name your project"
          : "Name your project";
    case "creating":
      return "Creating project...";
    case "materializing":
      return "Running your pipeline";
    case "import-connect":
      return "Connect your database";
    case "import-tables":
      return "Pick tables to import";
    case "done":
      return "You're all set";
    default:
      return "Welcome to renart";
  }
}

function stageSubtitle(stage: WelcomeStage, inPlace: boolean, workspacePath: string) {
  if (stage === "choose") {
    return inPlace && workspacePath
      ? `This workspace is empty. Choose how to set it up.`
      : "Create a new project — every project is a plain git repository.";
  }
  if (stage === "import-connect") {
    return "renart connects, lists the tables, and generates source assets for the ones you pick.";
  }
  if (stage === "materializing") {
    return "The initial run materializes every asset so you land in a working workspace.";
  }
  return "";
}

function demoSuggestedName(templateId: string) {
  const id = templateId.replace(/^demo:/, "");
  return id ? `${id}-demo` : "demo";
}

function defaultConnectionValues(connectionType?: WorkspaceConfigConnectionType) {
  const values: Record<string, string | boolean> = {};
  for (const field of connectionType?.fields ?? []) {
    if (field.is_sensitive || field.is_sensitive_file) {
      continue;
    }
    if (field.default_value) {
      values[field.name] =
        field.type === "bool" ? field.default_value === "true" : field.default_value;
    }
  }
  return values;
}

function defaultConnectionSecretModes(connectionType?: WorkspaceConfigConnectionType) {
  return Object.fromEntries(
    (connectionType?.fields ?? [])
      .filter((field) => field.is_sensitive || field.is_sensitive_file)
      .map((field) => [field.name, "local" as const]),
  );
}

// Saves the import flow's connection as `<type>-default` in the workspace
// config; the import endpoint resolves the connection from the saved config.
async function persistImportConnection(
  connectionType: WorkspaceConfigConnectionType | undefined,
  values: Record<string, unknown>,
  secretStorageModes: Record<string, "local" | "env">,
  database: string,
) {
  const connectionValues = { ...values };
  if (database && connectionType?.type_name !== "duckdb" && !connectionValues.database) {
    connectionValues.database = database;
  }
  const draft = splitConnectionDraftValues(connectionType, connectionValues, secretStorageModes);
  const typeName = connectionType?.type_name ?? "";
  const input = {
    environment_name: IMPORT_ENVIRONMENT,
    name: `${typeName}-default`,
    type: typeName,
    values: draft.values,
    secret_changes: draft.secretChanges,
  };
  try {
    await createWorkspaceConnection(input);
  } catch (createError) {
    const message = createError instanceof Error ? createError.message : "";
    if (!/already exists/i.test(message)) {
      throw createError;
    }
    await updateWorkspaceConnection({ ...input, current_name: input.name });
  }
}
