"use client";

import { useMemo, useState } from "react";

import { useAtomValue } from "jotai";
import { Boxes, Check } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
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
  dependencyKind: "asset" | "uri";
  dependencyValue: string;
  unavailableReason?: string;
};

type CandidateGroup = {
  pipelineId: string;
  heading: string;
  candidates: DependencyCandidate[];
};

function dependencyMatchKey(kind: "asset" | "uri", value: string) {
  return `${kind}:${value.trim().toLowerCase()}`;
}

/**
 * Workspace-aware dependency chooser. Bare asset names are offered only from
 * the owning pipeline; sibling-pipeline assets are represented by their exact
 * committed Bruin URI and remain disabled until a URI has been declared.
 */
export function AssetDependencyPicker({
  assetId,
  present,
  mode,
  onPick,
  className,
}: {
  assetId: string;
  present: Set<string>;
  mode: DependencyMode;
  onPick: (dependency: PickedAssetDependency) => void;
  className?: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const [open, setOpen] = useState(false);

  const groups = useMemo<CandidateGroup[]>(() => {
    const pipelines = workspace?.pipelines ?? [];
    const owner = pipelines.find((pipeline) =>
      pipeline.assets.some((candidate) => candidate.id === assetId),
    );
    if (!owner) return [];

    return pipelines
      .map((pipeline) => {
        const isOwner = pipeline.id === owner.id;
        const candidates = pipeline.assets
          .filter((candidate) => candidate.id !== assetId)
          .map<DependencyCandidate>((candidate) => {
            const uri = candidate.uri?.trim() ?? "";
            if (isOwner) {
              return {
                id: candidate.id,
                name: candidate.name,
                type: candidate.type,
                uri,
                dependencyKind: "asset",
                dependencyValue: candidate.name,
              };
            }
            return {
              id: candidate.id,
              name: candidate.name,
              type: candidate.type,
              uri,
              dependencyKind: "uri",
              dependencyValue: uri,
              unavailableReason: uri ? undefined : "Declare a URI on this producer first",
            };
          })
          .sort((left, right) => left.name.localeCompare(right.name));
        return {
          pipelineId: pipeline.id,
          heading: isOwner ? `${pipeline.name} · current pipeline` : pipeline.name,
          candidates,
        };
      })
      .filter((group) => group.candidates.length > 0)
      .sort((left, right) => {
        if (left.pipelineId === owner.id) return -1;
        if (right.pipelineId === owner.id) return 1;
        return left.heading.localeCompare(right.heading);
      });
  }, [assetId, workspace]);

  const normalizedPresent = useMemo(
    () => new Set(Array.from(present, (value) => value.trim().toLowerCase())),
    [present],
  );

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className={cn("justify-start text-muted-foreground", className)}
        >
          <Boxes className="size-3" />
          Pick from workspace
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <Command>
          <CommandInput placeholder="Search workspace assets…" className="text-xs" />
          <CommandList>
            <CommandEmpty className="py-4 text-xs">No assets found.</CommandEmpty>
            {groups.map((group) => (
              <CommandGroup key={group.pipelineId} heading={group.heading}>
                {group.candidates.map((candidate) => {
                  const added = normalizedPresent.has(
                    dependencyMatchKey(candidate.dependencyKind, candidate.dependencyValue),
                  );
                  const disabled = added || Boolean(candidate.unavailableReason);
                  return (
                    <CommandItem
                      key={`${group.pipelineId}:${candidate.id}`}
                      value={`${group.heading} ${candidate.name} ${candidate.uri} ${candidate.type}`}
                      disabled={disabled}
                      onSelect={() => {
                        if (candidate.dependencyKind === "uri") {
                          onPick({ uri: candidate.dependencyValue, mode });
                        } else {
                          onPick({ asset: candidate.dependencyValue, mode });
                        }
                        setOpen(false);
                      }}
                      className="text-xs"
                    >
                      <Boxes className="mr-1 size-3 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">{candidate.name}</span>
                        <span className="block truncate text-[10px] text-muted-foreground">
                          {candidate.unavailableReason ||
                            (candidate.dependencyKind === "uri" ? candidate.uri : candidate.type)}
                        </span>
                      </span>
                      {added ? <Check className="size-3 shrink-0 text-muted-foreground" /> : null}
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
