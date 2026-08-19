"use client";

import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import {
  AUTHORED_CONTROL_TYPE_LABELS,
  AUTHORED_CONTROL_TYPES,
  type AuthoredControlType,
} from "@/lib/authored-controls";
import { cn } from "@/lib/utils";

import { writeAuthoringDragItem } from "./authoring-drag";

export function ControlTypePicker({
  disabled = false,
  draggable = false,
  onValueChange,
}: {
  disabled?: boolean;
  draggable?: boolean;
  onValueChange: (type: AuthoredControlType) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-2">
      {AUTHORED_CONTROL_TYPES.map((type) => (
        <Button
          key={type}
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled}
          draggable={draggable && !disabled}
          title={
            draggable
              ? `Drag ${AUTHORED_CONTROL_TYPE_LABELS[type]} onto the canvas, or click to add`
              : undefined
          }
          className={cn(
            "h-auto min-h-20 min-w-0 flex-col gap-1.5 px-2 py-2 text-center",
            draggable && !disabled && "cursor-grab active:cursor-grabbing",
          )}
          onDragStart={(event) =>
            writeAuthoringDragItem(event, { kind: "control", controlType: type })
          }
          onClick={() => onValueChange(type)}
        >
          <ControlTypePreview type={type} />
          <span className="truncate text-xs leading-tight">
            {AUTHORED_CONTROL_TYPE_LABELS[type]}
          </span>
        </Button>
      ))}
    </div>
  );
}

export function ControlTypePreview({
  type,
  className,
}: {
  type: AuthoredControlType;
  className?: string;
}) {
  return (
    <svg
      viewBox="0 0 72 42"
      aria-hidden="true"
      className={cn("size-auto h-11 w-full max-w-20 text-primary", className)}
      fill="none"
    >
      {type === "text" ? <TextPreview /> : null}
      {type === "number" ? <NumberPreview /> : null}
      {type === "slider" ? <SliderPreview /> : null}
      {type === "boolean" ? <SwitchPreview /> : null}
      {type === "select" ? <SelectPreview /> : null}
      {type === "multi_select" ? <MultiSelectPreview /> : null}
      {type === "date" ? <DatePreview /> : null}
      {type === "date_range" ? <DateRangePreview /> : null}
    </svg>
  );
}

function InputFrame({ children }: { children?: ReactNode }) {
  return (
    <g>
      <rect x="8" y="9" width="56" height="24" rx="4" className="fill-primary/5 stroke-border" />
      {children}
    </g>
  );
}

function TextPreview() {
  return (
    <InputFrame>
      <path
        d="M17 17H45M17 24H36"
        className="stroke-current"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <path d="M51 15V27" className="stroke-primary/45" strokeWidth="1.5" />
    </InputFrame>
  );
}

function NumberPreview() {
  return (
    <InputFrame>
      <text x="36" y="25" textAnchor="middle" className="fill-current text-[13px] font-semibold">
        42
      </text>
    </InputFrame>
  );
}

function SliderPreview() {
  return (
    <g>
      <path d="M10 21H62" className="stroke-border" strokeWidth="5" strokeLinecap="round" />
      <path d="M10 21H40" className="stroke-current" strokeWidth="5" strokeLinecap="round" />
      <circle cx="40" cy="21" r="6" className="fill-background stroke-current" strokeWidth="2" />
    </g>
  );
}

function SwitchPreview() {
  return (
    <g>
      <rect x="18" y="11" width="36" height="20" rx="10" className="fill-current" />
      <circle cx="44" cy="21" r="7" className="fill-background" />
    </g>
  );
}

function SelectPreview() {
  return (
    <InputFrame>
      <path
        d="M17 18H39M17 24H32"
        className="stroke-current"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <path
        d="M49 19L53 23L57 19"
        className="stroke-current"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </InputFrame>
  );
}

function MultiSelectPreview() {
  return (
    <g className="stroke-current" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
      <rect x="12" y="8" width="10" height="10" rx="2" className="fill-primary/10" />
      <path d="M14.5 13L16.5 15L20 11" />
      <path d="M28 13H59" />
      <rect x="12" y="24" width="10" height="10" rx="2" className="fill-primary/10" />
      <path d="M14.5 29L16.5 31L20 27" />
      <path d="M28 29H51" />
    </g>
  );
}

function DatePreview() {
  return (
    <g className="stroke-current" strokeWidth="1.7">
      <rect x="17" y="7" width="38" height="29" rx="3" className="fill-primary/5" />
      <path d="M17 15H55M25 4V10M47 4V10" strokeLinecap="round" />
      <path
        d="M24 21H29M34 21H39M44 21H49M24 28H29M34 28H39"
        strokeWidth="3"
        strokeLinecap="round"
        opacity=".55"
      />
    </g>
  );
}

function DateRangePreview() {
  return (
    <g className="stroke-current" strokeWidth="1.5">
      <rect x="7" y="10" width="25" height="23" rx="3" className="fill-primary/5" />
      <rect x="40" y="10" width="25" height="23" rx="3" className="fill-primary/5" />
      <path d="M7 17H32M40 17H65M13 7V13M26 7V13M46 7V13M59 7V13" strokeLinecap="round" />
      <path d="M34 22H38" strokeWidth="2" strokeLinecap="round" />
      <path d="M13 23H20M46 23H53" strokeWidth="3" strokeLinecap="round" opacity=".55" />
    </g>
  );
}
