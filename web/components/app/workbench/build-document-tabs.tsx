import { useAtomValue } from "jotai";
import { useLayoutEffect, useRef, useState } from "react";
import { BookOpen, Terminal, X, type LucideIcon } from "lucide-react";

import { assetPresentationFields } from "@/lib/asset-presentation";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { cn } from "@/lib/utils";

import { kindMeta } from "../app-data";
import { buildDocumentKey, type BuildDocument } from "./build-document-state";

export function BuildDocumentTabs({
  documents,
  activeDocument,
  emptyLabel,
  onSelectDocument,
  onCloseDocument,
  onMoveDocument,
}: {
  documents: readonly BuildDocument[];
  activeDocument: BuildDocument | null;
  emptyLabel: string;
  onSelectDocument?: (document: BuildDocument) => void;
  onCloseDocument?: (document: BuildDocument) => void;
  onMoveDocument?: (key: string, targetKey: string) => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const dragging = useRef<string | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const move = (key: string, targetKey: string) => {
    onMoveDocument?.(key, targetKey);
    setAnnouncement(
      `Tab moved to position ${documents.findIndex((document) => buildDocumentKey(document) === targetKey) + 1} of ${documents.length}.`,
    );
  };
  const reorderProps = (key: string) => ({
    draggable: Boolean(onMoveDocument),
    dropTarget: dropTarget === key,
    onDragStart: (event: React.DragEvent<HTMLDivElement>) => {
      dragging.current = key;
      event.dataTransfer.setData("application/x-renart-document-tab", key);
      event.dataTransfer.effectAllowed = "move";
    },
    onDragOver: (event: React.DragEvent<HTMLDivElement>) => {
      if (!dragging.current || dragging.current === key) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      setDropTarget(key);
    },
    onDrop: (event: React.DragEvent<HTMLDivElement>) => {
      event.preventDefault();
      if (dragging.current && dragging.current !== key) move(dragging.current, key);
      dragging.current = null;
      setDropTarget(null);
    },
    onDragEnd: () => {
      dragging.current = null;
      setDropTarget(null);
    },
    onMove: (direction: -1 | 1) => {
      const index = documents.findIndex((document) => buildDocumentKey(document) === key);
      const target = documents[index + direction];
      if (target) move(key, buildDocumentKey(target));
    },
  });
  const activeKey = activeDocument ? buildDocumentKey(activeDocument) : null;

  return (
    <div
      role="tablist"
      aria-label="Open authoring documents"
      className="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto py-1"
    >
      <span className="sr-only" aria-live="polite">
        {announcement}
      </span>
      {documents.length === 0 ? (
        <span className="min-w-0 truncate px-2 text-xs text-muted-foreground">{emptyLabel}</span>
      ) : null}
      {documents.map((document) => {
        const key = buildDocumentKey(document);
        if (document.kind === "adhoc") {
          const pipeline = workspace?.pipelines.find(
            (candidate) => candidate.id === document.pipelineId,
          );
          return (
            <BuildDocumentTab
              key={key}
              {...reorderProps(key)}
              active={activeKey === key}
              icon={Terminal}
              label="Ad-hoc query"
              title={`Ad-hoc query · ${pipeline?.name ?? document.pipelineId}`}
              onSelect={() => onSelectDocument?.(document)}
              onClose={() => onCloseDocument?.(document)}
            />
          );
        }
        if (document.kind === "notebook") {
          const notebook = workspace?.notebooks?.find(
            (candidate) => candidate.id === document.notebookId,
          );
          if (!notebook) return null;
          return (
            <BuildDocumentTab
              key={key}
              {...reorderProps(key)}
              active={activeKey === key}
              icon={BookOpen}
              label={notebook.title}
              title={`Notebook · ${notebook.path}`}
              onSelect={() => onSelectDocument?.(document)}
              onClose={() => onCloseDocument?.(document)}
            />
          );
        }
        const pipeline = workspace?.pipelines.find(
          (candidate) => candidate.id === document.pipelineId,
        );
        if (!pipeline) return null;
        const asset = pipeline.assets.find((candidate) => candidate.id === document.assetId);
        if (!asset) return null;
        const presentation = assetPresentationFields(asset, pipeline);
        const Icon = kindMeta[presentation.kind].icon;
        const label = asset.path?.split("/").pop() ?? asset.name;
        return (
          <BuildDocumentTab
            key={key}
            {...reorderProps(key)}
            active={activeKey === key}
            icon={Icon}
            label={label}
            title={`${pipeline.name} · ${asset.name}`}
            onSelect={() => onSelectDocument?.(document)}
            onClose={() => onCloseDocument?.(document)}
          />
        );
      })}
    </div>
  );
}

function BuildDocumentTab({
  active,
  icon: Icon,
  label,
  title,
  onSelect,
  onClose,
  onMove,
  dropTarget,
  ...dragProps
}: {
  active: boolean;
  icon: LucideIcon;
  label: string;
  title: string;
  onSelect: () => void;
  onClose: () => void;
  onMove: (direction: -1 | 1) => void;
  dropTarget: boolean;
  draggable: boolean;
  onDragStart: React.DragEventHandler<HTMLDivElement>;
  onDragOver: React.DragEventHandler<HTMLDivElement>;
  onDrop: React.DragEventHandler<HTMLDivElement>;
  onDragEnd: React.DragEventHandler<HTMLDivElement>;
}) {
  const tab = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!active || !tab.current) return;
    const item = tab.current;
    const strip = item.parentElement;
    if (!strip) return;
    const selected = item.getBoundingClientRect();
    const viewport = strip.getBoundingClientRect();
    // Scroll only this strip, never a page/canvas ancestor, and do not steal focus.
    if (selected.left < viewport.left) strip.scrollLeft -= viewport.left - selected.left;
    else if (selected.right > viewport.right) strip.scrollLeft += selected.right - viewport.right;
  }, [active]);
  return (
    <div
      ref={tab}
      {...dragProps}
      className={cn(
        "group flex h-8 min-w-28 max-w-48 shrink-0 items-center rounded-lg border text-xs transition-colors",
        dropTarget && "ring-2 ring-primary",
        active
          ? "border-primary/30 bg-primary/10 text-foreground shadow-sm"
          : "border-transparent bg-muted/50 text-muted-foreground hover:border-border hover:bg-muted",
      )}
      title={`${title} · Drag to reorder, or use Alt+Shift+Left/Right`}
    >
      <button
        type="button"
        role="tab"
        aria-selected={active}
        className="flex min-w-0 flex-1 items-center gap-1.5 self-stretch pl-2 text-left"
        onClick={onSelect}
        aria-keyshortcuts="Alt+Shift+ArrowLeft Alt+Shift+ArrowRight"
        onKeyDown={(event) => {
          if (!event.altKey || !event.shiftKey || !["ArrowLeft", "ArrowRight"].includes(event.key))
            return;
          event.preventDefault();
          onMove(event.key === "ArrowLeft" ? -1 : 1);
        }}
      >
        <Icon className={cn("size-3.5 shrink-0", active && "text-primary")} />
        <span className="truncate font-mono">{label}</span>
      </button>
      <button
        type="button"
        className="mx-1 flex size-5 shrink-0 items-center justify-center rounded-md opacity-50 hover:bg-background/80 hover:opacity-100 focus-visible:opacity-100"
        aria-label={`Close ${label}`}
        onClick={onClose}
      >
        <X className="size-3" />
      </button>
    </div>
  );
}
