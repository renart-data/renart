"use client";

import { Lightbulb, Loader2, Plus } from "lucide-react";
import { useEffect, useState } from "react";

import { searchPythonPackages, type PythonPackage } from "@/lib/api-python-packages";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * Helpful suggestion listing Python imports that are not yet declared as
 * dependencies, each offering a one-click "add to deps" via a PyPI lookup.
 * Shared between the notebook cell editor and the pipeline asset editor so both
 * use the exact same missing-dependency hint and installation affordance.
 */
export function MissingPythonDepsBanner({
  missingImports,
  onAddDependency,
}: {
  missingImports: string[];
  onAddDependency: (pkg: string) => void;
}) {
  if (missingImports.length === 0) {
    return null;
  }
  return (
    <Alert role="status" data-testid="python-dependency-suggestion">
      <Lightbulb />
      <AlertTitle>Dependency suggestion</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center gap-1.5">
        <span>Add imported packages so this code runs reliably elsewhere:</span>
        {missingImports.map((importName) => (
          <MissingImportSuggestion
            key={importName}
            importName={importName}
            onAdd={onAddDependency}
          />
        ))}
      </AlertDescription>
    </Alert>
  );
}

function MissingImportSuggestion({
  importName,
  onAdd,
}: {
  importName: string;
  onAdd: (pkg: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [packages, setPackages] = useState<PythonPackage[] | null>(null);

  useEffect(() => {
    if (!open || packages !== null) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    searchPythonPackages(importName)
      .then((results) => {
        if (!cancelled) {
          setPackages(results);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setPackages([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open, importName, packages]);

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button size="xs" variant="outline" title={`Find a PyPI package for "${importName}"`}>
          <Plus data-icon="inline-start" />
          <span className="font-mono">{importName}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        {loading ? (
          <div className="flex items-center gap-2 px-2 py-2 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" />
            Searching PyPI…
          </div>
        ) : packages && packages.length > 0 ? (
          <>
            <div className="px-2 py-1 text-[11px] text-muted-foreground">
              PyPI packages for “{importName}”
            </div>
            {packages.map((pkg) => (
              <DropdownMenuItem
                key={pkg.name}
                className="flex flex-col items-start gap-0.5"
                onSelect={() => onAdd(pkg.name)}
              >
                <span className="font-mono text-xs font-medium">{pkg.name}</span>
                {pkg.summary ? (
                  <span className="line-clamp-2 text-[11px] text-muted-foreground">
                    {pkg.summary}
                  </span>
                ) : null}
              </DropdownMenuItem>
            ))}
          </>
        ) : (
          <DropdownMenuItem onSelect={() => onAdd(importName)}>
            <Plus className="size-3.5" />
            Add <span className="font-mono">{importName}</span> anyway
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
