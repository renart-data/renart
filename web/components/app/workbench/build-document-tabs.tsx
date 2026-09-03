import { useAtomValue } from "jotai";
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
}: {
  documents: readonly BuildDocument[];
  activeDocument: BuildDocument | null;
  emptyLabel: string;
  onSelectDocument?: (document: BuildDocument) => void;
  onCloseDocument?: (document: BuildDocument) => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const activeKey = activeDocument ? buildDocumentKey(activeDocument) : null;

  return (
    <div
      role="tablist"
      aria-label="Open authoring documents"
      className="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto py-1"
    >
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
}: {
  active: boolean;
  icon: LucideIcon;
  label: string;
  title: string;
  onSelect: () => void;
  onClose: () => void;
}) {
  return (
    <div
      className={cn(
        "group flex h-8 min-w-28 max-w-48 shrink-0 items-center rounded-lg border text-xs transition-colors",
        active
          ? "border-primary/30 bg-primary/10 text-foreground shadow-sm"
          : "border-transparent bg-muted/50 text-muted-foreground hover:border-border hover:bg-muted",
      )}
      title={title}
    >
      <button
        type="button"
        role="tab"
        aria-selected={active}
        className="flex min-w-0 flex-1 items-center gap-1.5 self-stretch pl-2 text-left"
        onClick={onSelect}
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
