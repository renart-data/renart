import type { DragEvent as ReactDragEvent } from "react";

import type { ChartType } from "./chart-type-picker";
import type { AuthoredControlType } from "@/lib/authored-controls";

export const RENART_AUTHORING_DRAG_TYPE = "application/x-renart-authoring-item";

export type RenartAuthoringDragItem =
  | { kind: "visualization"; chartType: ChartType }
  | { kind: "control"; controlType: AuthoredControlType }
  | { kind: "notebook-block"; blockType: "sql" | "python" | "markdown" };

export function writeAuthoringDragItem(
  event: ReactDragEvent<HTMLElement>,
  item: RenartAuthoringDragItem,
) {
  event.dataTransfer.effectAllowed = "copy";
  event.dataTransfer.setData(RENART_AUTHORING_DRAG_TYPE, JSON.stringify(item));
  const label =
    item.kind === "visualization"
      ? `${item.chartType} visualization`
      : item.kind === "control"
        ? `${item.controlType} control`
        : `${item.blockType} notebook block`;
  event.dataTransfer.setData("text/plain", `Renart ${label}`);
}

export function readAuthoringDragItem(
  event: Pick<DragEvent, "dataTransfer"> | Pick<ReactDragEvent<HTMLElement>, "dataTransfer">,
): RenartAuthoringDragItem | null {
  const raw = event.dataTransfer?.getData(RENART_AUTHORING_DRAG_TYPE);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (parsed.kind === "visualization" && isChartType(parsed.chartType)) {
      return { kind: "visualization", chartType: parsed.chartType };
    }
    if (parsed.kind === "control" && isControlType(parsed.controlType)) {
      return { kind: "control", controlType: parsed.controlType };
    }
    if (parsed.kind === "notebook-block" && isNotebookBlockType(parsed.blockType)) {
      return { kind: "notebook-block", blockType: parsed.blockType };
    }
    return null;
  } catch {
    return null;
  }
}

export function hasAuthoringDragItem(
  event: Pick<DragEvent, "dataTransfer"> | Pick<ReactDragEvent<HTMLElement>, "dataTransfer">,
) {
  return Array.from(event.dataTransfer?.types ?? []).includes(RENART_AUTHORING_DRAG_TYPE);
}

function isChartType(value: unknown): value is ChartType {
  return ["table", "kpi", "bar", "line", "area", "scatter", "pie", "donut"].includes(String(value));
}

function isControlType(value: unknown): value is AuthoredControlType {
  return [
    "text",
    "number",
    "slider",
    "boolean",
    "select",
    "multi_select",
    "date",
    "date_range",
  ].includes(String(value));
}

function isNotebookBlockType(value: unknown): value is "sql" | "python" | "markdown" {
  return ["sql", "python", "markdown"].includes(String(value));
}
