"use client";

import { useMemo, useState } from "react";

import { useAtomValue } from "jotai";
import { Boxes, Plus, TriangleAlert } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox";
import { workspaceAtom } from "@/lib/atoms/workspace";
import type { DependencyMode } from "@/lib/asset-provenance";
import { cn } from "@/lib/utils";

export type PickedAssetDependency = {
  asset?: string;
  uri?: string;
  mode: DependencyMode;
};

type DependencyCandidate = {
  id: string;
  name: string;
  type: string;
  uri: string;
  pipelineName: string;
  currentPipeline: boolean;
  dependencyKind: "asset" | "uri";
  dependencyValue: string;
  missingURI?: boolean;
  custom?: boolean;
};

function dependencyMatchKey(kind: "asset" | "uri", value: string) {
  return `${kind}:${value.trim().toLowerCase()}`;
}

function looksLikeURI(value: string) {
  return /^[a-z][a-z0-9+.-]*:\/\//i.test(value.trim());
}

/**
 * A workspace-aware creatable combobox. Same-pipeline selections use the
 * asset name; sibling-pipeline selections use the producer URI whenever one
 * is declared. Free text remains available for dependencies not in the graph.
 */
export function AssetDependencyPicker({
  assetId,
  present,
  onPick,
  className,
}: {
  assetId: string;
  present: Set<string>;
  onPick: (dependency: PickedAssetDependency) => void;
  className?: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const [inputValue, setInputValue] = useState("");
  const [warning, setWarning] = useState<string | null>(null);

  const candidates = useMemo<DependencyCandidate[]>(() => {
    const pipelines = workspace?.pipelines ?? [];
    const owner = pipelines.find((pipeline) =>
      pipeline.assets.some((candidate) => candidate.id === assetId),
    );
    if (!owner) return [];

    return pipelines
      .flatMap((pipeline) => {
        const currentPipeline = pipeline.id === owner.id;
        return pipeline.assets
          .filter((candidate) => candidate.id !== assetId)
          .map<DependencyCandidate>((candidate) => {
            const uri = candidate.uri?.trim() ?? "";
            return {
              id: `${pipeline.id}:${candidate.id}`,
              name: candidate.name,
              type: candidate.type,
              uri,
              pipelineName: pipeline.name,
              currentPipeline,
              dependencyKind: currentPipeline || !uri ? "asset" : "uri",
              dependencyValue: currentPipeline || !uri ? candidate.name : uri,
              missingURI: !currentPipeline && !uri,
            };
          });
      })
      .sort((left, right) => {
        if (left.currentPipeline !== right.currentPipeline) return left.currentPipeline ? -1 : 1;
        const pipelineOrder = left.pipelineName.localeCompare(right.pipelineName);
        return pipelineOrder || left.name.localeCompare(right.name);
      });
  }, [assetId, workspace]);

  const normalizedPresent = useMemo(
    () => new Set(Array.from(present, (value) => value.trim().toLowerCase())),
    [present],
  );

  const items = useMemo(() => {
    const trimmed = inputValue.trim();
    if (!trimmed) return candidates;
    const exact = candidates.some(
      (candidate) =>
        candidate.name.toLowerCase() === trimmed.toLowerCase() ||
        candidate.uri.toLowerCase() === trimmed.toLowerCase() ||
        candidate.dependencyValue.toLowerCase() === trimmed.toLowerCase(),
    );
    if (exact) return candidates;
    const dependencyKind = looksLikeURI(trimmed) ? "uri" : "asset";
    return [
      ...candidates,
      {
        id: `custom:${dependencyKind}:${trimmed}`,
        name: trimmed,
        type: "Custom dependency",
        uri: dependencyKind === "uri" ? trimmed : "",
        pipelineName: "Not in this workspace",
        currentPipeline: false,
        dependencyKind,
        dependencyValue: trimmed,
        custom: true,
      } satisfies DependencyCandidate,
    ];
  }, [candidates, inputValue]);

  const selectCandidate = (candidate: DependencyCandidate | null) => {
    if (!candidate) return;
    const matchKey = dependencyMatchKey(candidate.dependencyKind, candidate.dependencyValue);
    if (normalizedPresent.has(matchKey)) return;

    onPick(
      candidate.dependencyKind === "uri"
        ? { uri: candidate.dependencyValue, mode: "full" }
        : { asset: candidate.dependencyValue, mode: "full" },
    );
    setWarning(
      candidate.missingURI
        ? `${candidate.name} is in ${candidate.pipelineName}, but it has no producer URI. The dependency was added as a name and will not link across pipelines until that URI is declared.`
        : null,
    );
    setInputValue("");
  };

  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)}>
      <Combobox
        autoHighlight
        items={items}
        inputValue={inputValue}
        value={null}
        itemToStringLabel={(candidate: DependencyCandidate) => candidate.name}
        itemToStringValue={(candidate: DependencyCandidate) => candidate.dependencyValue}
        isItemEqualToValue={(left, right) => left.id === right.id}
        onInputValueChange={(value, details) => {
          if (details.reason !== "item-press") setInputValue(value);
        }}
        onValueChange={selectCandidate}
      >
        <ComboboxInput
          aria-label="Add dependency"
          placeholder="Add dependency by asset name or URI…"
          className="h-8 w-full text-xs"
          showClear={Boolean(inputValue)}
        />
        <ComboboxContent>
          <ComboboxEmpty>Type an asset name or producer URI.</ComboboxEmpty>
          <ComboboxList>
            {(candidate: DependencyCandidate) => {
              const added = normalizedPresent.has(
                dependencyMatchKey(candidate.dependencyKind, candidate.dependencyValue),
              );
              return (
                <ComboboxItem key={candidate.id} value={candidate} disabled={added}>
                  {candidate.custom ? (
                    <Plus className="size-3.5 text-muted-foreground" />
                  ) : (
                    <Boxes className="size-3.5 text-muted-foreground" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">
                      {candidate.custom ? `Add “${candidate.name}”` : candidate.name}
                    </span>
                    <span className="block truncate text-[10px] text-muted-foreground">
                      {added
                        ? "Already added"
                        : candidate.missingURI
                          ? `${candidate.pipelineName} · missing producer URI`
                          : candidate.currentPipeline
                            ? `${candidate.pipelineName} · ${candidate.type}`
                            : candidate.custom
                              ? candidate.pipelineName
                              : `${candidate.pipelineName} · ${candidate.uri}`}
                    </span>
                  </span>
                  {candidate.missingURI ? (
                    <TriangleAlert className="size-3.5 text-amber-600" />
                  ) : null}
                </ComboboxItem>
              );
            }}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
      {warning ? (
        <Alert className="py-2">
          <TriangleAlert />
          <AlertTitle>Producer URI missing</AlertTitle>
          <AlertDescription>{warning}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
