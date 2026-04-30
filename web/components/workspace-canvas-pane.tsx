"use client";

import { CSSProperties, MutableRefObject, ReactNode, useEffect } from "react";
import { FilePenLine, LoaderCircle, Rows3 } from "lucide-react";
import {
  Background,
  Controls,
  Edge,
  Node,
  NodeTypes,
  ReactFlow,
  ReactFlowInstance,
} from "reactflow";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";

import { WorkspaceResultsPanel } from "@/components/workspace-results-panel";
import { Button } from "@/components/ui/button";
import { useOnboarding } from "@/hooks/use-onboarding";
import { MaterializeHistoryEntry } from "@/lib/atoms/results";
import type { OnboardingRect } from "@/lib/atoms/onboarding";
import { AssetInspectResponse } from "@/lib/types";

export type WorkspaceCanvasPaneProps = {
  highlighted: boolean;
  highlightStyle?: CSSProperties;
  hasResultData: boolean;
  canvasContainerRef: MutableRefObject<HTMLDivElement | null>;
  nodes: Node[];
  edges: Edge[];
  nodeTypes: NodeTypes;
  inspectResult: AssetInspectResponse | null;
  inspectLoading: boolean;
  inspectMeta?: Record<string, string>;
  materializeLoading: boolean;
  pipelineMaterializeLoading?: boolean;
  hasInspectData: boolean;
  effectiveResultTab: "inspect" | "materialize";
  selectedMaterializeEntry: MaterializeHistoryEntry | null;
  materializeHistory: MaterializeHistoryEntry[];
  materializeOutputHtml: string | null;
  canLoadMoreInspectRows?: boolean;
  onLoadMoreInspectRows?: () => void;
  onResultTabChange: (tab: "inspect" | "materialize") => void;
  onSelectMaterializeEntry: (entryId: string) => void;
  onInit: (instance: ReactFlowInstance) => void;
  onNodesChange: Parameters<typeof ReactFlow>[0]["onNodesChange"];
  onEdgesChange: Parameters<typeof ReactFlow>[0]["onEdgesChange"];
  onNodeDragStop: Parameters<typeof ReactFlow>[0]["onNodeDragStop"];
  onPaneClick: Parameters<typeof ReactFlow>[0]["onPaneClick"];
  onPaneContextMenu: Parameters<typeof ReactFlow>[0]["onPaneContextMenu"];
  onNodeClick: Parameters<typeof ReactFlow>[0]["onNodeClick"];
  onRecomputeGraph: () => void;
  onRunPipeline?: () => void;
  canRunPipeline?: boolean;
  showEditorButton?: boolean;
  isEditorButtonDisabled?: boolean;
  onOpenEditor?: () => void;
  quickstartTour?: {
    step: number;
    title: string;
    body: string;
    actionLabel?: string;
    onAction?: () => void;
    onSkip: () => void;
  } | null;
  tourHighlightedNodeIds?: string[];
  tourHighlightedEdgeIds?: string[];
  tourSpotlightSelector?: string;
};

export function WorkspaceCanvasPane({
  highlighted,
  highlightStyle,
  hasResultData,
  canvasContainerRef,
  nodes,
  edges,
  nodeTypes,
  inspectResult,
  inspectLoading,
  inspectMeta,
  materializeLoading,
  pipelineMaterializeLoading = false,
  hasInspectData,
  effectiveResultTab,
  selectedMaterializeEntry,
  materializeHistory,
  materializeOutputHtml,
  canLoadMoreInspectRows,
  onLoadMoreInspectRows,
  onResultTabChange,
  onSelectMaterializeEntry,
  onInit,
  onNodesChange,
  onEdgesChange,
  onNodeDragStop,
  onPaneClick,
  onPaneContextMenu,
  onNodeClick,
  onRecomputeGraph,
  onRunPipeline,
  canRunPipeline = false,
  showEditorButton = false,
  isEditorButtonDisabled = false,
  onOpenEditor,
  quickstartTour,
  tourHighlightedNodeIds = [],
  tourHighlightedEdgeIds = [],
  tourSpotlightSelector,
}: WorkspaceCanvasPaneProps) {
  const onboarding = useOnboarding({
    spotlightActive: Boolean(quickstartTour),
    spotlightSelectors: tourSpotlightSelector ? [tourSpotlightSelector] : [],
  });

  useEffect(() => {
    if (!quickstartTour) {
      return;
    }
    onboarding.pulseOverlay();
  }, [quickstartTour?.step]);

  return (
    <Panel
      className={highlighted ? "ring-2 ring-primary/70 ring-inset" : ""}
      style={highlighted ? highlightStyle : undefined}
      defaultSize={50}
      minSize={30}
    >
      <PanelGroup direction="vertical">
        <Panel defaultSize={hasResultData ? 72 : 100} minSize={45}>
          <div className="relative h-full" ref={canvasContainerRef}>
            <div className="absolute right-3 top-3 z-10 flex gap-2">
              {onRunPipeline ? (
                <Button
                  onClick={onRunPipeline}
                  size="sm"
                  type="button"
                  disabled={!canRunPipeline || pipelineMaterializeLoading}
                  className={quickstartTour?.step === 2 ? "quickstart-tour-spotlight quickstart-tour-halo" : undefined}
                >
                  {pipelineMaterializeLoading ? (
                    <LoaderCircle className="mr-2 size-3.5 animate-spin" />
                  ) : (
                    <PlayIcon className="mr-2 size-3.5" />
                  )}
                  {pipelineMaterializeLoading ? "Running pipeline..." : "Run pipeline"}
                </Button>
              ) : null}
              {showEditorButton ? (
                <Button
                  onClick={onOpenEditor}
                  size="sm"
                  type="button"
                  variant="outline"
                  disabled={isEditorButtonDisabled}
                >
                  <FilePenLine className="mr-2 size-3.5" />
                  Edit asset
                </Button>
              ) : null}
              <Button
                onClick={onRecomputeGraph}
                size="sm"
                type="button"
                variant="outline"
              >
                <Rows3 className="mr-2 size-3.5" />
                Reload layout
              </Button>
            </div>
            {onboarding.overlayVisible ? <QuickstartTourOverlay rect={onboarding.spotlightRect} /> : null}
            {quickstartTour ? (
              <QuickstartTourCard style={onboarding.cardStyle}>
                <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  Quickstart tour
                </div>
                <div className="mt-1 font-semibold">{quickstartTour.title}</div>
                <p className="mt-2 text-sm text-muted-foreground">{quickstartTour.body}</p>
                <div className="mt-3 flex gap-2">
                  {quickstartTour.actionLabel && quickstartTour.onAction ? (
                    <Button size="sm" type="button" onClick={quickstartTour.onAction}>
                      {quickstartTour.actionLabel}
                    </Button>
                  ) : null}
                  <Button size="sm" type="button" variant="ghost" onClick={quickstartTour.onSkip}>
                    Dismiss
                  </Button>
                </div>
              </QuickstartTourCard>
            ) : null}
            <ReactFlow
              nodes={nodes.map((node) =>
                tourHighlightedNodeIds.includes(node.id)
                  ? { ...node, className: `${node.className ?? ""} quickstart-tour-spotlight quickstart-tour-halo`, zIndex: 40 }
                  : node
              )}
              edges={edges.map((edge) =>
                tourHighlightedEdgeIds.includes(edge.id)
                  ? { ...edge, className: `${edge.className ?? ""} quickstart-tour-spotlight quickstart-tour-edge`, animated: true, zIndex: 40 }
                  : edge
              )}
              nodesDraggable
              nodeTypes={nodeTypes}
              panActivationKeyCode={null}
              deleteKeyCode={null}
              minZoom={0.1}
              onInit={onInit}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodeDragStop={onNodeDragStop}
              onPaneClick={onPaneClick}
              onPaneContextMenu={onPaneContextMenu}
              onNodeClick={onNodeClick}
            >
              <Background />
              <Controls />
            </ReactFlow>
          </div>
        </Panel>

        {hasResultData && (
          <>
            <PanelResizeHandle className="h-px bg-border" />

            <Panel defaultSize={28} minSize={20}>
              <WorkspaceResultsPanel
                inspectResult={inspectResult}
                inspectLoading={inspectLoading}
                inspectMeta={inspectMeta}
                materializeLoading={materializeLoading}
                pipelineMaterializeLoading={pipelineMaterializeLoading}
                hasInspectData={hasInspectData}
                effectiveResultTab={effectiveResultTab}
                selectedMaterializeEntry={selectedMaterializeEntry}
                materializeHistory={materializeHistory}
                materializeOutputHtml={materializeOutputHtml}
                canLoadMoreInspectRows={canLoadMoreInspectRows}
                onLoadMoreInspectRows={onLoadMoreInspectRows}
                onResultTabChange={onResultTabChange}
                onSelectMaterializeEntry={onSelectMaterializeEntry}
              />
            </Panel>
          </>
        )}
      </PanelGroup>
    </Panel>
  );
}

function QuickstartTourOverlay({ rect }: { rect: OnboardingRect | null }) {
  if (!rect) {
    return <div className="pointer-events-none fixed inset-0 z-[60] bg-black/55" />;
  }

  const padding = 12;
  return (
    <div
      className="pointer-events-none fixed z-[60] rounded-2xl border-2 border-primary/80 shadow-[0_0_0_9999px_rgba(0,0,0,0.55),0_0_32px_rgba(59,130,246,0.75)]"
      style={{
        left: Math.max(8, rect.left - padding),
        top: Math.max(8, rect.top - padding),
        width: rect.width + padding * 2,
        height: rect.height + padding * 2,
      }}
    />
  );
}

function QuickstartTourCard({
  children,
  style,
}: {
  children: ReactNode;
  style: CSSProperties;
}) {
  return (
    <div className="fixed z-[70] max-w-[360px] rounded-xl border bg-popover p-4 text-popover-foreground shadow-lg ring-4 ring-primary/20" style={style}>
      {children}
    </div>
  );
}

function PlayIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M8 5.14v13.72a1 1 0 0 0 1.5.86l10-6.86a1 1 0 0 0 0-1.72l-10-6.86A1 1 0 0 0 8 5.14Z" />
    </svg>
  );
}
