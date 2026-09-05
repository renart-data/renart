import { useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { X } from "lucide-react";
import { lazy, Suspense, useEffect, useRef } from "react";
import { useIsMobile } from "@/hooks/use-mobile";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { workspaceAtom } from "@/lib/atoms/workspace";
import {
  resolveColumn,
  type ResourceDetail,
  type ResourceSearch,
  type ColumnTarget,
} from "@/lib/resource-navigation";
import { ErrorBoundary } from "@/components/ui/error-boundary";
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
const DataObjectDetail = lazy(() =>
  import("./data-browser/data-browser").then((m) => ({ default: m.DataObjectDetail })),
);
const ResourceAssetSection = lazy(() =>
  import("./resource-asset-section").then((m) => ({ default: m.ResourceAssetSection })),
);
const ResourceConnectionDetail = lazy(() =>
  import("./resource-connection-detail").then((m) => ({ default: m.ResourceConnectionDetail })),
);
const ResourceDocumentDetail = lazy(() =>
  import("./resource-document-detail").then((m) => ({ default: m.ResourceDocumentDetail })),
);

export function ResourceDetailOutlet() {
  const location = useLocation();
  const detail = (location.search as ResourceSearch).detail;
  const navigate = useNavigate();
  const mobile = useIsMobile();
  const returnFocus = useRef<HTMLElement | null>(null);
  const heading = useRef<HTMLHeadingElement | null>(null);
  const detailKey = JSON.stringify(detail);
  useEffect(() => {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => {
      if (returnFocus.current?.isConnected) returnFocus.current.focus({ preventScroll: true });
    };
  }, []);
  useEffect(() => {
    if (detail?.target.column || (detail?.target.kind === "connection" && detail.target.field))
      return;
    heading.current?.focus({ preventScroll: true });
  }, [detailKey]);
  const close = () =>
    void navigate({
      to: ".",
      search: (search) => ({ ...search, detail: undefined }),
      replace: true,
    });
  if (!detail) return null;
  const target = detail.target;
  const title =
    target.kind === "asset-column"
      ? "Current column definition"
      : target.kind === "data-object"
        ? "Data object details"
        : target.kind === "connection"
          ? "Connection"
          : target.kind === "notebook-cell"
            ? "Saved notebook cell"
            : target.kind === "presentation"
              ? "Saved presentation definition"
              : `Current ${target.section}`;
  const label =
    target.kind === "data-object"
      ? (target.address.name ?? target.address.path)
      : target.kind === "connection"
        ? target.connection
        : target.column;
  const identity = JSON.stringify(detail);
  const content = (
    <ErrorBoundary
      resetKey={identity}
      fallback={
        <p role="alert" className="p-3">
          Could not open this detail. Your main workspace is unchanged.
        </p>
      }
    >
      <Suspense
        fallback={
          <p role="status" className="p-3">
            Opening detail…
          </p>
        }
      >
        {target.kind === "asset-column" ? (
          <ColumnDetail detail={{ ...detail, target }} navigationKey={identity} />
        ) : target.kind === "data-object" ? (
          <DataObjectDetail target={target} environment={detail.environment} />
        ) : target.kind === "connection" ? (
          <ResourceConnectionDetail target={target} environment={detail.environment} />
        ) : target.kind === "notebook-cell" || target.kind === "presentation" ? (
          <ResourceDocumentDetail target={target} />
        ) : (
          <ResourceAssetSection
            key={`${target.asset_id}:${target.section}:${target.column}:${target.check_name}`}
            target={target}
            environment={detail.environment}
          />
        )}
      </Suspense>
    </ErrorBoundary>
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
            <SheetTitle ref={heading} tabIndex={-1}>
              {title}
            </SheetTitle>
            <SheetDescription>
              {label} · {detail.environment}
            </SheetDescription>
          </SheetHeader>
          {content}
        </SheetContent>
      </Sheet>
    );
  return (
    <aside
      aria-label={title}
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented) close();
      }}
      className="flex min-h-0 w-96 shrink-0 flex-col overflow-hidden rounded-xl border bg-card shadow-sm"
    >
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="min-w-0">
          <h2 ref={heading} tabIndex={-1} className="text-sm font-medium">
            {title}
          </h2>
          <p className="truncate text-xs text-muted-foreground">
            {label} · {detail.environment}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={target.kind === "asset-column" ? "Close column definition" : "Close detail"}
          onClick={close}
        >
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
  detail: ResourceDetail & { target: ColumnTarget };
  navigationKey: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const { workspaceConfig } = useWorkspaceSettingsData();
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
