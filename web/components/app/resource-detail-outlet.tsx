import { useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { X } from "lucide-react";
import { lazy, Suspense, useEffect, useRef } from "react";
import { useIsMobile } from "@/hooks/use-mobile";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { workspaceAtom } from "@/lib/atoms/workspace";
import { resolveColumn, type ResourceDetail, type ResourceSearch } from "@/lib/resource-navigation";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
const ColumnsCard = lazy(() =>
  import("./asset-guided-cards").then((module) => ({ default: module.ColumnsCard })),
);

export function ResourceDetailOutlet() {
  const location = useLocation();
  const detail = (location.search as ResourceSearch).detail;
  const navigate = useNavigate();
  const mobile = useIsMobile();
  const close = () =>
    void navigate({
      to: ".",
      search: (search) => ({ ...search, detail: undefined }),
      replace: true,
    });
  if (!detail) return null;
  const content = (
    <ColumnDetail detail={detail} navigationKey={location.state.key ?? location.href} />
  );
  if (mobile)
    return (
      <Sheet
        open
        onOpenChange={(open) => {
          if (!open) close();
        }}
      >
        <SheetContent
          side="right"
          className="gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-none"
          onOpenAutoFocus={(event) => event.preventDefault()}
        >
          <SheetHeader className="border-b pr-12">
            <SheetTitle>Current column definition</SheetTitle>
            <SheetDescription>
              {detail.target.column} · {detail.environment}
            </SheetDescription>
          </SheetHeader>
          {content}
        </SheetContent>
      </Sheet>
    );
  return (
    <aside
      aria-label="Current column definition"
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented) close();
      }}
      className="flex min-h-0 w-96 shrink-0 flex-col overflow-hidden rounded-xl border bg-card shadow-sm"
    >
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="min-w-0">
          <h2 className="text-sm font-medium">Current column definition</h2>
          <p className="truncate text-xs text-muted-foreground">
            {detail.target.column} · {detail.environment}
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Close column definition" onClick={close}>
          <X />
        </Button>
      </div>
      {content}
    </aside>
  );
}

function ColumnDetail({
  detail,
  navigationKey,
}: {
  detail: ResourceDetail;
  navigationKey: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const { workspaceConfig } = useWorkspaceSettingsData();
  const returnFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => {
      if (returnFocus.current?.isConnected) returnFocus.current.focus({ preventScroll: true });
    };
  }, []);
  const matches =
    workspace?.pipelines.flatMap((pipeline) =>
      pipeline.assets.filter((asset) => asset.id === detail.target.asset_id),
    ) ?? [];
  const asset = matches.length === 1 ? matches[0] : undefined;
  const column = resolveColumn(asset?.columns ?? [], detail.target.column);
  const environmentExists = workspaceConfig?.environments.some(
    (environment) => environment.name === detail.environment,
  );
  if (!workspace || !workspaceConfig)
    return (
      <p role="status" className="p-4 text-sm">
        Loading column definition…
      </p>
    );
  if (!asset || !environmentExists)
    return (
      <p role="alert" className="p-4 text-sm">
        {!asset
          ? "This asset is no longer available in the linked project."
          : "The environment in this link is no longer available."}
      </p>
    );
  return (
    <ScrollArea className="min-h-0 flex-1">
      <div className="px-3" data-testid="routed-column-definition">
        <p className="mt-3 truncate text-xs text-muted-foreground" title={asset.name}>
          {asset.name}
        </p>
        {!column ? (
          <p role="alert" className="mt-3 text-sm text-warning">
            The linked column was renamed, removed, or is ambiguous. No other column has been
            selected.
          </p>
        ) : null}
        <Suspense
          fallback={
            <p role="status" className="py-3 text-sm">
              Loading column editor…
            </p>
          }
        >
          <ColumnsCard
            key={asset.id}
            asset={asset}
            environmentOverride={detail.environment}
            focusedColumn={column?.name}
            focusToken={`${navigationKey}:${detail.target.column}`}
          />
        </Suspense>
      </div>
    </ScrollArea>
  );
}
