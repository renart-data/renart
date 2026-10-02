import { useAtom } from "jotai";
import { FilePlus2, Info, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { useGettingStartedStalenessObserver } from "@/hooks/use-getting-started-observer";
import type { AssetStaleness } from "@/lib/api-staleness";
import { connectDataHandoffAtom } from "@/lib/atoms/domains/connect-data";
import { cn } from "@/lib/utils";

const editedHintKey = "renart.hint.edited.v1";

function hintSeen(key: string) {
  try {
    return window.localStorage.getItem(key) === "seen";
  } catch {
    return false;
  }
}

function markHintSeen(key: string) {
  try {
    window.localStorage.setItem(key, "seen");
  } catch {
    // The hint stays dismissed for this page load.
  }
}

// One callout at a time over the pipeline canvas: the next step after an
// import, or a one-time explanation the first time an asset shows Edited.
export function PipelineCanvasCallouts({
  pipelineId,
  assets,
  onCreateFromSources,
}: {
  pipelineId: string;
  assets: Array<{ name: string; staleness?: AssetStaleness }>;
  onCreateFromSources: (sourceNames: string[]) => void;
}) {
  const [handoff, setHandoff] = useAtom(connectDataHandoffAtom);
  const [editedHintDismissed, setEditedHintDismissed] = useState(() => hintSeen(editedHintKey));
  const observeStaleness = useGettingStartedStalenessObserver();
  const staleness = useMemo(
    () => assets.flatMap((asset) => (asset.staleness ? [asset.staleness] : [])),
    [assets],
  );
  useEffect(() => observeStaleness(staleness), [observeStaleness, staleness]);

  if (handoff && handoff.pipelineId === pipelineId) {
    const imported = handoff.tables.filter((table) => assets.some((asset) => asset.name === table));
    return (
      <Callout onDismiss={() => setHandoff(null)}>
        <p className="min-w-0 text-sm">
          <span className="font-medium">
            Imported {handoff.tables.length} {handoff.tables.length === 1 ? "table" : "tables"}
          </span>{" "}
          <span className="text-muted-foreground">
            from <code className="font-mono text-xs">{handoff.connectionName}</code> as source
            assets. Build on them with SQL.
          </span>
        </p>
        <Button
          size="sm"
          className="shrink-0"
          onClick={() => {
            setHandoff(null);
            onCreateFromSources(imported.length > 0 ? imported : handoff.tables);
          }}
        >
          <FilePlus2 data-icon="inline-start" />
          New SQL asset from these sources
        </Button>
      </Callout>
    );
  }

  const showsEdited = assets.some((asset) => asset.staleness?.status === "stale_edited");
  if (showsEdited && !editedHintDismissed) {
    return (
      <Callout
        onDismiss={() => {
          markHintSeen(editedHintKey);
          setEditedHintDismissed(true);
        }}
        dismissLabel="Got it"
      >
        <Info className="mt-0.5 size-4 shrink-0 text-primary" />
        <p className="min-w-0 text-sm text-muted-foreground">
          <span className="font-medium text-foreground">Edited</span> means an asset's code changed
          since its last build, and assets that read from it show{" "}
          <span className="font-medium text-foreground">Upstream changed</span>.{" "}
          <span className="font-medium text-foreground">Review run</span> rebuilds only those.
        </p>
      </Callout>
    );
  }
  return null;
}

function Callout({
  children,
  onDismiss,
  dismissLabel,
}: {
  children: ReactNode;
  onDismiss: () => void;
  dismissLabel?: string;
}) {
  return (
    <div
      role="status"
      className={cn(
        "absolute inset-x-3 top-3 z-20 mx-auto flex max-w-2xl min-w-0 flex-wrap items-start gap-3 rounded-xl border bg-background/95 px-4 py-3 shadow-lg backdrop-blur sm:flex-nowrap sm:items-center",
        "motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-top-2 motion-safe:duration-200",
      )}
    >
      {children}
      {dismissLabel ? (
        <Button variant="outline" size="sm" className="shrink-0" onClick={onDismiss}>
          {dismissLabel}
        </Button>
      ) : (
        <Button
          variant="ghost"
          size="icon-sm"
          className="shrink-0"
          aria-label="Dismiss"
          onClick={onDismiss}
        >
          <X />
        </Button>
      )}
    </div>
  );
}
