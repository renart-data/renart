"use client";

import { Link } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { FileText, LayoutDashboard, Plus } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { cn } from "@/lib/utils";

import type { PresentationKind } from "./presentation-page";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";

const presentationLibraryMeta = {
  dashboard: {
    plural: "Dashboards",
    singular: "dashboard",
    icon: LayoutDashboard,
  },
  report: {
    plural: "Reports",
    singular: "report",
    icon: FileText,
  },
} as const;

export function PresentationLibrarySidebar({
  kind,
  activePresentationId,
  onCreate,
}: {
  kind: PresentationKind;
  activePresentationId?: string;
  onCreate?: () => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const items = (workspace?.presentations ?? []).filter((item) => item.kind === kind);
  const meta = presentationLibraryMeta[kind];
  const Icon = meta.icon;

  return (
    <AppContextSidebarFrame
      title={meta.plural}
      subtitle={`${items.length} ${items.length === 1 ? meta.singular : meta.plural.toLowerCase()}`}
      actions={
        onCreate ? (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`New ${meta.singular}`}
            onClick={onCreate}
          >
            <Plus />
          </Button>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-1 p-2">
        {items.length === 0 ? (
          <div className="px-2 py-6 text-center text-xs text-muted-foreground">
            No {meta.plural.toLowerCase()} yet.
          </div>
        ) : (
          items.map((item) => (
            <Link
              key={item.workspace_id}
              to={kind === "dashboard" ? "/dashboards/$presentationId" : "/reports/$presentationId"}
              params={{ presentationId: item.workspace_id }}
              className={cn(
                "flex min-w-0 items-center gap-2 rounded-md px-2.5 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                item.workspace_id === activePresentationId &&
                  "bg-primary/10 font-medium text-foreground",
              )}
            >
              <Icon className="size-4 shrink-0 text-primary" />
              <span className="min-w-0 flex-1">
                <span className="block truncate">{item.title}</span>
                <span className="block truncate font-mono text-[10px] text-muted-foreground">
                  {item.visualizations?.length ?? 0} visualization
                  {(item.visualizations?.length ?? 0) === 1 ? "" : "s"}
                </span>
              </span>
              {item.problems?.length ? (
                <Badge
                  variant="outline"
                  className="h-5 shrink-0 border-amber-500/30 px-1.5 text-[10px] text-amber-700 dark:text-amber-400"
                >
                  {item.problems.length}
                </Badge>
              ) : null}
            </Link>
          ))
        )}
      </div>
    </AppContextSidebarFrame>
  );
}
