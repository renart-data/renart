import { Link, Outlet, useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue, useSetAtom } from "jotai";
import {
  Group as PanelGroup,
  Panel,
  type PanelImperativeHandle,
  Separator as PanelResizeHandle,
} from "react-resizable-panels";
import {
  AlertTriangle,
  Bell,
  BookOpen,
  ChevronDown,
  ChevronUp,
  Columns2,
  Database,
  Eye,
  FileCode,
  FilePlus2,
  FolderPlus,
  Hammer,
  Layers,
  Loader2,
  Package,
  PanelLeft,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRight,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Plus,
  RefreshCw,
  Search,
  Sliders,
  Table2,
  Terminal,
  X,
} from "lucide-react";
import {
  ComponentType,
  ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { ButtonGroup } from "@/components/ui/button-group";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  DelimitedCardContent,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import { Input } from "@/components/ui/input";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Label } from "@/components/ui/label";
import { deleteAsset } from "@/lib/api-assets";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AssetInspectView } from "@/components/asset-inspect-view";
import { InspectWarningCard } from "@/components/inspect-warning-card";
import { InspectInfoCard } from "@/components/inspect-info-card";
import { WorkspaceMaterializeOutputView } from "@/components/workspace-materialize-output-view";
import { Spinner } from "@/components/ui/spinner";
import { runSQLQuery } from "@/lib/api-sql-discovery";
import type { PipelineRunSource } from "@/lib/api-scheduler";
import {
  typeCheckPipeline,
  type PipelineTypeCheckExternalRelation,
  type PipelineTypeCheckReport,
} from "@/lib/api-pipelines";
import { renderPipelineAsset, type AssetRenderResult } from "@/lib/api-asset-render";
import { assetPresentationFields } from "@/lib/asset-presentation";
import {
  isAPIAssetType,
  isIngestrAssetType,
  isLoadAssetType,
  isPythonAssetType,
  isSeedAssetType,
  isSensorAssetType,
  isSqlAssetType,
} from "@/lib/asset-types";
import { editorDraftAtom, sqlHoveredAssetAtom } from "@/lib/atoms/domains/editor";
import type { MaterializeHistoryEntry } from "@/lib/atoms/results";
import {
  routeSelectionAtom,
  selectedEnvironmentAtom,
  selectedExecutionTimeWindowAtom,
  sqlCatalogReadyEventAtom,
  workspaceAtom,
} from "@/lib/atoms/domains/workspace";
import { renderJinjaAsset } from "@/lib/jinja-intellisense";
import { effectiveConnectionForAsset } from "@/lib/sql-schema";
import { withSQLPreviewLimit } from "@/lib/sql-query-preview";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import type {
  AssetInspectResponse,
  SqlQueryResponse,
  WebAsset,
  WebPipeline,
  WorkspaceQueryConnection,
} from "@/lib/types";
import { cn } from "@/lib/utils";
import { deploymentLabel } from "@/lib/deployment-label";
import { copyTextToClipboard } from "@/lib/copy-to-clipboard";
import { useAssetResults } from "@/hooks/use-asset-results";
import { useSelectedEnvironmentPolicy } from "@/hooks/use-environment-policy";
import { useIsMobile } from "@/hooks/use-mobile";
import { usePipelineDeploy, type PipelineDeployState } from "@/hooks/use-pipeline-deploy";
import { isStaleStatus, usePipelineStaleness } from "@/hooks/use-pipeline-staleness";
import type { MaterializeScope } from "@/lib/materialize-scope";
import {
  labelForAppMaterializationState,
  useAppAssetMaterializationStatus,
} from "@/hooks/use-app-asset-materialization-status";

import { kindMeta } from "./app-data";
import { AdhocToNotebookDialog } from "./adhoc-convert-dialog";
import { AppAdhocEditor, useAdhocConnectionSelection, useAdhocQueryDraft } from "./adhoc-editor";
import { AppAssetEditor } from "./asset-editor";
import { ApiParametersEditor } from "./api-parameters-editor";
import { AssetGuidedCards, type QualityCheckFocus } from "./asset-guided-cards";
import { NewAssetDialog, NewFolderDialog, NewPipelineDialog } from "./build-create-dialogs";
import { ErrorBoundary } from "@/components/ui/error-boundary";
import { SqlPreview } from "./sql-preview";
import { LoadParametersEditor } from "./load-parameters-editor";
import { SemanticParametersEditor } from "./semantic-parameters-editor";
import { AssetRenderView } from "./asset-render-view";
import { PipelinePlanSheet } from "./pipeline-plan-sheet";
import {
  AppLineageCanvas,
  assetDisplayName,
  assetGroupName,
  assetNameParts,
  type AppLineageCanvasAsset,
} from "./lineage-canvas";
import { NewNotebookDialog } from "./new-notebook-dialog";
import { PipelineSettingsDialog, type PipelineSettingsSection } from "./pipeline-settings-dialog";
import { TypeCheckPanel } from "./type-check-panel";
import { ExternalRelationImportDialog } from "./external-relation-import-dialog";
import {
  AppPage,
  AppPanel,
  lastRunLabel,
  PageHeader,
  stalenessDotClassName,
  stalenessLabel,
} from "./app-primitives";

const WORKING_TREE_RUN_SOURCE: PipelineRunSource = { source: "working_tree" };
const adhocQueryLimit = 500;

export type AppBuildView = "canvas" | "split" | "code";
export type AppResultTab = "inspect" | "render" | "materialize" | "query" | "typecheck";
export type AppEditorMode = "asset" | "adhoc";

export type AppBuildSearch = {
  result?: AppResultTab;
  editor?: AppEditorMode;
};

const resultTabs: AppResultTab[] = ["inspect", "render", "materialize", "query", "typecheck"];
const editorModes: AppEditorMode[] = ["asset", "adhoc"];

export function normalizeAppBuildSearch(search: Record<string, unknown>): AppBuildSearch {
  return {
    result: resultTabs.includes(search.result as AppResultTab)
      ? (search.result as AppResultTab)
      : undefined,
    editor: editorModes.includes(search.editor as AppEditorMode)
      ? (search.editor as AppEditorMode)
      : undefined,
  };
}

const scrollableTabsListClass = "w-max max-w-none";
const scrollableTabsTriggerClass = "flex-none";

function toUTCDateTimeInput(value: string) {
  if (!value) return "";
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return "";
  return new Date(timestamp).toISOString().slice(0, 19);
}

function fromUTCDateTimeInput(value: string) {
  if (!value) return "";
  const timestamp = Date.parse(`${value}Z`);
  if (Number.isNaN(timestamp)) return "";
  return new Date(timestamp).toISOString();
}

function isValidExecutionWindow(start: string, end: string) {
  const startTimestamp = Date.parse(start);
  const endTimestamp = Date.parse(end);
  return (
    !Number.isNaN(startTimestamp) && !Number.isNaN(endTimestamp) && endTimestamp > startTimestamp
  );
}

function catalogObservationLabel(observedAt?: string) {
  if (!observedAt) return "remote catalog";
  const observed = Date.parse(observedAt);
  if (Number.isNaN(observed)) return "remote catalog";
  const ageMs = Math.max(0, Date.now() - observed);
  const minutes = Math.floor(ageMs / 60_000);
  if (minutes < 1) return "observed just now";
  if (minutes < 60) return `observed ${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `observed ${hours}h ago`;
  return `observed ${Math.floor(hours / 24)}d ago`;
}

type BuildAsset = AppLineageCanvasAsset & {
  workspaceAsset?: WebAsset;
  pipelineId?: string;
  displayName?: string;
  prefix?: string;
  path?: string;
  type?: string;
  connection?: string;
  upstreams?: string[];
  readOnly?: boolean;
  externalRelation?: PipelineTypeCheckExternalRelation;
};

type AssetRenderSourceState = {
  identity: string;
  assetId: string;
  savedIntentContent: string;
  workspaceContentAtStart: string;
};

type BuildContextValue = {
  pipelineId: string;
  pipeline?: WebPipeline;
  pipelineAssets: BuildAsset[];
  routedAssetId?: string;
  selectedAssetId: string;
  selectedAsset: BuildAsset;
  view: AppBuildView;
  buildSearch: AppBuildSearch;
  editorMode: AppEditorMode;
  selectAsset: (assetId: string) => void;
  goToAsset: (pipelineId: string, assetId: string) => void;
  runAssetById: (assetId: string) => void;
  deleteAssetById: (assetId: string) => Promise<void>;
  goToCatalog: (assetId?: string) => void;
  openPipelineConnections: () => void;
  openPipelineVariable: (variableName: string) => void;
  openNewAsset: () => void;
  openNewAssetInGroup: (prefix?: string) => void;
  createDownstreamAsset: (source: { id: string; name: string }) => void;
  openInspector: () => void;
  reviewFailedCheck: (assetId: string) => void;
  importExternalRelation: (relationId: string) => void;
  openBottom: (tab: AppResultTab) => void;
  materializeSelectedAsset: () => void;
  fullRefreshSelectedAsset: () => void;
  backfillSelectedAsset: () => void;
  inspectSelectedAsset: () => void;
  renderSelectedAsset: () => void;
  runAdhocQuery: () => void;
  convertAdhocToAsset: () => void;
  convertAdhocToNotebook: () => void;
  adhocContextAsset: WebAsset | null;
  adhocConnections: WorkspaceQueryConnection[];
  adhocConnection: WorkspaceQueryConnection | null;
  setAdhocConnection: (connection: string) => void;
  adhocLoading: boolean;
  materializeLoading: boolean;
  inspectLoading: boolean;
  renderLoading: boolean;
  renderBlockedReason?: string;
  executionBlocked: boolean;
  executionBlockedReason?: string;
};

const BuildContext = createContext<BuildContextValue | null>(null);

function useBuildContext() {
  const context = useContext(BuildContext);
  if (!context) {
    throw new Error("Build view components must be rendered inside AppBuildPage");
  }
  return context;
}

function assetsForPipeline(pipeline: WebPipeline): BuildAsset[] {
  return pipeline.assets.map((asset) => ({
    ...assetPresentationFields(asset, pipeline),
    workspaceAsset: asset,
    pipelineId: pipeline.id,
    path: asset.path,
    type: asset.type,
    connection: asset.connection,
    upstreams: asset.upstreams,
    status: asset.is_materialized ? "success" : "pending",
    materializedAt: asset.is_materialized ? "current" : "not materialized",
    parseError: asset.parse_error,
    x: 0,
    y: 0,
  }));
}

function normalizeAssetContentIdentity(content: string) {
  // Monaco uses the browser/OS line ending while Bruin returns parsed asset
  // content with LF endings. Treat that save-time normalization as the same
  // source so an SSE refresh cannot invalidate a preview that was just
  // rendered from the successfully saved editor value.
  return content.replace(/\r\n?/g, "\n");
}

function assetSidebarName(asset: BuildAsset) {
  if (asset.path) {
    const file = asset.path.split("/").pop() ?? asset.name;
    const prefix = asset.prefix;
    if (prefix && file.startsWith(`${prefix}.`)) {
      return file.slice(prefix.length + 1);
    }
    return file;
  }
  return `${assetDisplayName(asset)}${kindMeta[asset.kind].ext}`;
}

// Shown in the editor/canvas area while the workspace or a redirected pipeline
// route is still resolving.
function BuildLoadingState() {
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center px-3 pb-3">
      <Loader2 className="size-6 animate-spin text-muted-foreground" />
    </div>
  );
}

export function AppBuildPage({
  pipelineId = "simple",
  selectedAssetId,
  resultTab = "inspect",
  editorMode = "asset",
  onResultTabChange,
  onAssetSelect,
}: {
  pipelineId?: string;
  selectedAssetId?: string;
  resultTab?: AppResultTab;
  editorMode?: AppEditorMode;
  onResultTabChange?: (tab: AppResultTab) => void;
  onAssetSelect?: (assetId: string) => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const catalogReady = useAtomValue(sqlCatalogReadyEventAtom);
  const navigate = useNavigate();
  const location = useLocation();
  const view = appBuildViewFromPath(location.pathname);
  const buildSearch: AppBuildSearch = useMemo(
    () => ({ result: resultTab, editor: editorMode }),
    [editorMode, resultTab],
  );
  const activePipeline = useMemo(
    () => workspace?.pipelines.find((pipeline) => pipeline.id === pipelineId),
    [pipelineId, workspace?.pipelines],
  );
  const pipelineAssets = useMemo(
    () => (activePipeline ? assetsForPipeline(activePipeline) : []),
    [activePipeline],
  );
  const existingAssetNames = useMemo(
    () => new Set((activePipeline?.assets ?? []).map((asset) => asset.name)),
    [activePipeline?.assets],
  );
  const staleness = usePipelineStaleness(activePipeline?.id);
  const materializationAssets = useMemo(
    () =>
      pipelineAssets.map((asset) => ({
        id: asset.id,
        name: asset.name,
        pipelineId: asset.pipelineId,
        isMaterialized:
          asset.workspaceAsset?.is_materialized ??
          (asset.status === "success" || asset.status === "ok"),
        staleness: staleness.byAssetName[asset.name],
      })),
    [pipelineAssets, staleness.byAssetName],
  );
  const materializationStatusByAssetId = useAppAssetMaterializationStatus(materializationAssets);
  const deployState = usePipelineDeploy(activePipeline?.id);
  const environmentPolicy = useSelectedEnvironmentPolicy();
  const executionBlocked = Boolean(
    environmentPolicy?.protected || environmentPolicy?.deployed_only,
  );
  const executionBlockedReason = environmentPolicy?.protected
    ? "This environment is protected: interactive execution is disabled"
    : environmentPolicy?.deployed_only
      ? "This environment only allows deployed pipeline runs; asset and stale builds use the working tree"
      : undefined;
  const assetResults = useAssetResults();
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const effectiveEnvironment = selectedEnvironment ?? workspace?.selected_environment ?? "";
  const pipelineRunSource = useMemo<PipelineRunSource | null>(() => {
    if (!environmentPolicy?.deployed_only) {
      return WORKING_TREE_RUN_SOURCE;
    }
    const versionId = deployState.status?.version_id?.trim();
    if (
      deployState.loading ||
      deployState.error ||
      !deployState.status?.has_snapshot ||
      !deployState.status.executable ||
      !versionId
    ) {
      return null;
    }
    return { source: "snapshot", snapshot_version_id: versionId };
  }, [
    deployState.error,
    deployState.loading,
    deployState.status?.has_snapshot,
    deployState.status?.executable,
    deployState.status?.version_id,
    environmentPolicy?.deployed_only,
  ]);
  const pipelineRunSourceLabel = environmentPolicy?.deployed_only
    ? deployState.loading
      ? "Resolving source"
      : pipelineRunSource?.source === "snapshot"
        ? `Run ${deploymentLabel(deployState.status?.ordinal, pipelineRunSource.snapshot_version_id, "deployment")}`
        : "Deployment required"
    : "Run workspace";
  const selectedExecutionTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const executionWindowBlockedReason = selectedExecutionTimeWindow
    ? undefined
    : "Resolving the execution window";
  const editorDraft = useAtomValue(editorDraftAtom);
  const setEditorDraft = useSetAtom(editorDraftAtom);
  const [typeCheckReport, setTypeCheckReport] = useState<PipelineTypeCheckReport | null>(null);
  const [typeCheckLoading, setTypeCheckLoading] = useState(false);
  const [typeCheckError, setTypeCheckError] = useState<string | null>(null);
  const [externalRelationImportId, setExternalRelationImportId] = useState<string | null>(null);
  const [adhocResult, setAdhocResult] = useState<SqlQueryResponse | null>(null);
  const [adhocRenderedQuery, setAdhocRenderedQuery] = useState<string | null>(null);
  const [adhocLoading, setAdhocLoading] = useState(false);
  const [assetRenderResult, setAssetRenderResult] = useState<AssetRenderResult | null>(null);
  const [assetRenderLoading, setAssetRenderLoading] = useState(false);
  const [assetRenderError, setAssetRenderError] = useState<string | null>(null);
  const [assetRenderSource, setAssetRenderSource] = useState<AssetRenderSourceState | null>(null);
  const assetRenderRequestId = useRef(0);
  const assetRenderIdentityRef = useRef<string | null>(null);
  const [adhocQuery] = useAdhocQueryDraft(pipelineId);
  const [storedAdhocConnection, storeAdhocConnection] = useAdhocConnectionSelection(pipelineId);
  const typeCheckErrorAssetIds = useMemo(
    () =>
      new Set(
        (typeCheckReport?.assets ?? [])
          .filter((asset) => asset.findings.some((finding) => finding.severity === "error"))
          .flatMap((asset) => [asset.id, asset.name].filter(Boolean)),
      ),
    [typeCheckReport],
  );
  const displayedPipelineAssets = useMemo(() => {
    const authored = pipelineAssets.map((asset) => ({
      ...asset,
      status: materializationStatusByAssetId[asset.id]?.status ?? asset.status,
      materializedAt: labelForAppMaterializationState(materializationStatusByAssetId[asset.id]),
      staleness: staleness.byAssetName[asset.name],
      hasTypeCheckError:
        typeCheckErrorAssetIds.has(asset.id) || typeCheckErrorAssetIds.has(asset.name),
    }));
    const externalRelations = typeCheckReport?.external_relations ?? [];
    if (externalRelations.length === 0) return authored;

    const externalByConsumer = new Map<string, string[]>();
    for (const relation of externalRelations) {
      for (const id of relation.referenced_by_asset_ids) {
        externalByConsumer.set(id, [...(externalByConsumer.get(id) ?? []), relation.id]);
      }
      for (const name of relation.referenced_by_asset_names) {
        externalByConsumer.set(name, [...(externalByConsumer.get(name) ?? []), relation.id]);
      }
    }
    const consumers = authored.map((asset) => ({
      ...asset,
      upstreams: [
        ...(asset.upstreams ?? []),
        ...(externalByConsumer.get(asset.id) ?? externalByConsumer.get(asset.name) ?? []),
      ],
    }));
    const externalNodes: BuildAsset[] = externalRelations.map((relation) => ({
      id: relation.id,
      name: relation.qualified_name,
      displayName: relation.name || relation.qualified_name,
      prefix: `External · ${relation.connection}`,
      group: `External · ${relation.connection}`,
      kind: "source",
      integration: relation.connection,
      description: `Observed on ${relation.connection}${relation.environment ? ` in ${relation.environment}` : ""}`,
      status: "unknown",
      materializedAt: `${catalogObservationLabel(relation.observed_at)}${
        relation.stale ? " · stale" : ""
      }`,
      connection: relation.connection,
      upstreams: [],
      readOnly: true,
      isExternal: true,
      externalRelation: relation,
      x: 0,
      y: 0,
    }));
    return [...consumers, ...externalNodes];
  }, [
    materializationStatusByAssetId,
    pipelineAssets,
    staleness.byAssetName,
    typeCheckErrorAssetIds,
    typeCheckReport?.external_relations,
  ]);
  // Transitive stale upstreams of an asset, walked over the dependency graph.
  // Materializing an asset while these are stale reads their outdated tables, so
  // the asset cannot become fresh — we warn before building (§9 / §17).
  const assetsByName = useMemo(() => {
    const map = new Map<string, WebAsset>();
    for (const asset of activePipeline?.assets ?? []) {
      map.set(asset.name, asset);
    }
    return map;
  }, [activePipeline?.assets]);
  const staleUpstreamsOf = useCallback(
    (assetName: string): string[] => {
      const stale: string[] = [];
      const seen = new Set<string>();
      const walk = (name: string) => {
        const asset = assetsByName.get(name);
        if (!asset) return;
        for (const upstream of asset.upstreams ?? []) {
          if (seen.has(upstream)) continue;
          seen.add(upstream);
          const status = staleness.byAssetName[upstream];
          if (status && isStaleStatus(status.status)) stale.push(upstream);
          walk(upstream);
        }
      };
      walk(assetName);
      return stale;
    },
    [assetsByName, staleness.byAssetName],
  );
  const [staleBuildPrompt, setStaleBuildPrompt] = useState<{
    assetId: string;
    assetName: string;
    staleUpstreams: string[];
  } | null>(null);
  const [destructiveMaterializationPrompt, setDestructiveMaterializationPrompt] = useState<{
    kind: "full-refresh" | "backfill";
    assetId: string;
    assetName: string;
    start: string;
    end: string;
  } | null>(null);
  const [destructiveMaterializationConfirmation, setDestructiveMaterializationConfirmation] =
    useState("");
  const firstAssetId = displayedPipelineAssets[0]?.id ?? "";
  const [visualSelectedAssetId, setVisualSelectedAssetId] = useState(
    selectedAssetId ?? firstAssetId,
  );
  const effectiveSelectedAssetId = visualSelectedAssetId ?? selectedAssetId ?? firstAssetId;
  const selectedAsset =
    displayedPipelineAssets.find((asset) => asset.id === effectiveSelectedAssetId) ??
    displayedPipelineAssets[0];
  const selectedWorkspaceAsset = selectedAsset?.workspaceAsset;
  const selectedAssetSavedIntentContent = selectedWorkspaceAsset
    ? (editorDraft[selectedWorkspaceAsset.id] ?? selectedWorkspaceAsset.content)
    : null;
  const selectedAssetSavedIntentIdentity =
    selectedAssetSavedIntentContent === null
      ? null
      : normalizeAssetContentIdentity(selectedAssetSavedIntentContent);
  const selectedAssetRenderIdentity =
    activePipeline &&
    selectedWorkspaceAsset &&
    selectedAssetSavedIntentIdentity !== null &&
    selectedExecutionTimeWindow
      ? JSON.stringify([
          activePipeline.id,
          selectedWorkspaceAsset.id,
          selectedAssetSavedIntentIdentity,
          effectiveEnvironment || "default",
          selectedExecutionTimeWindow.start,
          selectedExecutionTimeWindow.end,
          false,
        ])
      : null;
  assetRenderIdentityRef.current = selectedAssetRenderIdentity;
  const assetRenderWorkspaceContentCompatible = Boolean(
    assetRenderSource &&
    selectedWorkspaceAsset?.id === assetRenderSource.assetId &&
    (normalizeAssetContentIdentity(selectedWorkspaceAsset.content) ===
      normalizeAssetContentIdentity(assetRenderSource.savedIntentContent) ||
      normalizeAssetContentIdentity(selectedWorkspaceAsset.content) ===
        normalizeAssetContentIdentity(assetRenderSource.workspaceContentAtStart)),
  );
  const assetRenderMatchesSelection =
    selectedAssetRenderIdentity !== null &&
    assetRenderSource?.identity === selectedAssetRenderIdentity &&
    assetRenderWorkspaceContentCompatible;
  const visibleAssetRenderResult = assetRenderMatchesSelection ? assetRenderResult : null;
  const visibleAssetRenderLoading = assetRenderMatchesSelection && assetRenderLoading;
  const visibleAssetRenderError = assetRenderMatchesSelection ? assetRenderError : null;
  const [explorerOpen, setExplorerOpen] = useState(false);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  // Large-screen collapse for the side columns; small screens keep using the
  // Sheets above. Collapsed columns are dropped from the grid entirely and
  // reopened from the top-bar toggles.
  const [explorerCollapsed, setExplorerCollapsed] = useState(false);
  const [inspectorCollapsed, setInspectorCollapsed] = useState(false);
  const [focusedQualityCheck, setFocusedQualityCheck] = useState<
    (QualityCheckFocus & { assetId: string }) | null
  >(null);
  const qualityFocusToken = useRef(0);
  const sidePanelGridColsClass = explorerCollapsed
    ? inspectorCollapsed
      ? "xl:grid-cols-[minmax(0,1fr)]"
      : "xl:grid-cols-[minmax(0,1fr)_320px]"
    : inspectorCollapsed
      ? "xl:grid-cols-[248px_minmax(0,1fr)]"
      : "xl:grid-cols-[248px_minmax(0,1fr)_320px]";
  const [newAssetOpen, setNewAssetOpen] = useState(false);
  const [newAssetPrefix, setNewAssetPrefix] = useState<string | null>(null);
  const [newAssetInitialExecutableContent, setNewAssetInitialExecutableContent] = useState<
    string | null
  >(null);
  const [newAssetInitialConnection, setNewAssetInitialConnection] = useState<string | null>(null);
  const [adhocNotebookOpen, setAdhocNotebookOpen] = useState(false);
  const [newPipelineOpen, setNewPipelineOpen] = useState(false);
  const [newFolderOpen, setNewFolderOpen] = useState(false);
  // Path of a pipeline just created here; once the workspace SSE update lists
  // it, we navigate onto it (the create response carries no ID).
  const [pendingPipelinePath, setPendingPipelinePath] = useState<string | null>(null);
  const [downstreamSource, setDownstreamSource] = useState<{
    id: string;
    name: string;
    connection?: string;
  } | null>(null);
  const [pipelineSettingsOpen, setPipelineSettingsOpen] = useState(false);
  const [pipelineSettingsSection, setPipelineSettingsSection] = useState<
    PipelineSettingsSection | undefined
  >(undefined);
  const [pipelineSettingsVariable, setPipelineSettingsVariable] = useState<string | undefined>();
  const openPipelineSettings = (section?: PipelineSettingsSection, variableName?: string) => {
    setExplorerOpen(false);
    setPipelineSettingsSection(section);
    setPipelineSettingsVariable(variableName);
    setPipelineSettingsOpen(true);
  };
  const openJinjaVariable = (variableName: string) =>
    openPipelineSettings("variables", variableName);
  const [pipelinePlanOpen, setPipelinePlanOpen] = useState(false);
  const [deploymentPlanOpen, setDeploymentPlanOpen] = useState(false);
  const resultsPanelRef = useRef<PanelImperativeHandle | null>(null);
  const [resultsCollapsed, setResultsCollapsed] = useState(false);
  const toggleResultsPanel = () => {
    const panel = resultsPanelRef.current;
    if (!panel) {
      return;
    }
    if (panel.isCollapsed()) {
      panel.expand();
    } else {
      panel.collapse();
    }
  };

  useEffect(() => {
    setVisualSelectedAssetId(selectedAssetId ?? firstAssetId);
  }, [firstAssetId, selectedAssetId]);

  useEffect(() => {
    assetRenderRequestId.current += 1;
    setAssetRenderSource(null);
    setAssetRenderResult(null);
    setAssetRenderError(null);
    setAssetRenderLoading(false);
  }, [selectedAssetRenderIdentity]);

  useEffect(() => {
    if (!assetRenderSource || selectedWorkspaceAsset?.id !== assetRenderSource.assetId) return;
    const workspaceContent = selectedWorkspaceAsset.content;
    if (
      normalizeAssetContentIdentity(workspaceContent) ===
        normalizeAssetContentIdentity(assetRenderSource.savedIntentContent) ||
      normalizeAssetContentIdentity(workspaceContent) ===
        normalizeAssetContentIdentity(assetRenderSource.workspaceContentAtStart)
    ) {
      return;
    }
    setEditorDraft((previous) => {
      if (previous[assetRenderSource.assetId] !== assetRenderSource.savedIntentContent) {
        return previous;
      }
      const next = { ...previous };
      delete next[assetRenderSource.assetId];
      return next;
    });
  }, [assetRenderSource, selectedWorkspaceAsset, setEditorDraft]);

  // Keep the global selection atoms pointed at the asset shown here so the
  // selection-derived state (editor drafts, schema suggestion tables,
  // intellisense context) works the same as on the classic workspace page.
  const setRouteSelection = useSetAtom(routeSelectionAtom);
  useEffect(() => {
    if (!activePipeline) {
      return;
    }

    setRouteSelection({
      pipeline: activePipeline.id,
      asset: effectiveSelectedAssetId ?? null,
    });
  }, [activePipeline, effectiveSelectedAssetId, setRouteSelection]);

  useEffect(() => {
    if (!workspace?.pipelines.length || activePipeline) {
      return;
    }

    navigate({
      to: "/pipelines/$pipelineId/canvas",
      params: { pipelineId: workspace.pipelines[0].id },
      search: buildSearch,
      replace: true,
    });
  }, [activePipeline, buildSearch, navigate, workspace?.pipelines]);

  useEffect(() => {
    if (!pendingPipelinePath || !workspace?.pipelines?.length) {
      return;
    }
    const normalized = pendingPipelinePath.replace(/^\.?\//, "").replace(/\/+$/, "");
    const created = workspace.pipelines.find(
      (item) => item.path === normalized || item.path.startsWith(`${normalized}/`),
    );
    if (created) {
      setPendingPipelinePath(null);
      void navigate({
        to: "/pipelines/$pipelineId/canvas",
        params: { pipelineId: created.id },
        search: buildSearch,
      });
    }
  }, [buildSearch, navigate, pendingPipelinePath, workspace?.pipelines]);

  const openBottom = (tab: AppResultTab) => {
    onResultTabChange?.(tab);
    // Make sure the results are visible when something routes output here.
    resultsPanelRef.current?.expand();
  };
  const runTypeCheck = useCallback(
    async (openTab = false) => {
      if (!activePipeline) {
        return;
      }
      if (openTab) {
        openBottom("typecheck");
      }
      setTypeCheckLoading(true);
      setTypeCheckError(null);
      try {
        await awaitWorkspaceSaves();
        const report = await typeCheckPipeline(activePipeline.id, {
          startDate: selectedExecutionTimeWindow?.start,
          endDate: selectedExecutionTimeWindow?.end,
        });
        setTypeCheckReport(report);
      } catch (cause) {
        setTypeCheckError(cause instanceof Error ? cause.message : "Type check failed.");
      } finally {
        setTypeCheckLoading(false);
      }
    },
    [activePipeline, selectedExecutionTimeWindow?.start, selectedExecutionTimeWindow?.end],
  );
  // Run the type check once per pipeline so the results badge and node markers
  // reflect the current state; the user can re-run from the panel after edits.
  useEffect(() => {
    if (!activePipeline) {
      return;
    }
    void runTypeCheck(false);
  }, [activePipeline?.id, runTypeCheck]);

  const seenCatalogReadySequenceRef = useRef(catalogReady.sequence);
  useEffect(() => {
    if (seenCatalogReadySequenceRef.current === catalogReady.sequence) {
      return;
    }
    seenCatalogReadySequenceRef.current = catalogReady.sequence;
    if (!activePipeline) {
      return;
    }
    // Catalog and lazy-column refreshes can complete back-to-back. Coalesce
    // them into one interactive report refresh so external canvas nodes appear
    // without polling or a manual type-check rerun.
    const timer = window.setTimeout(() => {
      void runTypeCheck(false);
    }, 100);
    return () => window.clearTimeout(timer);
  }, [activePipeline, catalogReady.sequence, runTypeCheck]);
  const runMaterialize = (assetId: string, name: string, scope: MaterializeScope = "asset") => {
    openBottom("materialize");
    void assetResults.runMaterializeForAsset(assetId, scope);
  };
  // Guarded materialize: if the asset depends on stale upstreams, prompt before
  // building (the build would read outdated upstream data and stay stale).
  const requestMaterialize = (assetId: string, name: string) => {
    const stale = staleUpstreamsOf(name);
    if (stale.length > 0) {
      setStaleBuildPrompt({ assetId, assetName: name, staleUpstreams: stale });
      return;
    }
    runMaterialize(assetId, name);
  };
  const materializeSelectedAsset = () => {
    const workspaceAsset = selectedAsset?.workspaceAsset;
    if (!activePipeline || !workspaceAsset || !selectedExecutionTimeWindow) {
      return;
    }
    requestMaterialize(workspaceAsset.id, workspaceAsset.name);
  };
  const fullRefreshSelectedAsset = () => {
    const workspaceAsset = selectedAsset?.workspaceAsset;
    if (!activePipeline || !workspaceAsset) {
      return;
    }
    setDestructiveMaterializationConfirmation("");
    setDestructiveMaterializationPrompt({
      kind: "full-refresh",
      assetId: workspaceAsset.id,
      assetName: workspaceAsset.name,
      start: selectedExecutionTimeWindow?.start ?? "",
      end: selectedExecutionTimeWindow?.end ?? "",
    });
  };
  const backfillSelectedAsset = () => {
    const workspaceAsset = selectedAsset?.workspaceAsset;
    if (!activePipeline || !workspaceAsset) {
      return;
    }
    setDestructiveMaterializationConfirmation("");
    setDestructiveMaterializationPrompt({
      kind: "backfill",
      assetId: workspaceAsset.id,
      assetName: workspaceAsset.name,
      start: selectedExecutionTimeWindow?.start ?? "",
      end: selectedExecutionTimeWindow?.end ?? "",
    });
  };
  const confirmDestructiveMaterialization = () => {
    if (!destructiveMaterializationPrompt) return;
    const isBackfill = destructiveMaterializationPrompt.kind === "backfill";
    openBottom("materialize");
    void assetResults.runMaterializeForAsset(
      destructiveMaterializationPrompt.assetId,
      "asset",
      undefined,
      {
        assetName: destructiveMaterializationPrompt.assetName,
        fullRefresh: !isBackfill,
        backfill: isBackfill,
        timeWindow:
          destructiveMaterializationPrompt.start && destructiveMaterializationPrompt.end
            ? {
                start: destructiveMaterializationPrompt.start,
                end: destructiveMaterializationPrompt.end,
              }
            : null,
        confirmedEnvironment: environmentPolicy?.confirm_destructive
          ? destructiveMaterializationConfirmation.trim()
          : undefined,
      },
    );
    setDestructiveMaterializationPrompt(null);
    setDestructiveMaterializationConfirmation("");
  };
  const inspectSelectedAsset = () => {
    const workspaceAsset = selectedAsset?.workspaceAsset;
    if (!activePipeline || !workspaceAsset) {
      return;
    }
    openBottom("inspect");
    void assetResults.runInspectForAsset(
      workspaceAsset.id,
      editorDraft[workspaceAsset.id] ?? workspaceAsset.content,
    );
  };
  const renderSelectedAsset = async () => {
    const workspaceAsset = selectedAsset?.workspaceAsset;
    const executionWindow = selectedExecutionTimeWindow;
    if (!activePipeline || !workspaceAsset || !executionWindow) {
      return;
    }
    const sourceIdentity = selectedAssetRenderIdentity;
    const sourceIntentContent = selectedAssetSavedIntentContent;
    if (!sourceIdentity || sourceIntentContent === null) {
      return;
    }
    const requestId = ++assetRenderRequestId.current;
    openBottom("render");
    setAssetRenderSource({
      identity: sourceIdentity,
      assetId: workspaceAsset.id,
      savedIntentContent: sourceIntentContent,
      workspaceContentAtStart: workspaceAsset.content,
    });
    setAssetRenderLoading(true);
    setAssetRenderError(null);
    try {
      await awaitWorkspaceSaves();
      const result = await renderPipelineAsset(activePipeline.id, {
        asset_name: workspaceAsset.name,
        source: { kind: "working_tree" },
        environment: effectiveEnvironment || undefined,
        start_date: executionWindow.start,
        end_date: executionWindow.end,
        full_refresh: false,
      });
      if (
        assetRenderRequestId.current === requestId &&
        assetRenderIdentityRef.current === sourceIdentity
      ) {
        setAssetRenderResult(result);
      }
    } catch (cause) {
      if (
        assetRenderRequestId.current === requestId &&
        assetRenderIdentityRef.current === sourceIdentity
      ) {
        setAssetRenderError(cause instanceof Error ? cause.message : "Asset render failed.");
      }
    } finally {
      if (
        assetRenderRequestId.current === requestId &&
        assetRenderIdentityRef.current === sourceIdentity
      ) {
        setAssetRenderLoading(false);
      }
    }
  };
  useEffect(() => {
    if (
      resultTab !== "render" ||
      selectedAssetRenderIdentity === null ||
      assetRenderSource?.identity === selectedAssetRenderIdentity
    ) {
      return;
    }
    const timer = window.setTimeout(() => void renderSelectedAsset(), 350);
    // The render identity includes the asset, saved-intent content,
    // environment, and execution window. Changing any of them while the Render
    // tab is active therefore loads the latest preview after typing settles.
    return () => window.clearTimeout(timer);
  }, [assetRenderSource?.identity, resultTab, selectedAssetRenderIdentity]);
  // Any pipeline asset can provide graph and Jinja scope. The separately
  // selected query connection supplies the dialect and execution target.
  const adhocContextAsset = useMemo(() => {
    const candidates = activePipeline?.assets ?? [];
    const selected = candidates.find((asset) => asset.id === effectiveSelectedAssetId);
    return selected ?? candidates[0] ?? null;
  }, [activePipeline?.assets, effectiveSelectedAssetId]);
  const adhocConnections = workspace?.query_connections ?? [];
  const adhocContextConnection = adhocContextAsset
    ? effectiveConnectionForAsset(adhocContextAsset)
    : null;
  const adhocConnection =
    adhocConnections.find((connection) => connection.name === storedAdhocConnection) ??
    adhocConnections.find((connection) => connection.name === adhocContextConnection) ??
    adhocConnections[0] ??
    null;
  const setAdhocConnection = useCallback(
    (connection: string) => {
      storeAdhocConnection(connection);
      setAdhocRenderedQuery(null);
      setAdhocResult(null);
    },
    [storeAdhocConnection],
  );
  useEffect(() => {
    setAdhocRenderedQuery(null);
    setAdhocResult(null);
  }, [adhocConnection?.name, pipelineId]);
  const runAdhocQuery = async () => {
    if (!activePipeline) {
      return;
    }
    openBottom("query");
    const connection = adhocConnection?.name ?? null;
    if (!connection || !adhocContextAsset) {
      setAdhocRenderedQuery(null);
      setAdhocResult({
        status: "error",
        columns: [],
        rows: [],
        error: "No query-capable connection is configured for this environment.",
      });
      return;
    }
    setAdhocLoading(true);
    try {
      // Ad hoc queries are Jinja templates: render them with the pipeline's
      // variables (and the selected execution window) before executing.
      let queryText = adhocQuery;
      try {
        const rendered = await renderJinjaAsset({
          assetId: adhocContextAsset.id,
          content: adhocQuery,
          timeWindow: selectedExecutionTimeWindow,
        });
        if (rendered.status === "error") {
          setAdhocRenderedQuery(null);
          setAdhocResult({
            status: "error",
            columns: [],
            rows: [],
            error: `Jinja rendering failed: ${rendered.error || "unknown error"}`,
          });
          return;
        }
        if (rendered.rendered?.trim()) {
          queryText = rendered.rendered;
        }
      } catch {
        // Rendering is best-effort; fall back to the raw query text.
      }
      setAdhocRenderedQuery(queryText);
      const result = await runSQLQuery({
        connection,
        environment: selectedEnvironment,
        query: queryText,
        limit: adhocQueryLimit,
      });
      setAdhocResult(result);
    } catch (error) {
      setAdhocResult({
        status: "error",
        columns: [],
        rows: [],
        error: String(error),
      });
    } finally {
      setAdhocLoading(false);
    }
  };
  const selectAsset = (assetId: string) => {
    setVisualSelectedAssetId(assetId);
    onAssetSelect?.(assetId);
    setExplorerOpen(false);
  };
  const reviewFailedCheck = (assetId: string) => {
    const target = displayedPipelineAssets.find((asset) => asset.id === assetId);
    const failure = target?.staleness?.failed_checks?.[0];
    if (!failure || !target?.staleness?.quality_on_current_content) return;
    selectAsset(assetId);
    qualityFocusToken.current += 1;
    setFocusedQualityCheck({ assetId, ...failure, token: qualityFocusToken.current });
    if (window.matchMedia("(min-width: 1280px)").matches) {
      setInspectorCollapsed(false);
    } else {
      setInspectorOpen(true);
    }
  };
  const goToAsset = (targetPipelineId: string, assetId: string) => {
    void navigate({
      to: appAssetViewPath(view),
      params: { pipelineId: targetPipelineId, assetId },
      search: { ...buildSearch, editor: "asset" },
    });
  };
  const runAssetById = (assetId: string) => {
    const target = displayedPipelineAssets.find((asset) => asset.id === assetId);
    const workspaceAsset = target?.workspaceAsset;
    if (!activePipeline || !workspaceAsset) {
      return;
    }
    setVisualSelectedAssetId(assetId);
    requestMaterialize(workspaceAsset.id, target?.name ?? assetId);
  };
  const deleteAssetById = async (assetId: string) => {
    if (!activePipeline) {
      return;
    }
    // The workspace event stream refreshes the atom once the file is gone.
    await deleteAsset(activePipeline.id, assetId);
  };
  const goToCatalog = (assetId?: string) => {
    void navigate({
      to: "/catalog",
      search: assetId ? { asset: assetId } : {},
    });
  };
  // The ad hoc editor only renders in code/split editor panes. Preserve the
  // current editor layout when one exists, and add an editor beside the canvas
  // when it does not. Clicking again toggles back to the current asset.
  const openAdhoc = () => {
    setExplorerOpen(false);
    if (editorMode === "adhoc") {
      void navigate({
        to: appAssetViewPath(view),
        params: { pipelineId, assetId: effectiveSelectedAssetId },
        search: { ...buildSearch, editor: "asset" },
      });
      return;
    }
    void navigate({
      to: appAssetViewPath(view === "canvas" ? "split" : view),
      params: { pipelineId, assetId: effectiveSelectedAssetId },
      search: { ...buildSearch, editor: "adhoc" },
    });
  };
  const openNewAsset = () => {
    setDownstreamSource(null);
    setNewAssetPrefix(null);
    setNewAssetInitialExecutableContent(null);
    setNewAssetInitialConnection(null);
    setNewAssetOpen(true);
  };
  // Canvas right-click entry point: seeds the dialog's name suggestion with
  // the prefix group the click landed in.
  const openNewAssetInGroup = (prefix?: string) => {
    setDownstreamSource(null);
    setNewAssetPrefix(prefix ?? null);
    setNewAssetInitialExecutableContent(null);
    setNewAssetInitialConnection(null);
    setNewAssetOpen(true);
  };
  const createDownstreamAsset = (source: { id: string; name: string }) => {
    const sourceAsset = activePipeline?.assets.find((asset) => asset.id === source.id);
    const sourceConnection = sourceAsset ? effectiveConnectionForAsset(sourceAsset) : null;
    setDownstreamSource({
      ...source,
      ...(sourceConnection ? { connection: sourceConnection } : {}),
    });
    setNewAssetPrefix(null);
    setNewAssetInitialExecutableContent(null);
    setNewAssetInitialConnection(null);
    setNewAssetOpen(true);
  };
  const convertAdhocToAsset = () => {
    setDownstreamSource(null);
    setNewAssetPrefix(null);
    setNewAssetInitialExecutableContent(adhocQuery);
    setNewAssetInitialConnection(adhocConnection?.name ?? null);
    setNewAssetOpen(true);
  };

  if (!workspace) {
    return (
      <AppPage>
        <BuildLoadingState />
      </AppPage>
    );
  }

  if (!activePipeline) {
    if (workspace.pipelines.length > 0) {
      return (
        <AppPage>
          <BuildLoadingState />
        </AppPage>
      );
    }
    return (
      <AppPage>
        <PageHeader title="Build" subtitle="Create a pipeline to start building assets" />
        <div className="flex min-h-0 flex-1 items-center justify-center px-3 pb-3">
          <AppPanel className="flex max-w-md flex-col items-center gap-3 p-6 text-center">
            <Layers className="size-8 text-muted-foreground" />
            <div>
              <h2 className="font-medium">No pipelines yet</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Create a pipeline to add assets and see their lineage.
              </p>
            </div>
            <Button onClick={() => setNewPipelineOpen(true)}>
              <Plus data-icon="inline-start" />
              New pipeline
            </Button>
          </AppPanel>
        </div>
        <NewPipelineDialog
          open={newPipelineOpen}
          onOpenChange={setNewPipelineOpen}
          existingPaths={new Set()}
          onCreated={(path) => setPendingPipelinePath(path)}
        />
      </AppPage>
    );
  }

  if (!selectedAsset) {
    return (
      <AppPage>
        <PageHeader
          title={activePipeline.name}
          subtitle="This pipeline does not contain any assets yet"
          actions={
            <Button onClick={openNewAsset}>
              <FilePlus2 data-icon="inline-start" />
              New asset
            </Button>
          }
        />
        <div className="flex min-h-0 flex-1 items-center justify-center px-3 pb-3">
          <AppPanel className="flex max-w-md flex-col items-center gap-3 p-6 text-center">
            <FileCode className="size-8 text-muted-foreground" />
            <div>
              <h2 className="font-medium">No assets yet</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Add the first asset to begin shaping this pipeline.
              </p>
            </div>
            <Button onClick={openNewAsset}>
              <FilePlus2 data-icon="inline-start" />
              New asset
            </Button>
          </AppPanel>
        </div>
        <NewAssetDialog
          open={newAssetOpen}
          onOpenChange={setNewAssetOpen}
          pipelineId={activePipeline.id}
          pipelineName={activePipeline.name}
          existingAssetNames={existingAssetNames}
          onCreated={(assetId) => goToAsset(activePipeline.id, assetId)}
        />
      </AppPage>
    );
  }

  const buildContext: BuildContextValue = {
    pipelineId,
    pipeline: activePipeline,
    pipelineAssets: displayedPipelineAssets,
    routedAssetId: selectedAssetId,
    selectedAssetId: effectiveSelectedAssetId,
    selectedAsset,
    view,
    buildSearch,
    editorMode,
    selectAsset,
    goToAsset,
    runAssetById,
    deleteAssetById,
    goToCatalog,
    openPipelineConnections: () => openPipelineSettings("connections"),
    openPipelineVariable: openJinjaVariable,
    openNewAsset,
    openNewAssetInGroup,
    createDownstreamAsset,
    openInspector: () => setInspectorOpen(true),
    reviewFailedCheck,
    importExternalRelation: setExternalRelationImportId,
    openBottom,
    materializeSelectedAsset,
    fullRefreshSelectedAsset,
    backfillSelectedAsset,
    inspectSelectedAsset,
    renderSelectedAsset: () => void renderSelectedAsset(),
    runAdhocQuery,
    convertAdhocToAsset,
    convertAdhocToNotebook: () => setAdhocNotebookOpen(true),
    adhocContextAsset,
    adhocConnections,
    adhocConnection,
    setAdhocConnection,
    adhocLoading,
    materializeLoading: assetResults.materializeLoading,
    inspectLoading: assetResults.inspectLoading,
    renderLoading: visibleAssetRenderLoading,
    renderBlockedReason: executionWindowBlockedReason,
    executionBlocked,
    executionBlockedReason,
  };

  return (
    <BuildContext.Provider value={buildContext}>
      <AppPage>
        <BuildTopBar
          pipelineId={pipelineId}
          pipelineLabel={activePipeline?.name ?? pipelineId}
          selectedAsset={selectedAsset}
          selectedAssetId={effectiveSelectedAssetId}
          assetCrumbLoading={!selectedAsset.workspaceAsset}
          resultTab={resultTab}
          editorMode={editorMode}
          currentView={view}
          onOpenExplorer={() => setExplorerOpen(true)}
          onOpenInspector={() => setInspectorOpen(true)}
          explorerCollapsed={explorerCollapsed}
          inspectorCollapsed={inspectorCollapsed}
          onToggleExplorer={() => setExplorerCollapsed((value) => !value)}
          onToggleInspector={() => setInspectorCollapsed((value) => !value)}
          onReviewRun={() => setPipelinePlanOpen(true)}
          onReviewDeploy={() => setDeploymentPlanOpen(true)}
          deployState={deployState}
          runSourceLabel={pipelineRunSourceLabel.replace(/^Run /, "")}
          runDisabled={!activePipeline}
          runTitle="Review the saved source, readiness checks, and rendered operations before running"
        />
        <div
          className={cn("grid min-h-0 flex-1 grid-cols-1 gap-3 px-3 pb-3", sidePanelGridColsClass)}
        >
          {!explorerCollapsed ? (
            <AppPanel className="hidden min-h-0 xl:flex xl:flex-col">
              <Explorer
                pipelineId={pipelineId}
                selectedAssetId={effectiveSelectedAssetId}
                buildSearch={buildSearch}
                onAssetSelect={selectAsset}
                onAdhoc={openAdhoc}
                onNewAsset={openNewAsset}
                onNewPipeline={() => setNewPipelineOpen(true)}
                onNewFolder={() => setNewFolderOpen(true)}
                onPipelineSettings={() => openPipelineSettings()}
              />
            </AppPanel>
          ) : null}

          <PanelGroup orientation="vertical" className="h-full min-h-0">
            <Panel minSize="120px" className="min-h-0">
              <AppPanel className="relative flex h-full min-h-0 overflow-hidden">
                <DelimitedCardContent className="h-full min-h-0 flex-1 p-0">
                  <Outlet />
                </DelimitedCardContent>
                {view !== "code" ? (
                  <FloatingViewSwitcher
                    pipelineId={pipelineId}
                    selectedAssetId={effectiveSelectedAssetId}
                    currentView={view}
                    search={buildSearch}
                    onNewAsset={openNewAsset}
                  />
                ) : null}
              </AppPanel>
            </Panel>
            <PanelResizeHandle
              className={cn(
                "my-1.5 h-1.5 shrink-0 cursor-row-resize rounded-full bg-border transition-colors hover:bg-primary/40",
                resultsCollapsed && "pointer-events-none opacity-0",
              )}
            />
            <Panel
              collapsible
              collapsedSize="38px"
              minSize="120px"
              defaultSize="224px"
              panelRef={resultsPanelRef}
              onResize={() => setResultsCollapsed(Boolean(resultsPanelRef.current?.isCollapsed()))}
              className="min-h-0"
            >
              <ResultsPanel
                pipelineId={pipelineId}
                activeTab={resultTab}
                onTabChange={openBottom}
                collapsed={resultsCollapsed}
                onToggleCollapse={toggleResultsPanel}
                typeCheckReport={typeCheckReport}
                typeCheckLoading={typeCheckLoading}
                typeCheckError={typeCheckError}
                onRunTypeCheck={() => void runTypeCheck(false)}
                onSelectAsset={selectAsset}
                onImportExternalRelation={setExternalRelationImportId}
                inspectResult={assetResults.inspectResult}
                inspectLoading={assetResults.inspectLoading}
                renderResult={visibleAssetRenderResult}
                renderLoading={visibleAssetRenderLoading}
                renderError={visibleAssetRenderError}
                onRender={() => void renderSelectedAsset()}
                canLoadMoreInspectRows={assetResults.canLoadMoreInspectRows}
                onLoadMoreInspectRows={assetResults.loadMoreInspectRows}
                selectedMaterializeEntry={assetResults.selectedMaterializeEntry}
                materializeOutputHtml={assetResults.materializeOutputHtml}
                pipelineMaterializeLoading={assetResults.pipelineMaterializeLoading}
                adhocResult={adhocResult}
                adhocRenderedQuery={adhocRenderedQuery}
                adhocLoading={adhocLoading}
              />
            </Panel>
          </PanelGroup>

          {!inspectorCollapsed ? (
            <AppPanel className="hidden min-h-0 xl:flex xl:flex-col">
              <Inspector
                asset={selectedAsset}
                focusedCheck={
                  focusedQualityCheck?.assetId === selectedAsset.id
                    ? focusedQualityCheck
                    : undefined
                }
              />
            </AppPanel>
          ) : null}
        </div>

        <Sheet open={explorerOpen} onOpenChange={setExplorerOpen}>
          <SheetContent side="left" className="w-80 gap-0 p-0 sm:max-w-80">
            <SheetTitle className="sr-only">Explorer</SheetTitle>
            <Explorer
              pipelineId={pipelineId}
              selectedAssetId={effectiveSelectedAssetId}
              buildSearch={buildSearch}
              onAssetSelect={selectAsset}
              onAdhoc={openAdhoc}
              onNewAsset={openNewAsset}
              onNewPipeline={() => setNewPipelineOpen(true)}
              onNewFolder={() => setNewFolderOpen(true)}
              onPipelineSettings={() => openPipelineSettings()}
            />
          </SheetContent>
        </Sheet>
        <Sheet open={inspectorOpen} onOpenChange={setInspectorOpen}>
          <SheetContent side="right" className="w-[22rem] gap-0 p-0 sm:max-w-[22rem]">
            <SheetTitle className="sr-only">Asset properties</SheetTitle>
            <Inspector
              asset={selectedAsset}
              focusedCheck={
                focusedQualityCheck?.assetId === selectedAsset.id ? focusedQualityCheck : undefined
              }
            />
          </SheetContent>
        </Sheet>

        <PipelinePlanSheet
          open={pipelinePlanOpen}
          onOpenChange={setPipelinePlanOpen}
          pipelineId={activePipeline?.id ?? pipelineId}
          pipelineName={activePipeline?.name ?? pipelineId}
          environment={effectiveEnvironment}
          timeWindow={selectedExecutionTimeWindow}
          source={pipelineRunSource}
          confirmDestructive={Boolean(environmentPolicy?.confirm_destructive)}
          onAccepted={(run, plan) => {
            openBottom("materialize");
            assetResults.trackConfirmedPipelineRun(run, {
              start: plan.context.start_date,
              end: plan.context.end_date,
            });
          }}
        />
        <PipelinePlanSheet
          open={deploymentPlanOpen}
          onOpenChange={setDeploymentPlanOpen}
          pipelineId={activePipeline?.id ?? pipelineId}
          pipelineName={activePipeline?.name ?? pipelineId}
          environment={effectiveEnvironment}
          timeWindow={selectedExecutionTimeWindow}
          intent="deploy"
          onDeploy={(expectedSourceMerkle) => deployState.deploy(expectedSourceMerkle)}
        />

        <NewAssetDialog
          open={newAssetOpen}
          onOpenChange={(open) => {
            setNewAssetOpen(open);
            if (!open) {
              setDownstreamSource(null);
              setNewAssetPrefix(null);
              setNewAssetInitialExecutableContent(null);
              setNewAssetInitialConnection(null);
            }
          }}
          pipelineId={activePipeline?.id}
          pipelineName={activePipeline?.name}
          existingAssetNames={existingAssetNames}
          downstreamSource={downstreamSource}
          namePrefix={newAssetPrefix}
          initialExecutableContent={newAssetInitialExecutableContent}
          initialConnection={newAssetInitialConnection}
          onCreated={(assetId) => goToAsset(activePipeline?.id ?? pipelineId, assetId)}
        />
        <AdhocToNotebookDialog
          open={adhocNotebookOpen}
          onOpenChange={setAdhocNotebookOpen}
          notebooks={workspace?.notebooks ?? []}
          query={adhocQuery}
        />
        <NewPipelineDialog
          open={newPipelineOpen}
          onOpenChange={setNewPipelineOpen}
          existingPaths={new Set((workspace?.pipelines ?? []).map((item) => item.path))}
          onCreated={(path) => setPendingPipelinePath(path)}
        />
        <NewFolderDialog
          open={newFolderOpen}
          onOpenChange={setNewFolderOpen}
          pipelineName={activePipeline?.name}
          onConfirm={(prefix) => {
            setNewFolderOpen(false);
            openNewAssetInGroup(prefix);
          }}
        />
        <PipelineSettingsDialog
          open={pipelineSettingsOpen}
          onOpenChange={setPipelineSettingsOpen}
          pipelineId={pipelineId}
          initialSection={pipelineSettingsSection}
          highlightedVariable={pipelineSettingsVariable}
        />
        <ExternalRelationImportDialog
          pipelineId={activePipeline.id}
          relationId={externalRelationImportId}
          onOpenChange={(open) => {
            if (!open) setExternalRelationImportId(null);
          }}
          onImported={() => runTypeCheck(false)}
        />
        <Dialog
          open={destructiveMaterializationPrompt !== null}
          onOpenChange={(open) => {
            if (!open) {
              setDestructiveMaterializationPrompt(null);
              setDestructiveMaterializationConfirmation("");
            }
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {destructiveMaterializationPrompt?.kind === "backfill"
                  ? "Backfill"
                  : "Full refresh"}{" "}
                {destructiveMaterializationPrompt?.assetName}?
              </DialogTitle>
              <DialogDescription>
                {destructiveMaterializationPrompt?.kind === "backfill"
                  ? "Run this asset for the exact UTC window below. Backfill is available only when independent windows can be replayed safely."
                  : "This rebuilds the table from scratch and can be expensive. Existing rows are replaced with the result for the selected execution window."}
              </DialogDescription>
            </DialogHeader>
            {destructiveMaterializationPrompt?.kind === "backfill" ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="backfill-start">Start (UTC)</Label>
                  <Input
                    id="backfill-start"
                    type="datetime-local"
                    step={1}
                    value={toUTCDateTimeInput(destructiveMaterializationPrompt.start)}
                    onChange={(event) =>
                      setDestructiveMaterializationPrompt((current) =>
                        current
                          ? { ...current, start: fromUTCDateTimeInput(event.target.value) }
                          : current,
                      )
                    }
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="backfill-end">End (UTC)</Label>
                  <Input
                    id="backfill-end"
                    type="datetime-local"
                    step={1}
                    value={toUTCDateTimeInput(destructiveMaterializationPrompt.end)}
                    onChange={(event) =>
                      setDestructiveMaterializationPrompt((current) =>
                        current
                          ? { ...current, end: fromUTCDateTimeInput(event.target.value) }
                          : current,
                      )
                    }
                  />
                </div>
              </div>
            ) : null}
            {environmentPolicy?.confirm_destructive ? (
              <div className="space-y-2">
                <Label htmlFor="destructive-materialization-environment-confirmation">
                  Type <span className="font-mono">{effectiveEnvironment}</span> to confirm
                </Label>
                <Input
                  id="destructive-materialization-environment-confirmation"
                  value={destructiveMaterializationConfirmation}
                  onChange={(event) =>
                    setDestructiveMaterializationConfirmation(event.target.value)
                  }
                  autoComplete="off"
                />
              </div>
            ) : null}
            <DialogFooter>
              <Button variant="outline" onClick={() => setDestructiveMaterializationPrompt(null)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={
                  (destructiveMaterializationPrompt?.kind === "backfill" &&
                    !isValidExecutionWindow(
                      destructiveMaterializationPrompt.start,
                      destructiveMaterializationPrompt.end,
                    )) ||
                  (Boolean(environmentPolicy?.confirm_destructive) &&
                    destructiveMaterializationConfirmation.trim() !== effectiveEnvironment)
                }
                onClick={confirmDestructiveMaterialization}
              >
                Run{" "}
                {destructiveMaterializationPrompt?.kind === "backfill"
                  ? "backfill"
                  : "full refresh"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        <Dialog
          open={staleBuildPrompt !== null}
          onOpenChange={(open) => {
            if (!open) setStaleBuildPrompt(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <AlertTriangle className="size-4 text-amber-500" />
                Upstream is out of date
              </DialogTitle>
              <DialogDescription>
                <span className="font-mono">{staleBuildPrompt?.assetName}</span> depends on{" "}
                {staleBuildPrompt?.staleUpstreams.length} stale upstream
                {staleBuildPrompt?.staleUpstreams.length === 1 ? "" : "s"}. Building now reads their
                outdated tables, so this asset will stay stale until its upstreams are current.
                Build the upstreams first to get an up-to-date result.
              </DialogDescription>
            </DialogHeader>
            <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border p-2 font-mono text-xs">
              {staleBuildPrompt?.staleUpstreams.map((name) => (
                <div key={name} className="flex items-center gap-1.5">
                  <span className="size-1.5 rounded-full bg-amber-500" />
                  {name}
                </div>
              ))}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setStaleBuildPrompt(null)}>
                Cancel
              </Button>
              <Button
                variant="outline"
                onClick={() => {
                  if (staleBuildPrompt)
                    runMaterialize(staleBuildPrompt.assetId, staleBuildPrompt.assetName, "asset");
                  setStaleBuildPrompt(null);
                }}
              >
                Build anyway
              </Button>
              <Button
                onClick={() => {
                  if (staleBuildPrompt)
                    runMaterialize(
                      staleBuildPrompt.assetId,
                      staleBuildPrompt.assetName,
                      "asset_with_upstreams",
                    );
                  setStaleBuildPrompt(null);
                }}
              >
                <Hammer className="size-4" />
                Build upstreams first
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </AppPage>
    </BuildContext.Provider>
  );
}

function BuildTopBar({
  pipelineId,
  pipelineLabel,
  selectedAsset,
  selectedAssetId,
  assetCrumbLoading = false,
  resultTab,
  editorMode,
  currentView,
  onOpenExplorer,
  onOpenInspector,
  explorerCollapsed = false,
  inspectorCollapsed = false,
  onToggleExplorer,
  onToggleInspector,
  onReviewRun,
  onReviewDeploy,
  deployState,
  runSourceLabel,
  runDisabled = false,
  runTitle,
}: {
  pipelineId: string;
  pipelineLabel: string;
  selectedAsset: BuildAsset;
  selectedAssetId: string;
  assetCrumbLoading?: boolean;
  resultTab: AppResultTab;
  editorMode: AppEditorMode;
  currentView: AppBuildView;
  onOpenExplorer: () => void;
  onOpenInspector: () => void;
  explorerCollapsed?: boolean;
  inspectorCollapsed?: boolean;
  onToggleExplorer?: () => void;
  onToggleInspector?: () => void;
  onReviewRun: () => void;
  onReviewDeploy: () => void;
  deployState?: PipelineDeployState;
  runSourceLabel?: string;
  runDisabled?: boolean;
  runTitle?: string;
}) {
  const search: AppBuildSearch = { result: resultTab, editor: editorMode };

  return (
    <div className="flex min-h-12 shrink-0 items-center gap-2 px-3">
      <Button
        variant="ghost"
        size="sm"
        className="xl:hidden"
        onClick={onOpenExplorer}
        aria-label="Open explorer"
      >
        <PanelLeft className="size-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="hidden xl:inline-flex"
        onClick={onToggleExplorer}
        aria-pressed={!explorerCollapsed}
        title={explorerCollapsed ? "Show explorer" : "Hide explorer"}
        aria-label={explorerCollapsed ? "Show explorer" : "Hide explorer"}
      >
        {explorerCollapsed ? (
          <PanelLeftOpen className="size-3.5" />
        ) : (
          <PanelLeftClose className="size-3.5" />
        )}
      </Button>
      <Breadcrumb className="min-w-0 flex-1">
        <BreadcrumbList className="flex-nowrap text-xs">
          <BreadcrumbItem className="min-w-0">
            <BreadcrumbLink asChild className="truncate">
              <Link to="/">data_platform</Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <span className="text-muted-foreground">pipeline</span>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem className="min-w-0">
            <BreadcrumbLink asChild className="truncate font-mono">
              <Link to="/pipelines/$pipelineId/canvas" params={{ pipelineId }} search={search}>
                {pipelineLabel}
              </Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem className="min-w-0">
            {assetCrumbLoading ? (
              <Skeleton className="h-4 w-32" aria-label="Loading asset" />
            ) : (
              <BreadcrumbPage className="truncate font-mono">{selectedAsset.name}</BreadcrumbPage>
            )}
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
      {/* Toggle: a second click leaves ad-hoc mode and returns to the asset. */}
      <Button
        asChild
        variant={editorMode === "adhoc" ? "secondary" : "outline"}
        size="sm"
        className={cn(
          "hidden lg:inline-flex",
          editorMode === "adhoc" ? "text-primary ring-1 ring-primary/30" : null,
        )}
      >
        <Link
          to={appAssetViewPath(
            editorMode === "adhoc" ? currentView : currentView === "canvas" ? "split" : currentView,
          )}
          params={{ pipelineId, assetId: selectedAssetId }}
          search={{
            result: resultTab,
            editor: editorMode === "adhoc" ? "asset" : "adhoc",
          }}
          aria-pressed={editorMode === "adhoc"}
        >
          <Terminal className="size-3.5" /> Ad-hoc
        </Link>
      </Button>
      {deployState ? <DeployButton deployState={deployState} onReview={onReviewDeploy} /> : null}
      <Button size="sm" onClick={onReviewRun} disabled={runDisabled} title={runTitle}>
        <Play data-icon="inline-start" /> Review run
        {runSourceLabel ? <span className="sr-only"> from {runSourceLabel}</span> : null}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="hidden xl:inline-flex"
        onClick={onToggleInspector}
        aria-pressed={!inspectorCollapsed}
        title={inspectorCollapsed ? "Show properties" : "Hide properties"}
        aria-label={inspectorCollapsed ? "Show properties" : "Hide properties"}
      >
        {inspectorCollapsed ? (
          <PanelRightOpen className="size-3.5" />
        ) : (
          <PanelRightClose className="size-3.5" />
        )}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="xl:hidden"
        onClick={onOpenInspector}
        title="Asset properties"
        aria-label="Asset properties"
      >
        <PanelRight className="size-3.5" />
      </Button>
    </div>
  );
}

function FloatingViewSwitcher({
  pipelineId,
  selectedAssetId,
  currentView,
  search,
  onNewAsset,
}: {
  pipelineId: string;
  selectedAssetId: string;
  currentView: AppBuildView;
  search: AppBuildSearch;
  onNewAsset?: () => void;
}) {
  return (
    <div className="absolute right-1 top-1 z-20 flex items-center gap-2">
      {onNewAsset ? (
        <Button size="sm" onClick={onNewAsset} className="shadow-sm">
          <Plus className="size-3.5" /> New asset
        </Button>
      ) : null}
      <BuildViewButtonGroup
        pipelineId={pipelineId}
        selectedAssetId={selectedAssetId}
        currentView={currentView}
        search={search}
        className="rounded-lg border bg-background/90 shadow-sm backdrop-blur"
      />
    </div>
  );
}

function BuildViewButtonGroup({
  pipelineId,
  selectedAssetId,
  currentView,
  search,
  className,
}: {
  pipelineId: string;
  selectedAssetId: string;
  currentView: AppBuildView;
  search: AppBuildSearch;
  className?: string;
}) {
  return (
    <ButtonGroup className={className}>
      <ViewLink
        pipelineId={pipelineId}
        selectedAssetId={selectedAssetId}
        currentView={currentView}
        view="code"
        search={search}
        icon={FileCode}
        label="Code"
      />
      <ViewLink
        pipelineId={pipelineId}
        selectedAssetId={selectedAssetId}
        currentView={currentView}
        view="split"
        search={search}
        icon={Columns2}
        label="Split"
      />
      <ViewLink
        pipelineId={pipelineId}
        selectedAssetId={selectedAssetId}
        currentView={currentView}
        view="canvas"
        search={search}
        icon={Layers}
        label="Canvas"
      />
    </ButtonGroup>
  );
}

function ViewLink({
  pipelineId,
  selectedAssetId,
  currentView,
  view,
  search,
  icon: Icon,
  label,
}: {
  pipelineId: string;
  selectedAssetId: string;
  currentView: AppBuildView;
  view: AppBuildView;
  search: AppBuildSearch;
  icon: ComponentType<{ className?: string }>;
  label: string;
}) {
  return (
    <Button asChild variant={currentView === view ? "secondary" : "outline"} size="icon-sm">
      <Link
        to={appAssetViewPath(view)}
        params={{ pipelineId, assetId: selectedAssetId }}
        search={search}
        aria-label={`${label} view`}
        title={`${label} view`}
      >
        <Icon className="size-3.5" />
        <span className="sr-only">{label}</span>
      </Link>
    </Button>
  );
}

export function appAssetViewPath(view: AppBuildView) {
  if (view === "split") return "/pipelines/$pipelineId/assets/$assetId/split" as const;
  if (view === "code") return "/pipelines/$pipelineId/assets/$assetId/code" as const;
  return "/pipelines/$pipelineId/assets/$assetId/canvas" as const;
}

export function appBuildViewFromPath(pathname: string): AppBuildView {
  if (pathname.endsWith("/split")) return "split";
  if (pathname.endsWith("/code")) return "code";
  return "canvas";
}

function Explorer({
  pipelineId,
  selectedAssetId,
  buildSearch,
  onAssetSelect,
  onAdhoc,
  onNewAsset,
  onNewPipeline,
  onNewFolder,
  onPipelineSettings,
}: {
  pipelineId: string;
  selectedAssetId: string;
  buildSearch: AppBuildSearch;
  onAssetSelect: (assetId: string) => void;
  onAdhoc: () => void;
  onNewAsset: () => void;
  onNewPipeline: () => void;
  onNewFolder: () => void;
  onPipelineSettings: () => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const { pipelineAssets } = useBuildContext();
  const adhocActive = buildSearch.editor === "adhoc";
  const pipelineItems = workspace?.pipelines ?? [];
  const notebookItems = workspace?.notebooks ?? [];
  const [newNotebookOpen, setNewNotebookOpen] = useState(false);
  const [assetFilter, setAssetFilter] = useState("");
  const normalizedAssetFilter = assetFilter.trim().toLowerCase();
  const filteredAssets = useMemo(() => {
    const authoredAssets = pipelineAssets.filter((asset) => !asset.readOnly);
    if (!normalizedAssetFilter) {
      return authoredAssets;
    }
    return authoredAssets.filter((asset) =>
      [
        asset.name,
        asset.displayName,
        assetSidebarName(asset),
        assetGroupName(asset),
        asset.prefix,
        asset.path,
        asset.type,
        asset.connection,
      ]
        .filter((value): value is string => Boolean(value))
        .some((value) => value.toLowerCase().includes(normalizedAssetFilter)),
    );
  }, [normalizedAssetFilter, pipelineAssets]);
  const assetsByGroup = useMemo(
    () =>
      filteredAssets.reduce<Record<string, BuildAsset[]>>((groups, asset) => {
        const group = assetGroupName(asset);
        groups[group] = [...(groups[group] ?? []), asset];
        return groups;
      }, {}),
    [filteredAssets],
  );

  return (
    <>
      <DelimitedCardHeader>
        <Database className="size-4 text-primary" />
        <DelimitedCardTitle>Explorer</DelimitedCardTitle>
        <Button
          size="icon-sm"
          variant="ghost"
          className="ml-auto"
          onClick={onNewPipeline}
          aria-label="New pipeline"
          title="New pipeline"
        >
          <Plus data-icon="inline-start" />
        </Button>
      </DelimitedCardHeader>
      <div className="border-b p-2">
        <InputGroup className="bg-background">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            value={assetFilter}
            onChange={(event) => setAssetFilter(event.target.value)}
            placeholder="Filter assets..."
            aria-label="Filter assets"
            autoComplete="off"
            className="font-mono text-xs"
          />
          {assetFilter ? (
            <InputGroupAddon align="inline-end">
              <InputGroupButton
                size="icon-xs"
                onClick={() => setAssetFilter("")}
                aria-label="Clear asset filter"
                title="Clear asset filter"
              >
                <X />
              </InputGroupButton>
            </InputGroupAddon>
          ) : null}
        </InputGroup>
      </div>
      {/* Force Radix's inline `display: table` content wrapper to block so long
          asset names truncate instead of widening the sidebar horizontally. */}
      <ScrollArea
        className="min-h-0 flex-1"
        horizontalScrollBarClassName="hidden"
        viewportClassName="[&>div]:!block"
      >
        <div className="space-y-2 p-2">
          <ExplorerSection label="Pipelines" icon={Layers} count={pipelineItems.length}>
            {pipelineItems.map((item) => {
              const activePipeline = item.id === pipelineId;
              const pipelineLabel = item.name || item.path || item.id;
              return (
                <div key={item.id}>
                  <div
                    className={cn(
                      "group flex h-7 w-full items-center rounded-md hover:bg-muted",
                      activePipeline ? "bg-muted text-foreground" : "text-muted-foreground",
                    )}
                  >
                    <Link
                      to="/pipelines/$pipelineId/canvas"
                      params={{ pipelineId: item.id }}
                      search={buildSearch}
                      className="flex min-w-0 flex-1 items-center gap-1.5 px-2 text-left font-mono text-xs"
                    >
                      <Layers className="size-3.5 text-primary" />
                      <span className="truncate">{pipelineLabel}</span>
                    </Link>
                    {activePipeline ? (
                      <div className="flex shrink-0 items-center pr-0.5">
                        <Button
                          type="button"
                          size="icon-xs"
                          variant="ghost"
                          onClick={onNewAsset}
                          aria-label={`New asset in ${pipelineLabel}`}
                          title="New asset"
                        >
                          <FilePlus2 data-icon="inline-start" />
                        </Button>
                        <Button
                          type="button"
                          size="icon-xs"
                          variant="ghost"
                          onClick={onNewFolder}
                          aria-label={`New folder in ${pipelineLabel}`}
                          title="New folder"
                        >
                          <FolderPlus data-icon="inline-start" />
                        </Button>
                      </div>
                    ) : null}
                  </div>
                  {activePipeline ? (
                    <div className="mt-1 space-y-0.5 border-l pl-3 ml-3">
                      {Object.entries(assetsByGroup).length > 0 ? (
                        Object.entries(assetsByGroup).map(([group, groupAssets]) => (
                          <div key={group}>
                            <div className="px-2 py-1 font-mono text-[11px] text-muted-foreground">
                              {group}/
                            </div>
                            {groupAssets.map((asset) => (
                              <AssetButton
                                key={asset.id}
                                asset={asset}
                                selected={!adhocActive && selectedAssetId === asset.id}
                                onSelect={() => onAssetSelect(asset.id)}
                              />
                            ))}
                          </div>
                        ))
                      ) : (
                        <div className="px-2 py-1 text-xs text-muted-foreground">
                          {normalizedAssetFilter ? "No matching assets." : "No assets found."}
                        </div>
                      )}
                      <div className="mt-1 border-t pt-1">
                        <button
                          className={cn(
                            "flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-left font-mono text-xs hover:bg-muted",
                            adhocActive
                              ? "bg-primary/10 text-foreground ring-1 ring-primary/20"
                              : "text-muted-foreground",
                          )}
                          onClick={onAdhoc}
                        >
                          <Terminal
                            className={cn("size-3.5", adhocActive ? "text-primary" : null)}
                          />{" "}
                          Ad-hoc query
                        </button>
                        <button
                          className="flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-left font-mono text-xs text-muted-foreground hover:bg-muted"
                          onClick={onPipelineSettings}
                        >
                          <SettingsIcon /> Pipeline settings
                        </button>
                      </div>
                    </div>
                  ) : null}
                </div>
              );
            })}
          </ExplorerSection>

          <ExplorerSection label="Notebooks" icon={BookOpen} count={notebookItems.length}>
            {notebookItems.length > 0 ? (
              notebookItems.map((notebook) => (
                <Link
                  key={notebook.id}
                  to="/notebooks/$notebookId"
                  params={{ notebookId: notebook.id }}
                  className="flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-left font-mono text-xs text-muted-foreground hover:bg-muted"
                  activeProps={{ className: "bg-muted text-foreground" }}
                >
                  <BookOpen className="size-3.5 text-primary" />
                  <span className="truncate">{notebook.title || notebook.path || notebook.id}</span>
                </Link>
              ))
            ) : (
              <div className="px-2 py-1 text-xs text-muted-foreground">No notebooks yet.</div>
            )}
          </ExplorerSection>
          <button
            onClick={() => setNewNotebookOpen(true)}
            className="mt-1 flex h-8 w-full items-center gap-2 rounded-md border border-dashed px-2 text-left text-xs text-muted-foreground hover:bg-muted disabled:opacity-50"
          >
            <Plus className="size-3.5" /> New notebook
          </button>
        </div>
      </ScrollArea>
      <NewNotebookDialog open={newNotebookOpen} onOpenChange={setNewNotebookOpen} />
    </>
  );
}

function ExplorerSection({
  label,
  icon: Icon,
  count,
  children,
}: {
  label: string;
  icon: ComponentType<{ className?: string }>;
  count: number;
  children: ReactNode;
}) {
  return (
    <div>
      <div className="flex h-7 items-center gap-1.5 rounded-md px-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        <Icon className="size-3.5" />
        {label}
        <span className="ml-auto">{count}</span>
      </div>
      <div className="space-y-0.5">{children}</div>
    </div>
  );
}

function AssetButton({
  asset,
  selected,
  onSelect,
}: {
  asset: BuildAsset;
  selected: boolean;
  onSelect: () => void;
}) {
  const Icon = kindMeta[asset.kind].icon;
  const latestAttemptFailed =
    asset.status === "failed" && asset.staleness?.last_run_status === "failed";
  const latestAttemptCancelled =
    asset.status !== "pending" && asset.staleness?.last_run_status === "cancelled";
  const attemptLabel = asset.staleness ? lastRunLabel(asset.staleness) : "Last run failed";
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-left font-mono text-xs hover:bg-muted",
        selected ? "bg-primary/10 text-foreground ring-1 ring-primary/20" : "text-muted-foreground",
      )}
    >
      <Icon className="size-3.5 text-primary" />
      <span className="min-w-0 flex-1 truncate">{assetSidebarName(asset)}</span>
      {asset.staleness &&
      (asset.staleness.status !== "fresh" || latestAttemptFailed || latestAttemptCancelled) ? (
        <span
          title={
            latestAttemptFailed || latestAttemptCancelled
              ? attemptLabel
              : `Staleness: ${stalenessLabel(asset.staleness)}`
          }
          className={cn(
            "size-1.5 rounded-full",
            latestAttemptFailed
              ? "bg-destructive"
              : latestAttemptCancelled
                ? "bg-muted-foreground"
                : stalenessDotClassName(asset.staleness),
          )}
        />
      ) : null}
    </button>
  );
}

function PipelineCanvas({ onAssetSelect }: { onAssetSelect: (assetId: string) => void }) {
  const {
    pipelineAssets,
    routedAssetId,
    createDownstreamAsset,
    openNewAssetInGroup,
    runAssetById,
    deleteAssetById,
    goToCatalog,
    openPipelineConnections,
    reviewFailedCheck,
    importExternalRelation,
  } = useBuildContext();
  const sqlHoveredAssetId = useAtomValue(sqlHoveredAssetAtom);
  return (
    <AppLineageCanvas
      assets={pipelineAssets}
      selectedAssetId={routedAssetId}
      focusAssetId={routedAssetId}
      highlightAssetId={sqlHoveredAssetId ?? undefined}
      onAssetSelect={onAssetSelect}
      onRunAsset={runAssetById}
      onDeleteAsset={deleteAssetById}
      onGoToAsset={(assetId) => goToCatalog(assetId)}
      onAssetConnectionClick={() => openPipelineConnections()}
      onReviewFailedCheck={reviewFailedCheck}
      onImportExternalRelation={importExternalRelation}
      goToLabel="Open in catalog"
      onCreateAsset={({ prefix }) => openNewAssetInGroup(prefix)}
      onCreateDownstream={(assetId) => {
        const source = pipelineAssets.find((asset) => asset.id === assetId);
        if (source) {
          createDownstreamAsset({ id: source.id, name: source.name });
        }
      }}
    />
  );
}

export function AppBuildCanvasView() {
  const { selectAsset } = useBuildContext();
  return <PipelineCanvas onAssetSelect={selectAsset} />;
}

export function AppBuildSplitView() {
  const { selectedAsset, selectAsset, editorMode } = useBuildContext();
  return (
    <PanelGroup orientation="horizontal" className="h-full min-h-0 min-w-0">
      <Panel defaultSize={50} minSize={28} className="min-w-0">
        <EditorWorkspace asset={selectedAsset} adhoc={editorMode === "adhoc"} />
      </Panel>
      <PanelResizeHandle className="w-px bg-border" />
      <Panel defaultSize={50} minSize={28} className="min-w-0">
        <PipelineCanvas onAssetSelect={selectAsset} />
      </Panel>
    </PanelGroup>
  );
}

export function AppBuildCodeView() {
  const { selectedAsset, editorMode } = useBuildContext();
  return <EditorWorkspace asset={selectedAsset} adhoc={editorMode === "adhoc"} />;
}

function EditorWorkspace({ asset, adhoc }: { asset: BuildAsset; adhoc: boolean }) {
  const {
    pipelineId,
    selectedAssetId,
    view,
    buildSearch,
    goToAsset,
    importExternalRelation,
    openPipelineVariable,
    openInspector,
    materializeSelectedAsset,
    fullRefreshSelectedAsset,
    backfillSelectedAsset,
    inspectSelectedAsset,
    renderSelectedAsset,
    materializeLoading,
    inspectLoading,
    renderLoading,
    renderBlockedReason,
    executionBlocked,
    executionBlockedReason,
  } = useBuildContext();
  const isMobile = useIsMobile();
  const editorOnly = view === "code";
  const showActionLabels = editorOnly && !isMobile;
  if (adhoc) {
    return <AdhocEditor showActionLabels={showActionLabels} />;
  }

  const actionLabel =
    asset.kind === "source"
      ? "Validate"
      : asset.kind === "sensor"
        ? "Check now"
        : asset.kind === "ingestr" || asset.kind === "load"
          ? "Run"
          : "Materialize";
  const filename =
    asset.path ?? `${asset.dir ? `${asset.dir}/` : ""}${asset.name}${kindMeta[asset.kind].ext}`;
  const renderableAsset = Boolean(
    asset.workspaceAsset && isRenderableAssetType(asset.workspaceAsset.type),
  );

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <EditorFilenameHeader filename={filename}>
        <EditorActionButtons
          actionLabel={actionLabel}
          showLabels={showActionLabels}
          showInspect={asset.kind !== "source"}
          showRender={renderableAsset}
          onRun={materializeSelectedAsset}
          onFullRefresh={
            asset.workspaceAsset?.supports_full_refresh ? fullRefreshSelectedAsset : undefined
          }
          onBackfill={asset.staleness?.backfill_safe ? backfillSelectedAsset : undefined}
          onInspect={inspectSelectedAsset}
          onRender={renderSelectedAsset}
          runDisabled={materializeLoading || executionBlocked || !asset.workspaceAsset}
          runBlockedReason={executionBlocked ? executionBlockedReason : undefined}
          runLoading={materializeLoading}
          inspectDisabled={inspectLoading || !asset.workspaceAsset}
          inspectLoading={inspectLoading}
          renderDisabled={renderLoading || !asset.workspaceAsset}
          renderLoading={renderLoading}
          renderBlockedReason={renderBlockedReason}
        />
        {asset.workspaceAsset ? (
          <Button
            variant="ghost"
            size="xs"
            className="text-muted-foreground xl:hidden"
            onClick={openInspector}
            title="Asset properties"
            aria-label="Asset properties"
          >
            <PanelRight className="size-3.5" />
            {showActionLabels ? <span className="ml-1">Properties</span> : null}
          </Button>
        ) : null}
        {editorOnly ? (
          <BuildViewButtonGroup
            pipelineId={pipelineId}
            selectedAssetId={selectedAssetId}
            currentView={view}
            search={buildSearch}
          />
        ) : null}
      </EditorFilenameHeader>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          {asset.workspaceAsset?.parse_error ? (
            <div className="flex shrink-0 items-start gap-2 border-b border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <div className="min-w-0">
                <span className="font-medium">This asset could not be parsed.</span> Fix the file
                below to restore it.
                <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap font-mono text-[11px] opacity-80">
                  {asset.workspaceAsset.parse_error}
                </pre>
              </div>
            </div>
          ) : null}
          {asset.workspaceAsset &&
          asset.pipelineId &&
          isLoadAssetType(asset.workspaceAsset.type) ? (
            <LoadParametersEditor
              asset={asset.workspaceAsset}
              pipelineId={asset.pipelineId}
              onGoToAsset={goToAsset}
            />
          ) : asset.workspaceAsset &&
            asset.pipelineId &&
            isAPIAssetType(asset.workspaceAsset.type) ? (
            <ApiParametersEditor
              key={asset.workspaceAsset.id}
              asset={asset.workspaceAsset}
              pipelineId={asset.pipelineId}
              onInspect={inspectSelectedAsset}
              onGoToAsset={goToAsset}
              onGoToJinjaVariable={openPipelineVariable}
            />
          ) : asset.workspaceAsset &&
            asset.pipelineId &&
            (isSeedAssetType(asset.workspaceAsset.type) ||
              isSensorAssetType(asset.workspaceAsset.type)) ? (
            <SemanticParametersEditor
              key={asset.workspaceAsset.id}
              asset={asset.workspaceAsset}
              pipelineId={asset.pipelineId}
              onCheck={materializeSelectedAsset}
              onGoToAsset={goToAsset}
              onGoToJinjaVariable={openPipelineVariable}
            />
          ) : asset.workspaceAsset && asset.pipelineId ? (
            <AppAssetEditor
              asset={asset.workspaceAsset}
              pipelineId={asset.pipelineId}
              onInspect={inspectSelectedAsset}
              onGoToAsset={goToAsset}
              onImportExternalRelation={importExternalRelation}
              onGoToJinjaVariable={openPipelineVariable}
            />
          ) : (
            <ResultsEmpty label="The asset source is unavailable." />
          )}
        </div>
      </div>
    </div>
  );
}

function EditorFilenameHeader({ filename, children }: { filename: string; children?: ReactNode }) {
  return (
    <div className="flex h-10 min-w-0 shrink-0 items-center gap-2 overflow-hidden border-b bg-background/70 px-3">
      <span className="block min-w-0 flex-[1_1_0] truncate font-mono text-[11px] text-muted-foreground">
        {filename}
      </span>
      {children ? (
        <div className="ml-auto flex shrink-0 items-center gap-1.5">{children}</div>
      ) : null}
    </div>
  );
}

function EditorActionButtons({
  actionLabel,
  showLabels,
  showInspect,
  showRender,
  onRun,
  onInspect,
  onRender,
  onFullRefresh,
  onBackfill,
  runDisabled = false,
  runBlockedReason,
  runLoading = false,
  inspectDisabled = false,
  inspectLoading = false,
  renderDisabled = false,
  renderLoading = false,
  renderBlockedReason,
}: {
  actionLabel: string;
  showLabels: boolean;
  showInspect: boolean;
  showRender: boolean;
  onRun: () => void;
  onInspect: () => void;
  onRender: () => void;
  onFullRefresh?: () => void;
  onBackfill?: () => void;
  runDisabled?: boolean;
  runBlockedReason?: string;
  runLoading?: boolean;
  inspectDisabled?: boolean;
  inspectLoading?: boolean;
  renderDisabled?: boolean;
  renderLoading?: boolean;
  renderBlockedReason?: string;
}) {
  const runLabel = runLoading ? "Running..." : actionLabel;
  const inspectLabel = inspectLoading ? "Loading..." : "Inspect";
  const renderLabel = renderLoading ? "Rendering..." : "Render";
  return (
    <>
      {onFullRefresh || onBackfill ? (
        <ButtonGroup>
          <Button
            size={showLabels ? "sm" : "icon-sm"}
            onClick={onRun}
            disabled={runDisabled}
            aria-label={actionLabel}
            title={runBlockedReason ?? actionLabel}
          >
            <Hammer data-icon="inline-start" />
            {showLabels ? runLabel : <span className="sr-only">{runLabel}</span>}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                size="icon-sm"
                disabled={runDisabled}
                aria-label="Materialization options"
              >
                <ChevronDown />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuGroup>
                <DropdownMenuLabel>Materialization</DropdownMenuLabel>
                {onFullRefresh ? (
                  <DropdownMenuItem onClick={onFullRefresh}>Full refresh</DropdownMenuItem>
                ) : null}
                {onBackfill ? (
                  <DropdownMenuItem onClick={onBackfill}>Backfill range…</DropdownMenuItem>
                ) : null}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </ButtonGroup>
      ) : (
        <Button
          size={showLabels ? "sm" : "icon-sm"}
          onClick={onRun}
          disabled={runDisabled}
          aria-label={actionLabel}
          title={runBlockedReason ?? actionLabel}
        >
          <Hammer data-icon="inline-start" />
          {showLabels ? runLabel : <span className="sr-only">{runLabel}</span>}
        </Button>
      )}
      {showRender ? (
        <Button
          variant="outline"
          size={showLabels ? "sm" : "icon-sm"}
          onClick={onRender}
          disabled={renderDisabled || Boolean(renderBlockedReason)}
          aria-busy={renderLoading}
          aria-label="Render saved asset"
          title={renderBlockedReason ?? "Render saved asset"}
        >
          <FileCode className="size-3.5" data-icon="inline-start" />
          {showLabels ? renderLabel : <span className="sr-only">{renderLabel}</span>}
        </Button>
      ) : null}
      {showInspect ? (
        <Button
          variant="outline"
          size={showLabels ? "sm" : "icon-sm"}
          onClick={onInspect}
          disabled={inspectDisabled}
          aria-label="Inspect"
          title="Inspect"
        >
          <Eye className="size-3.5" />
          {showLabels ? inspectLabel : <span className="sr-only">{inspectLabel}</span>}
        </Button>
      ) : null}
    </>
  );
}

function AdhocEditor({ showActionLabels }: { showActionLabels: boolean }) {
  const {
    pipelineId,
    selectedAssetId,
    view,
    buildSearch,
    adhocContextAsset,
    adhocConnections,
    adhocConnection,
    setAdhocConnection,
    adhocLoading,
    runAdhocQuery,
    convertAdhocToAsset,
    convertAdhocToNotebook,
    goToAsset,
  } = useBuildContext();
  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <EditorFilenameHeader filename="Ad-hoc query">
        <Select
          value={adhocConnection?.name}
          onValueChange={setAdhocConnection}
          disabled={adhocLoading || adhocConnections.length === 0}
        >
          <SelectTrigger size="sm" className="min-w-32 max-w-48" aria-label="Ad-hoc connection">
            <Database className="text-muted-foreground" />
            <SelectValue placeholder="Connection" />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectLabel>Query connection</SelectLabel>
              {adhocConnections.map((connection) => (
                <SelectItem key={connection.name} value={connection.name}>
                  {connection.name}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Button
          variant="outline"
          size={showActionLabels ? "sm" : "icon-sm"}
          onClick={convertAdhocToNotebook}
          disabled={!adhocContextAsset || !adhocConnection}
          aria-label="Convert to notebook cell"
          title="Convert to notebook cell"
        >
          <BookOpen className="size-3.5" />
          {showActionLabels ? "Notebook cell" : <span className="sr-only">Notebook cell</span>}
        </Button>
        <Button
          variant="outline"
          size={showActionLabels ? "sm" : "icon-sm"}
          onClick={convertAdhocToAsset}
          disabled={!adhocContextAsset || !adhocConnection}
          aria-label="Convert to asset"
          title="Convert to asset"
        >
          <FilePlus2 className="size-3.5" />
          {showActionLabels ? "Asset" : <span className="sr-only">Asset</span>}
        </Button>
        <Button
          size={showActionLabels ? "sm" : "icon-sm"}
          onClick={runAdhocQuery}
          disabled={adhocLoading || !adhocContextAsset || !adhocConnection}
          aria-label="Run"
          title="Run (⌘ + ↵)"
        >
          <Play className="size-3.5" />
          {showActionLabels ? (
            adhocLoading ? (
              "Running..."
            ) : (
              "Run"
            )
          ) : (
            <span className="sr-only">Run</span>
          )}
        </Button>
        {view === "code" ? (
          <BuildViewButtonGroup
            pipelineId={pipelineId}
            selectedAssetId={selectedAssetId}
            currentView={view}
            search={buildSearch}
          />
        ) : null}
      </EditorFilenameHeader>
      {adhocContextAsset && adhocConnection ? (
        <AppAdhocEditor
          pipelineId={pipelineId}
          contextAsset={adhocContextAsset}
          connection={adhocConnection}
          onRunQuery={runAdhocQuery}
          onGoToAsset={goToAsset}
        />
      ) : (
        <ResultsEmpty
          label={
            adhocContextAsset
              ? "Configure a query-capable connection to run ad-hoc SQL."
              : "Add an asset before opening an ad-hoc query."
          }
        />
      )}
    </div>
  );
}

function ResultsPanel({
  pipelineId,
  activeTab,
  onTabChange,
  collapsed,
  onToggleCollapse,
  typeCheckReport,
  typeCheckLoading,
  typeCheckError,
  onRunTypeCheck,
  onSelectAsset,
  onImportExternalRelation,
  inspectResult,
  inspectLoading,
  renderResult,
  renderLoading,
  renderError,
  onRender,
  canLoadMoreInspectRows,
  onLoadMoreInspectRows,
  selectedMaterializeEntry,
  materializeOutputHtml,
  pipelineMaterializeLoading,
  adhocResult,
  adhocRenderedQuery,
  adhocLoading,
}: {
  pipelineId: string;
  activeTab: AppResultTab;
  onTabChange: (tab: AppResultTab) => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
  typeCheckReport?: PipelineTypeCheckReport | null;
  typeCheckLoading?: boolean;
  typeCheckError?: string | null;
  onRunTypeCheck?: () => void;
  onSelectAsset?: (assetId: string) => void;
  onImportExternalRelation?: (relationId: string) => void;
  inspectResult: AssetInspectResponse | null;
  inspectLoading: boolean;
  renderResult: AssetRenderResult | null;
  renderLoading: boolean;
  renderError: string | null;
  onRender: () => void;
  canLoadMoreInspectRows: boolean;
  onLoadMoreInspectRows: () => void;
  selectedMaterializeEntry: MaterializeHistoryEntry | null;
  materializeOutputHtml: string | null;
  pipelineMaterializeLoading: boolean;
  adhocResult: SqlQueryResponse | null;
  adhocRenderedQuery: string | null;
  adhocLoading: boolean;
}) {
  return (
    <AppPanel className="flex h-full min-h-0 flex-col">
      <Tabs
        value={activeTab}
        onValueChange={(value) => {
          if (resultTabs.includes(value as AppResultTab)) onTabChange(value as AppResultTab);
        }}
        className="flex h-full min-h-0 flex-col"
      >
        <DelimitedCardHeader className="min-h-9 gap-1 bg-muted py-1">
          <ScrollArea
            className="min-w-0 flex-1"
            horizontalScrollBarClassName="hidden"
            viewportClassName="w-full"
          >
            <TabsList className={scrollableTabsListClass}>
              <TabsTrigger value="inspect" className={scrollableTabsTriggerClass}>
                <Table2 className="size-3.5" />
                Inspect
              </TabsTrigger>
              <TabsTrigger value="render" className={scrollableTabsTriggerClass}>
                <FileCode className="size-3.5" />
                Render
              </TabsTrigger>
              <TabsTrigger value="materialize" className={scrollableTabsTriggerClass}>
                <Hammer className="size-3.5" />
                Materialize
              </TabsTrigger>
              <TabsTrigger value="query" className={scrollableTabsTriggerClass}>
                <Terminal className="size-3.5" />
                Query
              </TabsTrigger>
              <TabsTrigger value="typecheck" className={scrollableTabsTriggerClass}>
                <Bell className="size-3.5" />
                Type check
                {typeCheckReport &&
                typeCheckReport.summary.errors + typeCheckReport.summary.warnings > 0 ? (
                  <span
                    className={cn(
                      "ml-1 rounded-full px-1 text-[10px] text-white",
                      typeCheckReport.summary.errors > 0 ? "bg-red-500" : "bg-amber-500",
                    )}
                  >
                    {typeCheckReport.summary.errors + typeCheckReport.summary.warnings}
                  </span>
                ) : null}
              </TabsTrigger>
            </TabsList>
          </ScrollArea>
          <Button
            variant="ghost"
            size="icon-sm"
            className="shrink-0"
            onClick={onToggleCollapse}
            aria-label={collapsed ? "Expand results panel" : "Collapse results panel"}
            title={collapsed ? "Expand" : "Collapse"}
          >
            {collapsed ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
          </Button>
        </DelimitedCardHeader>
        <TabsContent value="inspect" className="flex min-h-0 flex-1 flex-col overflow-hidden p-0">
          {inspectLoading && !inspectResult ? (
            <ResultsLoading label="Inspecting asset..." />
          ) : inspectResult?.info ? (
            <div className="flex h-full min-h-0 items-center justify-center overflow-auto p-3">
              <InspectInfoCard message={inspectResult.info} testId="app-inspect-info" />
            </div>
          ) : inspectResult?.error ? (
            <div className="flex h-full min-h-0 items-center justify-center overflow-auto p-3">
              <InspectWarningCard message={inspectResult.error} testId="app-inspect-warning" />
            </div>
          ) : inspectResult ? (
            <>
              <RenderedQueryDisclosure query={inspectResult.operation?.query} />
              <div className="min-h-0 flex-1">
                <AssetInspectView
                  columns={inspectResult.columns ?? []}
                  rows={inspectResult.rows ?? []}
                  loading={inspectLoading}
                  canLoadMore={canLoadMoreInspectRows}
                  onLoadMore={onLoadMoreInspectRows}
                  warning={inspectResult.warning}
                  frameless
                />
              </div>
            </>
          ) : (
            <ResultsEmpty label="Inspect an asset to preview its data here." />
          )}
        </TabsContent>
        <TabsContent value="render" className="min-h-0 flex-1 overflow-hidden p-0">
          <AssetRenderView
            pipelineId={pipelineId}
            result={renderResult}
            loading={renderLoading}
            error={renderError}
            onRetry={onRender}
          />
        </TabsContent>
        <TabsContent value="materialize" className="min-h-0 flex-1 overflow-hidden p-2">
          <WorkspaceMaterializeOutputView
            entry={selectedMaterializeEntry}
            outputHtml={materializeOutputHtml ?? ""}
            pipelineMaterializeLoading={pipelineMaterializeLoading}
          />
        </TabsContent>
        <TabsContent value="query" className="flex min-h-0 flex-1 flex-col overflow-hidden p-0">
          {adhocLoading ? (
            <ResultsLoading label="Running query..." />
          ) : adhocResult?.error ? (
            <div className="flex h-full min-h-0 items-center justify-center overflow-auto p-3">
              <InspectWarningCard message={adhocResult.error} testId="app-query-warning" />
            </div>
          ) : adhocResult ? (
            <>
              <RenderedQueryDisclosure
                query={
                  adhocResult.truncated && adhocRenderedQuery
                    ? withSQLPreviewLimit(adhocRenderedQuery, adhocQueryLimit)
                    : adhocRenderedQuery
                }
                warning={
                  adhocResult.truncated
                    ? `Result limited to the first ${adhocQueryLimit} rows`
                    : undefined
                }
              />
              <div className="min-h-0 flex-1">
                <AssetInspectView
                  columns={adhocResult.columns ?? []}
                  rows={(adhocResult.rows ?? []) as Record<string, unknown>[]}
                  frameless
                />
              </div>
            </>
          ) : (
            <ResultsEmpty label="Run an ad hoc query to see results here." />
          )}
        </TabsContent>
        <TabsContent value="typecheck" className="min-h-0 flex-1 overflow-hidden p-0">
          <TypeCheckPanel
            report={typeCheckReport ?? null}
            loading={Boolean(typeCheckLoading)}
            error={typeCheckError ?? null}
            onRun={onRunTypeCheck}
            onSelectAsset={onSelectAsset}
            onResolutionAction={(action) => onImportExternalRelation?.(action.relation_id)}
          />
        </TabsContent>
      </Tabs>
    </AppPanel>
  );
}

// RenderedQueryDisclosure shows the query that actually ran (post-Jinja) as a
// single collapsed line above a results table, expandable to the full text.
function RenderedQueryDisclosure({ query, warning }: { query?: string | null; warning?: string }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const trimmed = query?.trim();
  if (!trimmed) {
    return null;
  }

  const copyQuery = async () => {
    if (await copyTextToClipboard(trimmed)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    }
  };

  return (
    <div className="shrink-0 border-b bg-muted/30" data-testid="rendered-query-disclosure">
      <div className="flex min-w-0 items-center transition-colors hover:bg-muted">
        <button
          type="button"
          className="flex h-8 min-w-0 flex-1 items-center gap-1.5 px-2 text-left text-[11px] text-muted-foreground"
          onClick={() => setOpen((value) => !value)}
          aria-expanded={open}
        >
          <Terminal
            className="size-3 shrink-0"
            strokeWidth={open ? 2.75 : 1.5}
            data-testid="rendered-query-icon"
          />
          <span className="shrink-0 font-semibold uppercase tracking-wide">Query</span>
          {warning ? (
            <span
              role="img"
              aria-label={warning}
              title={warning}
              className="shrink-0 text-muted-foreground"
            >
              <AlertTriangle className="size-3" />
            </span>
          ) : null}
          {!open ? (
            <span className="min-w-0 flex-1 truncate font-mono">
              {trimmed.replace(/\s+/g, " ")}
            </span>
          ) : null}
        </button>
        <Button
          variant="outline"
          size="xs"
          className="mr-2 h-6 shrink-0 px-1.5 text-[10px] text-muted-foreground"
          onClick={() => void copyQuery()}
          aria-label="Copy rendered query"
        >
          {copied ? "copied" : "copy"}
        </Button>
      </div>
      {open ? <SqlPreview query={trimmed} /> : null}
    </div>
  );
}

function ResultsLoading({ label }: { label: string }) {
  return (
    <div className="flex h-full min-h-0 items-center justify-center bg-background">
      <div className="flex items-center gap-2 text-xs opacity-80">
        <Spinner className="size-4" />
        <span>{label}</span>
      </div>
    </div>
  );
}

function ResultsEmpty({ label }: { label: string }) {
  return (
    <div className="flex h-full min-h-0 items-center justify-center bg-background px-4 text-center text-xs text-muted-foreground">
      {label}
    </div>
  );
}

function Inspector({
  asset,
  focusedCheck,
}: {
  asset: BuildAsset;
  focusedCheck?: QualityCheckFocus;
}) {
  const workspaceAsset = asset.workspaceAsset;
  const editable = Boolean(workspaceAsset && asset.pipelineId);

  // Title: the asset's own (leaf) name; subtitle: its namespace and integration.
  // The file path is intentionally omitted — it just repeats those two.
  const { prefix, title } = assetNameParts(asset.name);
  const subtitle = [prefix, asset.type ?? asset.integration].filter(Boolean).join(" · ");

  return (
    <div data-testid="asset-inspector" className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2 pr-12">
        <Sliders className="size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <div className="truncate font-monaco text-[13px] font-medium">{title}</div>
          {subtitle ? (
            <p className="truncate text-[11px] text-muted-foreground">{subtitle}</p>
          ) : null}
        </div>
      </div>
      {editable && workspaceAsset && asset.pipelineId ? (
        <ErrorBoundary
          resetKey={workspaceAsset.content ?? ""}
          fallback={
            <div className="flex min-h-0 flex-1 items-center justify-center p-6 text-center text-xs text-muted-foreground">
              These properties can&apos;t be shown right now — the asset file may have a syntax
              error. Fix it in the code editor to continue.
            </div>
          }
        >
          <AssetGuidedCards
            asset={workspaceAsset}
            pipelineId={asset.pipelineId}
            quality={asset.staleness}
            focusedCheck={focusedCheck}
          />
        </ErrorBoundary>
      ) : (
        <div className="flex min-h-0 flex-1 items-center justify-center p-6 text-center text-xs text-muted-foreground">
          Properties become editable once this asset is saved to the pipeline.
        </div>
      )}
    </div>
  );
}

// DeployButton keeps deployment status compact; mutating always opens the
// reviewed deployment plan rather than snapshotting immediately.
function DeployButton({
  deployState,
  onReview,
}: {
  deployState: PipelineDeployState;
  onReview: () => void;
}) {
  const { status, loading, error, deploying, refresh, driftedFileCount } = deployState;
  if (!status) {
    if (!loading && !error) return null;
    if (error) {
      return (
        <Button variant="outline" size="sm" onClick={() => void refresh()} title={error}>
          <RefreshCw data-icon="inline-start" /> Retry deployment status
        </Button>
      );
    }
    return (
      <Button variant="ghost" size="sm" disabled title="Resolving deployment status">
        <Spinner data-icon="inline-start" />
        Deployment…
      </Button>
    );
  }

  if (status.has_snapshot && status.in_sync && status.executable) {
    const currentDeployment = deploymentLabel(status.ordinal, status.version_id);
    return (
      <Button variant="ghost" size="sm" disabled title={`${currentDeployment} is current`}>
        <Package className="size-3.5 text-emerald-600" /> Deployed
      </Button>
    );
  }

  const label = status.has_snapshot
    ? status.executable
      ? `Redeploy (${driftedFileCount} file${driftedFileCount === 1 ? "" : "s"} changed)`
      : "Repair deployment"
    : "Deploy";
  const title = status.has_snapshot
    ? status.executable
      ? `Working tree differs from ${deploymentLabel(status.ordinal, status.version_id, "deployment")}`
      : `The latest deployment is not executable: ${status.integrity_error ?? "integrity validation failed"}`
    : "No deployment exists yet; schedules require an exact deployment pin";
  return (
    <Button variant="outline" size="sm" onClick={onReview} disabled={deploying} title={title}>
      <Package
        className={cn(
          "size-3.5",
          status.has_snapshot
            ? status.executable
              ? "text-amber-600"
              : "text-destructive"
            : undefined,
        )}
      />
      {deploying ? "Deploying…" : label}
    </Button>
  );
}

function SettingsIcon() {
  return <Sliders className="size-3.5" />;
}

function isRenderableAssetType(assetType: string) {
  return (
    isSqlAssetType(assetType) ||
    isSeedAssetType(assetType) ||
    isSensorAssetType(assetType) ||
    isPythonAssetType(assetType) ||
    isLoadAssetType(assetType) ||
    isAPIAssetType(assetType) ||
    isIngestrAssetType(assetType)
  );
}
