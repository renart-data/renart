"use client";

import { ArrowRight, ChevronRight, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type {
  SemanticImpactLens,
  SemanticImpactLensPreset,
} from "@/lib/semantic-impact-playground";
import { cn } from "@/lib/utils";

export function SemanticImpactPeek({
  lens,
  open,
  onOpenChange,
  onApplyPreset,
}: {
  lens: SemanticImpactLens;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onApplyPreset: (preset: SemanticImpactLensPreset) => void;
}) {
  return (
    <Collapsible open={open} onOpenChange={onOpenChange} className="border-t">
      <CollapsibleTrigger className="flex w-full items-center gap-2 px-5 py-2 text-left text-[0.6875rem] outline-none hover:bg-muted/25 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40 sm:px-6">
        <ChevronRight
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
        <span className="min-w-0 flex-1 truncate">
          {open ? "Why this matters" : "Explain this change"}
        </span>
        <span className="text-muted-foreground">
          {lens.affectedConsumerCount
            ? `${lens.affectedConsumerCount} downstream`
            : "No downstream impact"}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="border-t bg-muted/10 px-5 py-3 sm:px-6">
          <div className="flex items-start gap-3">
            <p className="max-w-2xl flex-1 text-xs leading-5 text-muted-foreground">
              {lens.focusedDetail}
            </p>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Close explanation"
              onClick={() => onOpenChange(false)}
            >
              <X />
            </Button>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-[0.6875rem] text-muted-foreground">Try:</span>
            {lens.presets.map((preset) => (
              <button
                key={preset.id}
                type="button"
                disabled={preset.id === lens.currentPresetId}
                title={preset.description}
                onClick={() => onApplyPreset(preset)}
                className="text-[0.6875rem] font-medium underline decoration-border underline-offset-4 hover:decoration-foreground focus-visible:outline-ring disabled:cursor-default disabled:text-muted-foreground disabled:no-underline"
              >
                {preset.label}
              </button>
            ))}
          </div>
          <details className="group mt-3">
            <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[0.6875rem] text-muted-foreground [&::-webkit-details-marker]:hidden">
              <ChevronRight className="size-3 group-open:rotate-90" />
              Trace & consumers
            </summary>
            <div className="mt-3 border-l pl-3">
              <ol className="space-y-2">
                {lens.stages.slice(0, 3).map((stage) => (
                  <li
                    key={stage.id}
                    className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.6875rem]"
                  >
                    <span className="w-24 text-muted-foreground">{stage.eyebrow}</span>
                    <code className="break-all">{stage.title}</code>
                    <span className="text-muted-foreground">{stage.before}</span>
                    <ArrowRight className="size-3 text-muted-foreground" />
                    <span>{stage.after}</span>
                  </li>
                ))}
              </ol>
              <ul className="mt-3 space-y-2 border-t pt-3">
                {lens.consumers.map((consumer) => (
                  <li key={consumer.name} className="text-[0.6875rem] leading-5">
                    <code className="break-all">{consumer.name}</code>
                    <span className="text-muted-foreground"> · {consumer.effect}</span>
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-[0.625rem] text-muted-foreground">Curated example lineage.</p>
            </div>
          </details>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
