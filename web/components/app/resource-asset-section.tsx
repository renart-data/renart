import { Link } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { useState } from "react";
import { workspaceAtom } from "@/lib/atoms/workspace";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { ScrollArea } from "@/components/ui/scroll-area";
import { resolveColumn, type SectionTarget } from "@/lib/resource-navigation";
import { sourceAnchorFingerprint } from "@/lib/deployment-diff-annotations";
import {
  ColumnsCard,
  DependenciesCard,
  IdentityCard,
  MaterializationCard,
  QualityChecksCard,
  type QualityCheckFocus,
} from "./asset-guided-cards";
import { ResourceLink } from "./resource-link";

export function ResourceAssetSection({
  target,
  environment,
}: {
  target: SectionTarget;
  environment: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const { workspaceConfig } = useWorkspaceSettingsData();
  const [focusedCheck, setFocusedCheck] = useState<QualityCheckFocus | null>(null);
  if (!workspace || !workspaceConfig)
    return (
      <p role="status" className="p-3">
        Loading asset…
      </p>
    );
  const matches = workspace.pipelines.flatMap((p) =>
    p.assets.filter((a) => a.id === target.asset_id).map((asset) => ({ asset, pipeline: p })),
  );
  const owner = matches.length === 1 ? matches[0] : undefined;
  if (!owner || !workspaceConfig.environments.some((e) => e.name === environment))
    return (
      <p role="alert" className="p-3">
        The linked asset or environment is no longer available.
      </p>
    );
  const { asset, pipeline } = owner;
  const checkColumn = target.column ? resolveColumn(asset.columns ?? [], target.column) : undefined;
  const checkMatches = checkColumn?.checks?.filter((c) => c.name === target.check_name) ?? [];
  const linkedCheck: QualityCheckFocus | null =
    checkColumn && checkMatches.length === 1
      ? { kind: "column", column: checkColumn.name, name: checkMatches[0].name, token: 1 }
      : null;
  const sourceMatches = target.source_fingerprint === sourceAnchorFingerprint(asset.content);
  return (
    <ScrollArea className="min-h-0 flex-1">
      <div className="px-3" data-testid="routed-asset-section">
        <p className="my-3 truncate text-xs text-muted-foreground">
          {asset.name} · Current definition
        </p>
        {target.section === "columns" ? (
          <ColumnsCard asset={asset} environmentOverride={environment} />
        ) : null}
        {target.section === "dependencies" ? <DependenciesCard asset={asset} /> : null}
        {target.section === "identity" ? (
          <>
            <IdentityCard asset={asset} pipelineId={pipeline.id} />
            {asset.connection ? (
              <ResourceLink
                target={{ kind: "connection", connection: asset.connection }}
                environment={environment}
              />
            ) : null}
          </>
        ) : null}
        {target.section === "materialization" ? (
          <MaterializationCard asset={asset} pipelineId={pipeline.id} />
        ) : null}
        {target.section === "checks" && target.column && !linkedCheck ? (
          <p role="alert" className="text-sm">
            The linked check is missing or ambiguous. No other check has been selected.
          </p>
        ) : null}
        {target.section === "checks" ? (
          <QualityChecksCard
            asset={asset}
            focusedCheck={focusedCheck ?? linkedCheck}
            onFocusCheck={(check) => setFocusedCheck({ ...check, token: Date.now() })}
          />
        ) : null}
        {target.section === "source" ? (
          <>
            <p className="mb-3 text-xs text-muted-foreground">
              Saved source, not a historical deployment snapshot or an unsaved editor draft.
            </p>
            {target.source_fingerprint && !sourceMatches ? (
              <p role="alert" className="mb-3 text-sm">
                Source changed since this diagnostic. The old location is not highlighted.
              </p>
            ) : null}
            <Link
              to="/pipelines/$pipelineId/assets/$assetId/code"
              params={{ pipelineId: pipeline.id, assetId: asset.id }}
              search={(s) => ({ ...s, detail: undefined, editor: "asset" })}
              className="text-sm text-primary underline"
            >
              Edit source in main editor
            </Link>
            <pre className="my-3 overflow-auto text-xs">
              {asset.content.split(/\r?\n/).map((line, index) => (
                <div
                  key={index}
                  className={
                    sourceMatches &&
                    target.line &&
                    index + 1 >= target.line &&
                    index + 1 <= (target.end_line ?? target.line)
                      ? "bg-warning/15"
                      : undefined
                  }
                >
                  <span className="mr-3 select-none text-muted-foreground">{index + 1}</span>
                  {line || " "}
                </div>
              ))}
            </pre>
          </>
        ) : null}
      </div>
    </ScrollArea>
  );
}
