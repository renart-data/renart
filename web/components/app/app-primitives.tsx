import {
  AlertTriangle,
  CheckCircle2,
  Circle,
  History,
  Loader2,
  MoreHorizontal,
  XCircle,
} from "lucide-react";
import { ComponentType, Fragment, ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import {
  DelimitedCard,
  DelimitedCardContent,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { AssetStaleness, AssetStalenessStatus } from "@/lib/api-staleness";
import { cn } from "@/lib/utils";

import { AppAsset, integrations, kindMeta } from "./app-data";

export function AppPage({ children }: { children: ReactNode }) {
  return <div className="flex h-full min-h-0 flex-col bg-muted/40 text-foreground">{children}</div>;
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="flex min-h-12 shrink-0 items-center gap-3 px-3">
      <div className="min-w-0">
        <h1 className="truncate text-base font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="truncate text-xs text-muted-foreground">{subtitle}</p> : null}
      </div>
      <div className="ml-auto flex items-center gap-2">{actions}</div>
    </div>
  );
}

export function AppPanel({ children, className }: { children: ReactNode; className?: string }) {
  return <DelimitedCard className={cn("min-h-0", className)}>{children}</DelimitedCard>;
}

export function SectionCard({
  title,
  icon: Icon,
  children,
  action,
  className,
}: {
  title: string;
  icon?: ComponentType<{ className?: string }>;
  children: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <DelimitedCard className={className}>
      <DelimitedCardHeader>
        {Icon ? <Icon className="size-4 text-primary" /> : null}
        <DelimitedCardTitle>{title}</DelimitedCardTitle>
        <div className="ml-auto">{action}</div>
      </DelimitedCardHeader>
      <DelimitedCardContent>{children}</DelimitedCardContent>
    </DelimitedCard>
  );
}

export function IntegrationBadge({ name }: { name: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-background px-1.5 py-0.5 text-[11px] text-muted-foreground">
      <span
        className="size-2 rounded-sm"
        style={{ backgroundColor: integrations[name] ?? "#71717a" }}
      />
      <span className="truncate">{name}</span>
    </span>
  );
}

export function StatusPill({ status }: { status: string }) {
  if (status === "success" || status === "pass" || status === "ok") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-1.5 py-0.5 text-[11px] text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
        <CheckCircle2 className="size-3" />
        Success
      </span>
    );
  }
  if (status === "failed" || status === "fail" || status === "overdue") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-100 px-1.5 py-0.5 text-[11px] text-red-700 dark:bg-red-500/15 dark:text-red-300">
        <XCircle className="size-3" />
        Failed
      </span>
    );
  }
  if (status === "running") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-1.5 py-0.5 text-[11px] text-amber-700 dark:bg-amber-500/15 dark:text-amber-300">
        <Loader2 className="size-3 animate-spin" />
        Running
      </span>
    );
  }
  if (status === "queued") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-sky-100 px-1.5 py-0.5 text-[11px] text-sky-700 dark:bg-sky-500/15 dark:text-sky-300">
        <Circle className="size-3" />
        Queued
      </span>
    );
  }
  if (status === "cancelled") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-zinc-200 px-1.5 py-0.5 text-[11px] text-zinc-700 dark:bg-zinc-500/15 dark:text-zinc-300">
        <Circle className="size-3" />
        Cancelled
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
      <Circle className="size-3" />
      Idle
    </span>
  );
}

const stalenessMeta: Record<
  AssetStalenessStatus,
  { label: string; className: string; dotClassName: string }
> = {
  fresh: {
    label: "Fresh",
    className: "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300",
    dotClassName: "bg-emerald-500",
  },
  stale_edited: {
    label: "Edited",
    className: "bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300",
    dotClassName: "bg-amber-500",
  },
  stale_deployment: {
    label: "Deployment differs",
    className: "bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300",
    dotClassName: "bg-amber-500",
  },
  stale_upstream: {
    label: "Upstream changed",
    className: "bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-300",
    dotClassName: "bg-amber-400",
  },
  partial: {
    label: "Partial",
    className: "bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300",
    dotClassName: "bg-sky-500",
  },
  volatile: {
    label: "Always checked",
    className: "bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300",
    dotClassName: "bg-violet-500",
  },
  external: {
    label: "External source",
    className: "",
    dotClassName: "bg-muted-foreground",
  },
  never_built: {
    label: "Never built",
    className: "bg-zinc-200 text-zinc-700 dark:bg-zinc-500/15 dark:text-zinc-300",
    dotClassName: "bg-zinc-400",
  },
  missing: {
    label: "Missing",
    className: "bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300",
    dotClassName: "bg-red-500",
  },
};

function stalenessBaseLabel(staleness: AssetStaleness) {
  if (staleness.status === "partial" && staleness.total_seconds && staleness.total_seconds > 0) {
    const day = 24 * 60 * 60;
    if (staleness.total_seconds >= day) {
      return `${Math.floor((staleness.covered_seconds ?? 0) / day)}/${Math.round(staleness.total_seconds / day)} days`;
    }
    const hour = 60 * 60;
    return `${Math.floor((staleness.covered_seconds ?? 0) / hour)}/${Math.round(staleness.total_seconds / hour)} hours`;
  }
  return stalenessMeta[staleness.status]?.label ?? staleness.status;
}

export function resolveFreshnessDisplay(staleness: AssetStaleness): {
  label: string;
  className: string;
  dotClassName: string;
} {
  const meta = stalenessMeta[staleness.status];
  return {
    label: stalenessBaseLabel(staleness),
    className: meta?.className ?? "",
    dotClassName: meta?.dotClassName ?? "bg-zinc-400",
  };
}

export function stalenessLabel(staleness: AssetStaleness) {
  return resolveFreshnessDisplay(staleness).label;
}

export function StalenessBadge({
  staleness,
  className,
}: {
  staleness?: AssetStaleness;
  className?: string;
}) {
  if (!staleness || !stalenessMeta[staleness.status]) return null;
  const display = resolveFreshnessDisplay(staleness);
  return (
    <Badge
      size="xs"
      variant={staleness.status === "external" ? "outline" : undefined}
      data-staleness={staleness.status}
      title={
        staleness.status === "external"
          ? "External source: freshness is not tracked"
          : `Staleness: ${display.label}`
      }
      className={cn("max-w-full truncate", display.className, className)}
    >
      <span className={cn("size-1.5 shrink-0 rounded-full", display.dotClassName)} />
      {display.label}
    </Badge>
  );
}

export function lastRunLabel(staleness: AssetStaleness) {
  if (staleness.last_run_status === "cancelled") return "Last run cancelled";
  if (
    staleness.last_run_on_current_content &&
    (staleness.status === "stale_edited" ||
      staleness.status === "stale_deployment" ||
      staleness.status === "never_built")
  ) {
    return "Build failed";
  }
  return "Last run failed";
}

function lastRunTooltip(staleness: AssetStaleness) {
  const label = lastRunLabel(staleness);
  const at = staleness.last_run_at
    ? ` on ${new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(staleness.last_run_at))}`
    : "";
  if (staleness.status === "fresh" && staleness.last_run_status === "failed") {
    return `${label}${at}. Previously built data still covers the selected range.`;
  }
  if (!staleness.last_run_on_current_content) {
    return `${label}${at}. The asset has changed since that attempt.`;
  }
  return `${label}${at}.`;
}

export function LastRunBadge({ staleness }: { staleness?: AssetStaleness }) {
  if (
    !staleness ||
    (staleness.last_run_status !== "failed" && staleness.last_run_status !== "cancelled")
  ) {
    return null;
  }
  const label = lastRunLabel(staleness);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          size="xs"
          variant={staleness.last_run_status === "failed" ? "destructive" : "muted"}
          data-last-run={staleness.last_run_status}
          aria-label={label}
          tabIndex={0}
          className="max-w-full shrink-0 truncate"
        >
          {label}
        </Badge>
      </TooltipTrigger>
      <TooltipContent>{lastRunTooltip(staleness)}</TooltipContent>
    </Tooltip>
  );
}

function qualityFailureLabel(staleness: AssetStaleness) {
  const failures = staleness.failed_checks ?? [];
  if (failures.length === 0) return "Checks failed";
  const first = failures[0];
  const check =
    first.kind === "column" && first.column ? `${first.column} · ${first.name}` : first.name;
  return failures.length === 1 ? `Check failed: ${check}` : `${failures.length} checks failed`;
}

export function QualityFailureBadge({
  staleness,
  onReview,
}: {
  staleness?: AssetStaleness;
  onReview?: () => void;
}) {
  if (
    staleness?.quality_status !== "failed" ||
    !staleness.quality_on_current_content ||
    (staleness.failed_checks?.length ?? 0) === 0
  ) {
    return null;
  }
  const label = qualityFailureLabel(staleness);
  const className =
    "nodrag inline-flex max-w-full shrink-0 items-center gap-1 truncate rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium text-destructive outline-none hover:bg-destructive/15 focus-visible:ring-1 focus-visible:ring-destructive/50";
  const content = (
    <>
      <AlertTriangle className="size-2.5 shrink-0" />
      <span className="truncate">Checks failed</span>
    </>
  );
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {onReview ? (
          <button
            type="button"
            className={className}
            data-quality-status="failed"
            aria-label={`${label}. Open quality checks`}
            onClick={(event) => {
              event.stopPropagation();
              onReview();
            }}
            onKeyDown={(event) => event.stopPropagation()}
          >
            {content}
          </button>
        ) : (
          <span className={className} data-quality-status="failed" tabIndex={0} aria-label={label}>
            {content}
          </span>
        )}
      </TooltipTrigger>
      <TooltipContent>{onReview ? `${label}. Open the failed check.` : label}</TooltipContent>
    </Tooltip>
  );
}

export function stalenessDotClassName(staleness: AssetStaleness) {
  return resolveFreshnessDisplay(staleness).dotClassName;
}

export type AssetNodeAction = {
  key: string;
  label: string;
  icon: ComponentType<{ className?: string }>;
  onSelect: () => void;
  destructive?: boolean;
  separatorBefore?: boolean;
};

export function AssetNode({
  asset,
  selected,
  actions,
  onOpenConnection,
  onReviewFailedCheck,
}: {
  asset: AppAsset;
  selected?: boolean;
  actions?: AssetNodeAction[];
  onOpenConnection?: () => void;
  onReviewFailedCheck?: () => void;
}) {
  const meta = kindMeta[asset.kind];
  const Icon = meta.icon;
  const statusMeta = assetNodeStatusMeta(asset.status);
  const hasParseError = Boolean(asset.parseError);
  const showDescription = !hasParseError && Boolean(asset.description);
  const showLastRun =
    asset.status !== "pending" &&
    Boolean(asset.staleness) &&
    (asset.staleness?.last_run_status === "cancelled" ||
      (asset.staleness?.last_run_status === "failed" && asset.status === "failed"));
  const showTransientRunStatus =
    asset.status === "pending" ||
    asset.status === "overdue" ||
    (asset.status === "failed" && !showLastRun);
  const showQualityFailure =
    asset.staleness?.quality_status === "failed" &&
    asset.staleness.quality_on_current_content &&
    (asset.staleness.failed_checks?.length ?? 0) > 0;
  return (
    <div
      data-slot="asset-node"
      data-external={asset.isExternal ? "true" : undefined}
      className={cn(
        "w-58 overflow-hidden rounded-xl border-2 bg-card text-left shadow-sm transition hover:border-primary/60",
        asset.isExternal && "border-dashed bg-card/80",
        hasParseError
          ? "border-red-400 dark:border-red-500/70"
          : selected
            ? "border-primary"
            : "border-border",
      )}
    >
      <div
        className={cn(
          "flex h-8 items-center gap-1.5 border-b bg-muted/30 px-2.5",
          hasParseError && "bg-red-50 dark:bg-red-500/10",
        )}
      >
        {hasParseError ? (
          <AlertTriangle className="size-3.5 shrink-0 text-red-500" />
        ) : (
          <Icon className="size-3.5 text-muted-foreground" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">{asset.name}</span>
        {asset.hasTypeCheckError ? (
          <AlertTriangle
            data-testid="asset-type-check-error"
            className="size-3.5 shrink-0 text-amber-500"
            aria-label="Type check error"
          />
        ) : null}
        {actions && actions.length > 0 ? (
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label="Asset actions"
              className="nodrag -mr-1 flex size-5 items-center justify-center rounded text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring data-[state=open]:bg-muted data-[state=open]:text-foreground"
              onClick={(event) => event.stopPropagation()}
            >
              <MoreHorizontal className="size-3.5" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
              <AssetNodeMenuItems
                actions={actions}
                ItemComponent={DropdownMenuItem}
                SeparatorComponent={DropdownMenuSeparator}
              />
            </DropdownMenuContent>
          </DropdownMenu>
        ) : (
          <MoreHorizontal className="size-3.5 text-muted-foreground" />
        )}
      </div>
      <div className="flex flex-col gap-2 p-2.5">
        {hasParseError ? (
          <p className="truncate text-[11px] text-muted-foreground" title={asset.parseError}>
            {asset.parseError}
          </p>
        ) : null}
        {/* Freshness and the latest attempt are independent: a still-fresh asset
            can also say that its latest run failed. Live step state temporarily
            takes the attempt badge's place while that asset is running. */}
        {hasParseError ||
        asset.staleness ||
        asset.status === "pending" ||
        asset.status === "failed" ||
        asset.status === "overdue" ||
        asset.materializedAt ? (
          <div className="flex items-center gap-1.5">
            {hasParseError ? (
              <span className="inline-flex min-w-0 items-center gap-1 truncate rounded bg-red-100 px-1.5 py-0.5 text-[10px] text-red-700 dark:bg-red-500/15 dark:text-red-300">
                <AlertTriangle className="size-2.5 shrink-0" />
                Parse error
              </span>
            ) : asset.isExternal ? (
              <>
                <Badge variant="outline" className="h-5 px-1.5 text-[10px]">
                  External
                </Badge>
                <span className="min-w-0 truncate text-[10px] text-muted-foreground">
                  {asset.materializedAt}
                </span>
              </>
            ) : (
              <>
                <StalenessBadge staleness={asset.staleness} className="shrink-0" />
                {showLastRun ? <LastRunBadge staleness={asset.staleness} /> : null}
                {showQualityFailure ? (
                  <QualityFailureBadge staleness={asset.staleness} onReview={onReviewFailedCheck} />
                ) : null}
                {showTransientRunStatus ? (
                  <span
                    className={cn(
                      "min-w-0 shrink-0 truncate rounded px-1.5 py-0.5 text-[10px]",
                      statusMeta.className,
                    )}
                    title={asset.materializedAt ? `Last build: ${asset.materializedAt}` : undefined}
                  >
                    {statusMeta.label}
                  </span>
                ) : !showLastRun && !showQualityFailure && asset.materializedAt ? (
                  <span
                    className="inline-flex min-w-0 items-center gap-1 truncate text-[10px] text-muted-foreground"
                    title={`Last built: ${asset.materializedAt}`}
                  >
                    <History className="size-2.5 shrink-0" />
                    <span className="truncate">{asset.materializedAt}</span>
                  </span>
                ) : null}
              </>
            )}
          </div>
        ) : null}
        <div data-slot="asset-node-metadata" className="flex min-w-0 items-center gap-2">
          {showDescription ? (
            <p
              data-slot="asset-node-description"
              className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground"
              title={asset.description}
            >
              {asset.description}
            </p>
          ) : null}
          <div
            data-slot="asset-node-connection"
            className={cn(
              "ml-auto min-w-0",
              showDescription ? "max-w-[55%] shrink-0" : "max-w-full",
            )}
          >
            {onOpenConnection ? (
              <button
                type="button"
                className="nodrag min-w-0 max-w-full rounded-md outline-none focus-visible:ring-1 focus-visible:ring-ring"
                title="Open pipeline connection settings"
                aria-label={`Connection ${asset.integration} — open pipeline connection settings`}
                onClick={(event) => {
                  event.stopPropagation();
                  onOpenConnection();
                }}
              >
                <IntegrationBadge name={asset.integration} />
              </button>
            ) : (
              <IntegrationBadge name={asset.integration} />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

type AssetNodeMenuItemProps = {
  variant?: "default" | "destructive";
  onSelect?: (event: Event) => void;
  className?: string;
  children: ReactNode;
};

export function AssetNodeMenuItems({
  actions,
  ItemComponent,
  SeparatorComponent,
}: {
  actions: AssetNodeAction[];
  ItemComponent: ComponentType<AssetNodeMenuItemProps>;
  SeparatorComponent: ComponentType;
}) {
  return (
    <>
      {actions.map((action) => {
        const ActionIcon = action.icon;
        return (
          <Fragment key={action.key}>
            {action.separatorBefore ? <SeparatorComponent /> : null}
            <ItemComponent
              variant={action.destructive ? "destructive" : "default"}
              onSelect={() => action.onSelect()}
            >
              <ActionIcon className="size-3.5" />
              {action.label}
            </ItemComponent>
          </Fragment>
        );
      })}
    </>
  );
}

function assetNodeStatusMeta(status: AppAsset["status"]) {
  if (status === "unknown") {
    return {
      label: "Unknown",
      className: "bg-zinc-200 text-zinc-700 dark:bg-zinc-500/15 dark:text-zinc-300",
    };
  }
  if (status === "pending") {
    return {
      label: "Running",
      className: "bg-blue-100 text-blue-700 dark:bg-blue-500/15 dark:text-blue-300",
    };
  }
  if (status === "failed" || status === "overdue") {
    return {
      label: status === "overdue" ? "Overdue" : "Failed",
      className: "bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300",
    };
  }
  return {
    label: "Materialized",
    className: "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300",
  };
}

export function SimpleTable({
  columns,
  rows,
  className,
  viewportClassName,
  ariaLabel,
}: {
  columns: string[];
  rows: Array<Array<ReactNode>>;
  className?: string;
  // Constrain the scroll viewport (e.g. "max-h-72"): the cap must live on the
  // viewport, where Radix actually scrolls, not on the Root.
  viewportClassName?: string;
  ariaLabel?: string;
}) {
  return (
    <ScrollArea className={cn("h-full min-h-0", className)} viewportClassName={viewportClassName}>
      <Table aria-label={ariaLabel}>
        <TableHeader>
          <TableRow className="bg-muted/50">
            {columns.map((column) => (
              <TableHead key={column} className="h-8 text-xs uppercase text-muted-foreground">
                {column}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, index) => (
            <TableRow key={index}>
              {row.map((cell, cellIndex) => (
                <TableCell key={cellIndex} className="h-9 py-1.5 text-xs">
                  {cell}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </ScrollArea>
  );
}

export function SeverityIcon({ severity }: { severity: string }) {
  if (severity === "error") {
    return <XCircle className="size-4 text-red-500" />;
  }
  if (severity === "warn") {
    return <AlertTriangle className="size-4 text-amber-500" />;
  }
  return <CheckCircle2 className="size-4 text-sky-500" />;
}
