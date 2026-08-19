"use client";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { writeAuthoringDragItem } from "./authoring-drag";

export const CHART_TYPE_OPTIONS = [
  { value: "table", label: "Table" },
  { value: "kpi", label: "KPI" },
  { value: "bar", label: "Bar" },
  { value: "line", label: "Line" },
  { value: "area", label: "Area" },
  { value: "scatter", label: "Scatter" },
  { value: "pie", label: "Pie" },
  { value: "donut", label: "Donut" },
] as const;

export type ChartType = (typeof CHART_TYPE_OPTIONS)[number]["value"];

export function ChartTypePicker({
  value,
  compact = false,
  density = "large",
  disabled = false,
  draggable = false,
  onValueChange,
}: {
  value?: string;
  compact?: boolean;
  density?: "large" | "compact";
  disabled?: boolean;
  draggable?: boolean;
  onValueChange: (value: ChartType) => void;
}) {
  return (
    <div
      className={cn(
        "grid gap-2",
        density === "compact"
          ? "grid-cols-4"
          : compact
            ? "grid-cols-2"
            : "grid-cols-2 sm:grid-cols-4",
      )}
    >
      {CHART_TYPE_OPTIONS.map((option) => {
        const selected = value === option.value;
        return (
          <Button
            key={option.value}
            type="button"
            variant={selected ? "secondary" : "outline"}
            size="sm"
            aria-pressed={selected}
            disabled={disabled}
            draggable={draggable && !disabled}
            title={
              draggable ? `Drag ${option.label} onto the canvas, or click to configure` : undefined
            }
            className={cn(
              "h-auto min-w-0 flex-col",
              density === "compact"
                ? "min-h-11 gap-0.5 px-1.5 py-1 text-[10px]"
                : "min-h-20 gap-1.5 px-2 py-2",
              draggable && !disabled && "cursor-grab active:cursor-grabbing",
              selected && "border-primary/35 bg-primary/10 text-foreground",
            )}
            onDragStart={(event) =>
              writeAuthoringDragItem(event, { kind: "visualization", chartType: option.value })
            }
            onClick={() => onValueChange(option.value)}
          >
            <ChartTypePreview
              type={option.value}
              className={density === "compact" ? "h-6 max-w-10" : undefined}
            />
            <span className="truncate">{option.label}</span>
          </Button>
        );
      })}
    </div>
  );
}

export function ChartTypePreview({ type, className }: { type: ChartType; className?: string }) {
  return (
    <svg
      viewBox="0 0 72 42"
      aria-hidden="true"
      className={cn("size-auto h-11 w-full max-w-20 text-primary", className)}
      fill="none"
    >
      <path d="M8 35.5H66" className="stroke-border" strokeWidth="1.5" />
      {type === "table" ? <TablePreview /> : null}
      {type === "kpi" ? <KPIPreview /> : null}
      {type === "bar" ? <BarPreview /> : null}
      {type === "line" ? <LinePreview /> : null}
      {type === "area" ? <AreaPreview /> : null}
      {type === "scatter" ? <ScatterPreview /> : null}
      {type === "pie" ? <PiePreview /> : null}
      {type === "donut" ? <DonutPreview /> : null}
    </svg>
  );
}

function TablePreview() {
  return (
    <g className="stroke-current" strokeWidth="1.5">
      <rect x="9" y="6" width="54" height="26" rx="2" className="fill-primary/5" />
      <path d="M9 13H63M9 20H63M9 27H63M27 6V32M45 6V32" />
      <path d="M9 13H63" strokeWidth="3" className="stroke-primary/45" />
    </g>
  );
}

function KPIPreview() {
  return (
    <g>
      <text x="36" y="24" textAnchor="middle" className="fill-current text-[18px] font-semibold">
        42
      </text>
      <path d="M24 29H48" className="stroke-primary/40" strokeWidth="2" strokeLinecap="round" />
    </g>
  );
}

function BarPreview() {
  return (
    <g className="fill-current">
      <rect x="13" y="22" width="8" height="13" rx="1" opacity=".4" />
      <rect x="27" y="13" width="8" height="22" rx="1" opacity=".65" />
      <rect x="41" y="7" width="8" height="28" rx="1" />
      <rect x="55" y="18" width="8" height="17" rx="1" opacity=".55" />
    </g>
  );
}

function LinePreview() {
  return (
    <g className="stroke-current" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9 29L21 23L32 26L44 13L55 17L64 8" />
      {["9,29", "21,23", "32,26", "44,13", "55,17", "64,8"].map((point) => {
        const [cx, cy] = point.split(",");
        return <circle key={point} cx={cx} cy={cy} r="2" className="fill-background" />;
      })}
    </g>
  );
}

function AreaPreview() {
  return (
    <g>
      <path d="M9 30L20 25L31 27L44 13L55 16L64 8V35H9Z" className="fill-current" opacity=".18" />
      <path
        d="M9 30L20 25L31 27L44 13L55 16L64 8"
        className="stroke-current"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </g>
  );
}

function ScatterPreview() {
  return (
    <g className="fill-current">
      <circle cx="15" cy="28" r="2.4" opacity=".45" />
      <circle cx="23" cy="22" r="2.8" opacity=".6" />
      <circle cx="31" cy="26" r="2" opacity=".5" />
      <circle cx="39" cy="15" r="3" opacity=".75" />
      <circle cx="48" cy="19" r="2.3" opacity=".65" />
      <circle cx="57" cy="10" r="3.2" />
      <circle cx="62" cy="24" r="2" opacity=".4" />
    </g>
  );
}

function PiePreview() {
  return (
    <g transform="translate(36 20)">
      <circle r="14" className="fill-primary/15 stroke-current" strokeWidth="1.5" />
      <path d="M0 0V-14A14 14 0 0 1 12.1 7Z" className="fill-current" />
      <path d="M0 0L12.1 7A14 14 0 0 1-10 9.8Z" className="fill-primary/55" />
    </g>
  );
}

function DonutPreview() {
  return (
    <g transform="translate(36 20) rotate(-90)">
      <circle r="11" className="stroke-primary/15" strokeWidth="7" />
      <circle
        r="11"
        className="stroke-current"
        strokeWidth="7"
        strokeDasharray="31 69"
        strokeLinecap="butt"
      />
      <circle
        r="11"
        className="stroke-primary/55"
        strokeWidth="7"
        strokeDasharray="22 78"
        strokeDashoffset="-31"
      />
    </g>
  );
}
