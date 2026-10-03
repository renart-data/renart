import { Link, useNavigate } from "@tanstack/react-router";
import { useStore } from "jotai";
import { ArrowLeft, CircleAlert, Database, LoaderCircle, PlugZap, Table2 } from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactNode,
} from "react";

import {
  ConnectDataFlow,
  type ConnectDataResult,
  type ConnectDataStep,
} from "@/components/app/connect-data-flow";
import { connectDataHandoffAtom } from "@/lib/atoms/domains/connect-data";
import { DirectoryPickerDialog } from "@/components/app/directory-picker-dialog";
import {
  AppLineageCanvas,
  PIPELINE_CANVAS_TRANSITION,
  type AppLineageCanvasAsset,
} from "@/components/app/lineage-canvas";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { getWorkspaceConfig } from "@/lib/api-config";
import {
  browseProjectDirs,
  createProject,
  getDuckDBDriver,
  getProjectTemplates,
  listProjects,
  openProject,
  prepareDuckDBDriver,
} from "@/lib/api-projects";
import { buildStalePipelineStream } from "@/lib/api-staleness";
import { getWorkspace } from "@/lib/api-workspace";
import { assetNameParts } from "@/lib/asset-presentation";
import {
  receiveWorkspaceUpdateAtom,
  workspaceConnectionSequenceAtom,
} from "@/lib/atoms/domains/workspace";
import type {
  CreateProjectResponse,
  ProjectInfo,
  ProjectListResponse,
  ProjectTemplateInfo,
  SqlDiscoveryTable,
} from "@/lib/generated/api-types";
import { startGettingStarted } from "@/lib/getting-started";
import { pinProject } from "@/lib/project-context";
import { viewTransitionsEnabled } from "@/lib/view-transitions";
import type { WorkspaceConfigResponse } from "@/lib/types";

import {
  initialWelcomeState,
  recentProjects,
  stepsForPath,
  templateCanvasAssets,
  templateIdForPath,
  welcomeReducer,
  type WelcomePath,
} from "./welcome/welcome-flow";
import { WelcomeChoose } from "./welcome/welcome-choose";
import { WelcomeFrame, WelcomePreviewPane } from "./welcome/welcome-frame";
import { WelcomeRun } from "./welcome/welcome-run";
import { WelcomeSetup } from "./welcome/welcome-setup";

// The demo the welcome screen recommends and preselects.
const DEFAULT_DEMO = "demo:product";
// After a successful first run, keep the all-fresh canvas on screen briefly
// before it expands into the workspace.
const SUCCESS_PAUSE_MS = 900;

// The welcome flow is the first-run screen and the "New project" wizard behind
// the project switcher. It scaffolds a template through the process-level
// create-project endpoint, pins the new project to this tab and, for demos,
// runs the build-stale stream once so every asset starts out fresh.
export function WelcomePage({ forceNew = false }: { forceNew?: boolean }) {
  useWorkspaceTheme();
  const navigate = useNavigate();
  const store = useStore();
  const [state, dispatch] = useReducer(welcomeReducer, undefined, initialWelcomeState);

  const [templates, setTemplates] = useState<ProjectTemplateInfo[]>([]);
  const [directory, setDirectory] = useState<ProjectListResponse | null>(null);
  const [workspaceEmpty, setWorkspaceEmpty] = useState<boolean | null>(null);
  const [config, setConfig] = useState<WorkspaceConfigResponse | null>(null);
  const [parentDir, setParentDir] = useState("");
  const [parentDirLoading, setParentDirLoading] = useState(true);
  const [driverReady, setDriverReady] = useState<boolean | null>(null);
  const [driverDownloaded, setDriverDownloaded] = useState(false);
  const [highlightedPath, setHighlightedPath] = useState<WelcomePath>("demo");
  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [openFolderOpen, setOpenFolderOpen] = useState(false);
  const [openingProjectId, setOpeningProjectId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [connectStep, setConnectStep] = useState<ConnectDataStep>("connect");
  const [selectedTables, setSelectedTables] = useState<{
    tables: SqlDiscoveryTable[];
    connectionName: string;
  }>({ tables: [], connectionName: "" });

  useEffect(() => {
    void getProjectTemplates()
      .then((response) => {
        setTemplates(response.templates);
        const preferred =
          response.templates.find((template) => template.id === DEFAULT_DEMO) ??
          response.templates.find((template) => template.id.startsWith("demo:"));
        if (preferred) dispatch({ type: "select-template", templateId: preferred.id });
      })
      .catch(() => setTemplates([]));
    void listProjects()
      .then(setDirectory)
      .catch(() =>
        setDirectory({ status: "ok", default_project_id: "", bootstrap: true, projects: [] }),
      );
    void getWorkspace()
      .then((workspace) => setWorkspaceEmpty(workspace.pipelines.length === 0))
      .catch(() => setWorkspaceEmpty(false));
    void getWorkspaceConfig()
      .then(setConfig)
      .catch(() => {});
    void browseProjectDirs(undefined, "create")
      .then((response) => setParentDir((current) => current || response.path))
      .catch(() => {})
      .finally(() => setParentDirLoading(false));
    void getDuckDBDriver()
      .then((response) => setDriverReady(response.ready))
      .catch(() => setDriverReady(null));
  }, []);

  const bootstrapMode = directory?.bootstrap ?? null;
  // Scaffolding into the open (empty) workspace is the first-run default;
  // "New project" from the switcher always creates a fresh directory.
  const inPlace = !forceNew && bootstrapMode === false && workspaceEmpty === true;
  const workspacePath = config?.workspace_path ?? "";
  const loading = directory === null || workspaceEmpty === null;

  const demos = useMemo(
    () => templates.filter((template) => template.id.startsWith("demo:")),
    [templates],
  );
  const selectedDemo = demos.find((template) => template.id === state.templateId) ?? demos[0];
  const emptyTemplate = templates.find((template) => template.id === "empty");
  const activeTemplate = state.path === "demo" ? selectedDemo : emptyTemplate;
  const recent = useMemo(
    () =>
      forceNew || !directory
        ? []
        : recentProjects(
            directory.projects,
            inPlace
              ? {
                  id: config?.project_id ?? directory.default_project_id,
                  path: config?.workspace_path ?? null,
                }
              : { id: null, path: null },
          ),
    [config?.project_id, config?.workspace_path, directory, forceNew, inPlace],
  );

  // ---- Leaving the welcome screen ------------------------------------------------

  // A new project changes the tab's API scope, which needs a document load. An
  // in-place project keeps the scope: refresh the workspace snapshot and route
  // straight to the canvas, so the first-run canvas can expand into it.
  const enterWorkspace = useCallback(
    async (target: { pipelineId?: string; pipelinePath?: string }) => {
      const sameScope = inPlace;
      let pipelineId = target.pipelineId;
      try {
        const workspace = await getWorkspace();
        pipelineId ??= workspace.pipelines.find(
          (pipeline) =>
            pipeline.path === target.pipelinePath || pipeline.name === target.pipelinePath,
        )?.id;
        pipelineId ??= workspace.pipelines[0]?.id;
        if (sameScope) {
          store.set(receiveWorkspaceUpdateAtom, {
            workspace,
            connectionSequence: store.get(workspaceConnectionSequenceAtom),
            source: {
              method: "workspace-load",
              recordedAt: new Date().toISOString(),
              revision: workspace.revision,
            },
          });
        }
      } catch {
        // The workspace route loads the snapshot itself.
      }
      if (!sameScope) {
        window.location.assign(
          pipelineId ? `/pipelines/${encodeURIComponent(pipelineId)}/canvas` : "/",
        );
        return;
      }
      if (pipelineId) {
        void navigate({
          to: "/pipelines/$pipelineId/canvas",
          params: { pipelineId },
          viewTransition: viewTransitionsEnabled,
        });
      } else {
        void navigate({ to: "/", viewTransition: viewTransitionsEnabled });
      }
    },
    [inPlace, navigate, store],
  );

  const openExistingProject = useCallback(
    async (project: Pick<ProjectInfo, "id" | "path">) => {
      setOpeningProjectId(project.id);
      dispatch({ type: "set-error", error: null });
      try {
        const response = await openProject(project.path);
        pinProject(
          response.project.id === directory?.default_project_id ? null : response.project.id,
        );
        window.location.assign("/");
      } catch (openError) {
        setOpeningProjectId(null);
        dispatch({
          type: "set-error",
          error:
            openError instanceof Error ? openError.message : "The project could not be opened.",
        });
      }
    },
    [directory?.default_project_id],
  );

  // ---- Creating and running ----------------------------------------------------------

  const runFirstBuild = useCallback(
    async (created: CreateProjectResponse, template: ProjectTemplateInfo | undefined) => {
      try {
        const driver = await getDuckDBDriver();
        if (!driver.ready) {
          dispatch({ type: "prepare-started", downloading: true });
          setDriverDownloaded(true);
          await prepareDuckDBDriver();
          setDriverReady(true);
        }
      } catch (prepareError) {
        dispatch({
          type: "failed",
          at: "prepare",
          error:
            prepareError instanceof Error
              ? prepareError.message
              : "DuckDB's driver could not be prepared.",
          now: Date.now(),
        });
        return;
      }

      try {
        // Project creation pinned the new runtime. Resolve its selected
        // environment explicitly so the run facts and the workspace's
        // staleness selection use the same identity.
        const workspace = await getWorkspace();
        const environment = workspace.selected_environment.trim();
        if (!environment) {
          throw new Error("The project has no selected environment for its first run.");
        }
        dispatch({
          type: "run-started",
          assetNames: (template?.assets ?? []).map((asset) => asset.name),
          now: Date.now(),
        });
        const payload = await buildStalePipelineStream(
          created.pipeline_id,
          {
            onChunk: (chunk) => dispatch({ type: "log", chunk }),
            onAssetEvent: (event) => {
              if (event.asset_name && event.status) {
                dispatch({
                  type: "asset-event",
                  assetName: event.asset_name,
                  status: event.status,
                });
              }
            },
          },
          { environment },
        );
        if (payload?.status === "ok") {
          dispatch({ type: "run-finished", now: Date.now() });
        } else {
          dispatch({
            type: "failed",
            at: "run",
            error: payload?.error || "The first run failed.",
            now: Date.now(),
          });
        }
      } catch (runError) {
        dispatch({
          type: "failed",
          at: "run",
          error: runError instanceof Error ? runError.message : "The first run failed.",
          now: Date.now(),
        });
      }
    },
    [],
  );

  const createAndContinue = useCallback(async () => {
    const template = state.path === "demo" ? selectedDemo : undefined;
    setBusy(true);
    dispatch({ type: "create-started" });
    let created: CreateProjectResponse;
    try {
      created = await createProject({
        template: templateIdForPath(state.path, state.templateId),
        ...(inPlace
          ? { path: workspacePath }
          : { name: state.projectName.trim(), parent_dir: parentDir.trim() || undefined }),
      });
      if (!inPlace) {
        // Route every follow-up call (workspace, discovery, import, run) onto
        // the new project's API mount for this tab.
        pinProject(created.project.default ? null : created.project.id);
      }
      startGettingStarted(created.project.id);
      dispatch({ type: "created", response: created });
    } catch (createError) {
      dispatch({
        type: "failed",
        at: "create",
        error: createError instanceof Error ? createError.message : "Failed to create the project.",
      });
      setBusy(false);
      return;
    }
    setBusy(false);
    if (state.path === "demo") {
      void runFirstBuild(created, template);
    } else if (state.path === "empty") {
      void enterWorkspace({ pipelineId: created.pipeline_id });
    } else {
      // The connect step reads the new project's config, not the one this
      // screen was opened on.
      setConfig(null);
      void getWorkspaceConfig()
        .then(setConfig)
        .catch((configError: unknown) =>
          dispatch({
            type: "set-error",
            error:
              configError instanceof Error
                ? configError.message
                : "The new project's settings could not be loaded.",
          }),
        );
    }
  }, [
    enterWorkspace,
    inPlace,
    parentDir,
    runFirstBuild,
    selectedDemo,
    state.path,
    state.projectName,
    state.templateId,
    workspacePath,
  ]);

  // The all-fresh canvas expands into the workspace on its own.
  const created = state.created;
  const phase = state.run.phase;
  const enteredRef = useRef(false);
  useEffect(() => {
    if (state.path !== "demo" || phase !== "succeeded" || !created || enteredRef.current) return;
    const timer = window.setTimeout(() => {
      enteredRef.current = true;
      void enterWorkspace({ pipelineId: created.pipeline_id });
    }, SUCCESS_PAUSE_MS);
    return () => window.clearTimeout(timer);
  }, [created, enterWorkspace, phase, state.path]);

  const handleSelectionChange = useCallback(
    (tables: SqlDiscoveryTable[], connectionName: string) =>
      setSelectedTables({ tables, connectionName }),
    [],
  );

  const handleImported = useCallback(
    async (result: ConnectDataResult) => {
      if (!state.created && config?.project_id) startGettingStarted(config.project_id);
      let pipelineId: string | undefined;
      try {
        const workspace = await getWorkspace();
        pipelineId = workspace.pipelines.find(
          (pipeline) =>
            pipeline.path === result.pipelinePath || pipeline.name === result.pipelinePath,
        )?.id;
      } catch {
        // enterWorkspace falls back to the first pipeline.
      }
      if (pipelineId) {
        // The canvas offers a first SQL asset on the new sources.
        store.set(connectDataHandoffAtom, {
          pipelineId,
          connectionName: result.connectionName,
          tables: result.tables,
        });
      }
      void enterWorkspace({ pipelineId, pipelinePath: result.pipelinePath });
    },
    [config?.project_id, enterWorkspace, state.created, store],
  );

  // ---- Rendering -----------------------------------------------------------------------

  const steps =
    state.step === "choose" ? null : stepsForPath(state.path, inPlace && state.path === "import");
  const currentStep =
    state.step === "connect" && connectStep === "tables" ? ("tables" as const) : state.step;
  const runCanvasAssets = useMemo(
    () => templateCanvasAssets(selectedDemo?.assets ?? [], state.run.assets),
    [selectedDemo?.assets, state.run.assets],
  );

  const demoMeta =
    driverReady === false
      ? "Local data · the first run downloads DuckDB (about 70 MB)"
      : "Local data · ready in seconds";

  let preview: ReactNode;
  if (state.step === "run") {
    preview = (
      <TemplatePreview
        template={selectedDemo}
        assets={runCanvasAssets}
        live
        viewTransitionName={PIPELINE_CANVAS_TRANSITION}
      />
    );
  } else if (state.step === "connect") {
    preview =
      connectStep === "tables" ? (
        <SelectedTablesPreview
          tables={selectedTables.tables}
          connectionName={selectedTables.connectionName}
        />
      ) : (
        <ConnectPreview />
      );
  } else {
    const previewPath = state.step === "choose" ? highlightedPath : state.path;
    preview =
      previewPath === "import" ? (
        <ConnectPreview />
      ) : (
        <TemplatePreview
          template={previewPath === "demo" ? selectedDemo : emptyTemplate}
          assets={templateCanvasAssets(
            (previewPath === "demo" ? selectedDemo : emptyTemplate)?.assets ?? [],
          )}
        />
      );
  }

  return (
    <>
      <WelcomeFrame
        steps={steps}
        currentStep={currentStep}
        stepKey={`${state.step}:${state.path}`}
        preview={preview}
        previewOnMobile={state.step === "run"}
      >
        {loading ? (
          <div className="flex justify-center py-16">
            <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : state.step === "choose" ? (
          <>
            {state.error ? (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>Something went wrong</AlertTitle>
                <AlertDescription>{state.error}</AlertDescription>
              </Alert>
            ) : null}
            <WelcomeChoose
              recent={recent}
              openingProjectId={openingProjectId}
              demoMeta={demoMeta}
              onOpenProject={(project) => void openExistingProject(project)}
              onOpenFolder={() => setOpenFolderOpen(true)}
              onChoose={(path) => dispatch({ type: "choose-path", path, inPlace })}
              onHighlight={setHighlightedPath}
            />
            {workspaceEmpty === false ? (
              <Button variant="ghost" size="sm" className="w-fit" asChild>
                <Link to="/">
                  <ArrowLeft data-icon="inline-start" />
                  Back to workspace
                </Link>
              </Button>
            ) : null}
          </>
        ) : state.step === "setup" ? (
          <WelcomeSetup
            path={state.path}
            demos={demos}
            templateId={selectedDemo?.id ?? ""}
            inPlace={inPlace}
            workspacePath={workspacePath}
            targetFolder={state.path === "import" ? "" : (activeTemplate?.pipeline_name ?? "")}
            projectName={state.projectName}
            parentDir={parentDir}
            parentDirLoading={parentDirLoading}
            busy={busy}
            error={state.error}
            onSelectTemplate={(templateId) => dispatch({ type: "select-template", templateId })}
            onProjectNameChange={(name) => dispatch({ type: "set-project-name", name })}
            onChooseLocation={() => setLocationPickerOpen(true)}
            onBack={() => dispatch({ type: "back", inPlace })}
            onSubmit={() => void createAndContinue()}
          />
        ) : state.step === "run" ? (
          <WelcomeRun
            state={state}
            driverDownloaded={driverDownloaded}
            onRetry={() => {
              if (!state.created) return;
              dispatch({ type: "retry-run" });
              void runFirstBuild(state.created, selectedDemo);
            }}
            onOpenAnyway={() => void enterWorkspace({ pipelineId: state.created?.pipeline_id })}
          />
        ) : state.step === "connect" ? (
          <>
            <div>
              <h2 className="text-lg font-semibold tracking-tight">
                {connectStep === "tables" ? "Pick tables to import" : "Connect your database"}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {connectStep === "tables"
                  ? "Each table becomes a source asset you can build SQL models on."
                  : "Renart tests the connection before saving it to the project config. Credentials stay out of Git."}
              </p>
            </div>
            {config ? (
              <ConnectDataFlow
                config={config}
                environmentName={
                  config.default_environment || config.selected_environment || "default"
                }
                onStepChange={setConnectStep}
                onSelectionChange={handleSelectionChange}
                onCancel={state.created ? undefined : () => dispatch({ type: "back", inPlace })}
                onImported={(result) => void handleImported(result)}
              />
            ) : (
              <div className="flex justify-center py-10">
                <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
              </div>
            )}
          </>
        ) : null}
      </WelcomeFrame>
      {!inPlace ? (
        <DirectoryPickerDialog
          open={locationPickerOpen}
          onOpenChange={setLocationPickerOpen}
          initialPath={parentDir}
          browsePurpose="create"
          title="Choose project location"
          description="Choose the folder that will contain the new project directory."
          confirmLabel="Use this directory"
          allowCreate
          onSelect={(path) => {
            setParentDir(path);
            dispatch({ type: "set-error", error: null });
          }}
        />
      ) : null}
      <DirectoryPickerDialog
        open={openFolderOpen}
        onOpenChange={setOpenFolderOpen}
        title="Open project"
        description="Pick a directory. It becomes a project with its own connections, environments, and schedules."
        confirmLabel="Open this directory"
        showProjectMarkers
        onSelect={(path) => openExistingProject({ id: path, path })}
      />
    </>
  );
}

function TemplatePreview({
  template,
  assets,
  live = false,
  viewTransitionName,
}: {
  template: ProjectTemplateInfo | undefined;
  assets: AppLineageCanvasAsset[];
  live?: boolean;
  viewTransitionName?: string;
}) {
  if (!template) {
    return (
      <WelcomePreviewPane title="Preview">
        <div className="flex h-64 items-center justify-center lg:h-80">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </WelcomePreviewPane>
    );
  }
  return (
    <WelcomePreviewPane
      title={template.title}
      meta={`${template.assets.length} ${template.assets.length === 1 ? "asset" : "assets"}`}
      footer={
        live ? null : (
          <div className="grid gap-2">
            <p className="text-xs leading-relaxed text-muted-foreground">{template.description}</p>
            {template.features.length > 0 ? (
              <div className="flex flex-wrap gap-1" aria-label="Template features">
                {template.features.map((feature) => (
                  <Badge key={feature} variant="muted" size="xs">
                    {feature}
                  </Badge>
                ))}
              </div>
            ) : null}
          </div>
        )
      }
    >
      <div className="welcome-canvas h-64 lg:h-80" data-live={live || undefined}>
        <AppLineageCanvas assets={assets} preview viewTransitionName={viewTransitionName} />
      </div>
    </WelcomePreviewPane>
  );
}

function ConnectPreview() {
  return (
    <WelcomePreviewPane title="Your data, as source assets">
      <ol className="grid gap-4 p-5 text-sm">
        {[
          {
            icon: PlugZap,
            title: "Connect",
            text: "Pick your database and enter its connection details. Secrets go to your operating system's credential store or an environment variable.",
          },
          {
            icon: Database,
            title: "Test",
            text: "Renart connects and lists the databases and tables it can see before anything is saved.",
          },
          {
            icon: Table2,
            title: "Pick tables",
            text: "Each table you pick becomes a source asset on the canvas, with its columns, ready for SQL models downstream.",
          },
        ].map((item, index) => (
          <li key={item.title} className="flex gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <item.icon className="size-4" />
            </span>
            <span className="min-w-0">
              <span className="block font-medium">
                {index + 1}. {item.title}
              </span>
              <span className="block text-xs leading-relaxed text-muted-foreground">
                {item.text}
              </span>
            </span>
          </li>
        ))}
      </ol>
    </WelcomePreviewPane>
  );
}

function SelectedTablesPreview({
  tables,
  connectionName,
}: {
  tables: SqlDiscoveryTable[];
  connectionName: string;
}) {
  const assets = useMemo<AppLineageCanvasAsset[]>(
    () =>
      tables.map((table) => {
        const { prefix, title } = assetNameParts(table.name);
        return {
          id: table.name,
          name: table.name,
          displayName: title,
          prefix,
          kind: "source",
          group: prefix ?? "ASSETS",
          integration: connectionName,
          description: "",
          status: "ok",
          materializedAt: "",
          readOnly: true,
          x: 0,
          y: 0,
        };
      }),
    [connectionName, tables],
  );
  return (
    <WelcomePreviewPane title="New source assets" meta={`${tables.length} selected`}>
      {tables.length === 0 ? (
        <div className="flex h-64 items-center justify-center px-6 text-center text-sm text-muted-foreground lg:h-80">
          Selected tables appear here as source assets.
        </div>
      ) : (
        <div className="welcome-canvas h-64 lg:h-80">
          <AppLineageCanvas assets={assets} preview />
        </div>
      )}
    </WelcomePreviewPane>
  );
}
