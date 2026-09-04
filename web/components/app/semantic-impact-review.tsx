import { Badge } from "@/components/ui/badge";
import type {
  PipelinePlanSemanticAssetImpact,
  PipelinePlanSemanticColumnContract,
  PipelinePlanSemanticImpact,
} from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";

export function SemanticImpactReview({
  impact,
  id = "pipeline-deploy-semantic-impact",
  className,
}: {
  impact?: PipelinePlanSemanticImpact;
  id?: string;
  className?: string;
}) {
  if (!impact) return null;

  const baseline = impact.baseline_version_id
    ? `deployment ${impact.baseline_version_id.slice(0, 8)}`
    : "the previous deployment";
  const warning = impact.summary.warnings > 0;

  return (
    <section aria-labelledby={id} className={className}>
      <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h3 id={id} className="text-sm font-medium">
            Semantic impact
          </h3>
          <p className="text-xs text-muted-foreground">
            Formatting-insensitive SQL and inferred output contracts compared with {baseline}.
          </p>
        </div>
        <div className="flex items-center gap-1">
          {!impact.complete && impact.status === "available" ? (
            <Badge variant="muted" size="xs">
              incomplete
            </Badge>
          ) : null}
          <Badge
            variant="outline"
            size="xs"
            className={cn(warning && "border-warning/40 bg-warning/10")}
          >
            {warning ? <span className="size-1.5 rounded-full bg-warning" /> : null}
            {semanticImpactStatusLabel(impact)}
          </Badge>
        </div>
      </div>

      {impact.status === "no_baseline" ? (
        <div className="rounded-lg border px-3 py-2 text-xs text-muted-foreground">
          This is the first deployment, so there is no semantic baseline yet.
        </div>
      ) : impact.status !== "available" ? (
        <div className="rounded-lg border border-destructive/30 px-3 py-2 text-xs text-muted-foreground">
          {impact.reason || "Semantic impact analysis is unavailable for this deployment."}
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          <div className="border-b bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
            {semanticImpactSummary(impact)}
          </div>
          {impact.assets.length === 0 ? (
            <div className="px-3 py-3 text-xs text-muted-foreground">
              No semantic SQL changes detected.
            </div>
          ) : (
            <div className="divide-y">
              {impact.assets.map((asset) => (
                <SemanticAssetImpactRow
                  key={`${asset.name}:${asset.change}:${asset.source_change}`}
                  asset={asset}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </section>
  );
}

export function SemanticAssetImpactRow({ asset }: { asset: PipelinePlanSemanticAssetImpact }) {
  const warning = asset.severity === "warning";

  return (
    <div className="space-y-2 px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">{asset.name}</span>
        <Badge variant="muted" size="xs">
          {semanticSourceChangeLabel(asset)}
        </Badge>
        <Badge
          variant="outline"
          size="xs"
          className={cn(warning && "border-warning/40 bg-warning/10")}
        >
          {warning ? <span className="size-1.5 rounded-full bg-warning" /> : null}
          {asset.origin}
        </Badge>
      </div>
      {asset.source_change === "changed" && asset.columns.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          Query behavior changed while the inferred output contract stayed stable.
        </p>
      ) : null}
      {asset.source_change === "formatting_only" && asset.columns.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          Source formatting or presentation comments changed; the canonical query is identical.
        </p>
      ) : null}
      {!asset.complete ? (
        <p className="text-xs text-muted-foreground">
          Some source or type facts are unknown, so additional effects may be hidden.
        </p>
      ) : null}
      {asset.columns.length > 0 ? (
        <div className="space-y-1">
          {asset.columns.map((column) => (
            <div
              key={column.index}
              className="grid gap-1 text-xs sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center"
            >
              <code className="truncate rounded bg-muted/40 px-1.5 py-0.5">
                {semanticColumnContractLabel(column.before)}
              </code>
              <span className="text-muted-foreground" aria-hidden="true">
                →
              </span>
              <code className="truncate rounded bg-muted/40 px-1.5 py-0.5">
                {semanticColumnContractLabel(column.after)}
              </code>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function semanticImpactStatusLabel(impact: PipelinePlanSemanticImpact) {
  if (impact.status === "no_baseline") return "first deployment";
  if (impact.status !== "available") return "unavailable";
  if (impact.summary.warnings > 0) {
    return `${impact.summary.warnings} ${impact.summary.warnings === 1 ? "warning" : "warnings"}`;
  }
  return "no semantic warnings";
}

function semanticImpactSummary(impact: PipelinePlanSemanticImpact) {
  const parts: string[] = [];
  if (impact.summary.behavior_changes > 0) {
    parts.push(
      `${impact.summary.behavior_changes} behavior ${impact.summary.behavior_changes === 1 ? "change" : "changes"}`,
    );
  }
  if (impact.summary.schema_changes > 0) {
    parts.push(
      `${impact.summary.schema_changes} output ${impact.summary.schema_changes === 1 ? "contract" : "contracts"} changed`,
    );
  }
  if (impact.summary.formatting_only > 0) {
    parts.push(`${impact.summary.formatting_only} formatting-only`);
  }
  if (parts.length === 0) return "No behavior or inferred output-contract changes.";
  return parts.join(" · ");
}

function semanticSourceChangeLabel(asset: PipelinePlanSemanticAssetImpact) {
  if (asset.change === "added" || asset.change === "removed") return asset.change;
  switch (asset.source_change) {
    case "formatting_only":
      return "formatting only";
    case "unchanged":
      return "schema only";
    case "changed":
      return "query changed";
    default:
      return "source unknown";
  }
}

function semanticColumnContractLabel(contract?: PipelinePlanSemanticColumnContract) {
  if (!contract) return "not present";
  const type = contract.type || "unknown type";
  const nullability =
    contract.nullability && contract.nullability !== "unknown"
      ? ` · ${contract.nullability.replace("_", " ")}`
      : "";
  return `${contract.name || "(unnamed)"}: ${type}${nullability}`;
}
