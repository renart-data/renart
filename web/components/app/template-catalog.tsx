import { Check, Globe2, HardDrive } from "lucide-react";
import { useMemo } from "react";

import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export type TemplateCatalogItem = {
  id: string;
  title: string;
  description: string;
  category: string;
  offline: boolean;
  features: string[];
  assetNames: string[];
};

export function TemplateCatalog({
  items,
  selectedId,
  onSelect,
  ariaLabel,
  loading = false,
}: {
  items: TemplateCatalogItem[];
  selectedId: string;
  onSelect: (item: TemplateCatalogItem) => void;
  ariaLabel: string;
  loading?: boolean;
}) {
  const categories = useMemo(() => {
    const grouped = new Map<string, TemplateCatalogItem[]>();
    for (const item of items) {
      const category = item.category.trim() || "Other";
      grouped.set(category, [...(grouped.get(category) ?? []), item]);
    }
    return [...grouped.entries()];
  }, [items]);
  const selected = items.find((item) => item.id === selectedId) ?? items[0];

  if (loading && items.length === 0) {
    return <TemplateCatalogSkeleton />;
  }

  return (
    <div className="overflow-hidden rounded-lg border bg-background">
      <div className="grid min-w-0 sm:grid-cols-[minmax(12rem,0.85fr)_minmax(0,1.15fr)]">
        <ScrollArea className="h-56 border-b sm:h-[18rem] sm:border-r sm:border-b-0">
          <div
            className="grid gap-3 p-2"
            role="radiogroup"
            aria-label={ariaLabel}
            data-testid="template-catalog-options"
          >
            {categories.map(([category, categoryItems]) => (
              <div key={category} className="grid gap-1">
                <div className="px-2 pt-1 text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase">
                  {category}
                </div>
                {categoryItems.map((item) => {
                  const isSelected = item.id === selected?.id;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      role="radio"
                      aria-checked={isSelected}
                      onClick={() => onSelect(item)}
                      className={cn(
                        "flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring",
                        isSelected && "bg-muted font-medium text-foreground",
                      )}
                    >
                      <span
                        className={cn(
                          "flex size-4 shrink-0 items-center justify-center rounded-full border",
                          isSelected
                            ? "border-primary bg-primary text-primary-foreground"
                            : "border-muted-foreground/40 text-transparent",
                        )}
                        aria-hidden
                      >
                        <Check className="size-2.5" />
                      </span>
                      <span className="truncate">{item.title}</span>
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </ScrollArea>

        <div className="flex min-h-56 min-w-0 flex-col p-4 sm:h-[18rem]">
          {selected ? (
            <>
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h3 className="min-w-0 text-sm font-semibold">{selected.title}</h3>
                {selected.offline ? (
                  <Badge variant="secondary">
                    <HardDrive data-icon="inline-start" />
                    Local data
                  </Badge>
                ) : (
                  <Badge variant="outline">
                    <Globe2 data-icon="inline-start" />
                    Uses the internet
                  </Badge>
                )}
              </div>
              <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                {selected.description}
              </p>
              {selected.features.length > 0 ? (
                <div className="mt-3 flex flex-wrap gap-1" aria-label="Template features">
                  {selected.features.map((feature) => (
                    <Badge key={feature} variant="muted" size="xs">
                      {feature}
                    </Badge>
                  ))}
                </div>
              ) : null}
              <Separator className="my-3" />
              <div className="min-h-0">
                <p className="mb-1.5 text-[0.6875rem] font-medium text-muted-foreground">
                  {selected.assetNames.length === 0
                    ? "Creates an empty pipeline"
                    : `Creates ${selected.assetNames.length} ${
                        selected.assetNames.length === 1 ? "asset" : "assets"
                      }`}
                </p>
                {selected.assetNames.length > 0 ? (
                  <ScrollArea className="max-h-20">
                    <div className="grid gap-1 pr-3">
                      {selected.assetNames.map((assetName) => (
                        <code
                          key={assetName}
                          className="truncate rounded bg-muted px-2 py-1 text-[0.6875rem] text-muted-foreground"
                          title={assetName}
                        >
                          {assetName}
                        </code>
                      ))}
                    </div>
                  </ScrollArea>
                ) : null}
              </div>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">No templates are available.</p>
          )}
        </div>
      </div>
    </div>
  );
}

function TemplateCatalogSkeleton() {
  return (
    <div
      className="overflow-hidden rounded-lg border bg-background"
      aria-label="Loading templates"
      aria-busy="true"
    >
      <div className="grid min-w-0 sm:grid-cols-[minmax(12rem,0.85fr)_minmax(0,1.15fr)]">
        <div className="grid h-56 content-start gap-2 border-b p-3 sm:h-[18rem] sm:border-r sm:border-b-0">
          <Skeleton className="h-3 w-20" />
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-8 w-full" />
          ))}
        </div>
        <div className="grid min-h-56 content-start gap-3 p-4 sm:h-[18rem]">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-12 w-full" />
          <div className="flex gap-1">
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-4 w-24" />
          </div>
          <Separator />
          <Skeleton className="h-16 w-full" />
        </div>
      </div>
    </div>
  );
}
