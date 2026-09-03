"use client";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { writeAuthoringDragItem } from "./authoring-drag";
import { CodeTypeGlyph } from "./code-type-glyph";

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
    <div
      aria-hidden="true"
      className={cn(
        "flex h-11 w-full max-w-20 items-center justify-center text-primary",
        className,
      )}
    >
      <NotebookBlockTypeGlyph type={type} className="size-6" />
    </div>
  );
}

export function NotebookBlockTypeGlyph({
  type,
  className,
}: {
  type: NotebookBlockType;
  className?: string;
}) {
  return <CodeTypeGlyph type={type === "markdown" ? "text" : type} className={className} />;
}
