"use client";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { writeAuthoringDragItem } from "./authoring-drag";

export const NOTEBOOK_BLOCK_TYPE_OPTIONS = [
  { value: "sql", label: "SQL" },
  { value: "python", label: "Python" },
  { value: "markdown", label: "Text" },
] as const;

export type NotebookBlockType = (typeof NOTEBOOK_BLOCK_TYPE_OPTIONS)[number]["value"];

export function NotebookBlockTypePicker({
  disabled = false,
  draggable = false,
  onValueChange,
}: {
  disabled?: boolean;
  draggable?: boolean;
  onValueChange: (type: NotebookBlockType) => void;
}) {
  return (
    <div className="grid grid-cols-3 gap-2">
      {NOTEBOOK_BLOCK_TYPE_OPTIONS.map((option) => (
        <Button
          key={option.value}
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled}
          draggable={draggable && !disabled}
          title={
            draggable ? `Drag ${option.label} between notebook blocks, or click to add` : undefined
          }
          className={cn(
            "h-auto min-h-20 min-w-0 flex-col gap-1.5 px-1.5 py-2 text-center",
            draggable && !disabled && "cursor-grab active:cursor-grabbing",
          )}
          onDragStart={(event) =>
            writeAuthoringDragItem(event, {
              kind: "notebook-block",
              blockType: option.value,
            })
          }
          onClick={() => onValueChange(option.value)}
        >
          <NotebookBlockTypePreview type={option.value} />
          <span className="truncate text-xs leading-tight">{option.label}</span>
        </Button>
      ))}
    </div>
  );
}

export function NotebookBlockTypePreview({
  type,
  className,
}: {
  type: NotebookBlockType;
  className?: string;
}) {
  return (
    <svg
      viewBox="0 0 72 42"
      aria-hidden="true"
      className={cn("size-auto h-11 w-full max-w-20 text-primary", className)}
      fill="none"
    >
      <rect x="7" y="5" width="58" height="32" rx="4" className="fill-primary/5 stroke-border" />
      {type === "sql" ? <SQLPreview /> : null}
      {type === "python" ? <PythonPreview /> : null}
      {type === "markdown" ? <TextPreview /> : null}
    </svg>
  );
}

function SQLPreview() {
  return (
    <g strokeLinecap="round">
      <path d="M14 14H28" className="stroke-current" strokeWidth="3" />
      <path d="M33 14H53M14 21H22" className="stroke-primary/45" strokeWidth="2" />
      <path d="M26 21H58M20 28H49" className="stroke-current" strokeWidth="2" />
    </g>
  );
}

function PythonPreview() {
  return (
    <g strokeLinecap="round">
      <path d="M14 14H25" className="stroke-current" strokeWidth="3" />
      <path d="M30 14H54M19 21H42" className="stroke-primary/45" strokeWidth="2" />
      <path d="M24 28H57" className="stroke-current" strokeWidth="2" />
      <circle cx="16" cy="28" r="2" className="fill-current" />
    </g>
  );
}

function TextPreview() {
  return (
    <g strokeLinecap="round">
      <path d="M14 15H39" className="stroke-current" strokeWidth="4" />
      <path d="M14 23H57M14 29H48" className="stroke-primary/45" strokeWidth="2" />
    </g>
  );
}
