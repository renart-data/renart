"use client";

import type { AssetCreationKind } from "@/lib/asset-creation-profile";
import { cn } from "@/lib/utils";

import { CodeTypeGlyph } from "./code-type-glyph";

export function AssetTypePreview({
  type,
  className,
}: {
  type: AssetCreationKind;
  className?: string;
}) {
  if (type === "sql" || type === "python") {
    return (
      <div
        aria-hidden="true"
        className={cn(
          "flex h-11 w-full max-w-20 items-center justify-center rounded-lg border bg-primary/5 text-primary",
          className,
        )}
      >
        <CodeTypeGlyph type={type} className="size-6" />
      </div>
    );
  }

  return (
    <svg
      viewBox="0 0 72 42"
      aria-hidden="true"
      className={cn("size-auto h-11 w-full max-w-20 text-primary", className)}
      fill="none"
    >
      <rect x="7" y="5" width="58" height="32" rx="4" className="fill-primary/5 stroke-border" />
      {type === "api" ? <APIAssetGlyph /> : null}
      {type === "seed" ? <SeedAssetGlyph /> : null}
      {type === "sensor" ? <SensorAssetGlyph /> : null}
      {type === "load" ? <LoadAssetGlyph /> : null}
    </svg>
  );
}

function APIAssetGlyph() {
  return (
    <g className="stroke-current" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="25" cy="21" r="9" className="fill-current" fillOpacity=".08" />
      <path d="M16 21h18M25 12c3.5 3.4 3.5 14.6 0 18M25 12c-3.5 3.4-3.5 14.6 0 18" />
      <path d="M39 16h13l-3-3M52 16l-3 3" className="stroke-primary/60" strokeWidth="2.2" />
      <path d="M52 26H39l3-3M39 26l3 3" className="stroke-primary/60" strokeWidth="2.2" />
    </g>
  );
}

function SeedAssetGlyph() {
  return (
    <g className="stroke-current" strokeWidth="1.6" strokeLinejoin="round">
      <path d="M13 10h11l5 5v17H13Z" className="fill-current" fillOpacity=".08" />
      <path d="M24 10v6h5M17 21h8M17 26h8" strokeLinecap="round" />
      <path d="M34 21h8l-2.5-2.5M42 21l-2.5 2.5" className="stroke-primary/60" strokeWidth="2.2" />
      <ellipse cx="51" cy="16" rx="7" ry="2.7" className="fill-current" fillOpacity=".12" />
      <path d="M44 16v10c0 1.6 3.1 2.8 7 2.8s7-1.2 7-2.8V16M44 21c0 1.6 3.1 2.8 7 2.8s7-1.2 7-2.8" />
    </g>
  );
}

function SensorAssetGlyph() {
  return (
    <g className="stroke-current" strokeLinecap="round">
      <circle cx="36" cy="21" r="12" strokeWidth="1.4" opacity=".3" />
      <circle cx="36" cy="21" r="7" strokeWidth="1.4" opacity=".5" />
      <circle cx="36" cy="21" r="2.5" className="fill-current" strokeWidth="1.5" />
      <path d="M36 21 47 13" className="stroke-primary/65" strokeWidth="2.3" />
      <path d="M15 21h8M49 21h8" strokeWidth="1.5" />
    </g>
  );
}

function LoadAssetGlyph() {
  return (
    <g className="stroke-current" strokeWidth="1.6">
      <ellipse cx="20" cy="14" rx="8" ry="3" className="fill-current" fillOpacity=".1" />
      <path d="M12 14v12c0 1.7 3.6 3 8 3s8-1.3 8-3V14M12 20c0 1.7 3.6 3 8 3s8-1.3 8-3" />
      <path d="M31 21h10l-3-3M41 21l-3 3" className="stroke-primary/65" strokeWidth="2.3" />
      <ellipse cx="52" cy="14" rx="8" ry="3" className="fill-current" fillOpacity=".1" />
      <path d="M44 14v12c0 1.7 3.6 3 8 3s8-1.3 8-3V14M44 20c0 1.7 3.6 3 8 3s8-1.3 8-3" />
    </g>
  );
}
