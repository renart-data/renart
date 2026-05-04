"use client";

import { useAtom, useAtomValue } from "jotai";
import { useSetAtom } from "jotai";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import "reactflow/dist/style.css";

import { WorkspaceAssetDialogs } from "@/components/workspace-asset-dialogs";
import { WorkspaceMainContent } from "@/components/workspace-main-content";
import { WorkspacePageEffects } from "@/components/workspace-page-effects";
import {
  AssetConfigForm,
  WorkspaceEditorPane,
} from "@/components/workspace-editor-pane";
import {
  assetEditorTabAtom,
  editorValueAtom,
} from "@/lib/atoms/domains/editor";
import {
  enrichedSelectedAssetAtom,
} from "@/lib/atoms/domains/results";
import { workspaceAtom, workspaceSyncSourceAtom } from "@/lib/atoms/domains/workspace";
import { routeSelectionAtom } from "@/lib/atoms/domains/workspace";
import { useIsMobile } from "@/hooks/use-mobile";
import {
  computeGraphLayoutPositions,
} from "@/lib/graph";
import { buildCreateAssetInput } from "@/lib/workspace-shell-helpers";
import { useAssetCanvasInteractions } from "@/hooks/use-asset-canvas-interactions";
import { useDebouncedAssetSave } from "@/hooks/use-debounced-asset-save";
import { useEditorActions } from "@/hooks/use-editor-actions";
import { useWorkspaceGraphController } from "@/hooks/use-workspace-graph-controller";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { useWorkspaceDerivedState } from "@/hooks/use-workspace-derived-state";
import { useOnboarding } from "@/hooks/use-onboarding";
import { getAvailableAssetTypes } from "@/lib/asset-types";
import { getWorkspace } from "@/lib/api";

import { useWorkspaceLayout } from "./workspace-layout";

export function WorkspacePage() {
  const {
    assetActions,
    assetResults,
    monacoTheme,
    navigateSelection,
    pipeline,
    pipelineMaterialization,
    selectedAsset,
    workspace,
  } = useWorkspaceLayout();
  const [assetEditorTab, setAssetEditorTab] = useAtom(assetEditorTabAtom);
  const editorValue = useAtomValue(editorValueAtom);
  const asset = useAtomValue(enrichedSelectedAssetAtom);
  const routeSelection = useAtomValue(routeSelectionAtom);
  const setWorkspace = useSetAtom(workspaceAtom);
  const setWorkspaceSyncSource = useSetAtom(workspaceSyncSourceAtom);
  const navigate = useNavigate();
  const isMobile = useIsMobile();
  const [mobileEditorOpen, setMobileEditorOpen] = useState(false);
  const [assetRenameLoading, setAssetRenameLoading] = useState(false);
  const reloadedLayoutForPipelineRef = useRef<string | null>(null);
  const onboarding = useOnboarding();
  const quickstartTourStep = onboarding.quickstartStep;
  const setQuickstartTourStep = onboarding.setQuickstartStep;

  const { scheduleSave, flushAssetSave, hasPendingAssetSave, saveAssetNow } =
    useDebouncedAssetSave(500);
  const {
    workspaceConfig,
    handleCreateWorkspaceConnection,
    handleUpdateWorkspaceConnection,
    loadWorkspaceConfig,
  } = useWorkspaceSettingsData();

  const { enrichedPipeline, refreshPipelineMaterialization } =
    pipelineMaterialization;
  const { defaultAssetNamesByKind, visualAssets } =
    useWorkspaceDerivedState({
      workspace,
      enrichedPipeline,
    });

  const {
    areVisualPreviewsReady,
    assetPreviewRows,
    canvasContainerRef,
    clearPreviewForAsset,
    connectedNodeIDs,
    edges,
    graph,
    handleRecomputeGraph,
    inspectByAssetId,
    inspectLoadingByAssetId,
    nodeTypes,
    nodes,
    onEdgesChange,
    onNodesChange,
    reactFlowInstance,
    setEdges,
    setNodes,
    setReactFlowInstance,
    setStoredNodePositions,
    storedNodePositions,
  } = useWorkspaceGraphController({
    asset,
    enrichedPipeline,
    selectedAssetId: selectedAsset,
    selectedInspectRows: assetResults.inspectResult?.rows,
    visualAssets,
    materializingAssetIds: assetResults.materializingAssetIds,
  });

  const form = useForm<AssetConfigForm>({
    defaultValues: {
      type: "",
      materialization: "",
      custom_checks: "",
      columns: "",
    },
  });

  const helpMode = false;
  const helpPulseStyle = undefined;
  const onboardingHelp = { target: null as null | string };

  const buildCreateAssetInputForWorkspace = useCallback(
    (name: string, kind: "sql" | "python" | "ingestr") =>
      buildCreateAssetInput(name, kind, assetActions.defaultSqlAssetType),
    [assetActions.defaultSqlAssetType]
  );

  const {
    deleteDialogOpen,
    deleteLoading,
    setDeleteDialogOpen,
    handleEditorChange,
    handleSaveVisualizationSettings,
    handleSaveManualUpstreams,
    handleConfirmDeleteAsset,
    handleMaterializeSelectedAsset,
    handleInspectSelectedAsset,
    handleSaveSelectedAsset,
    handleAssetNameChange,
    handleAssetTypeChange,
    handleMaterializationTypeChange,
  } = useEditorActions({
    editorValue,
    scheduleSave,
    flushAssetSave,
    saveAssetNow,
    hasPendingAssetSave,
    runUpdateAsset: assetActions.runUpdateAsset,
    runDeleteAsset: assetActions.runDeleteAsset,
    runUpdatePipeline: assetActions.runUpdatePipeline,
    runInspectForAsset: assetResults.runInspectForAsset,
    runMaterializeForAsset: assetResults.runMaterializeForAsset,
    refreshPipelineMaterialization,
    navigateSelection,
    clearResultsAfterDelete: assetResults.clearResultsAfterDelete,
    clearPreviewForAsset,
  });

  const handleDeleteAssetFromNode = useCallback(
    (assetId: string) => {
      if (!pipeline) {
        return;
      }

      navigateSelection(pipeline.id, assetId);
      setDeleteDialogOpen(true);
    },
    [navigateSelection, pipeline, setDeleteDialogOpen]
  );

  const handleInspectAssetFromNode = useCallback(
    (assetId: string) => {
      if (!pipeline) {
        return;
      }

      navigateSelection(pipeline.id, assetId);
      void assetResults.runInspectForAsset(assetId);
    },
    [assetResults, navigateSelection, pipeline]
  );

  const handleMaterializeAssetFromNode = useCallback(
    (assetId: string) => {
      if (!pipeline) {
        return;
      }

      navigateSelection(pipeline.id, assetId);
      void assetResults.runMaterializeForAsset(assetId, async () => {
        await refreshPipelineMaterialization(pipeline.id).catch(() => undefined);
      });
    },
    [assetResults, navigateSelection, pipeline, refreshPipelineMaterialization]
  );

  const {
    handlePaneClick,
    handlePaneContextMenu,
    handleNodeDragStop,
    handleNodeClick,
  } = useAssetCanvasInteractions({
    reactFlowInstance,
    canvasContainerRef,
    graphNodes: graph.nodes,
    graphEdges: graph.edges,
    connectedNodeIDs,
    storedNodePositions,
    setStoredNodePositions,
    defaultAssetNamesByKind,
    setNodes,
    setEdges,
    runCreateAsset: assetActions.runCreateAsset,
    navigateSelection,
    inspectLoadingByAssetId: assetResults.inspectLoadingByAssetId,
    onDeleteAsset: handleDeleteAssetFromNode,
    onInspectAsset: handleInspectAssetFromNode,
    onMaterializeAsset: handleMaterializeAssetFromNode,
    isMobile,
    openSelectedAssetEditor: () => setMobileEditorOpen(true),
    buildCreateAssetInput: buildCreateAssetInputForWorkspace,
  });

  const handleRunPipeline = () => {
    if (!pipeline || assetResults.materializeLoading) {
      return;
    }

    void assetResults.runMaterializePipeline(pipeline.id, async () => {
      await refreshPipelineMaterialization(pipeline.id).catch(() => undefined);
    });
  };

  const quickstartAssets = useMemo(() => {
    const assets = enrichedPipeline?.assets ?? [];
    return {
      players: assets.find((current) => current.name === "quickstart.players") ?? null,
      stats: assets.find((current) => current.name === "quickstart.player_stats") ?? null,
    };
  }, [enrichedPipeline?.assets]);

  const isQuickstartPipeline = Boolean(
    enrichedPipeline &&
      (enrichedPipeline.name === "quickstart" || enrichedPipeline.path === "quickstart") &&
      quickstartAssets.players &&
      quickstartAssets.stats
  );

  useEffect(() => {
    if (!enrichedPipeline || reloadedLayoutForPipelineRef.current === enrichedPipeline.id) {
      return;
    }
    reloadedLayoutForPipelineRef.current = enrichedPipeline.id;
    window.setTimeout(() => handleRecomputeGraph(), 0);
  }, [enrichedPipeline, handleRecomputeGraph]);

  useEffect(() => {
    if (!isQuickstartPipeline || quickstartTourStep === null) {
      return;
    }
    if (quickstartTourStep === 0 && asset?.name === "quickstart.players") {
      setQuickstartTourStep(1);
    }
    if (quickstartTourStep === 2 && assetResults.materializeHistory.some((entry) => entry.status === "ok")) {
      setQuickstartTourStep(3);
    }
    if (quickstartTourStep === 3 && asset?.name === "quickstart.player_stats" && assetEditorTab === "visualization") {
      setQuickstartTourStep(4);
    }
    if (quickstartTourStep === 4 && asset?.meta?.web_view === "table") {
      setQuickstartTourStep(5);
    }
  }, [asset?.meta?.web_view, asset?.name, assetEditorTab, assetResults.materializeHistory, isQuickstartPipeline, quickstartTourStep]);

  const dismissQuickstartTour = onboarding.dismissQuickstartTour;

  const quickstartTour = useMemo(() => {
    if (!isQuickstartPipeline || quickstartTourStep === null) {
      return null;
    }
    if (quickstartTourStep === 0) {
      return {
        step: 0,
        title: "Start with the players asset",
        body: "This ingestr asset loads chess player data. Click the quickstart.players node to inspect it.",
        onSkip: dismissQuickstartTour,
      };
    }
    if (quickstartTourStep === 1) {
      return {
        step: 1,
        title: "Follow the dependency arrow",
        body: "quickstart.player_stats depends on players and games. Renart tracks that lineage automatically from the asset definitions.",
        actionLabel: "Next",
        onAction: () => setQuickstartTourStep(2),
        onSkip: dismissQuickstartTour,
      };
    }
    if (quickstartTourStep === 2) {
      return {
        step: 2,
        title: "Run the whole pipeline",
        body: "Use Run pipeline to materialize the DAG and refresh row counts.",
        actionLabel: "Run pipeline",
        onAction: handleRunPipeline,
        onSkip: dismissQuickstartTour,
      };
    }
    if (quickstartTourStep === 3) {
      return {
        step: 3,
        title: "Open the preview settings",
        body: "Open quickstart.player_stats and switch to the Preview tab to configure how this asset renders.",
        actionLabel: "Open Preview",
        onAction: () => {
          if (pipeline && quickstartAssets.stats) {
            navigateSelection(pipeline.id, quickstartAssets.stats.id);
          }
          setAssetEditorTab("visualization");
        },
        onSkip: dismissQuickstartTour,
      };
    }
    if (quickstartTourStep === 4) {
      return {
        step: 4,
        title: "Switch the preview to a table",
        body: "Use the View Type tabs inside Preview and choose Table for a column-focused result view.",
        onSkip: dismissQuickstartTour,
      };
    }
    return {
      step: 5,
      title: "See the configured connections",
      body: "The quickstart added DuckDB and chess connections. Open Environments to inspect them, then come back to the workspace anytime.",
      actionLabel: "Open Environments",
      onAction: () => {
        onboarding.setEnvironmentStepActive(true);
        void navigate({
          to: "/settings/environments/$environmentId",
          params: { environmentId: "default" },
        });
      },
      onSkip: dismissQuickstartTour,
    };
  }, [assetEditorTab, dismissQuickstartTour, handleRunPipeline, isQuickstartPipeline, navigate, navigateSelection, onboarding, pipeline, quickstartAssets.stats, quickstartTourStep, setAssetEditorTab]);

  const handleSaveSelectedAssetShortcut = useCallback(async () => {
    const result = await handleSaveSelectedAsset();

    if (result === "saved") {
      assetActions.pushUIMessage("success", "Saved.");
    } else if (result === "already-saved") {
      assetActions.pushUIMessage("success", "Already saved.");
    }

    return result;
  }, [assetActions, handleSaveSelectedAsset]);

  const handleAssetRename = useCallback(
    async (assetName: string) => {
      setAssetRenameLoading(true);
      try {
        return await handleAssetNameChange(assetName);
      } finally {
        setAssetRenameLoading(false);
      }
    },
    [handleAssetNameChange]
  );

  const handleSelectMaterializeEntry = useCallback(
    (entryId: string) => {
      assetResults.selectMaterializeEntry(entryId);

      const entry = assetResults.materializeHistory.find((item) => item.id === entryId);
      if (entry?.assetId && pipeline?.id) {
        navigateSelection(pipeline.id, entry.assetId);
      }
    },
    [assetResults, navigateSelection, pipeline?.id]
  );

  const hasPipelines = workspace.pipelines.length > 0;
  const availableAssetTypes = useMemo(
    () => getAvailableAssetTypes(workspaceConfig?.connection_types ?? []),
    [workspaceConfig?.connection_types]
  );
  const availableDependencyNames = useMemo(
    () =>
      (enrichedPipeline?.assets ?? [])
        .map((candidate) => candidate.name)
        .filter((name): name is string => Boolean(name)),
    [enrichedPipeline?.assets]
  );
  const editorPaneProps = {
    asset,
    pipelineId: pipeline?.id ?? null,
    helpMode,
    actionHighlighted: onboardingHelp.target === "actions",
    editorHighlighted: onboardingHelp.target === "editor",
    visualizationHighlighted: onboardingHelp.target === "visualization" || quickstartTourStep === 3,
    highlightStyle: helpPulseStyle,
    materializeLoading: assetResults.materializeLoading,
    inspectLoading: assetResults.inspectLoading,
    deleteLoading,
    assetRenameLoading,
    editorValue,
    monacoTheme,
    assetEditorTab,
    form,
    assetPreviewRows,
    inspectDiagnosticSnapshot: selectedAsset
      ? assetResults.inspectDiagnosticSnapshotByAssetId[selectedAsset] ?? null
      : null,
    onEditorTabChange: setAssetEditorTab,
    onEditorChange: handleEditorChange,
    onMaterializeSelectedAsset: handleMaterializeSelectedAsset,
    onInspectSelectedAsset: handleInspectSelectedAsset,
    onSaveSelectedAsset: handleSaveSelectedAssetShortcut,
    onOpenDeleteDialog: () => setDeleteDialogOpen(true),
    onAssetNameChange: handleAssetRename,
    onAssetTypeChange: handleAssetTypeChange,
    onMaterializationTypeChange: handleMaterializationTypeChange,
    onSaveVisualizationSettings: handleSaveVisualizationSettings,
    onSaveManualUpstreams: handleSaveManualUpstreams,
    onGoToAsset: navigateSelection,
    availableAssetTypes,
    availableDependencyNames,
  } as const;

  const editorPane = <WorkspaceEditorPane {...editorPaneProps} mobile={isMobile} />;

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!asset || !pipeline || deleteLoading || deleteDialogOpen) {
        return;
      }

      const target = event.target;
      if (
        target instanceof HTMLElement &&
        (target.isContentEditable ||
          target.closest("input, textarea, [role='dialog'], [data-radix-popper-content-wrapper]") ||
          target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA")
      ) {
        return;
      }

      if (event.key !== "Backspace" && event.key !== "Delete") {
        return;
      }

      event.preventDefault();
      setDeleteDialogOpen(true);
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [asset, deleteDialogOpen, deleteLoading, pipeline, setDeleteDialogOpen]);

  const handleReloadWorkspace = useCallback(async () => {
    const data = await getWorkspace();
    setWorkspace(data);
    setWorkspaceSyncSource({
      method: "workspace-load",
      recordedAt: new Date().toISOString(),
      revision: data.revision,
    });
  }, [setWorkspace, setWorkspaceSyncSource]);

  return (
    <>
      <WorkspacePageEffects
        asset={asset}
        enrichedPipeline={enrichedPipeline}
        areVisualPreviewsReady={areVisualPreviewsReady}
        inspectByAssetId={inspectByAssetId}
        inspectLoadingByAssetId={inspectLoadingByAssetId}
        storedNodePositions={storedNodePositions}
        setStoredNodePositions={setStoredNodePositions}
        computeInitialPositions={() =>
          computeGraphLayoutPositions(
            enrichedPipeline,
            inspectByAssetId,
            inspectLoadingByAssetId
          )
        }
        form={form}
        isMobile={isMobile}
        setMobileEditorOpen={setMobileEditorOpen}
        selectedAsset={selectedAsset}
        routeSelectedAsset={routeSelection.asset}
        flushAssetSave={flushAssetSave}
      />
      <WorkspaceMainContent
        hasPipelines={hasPipelines}
        isMobile={isMobile}
        mobileEditorOpen={mobileEditorOpen}
        setMobileEditorOpen={setMobileEditorOpen}
        assetPath={asset?.path}
        editorPane={editorPane}
        emptyStateAction={assetActions.openCreatePipelineDialog}
        canvasPaneProps={{
          highlighted: helpMode && onboardingHelp.target === "canvas",
          highlightStyle: helpPulseStyle,
          hasResultData: assetResults.hasResultData,
          canvasContainerRef,
          nodes,
          edges,
          nodeTypes,
          inspectResult: assetResults.inspectResult,
          inspectLoading: assetResults.inspectLoading,
          inspectMeta: asset?.meta,
          materializeLoading: assetResults.materializeLoading,
          pipelineMaterializeLoading: assetResults.pipelineMaterializeLoading,
          hasInspectData: assetResults.hasInspectData,
          effectiveResultTab: assetResults.effectiveResultTab,
          selectedMaterializeEntry: assetResults.selectedMaterializeEntry,
          materializeHistory: assetResults.materializeHistory,
          materializeOutputHtml: assetResults.materializeOutputHtml,
          canLoadMoreInspectRows: assetResults.canLoadMoreInspectRows,
          onLoadMoreInspectRows: assetResults.loadMoreInspectRows,
          onResultTabChange: assetResults.setResultTab,
          onSelectMaterializeEntry: handleSelectMaterializeEntry,
          onInit: setReactFlowInstance,
          onNodesChange,
          onEdgesChange,
          onNodeDragStop: handleNodeDragStop,
          onPaneClick: handlePaneClick,
          onPaneContextMenu: handlePaneContextMenu,
          onNodeClick: handleNodeClick,
          onRecomputeGraph: handleRecomputeGraph,
          onRunPipeline: handleRunPipeline,
          canRunPipeline: Boolean(pipeline),
          showEditorButton: isMobile,
          isEditorButtonDisabled: !asset,
          onOpenEditor: () => setMobileEditorOpen(true),
          quickstartTour,
          tourHighlightedNodeIds:
            quickstartTourStep === 0 && quickstartAssets.players
              ? [quickstartAssets.players.id]
              : quickstartTourStep === 1 && quickstartAssets.players && quickstartAssets.stats
                ? [quickstartAssets.players.id, quickstartAssets.stats.id]
                : [],
          tourHighlightedEdgeIds:
            quickstartTourStep === 1 && quickstartAssets.players && quickstartAssets.stats
              ? [`${quickstartAssets.players.id}->${quickstartAssets.stats.id}`]
              : [],
          tourSpotlightSelector:
            quickstartTourStep === 3
              ? '[data-testid="editor-preview-tab"]'
              : quickstartTourStep === 4
                ? '[data-testid="visualization-view-type-tabs"]'
                : undefined,
        }}
      />

      <WorkspaceAssetDialogs
        deleteDialogOpen={deleteDialogOpen}
        deleteLoading={deleteLoading}
        selectedAssetName={asset?.name}
        canDeleteAsset={Boolean(asset && pipeline)}
        onDeleteDialogOpenChange={(open) => {
          if (!deleteLoading) {
            setDeleteDialogOpen(open);
          }
        }}
        onConfirmDeleteAsset={handleConfirmDeleteAsset}
        onCancelDeleteAsset={() => setDeleteDialogOpen(false)}
      />
    </>
  );
}
