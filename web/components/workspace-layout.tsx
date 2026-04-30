"use client";

import {
  Outlet,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  createContext,
  CSSProperties,
  Dispatch,
  SetStateAction,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

import { WorkspaceCommandPalette } from "@/components/workspace-command-palette";
import { WorkspaceEnvironmentSwitcher } from "@/components/workspace-environment-switcher";
import { WorkspacePipelineDialogs } from "@/components/workspace-pipeline-dialogs";
import { WorkspaceSidebar } from "@/components/workspace-sidebar";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { pipelineAtom } from "@/lib/atoms/domains/workspace";
import { WebPipeline, WorkspaceState } from "@/lib/types";
import { useAssetActions } from "@/hooks/use-asset-actions";
import { useAssetResults } from "@/hooks/use-asset-results";
import { usePipelineMaterializationState } from "@/hooks/use-pipeline-materialization-state";
import { useWorkspaceEnvironment } from "@/hooks/use-workspace-environment";
import { useIsMobile } from "@/hooks/use-mobile";
import { useWorkspaceSelection } from "@/hooks/use-workspace-selection";
import { useWorkspaceSync } from "@/hooks/use-workspace-sync";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { useOnboarding } from "@/hooks/use-onboarding";
import type { OnboardingRect } from "@/lib/atoms/onboarding";

export type WorkspaceSidebarState = {
  highlighted?: boolean;
  highlightStyle?: CSSProperties;
};

type WorkspaceLayoutContextValue = {
  activePipeline: string | null;
  assetActions: ReturnType<typeof useAssetActions>;
  assetResults: ReturnType<typeof useAssetResults>;
  monacoTheme: ReturnType<typeof useWorkspaceTheme>["monacoTheme"];
  navigateSelection: (pipelineId: string, assetId: string | null) => void;
  pipeline: WebPipeline | null;
  pipelineMaterialization: ReturnType<typeof usePipelineMaterializationState>;
  sidebarOnboardingMount: HTMLDivElement | null;
  selectedAsset: string | null;
  setSidebarState: Dispatch<SetStateAction<WorkspaceSidebarState>>;
  theme: ReturnType<typeof useWorkspaceTheme>["theme"];
  workspace: WorkspaceState;
};

const WorkspaceLayoutContext =
  createContext<WorkspaceLayoutContextValue | null>(null);

export function WorkspaceLayout() {
  const workspace = useWorkspaceSync();
  const { activePipeline, selectedAsset, navigateSelection } =
    useWorkspaceSelection();
  const pipeline = useAtomValue(pipelineAtom);
  const { selectedEnvironment } = useWorkspaceEnvironment();
  const isMobile = useIsMobile();
  const assetActions = useAssetActions();
  const assetResults = useAssetResults();
  const pipelineMaterialization = usePipelineMaterializationState();
  const { theme, setTheme, monacoTheme } = useWorkspaceTheme();
  const navigate = useNavigate();
  const routeState = useRouterState({
    select: (state) => ({
      pathname: state.location.pathname,
      search: state.location.search as { environment?: string },
    }),
  });
  const [deletePipelineDialogOpen, setDeletePipelineDialogOpen] =
    useState(false);
  const [deletePipelineLoading, setDeletePipelineLoading] = useState(false);
  const [renamePipelineDialogOpen, setRenamePipelineDialogOpen] =
    useState(false);
  const [renamePipelineLoading, setRenamePipelineLoading] = useState(false);
  const [renamePipelineName, setRenamePipelineName] = useState("");
  const [deletePipelineTargetId, setDeletePipelineTargetId] =
    useState<string | null>(null);
  const [renamePipelineTargetId, setRenamePipelineTargetId] =
    useState<string | null>(null);
  const [pendingPipelinePathSelection, setPendingPipelinePathSelection] =
    useState<string | null>(null);
  const [sidebarState, setSidebarState] = useState<WorkspaceSidebarState>({});
  const [sidebarOnboardingMount, setSidebarOnboardingMount] =
    useState<HTMLDivElement | null>(null);
  const onboarding = useOnboarding({
    spotlightActive: routeState.pathname.startsWith("/settings/environments"),
    spotlightSelectors: [
      "[data-testid='environment-card-default']",
      "[data-testid='environments-new-button']",
    ],
  });

  const currentView = useMemo<"workspace" | "environments" | "connections">(
    () => {
      if (routeState.pathname.includes("/connections")) {
        return "connections";
      }

      if (routeState.pathname.startsWith("/settings")) {
        return "environments";
      }

      return "workspace";
    },
    [routeState.pathname]
  );

  const currentViewLabel = useMemo(() => {
    if (currentView === "connections") {
      return "Connections";
    }

    if (currentView === "environments") {
      return "Environments";
    }

    return pipeline?.name ?? "Workspace";
  }, [currentView, pipeline?.name]);

  const documentTitle = useMemo(() => {
    if (currentView !== "workspace") {
      return null;
    }

    const selectedPipeline = pipeline?.name ?? null;
    const selectedAssetName =
      pipeline?.assets.find((currentAsset) => currentAsset.id === selectedAsset)
        ?.name ?? null;

    if (!selectedPipeline) {
      return "Workspace · Renart";
    }

    if (!selectedAssetName) {
      return `${selectedPipeline} · Renart`;
    }

    return `${selectedAssetName} · ${selectedPipeline} · Renart`;
  }, [currentView, pipeline, selectedAsset]);

  useEffect(() => {
    if (!documentTitle) {
      return;
    }

    document.title = documentTitle;
  }, [documentTitle]);

  useEffect(() => {
    if (currentView !== "environments") {
      return;
    }
    onboarding.setEnvironmentStepActive(
      window.localStorage.getItem("renart-quickstart-tour-environments") === "true"
    );
  }, [currentView]);

  const handleRunPipelineById = useCallback(
    (pipelineId: string) => {
      if (assetResults.materializeLoading) {
        return;
      }

      void assetResults.runMaterializePipeline(pipelineId, async () => {
        await pipelineMaterialization
          .refreshPipelineMaterialization(pipelineId)
          .catch(() => undefined);
      });
    },
    [assetResults, pipelineMaterialization]
  );

  const handleConfirmDeletePipeline = useCallback(() => {
    const targetPipelineId = deletePipelineTargetId ?? pipeline?.id ?? null;
    if (!targetPipelineId || deletePipelineLoading) {
      return;
    }

    const targetPipeline =
      workspace?.pipelines.find(
        (currentPipeline) => currentPipeline.id === targetPipelineId
      ) ?? null;
    if (!targetPipeline) {
      return;
    }

    const remainingPipelines =
      workspace?.pipelines.filter(
        (currentPipeline) => currentPipeline.id !== targetPipeline.id
      ) ?? [];
    const fallbackPipeline = remainingPipelines[0] ?? null;

    setDeletePipelineLoading(true);
    void assetActions
      .runDeletePipeline(targetPipeline.id)
      .then((deleted) => {
        if (!deleted) {
          return;
        }

        setDeletePipelineDialogOpen(false);
        setDeletePipelineTargetId(null);
        assetResults.clearResultsAfterDelete();

        if (fallbackPipeline) {
          navigateSelection(
            fallbackPipeline.id,
            fallbackPipeline.assets[0]?.id ?? null
          );
          return;
        }

        void navigate({
          to: "/",
          search: {
            pipeline: undefined,
            asset: undefined,
            environment: routeState.search.environment,
          },
          replace: true,
        });
      })
      .finally(() => setDeletePipelineLoading(false));
  }, [
    assetActions,
    assetResults,
    deletePipelineLoading,
    deletePipelineTargetId,
    navigate,
    navigateSelection,
    pipeline,
    workspace?.pipelines,
  ]);

  const handleConfirmCreatePipeline = useCallback(async () => {
    const createdPath = assetActions.createPipelinePath.trim();
    const created = await assetActions.confirmCreatePipeline();
    if (created && createdPath) {
      setPendingPipelinePathSelection(createdPath);
    }
    return created;
  }, [assetActions]);

  const handleConfirmRenamePipeline = useCallback(async () => {
    const targetPipelineId = renamePipelineTargetId;
    const nextName = renamePipelineName.trim();
    if (!targetPipelineId || !nextName || renamePipelineLoading) {
      return false;
    }

    setRenamePipelineLoading(true);
    try {
      const renamed = await assetActions.runUpdatePipeline(targetPipelineId, {
        name: nextName,
      });
      if (!renamed) {
        return false;
      }

      setRenamePipelineDialogOpen(false);
      setRenamePipelineTargetId(null);
      return true;
    } finally {
      setRenamePipelineLoading(false);
    }
  }, [
    assetActions,
    renamePipelineLoading,
    renamePipelineName,
    renamePipelineTargetId,
  ]);

  useEffect(() => {
    if (!pendingPipelinePathSelection || !workspace) {
      return;
    }

    const normalizedPendingPath = pendingPipelinePathSelection
      .replaceAll("\\", "/")
      .trim();

    const matchingPipeline = workspace.pipelines.find((currentPipeline) => {
      const pipelinePath = currentPipeline.path.replaceAll("\\", "/").trim();
      return (
        pipelinePath === normalizedPendingPath ||
        pipelinePath.endsWith(`/${normalizedPendingPath}`)
      );
    });

    if (!matchingPipeline) {
      return;
    }

    navigateSelection(
      matchingPipeline.id,
      matchingPipeline.assets[0]?.id ?? null
    );
    setPendingPipelinePathSelection(null);
  }, [navigateSelection, pendingPipelinePathSelection, workspace]);

  const contextValue = useMemo<WorkspaceLayoutContextValue | null>(() => {
    if (!workspace) {
      return null;
    }

    return {
      activePipeline,
      assetActions,
      assetResults,
      monacoTheme,
      navigateSelection,
      pipeline,
      pipelineMaterialization,
      sidebarOnboardingMount,
      selectedAsset,
      setSidebarState,
      theme,
      workspace,
    };
  }, [
    activePipeline,
    assetActions,
    assetResults,
    monacoTheme,
    navigateSelection,
    pipeline,
    pipelineMaterialization,
    sidebarOnboardingMount,
    selectedAsset,
    theme,
    workspace,
  ]);

  if (!workspace || !contextValue) {
    return (
      <div className="flex h-screen w-screen items-center justify-center bg-background text-foreground">
        <div className="flex items-center gap-3 rounded-lg border bg-card px-4 py-3 shadow-sm">
          <Spinner className="size-4" />
          <span className="text-sm text-muted-foreground">
            Loading workspace...
          </span>
        </div>
      </div>
    );
  }

  return (
    <WorkspaceLayoutContext.Provider value={contextValue}>
      <div className="h-screen w-screen overflow-hidden bg-background text-foreground">
        {assetActions.uiMessage && (
          <div className="pointer-events-none fixed right-4 top-4 z-50">
            <div
              className={`rounded-md border px-3 py-2 text-sm shadow ${
                assetActions.uiMessage.type === "error"
                  ? "border-destructive/50 bg-destructive/10 text-destructive"
                  : "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
              }`}
            >
              {assetActions.uiMessage.text}
            </div>
          </div>
        )}

        <SidebarProvider defaultOpen className="h-full min-h-0 overflow-hidden">
          <WorkspaceSidebar
            workspace={workspace}
            activePipeline={activePipeline}
            selectedAsset={selectedAsset}
            highlighted={sidebarState.highlighted}
            highlightStyle={sidebarState.highlightStyle}
            theme={theme}
            onToggleTheme={() =>
              setTheme((current) => (current === "dark" ? "light" : "dark"))
            }
            currentView={currentView}
            connectionsEnvironment={selectedEnvironment ?? null}
            onCreatePipeline={assetActions.openCreatePipelineDialog}
            onRunPipeline={handleRunPipelineById}
            canRunPipeline={Boolean(pipeline)}
            runPipelineLoading={assetResults.pipelineMaterializeLoading}
            onRenamePipeline={(pipelineId) => {
              const targetPipeline = workspace.pipelines.find(
                (currentPipeline) => currentPipeline.id === pipelineId
              );
              setRenamePipelineTargetId(pipelineId);
              setRenamePipelineName(targetPipeline?.name ?? "");
              setRenamePipelineDialogOpen(true);
            }}
            onOpenPipelineSettings={(pipelineId) => {
              void navigate({
                to: isMobile
                  ? "/pipelines/$pipelineId/config"
                  : "/pipelines/$pipelineId/config/general",
                params: { pipelineId },
                search: {
                  pipeline: activePipeline ?? undefined,
                  asset: selectedAsset ?? undefined,
                  environment: selectedEnvironment ?? undefined,
                },
              });
            }}
            onDeletePipeline={(pipelineId) => {
              setDeletePipelineTargetId(pipelineId);
              setDeletePipelineDialogOpen(true);
            }}
            canDeletePipeline={Boolean(pipeline)}
            deletePipelineLoading={deletePipelineLoading}
            renamePipelineLoading={renamePipelineLoading}
            onOnboardingMountChange={setSidebarOnboardingMount}
          />

          <SidebarInset className="min-w-0 min-h-0">
            {onboarding.environmentStepActive ? (
              <div className="fixed inset-0 z-40">
                <EnvironmentTourOverlay rect={onboarding.spotlightRect} />
                <EnvironmentTourCard style={onboarding.cardStyle}>
                  <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Quickstart tour</div>
                  <div className="mt-1 font-semibold">These are your connections</div>
                  <p className="mt-2 text-sm text-muted-foreground">
                    The playground configured DuckDB for local tables and chess for source data. Return to the workspace to keep exploring the DAG.
                  </p>
                  <div className="mt-3 flex gap-2">
                    <Button
                      size="sm"
                      type="button"
                      onClick={() => {
                        onboarding.setEnvironmentStepActive(false);
                        void navigate({
                          to: "/",
                          search: {
                            pipeline: activePipeline ?? undefined,
                            asset: selectedAsset ?? undefined,
                            environment: selectedEnvironment,
                          },
                        });
                      }}
                    >
                      Back to workspace
                    </Button>
                    <Button
                      size="sm"
                      type="button"
                      variant="ghost"
                      onClick={() => {
                        onboarding.setEnvironmentStepActive(false);
                      }}
                    >
                      Dismiss
                    </Button>
                  </div>
                </EnvironmentTourCard>
              </div>
            ) : null}
            <header className="flex h-12 shrink-0 items-center gap-3 border-b bg-background/95 px-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
              <SidebarTrigger className="shrink-0" />
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold">
                  {currentViewLabel}
                </div>
                <div className="truncate text-xs text-muted-foreground">
                  {currentView === "workspace"
                    ? `${workspace.pipelines.length} pipeline${workspace.pipelines.length === 1 ? "" : "s"}`
                    : "Project settings"}
                </div>
              </div>
              <WorkspaceCommandPalette
                  workspace={workspace}
                  activePipeline={activePipeline}
                  selectedAsset={selectedAsset}
                  currentView={currentView}
	      />
              {currentView === "workspace" ? <WorkspaceEnvironmentSwitcher /> : null}
            </header>

            <div className="min-h-0 flex-1 overflow-hidden">
              <Outlet />
            </div>
          </SidebarInset>

          <WorkspacePipelineDialogs
            deletePipelineDialogOpen={deletePipelineDialogOpen}
            deletePipelineLoading={deletePipelineLoading}
            renamePipelineDialogOpen={renamePipelineDialogOpen}
            renamePipelineLoading={renamePipelineLoading}
            renamePipelineName={renamePipelineName}
            selectedPipelineName={
              workspace.pipelines.find(
                (currentPipeline) =>
                  currentPipeline.id ===
                  (renamePipelineTargetId ?? deletePipelineTargetId ?? pipeline?.id)
              )?.name
            }
            canDeletePipeline={Boolean(deletePipelineTargetId ?? pipeline)}
            onDeletePipelineDialogOpenChange={(open) => {
              if (!deletePipelineLoading) {
                setDeletePipelineDialogOpen(open);
                if (!open) {
                  setDeletePipelineTargetId(null);
                }
              }
            }}
            onRenamePipelineDialogOpenChange={(open) => {
              if (!renamePipelineLoading) {
                setRenamePipelineDialogOpen(open);
                if (!open) {
                  setRenamePipelineTargetId(null);
                }
              }
            }}
            onRenamePipelineNameChange={setRenamePipelineName}
            onConfirmRenamePipeline={handleConfirmRenamePipeline}
            onConfirmDeletePipeline={handleConfirmDeletePipeline}
            onCancelDeletePipeline={() => {
              setDeletePipelineDialogOpen(false);
              setDeletePipelineTargetId(null);
            }}
            createPipelineDialogOpen={assetActions.createPipelineDialogOpen}
            createPipelineLoading={assetActions.createPipelineLoading}
            createPipelinePath={assetActions.createPipelinePath}
            onCreatePipelineDialogOpenChange={(open) => {
              if (!assetActions.createPipelineLoading) {
                assetActions.setCreatePipelineDialogOpen(open);
              }
            }}
            onCreatePipelinePathChange={assetActions.setCreatePipelinePath}
            onConfirmCreatePipeline={handleConfirmCreatePipeline}
          />
        </SidebarProvider>
        <style>{`
          @keyframes bruin-help-scale {
            from {
              transform: scale(1);
            }
            to {
              transform: scale(1.05);
            }
          }
        `}</style>
      </div>
    </WorkspaceLayoutContext.Provider>
  );
}

function EnvironmentTourOverlay({ rect }: { rect: OnboardingRect | null }) {
  if (!rect) {
    return <div className="pointer-events-none fixed inset-0 bg-black/55" />;
  }

  const padding = 12;
  return (
    <div
      className="pointer-events-none fixed z-0 rounded-2xl border-2 border-primary shadow-[0_0_0_9999px_rgba(0,0,0,0.55),0_0_32px_rgba(59,130,246,0.8)]"
      style={{
        left: Math.max(8, rect.left - padding),
        top: Math.max(8, rect.top - padding),
        width: rect.width + padding * 2,
        height: rect.height + padding * 2,
      }}
    />
  );
}

function EnvironmentTourCard({
  children,
  style,
}: {
  children: ReactNode;
  style: CSSProperties;
}) {
  return (
    <div className="pointer-events-auto fixed max-w-[360px] rounded-xl border bg-popover p-4 text-popover-foreground shadow-lg ring-4 ring-primary/20" style={style}>
      {children}
    </div>
  );
}

export function useWorkspaceLayout() {
  const context = useContext(WorkspaceLayoutContext);

  if (!context) {
    throw new Error("useWorkspaceLayout must be used within WorkspaceLayout");
  }

  return context;
}
