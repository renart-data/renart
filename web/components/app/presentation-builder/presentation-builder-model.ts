import type { Layout, LayoutItem } from "react-grid-layout";
import { verticalCompactor } from "react-grid-layout";

import type { PresentationResolvedColumn } from "@/lib/api-notebooks";
import type {
  PresentationArtifact,
  PresentationFinding,
  PresentationLayoutItem,
} from "@/lib/api-presentations";

export type PresentationBuilderSelection =
  | { kind: "artifact" }
  | { kind: "dataset"; id: string }
  | { kind: "filter"; id: string }
  | { kind: "visualization"; id: string }
  | { kind: "section"; id: string };

export type PresentationPreviewMode = "desktop" | "tablet" | "mobile";

export type PresentationFindingTarget = {
  selection: PresentationBuilderSelection;
  path: string;
};

export function presentationFindingTarget(
  artifact: PresentationArtifact,
  finding: Pick<PresentationFinding, "path">,
): PresentationFindingTarget {
  const path = finding.path?.trim() ?? "";
  const dataset = [...(artifact.datasets ?? [])]
    .sort((left, right) => right.id.length - left.id.length)
    .find(
      (candidate) =>
        path === `datasets.${candidate.id}` || path.startsWith(`datasets.${candidate.id}.`),
    );
  if (dataset) return { selection: { kind: "dataset", id: dataset.id }, path };

  const filterIndex = indexedPath(path, "filters");
  if (filterIndex !== undefined) {
    const filter = artifact.filters?.[filterIndex];
    if (filter) return { selection: { kind: "filter", id: filter.id }, path };
  }

  const visualizationIndex = indexedPath(path, "visualizations");
  if (visualizationIndex !== undefined) {
    const visualization = artifact.visualizations?.[visualizationIndex];
    if (visualization) {
      return { selection: { kind: "visualization", id: visualization.id }, path };
    }
  }

  const sectionIndex = indexedPath(path, "sections");
  if (sectionIndex !== undefined) {
    const section = artifact.sections?.[sectionIndex];
    if (section) return { selection: { kind: "section", id: section.id }, path };
  }

  const layoutIndex = indexedPath(path, "layout");
  if (layoutIndex !== undefined) {
    const visualization = artifact.layout?.[layoutIndex]?.visualization;
    if (visualization) {
      return { selection: { kind: "visualization", id: visualization }, path };
    }
  }

  return { selection: { kind: "artifact" }, path };
}

function indexedPath(path: string, prefix: string): number | undefined {
  const match = new RegExp(`^${prefix}\\[(\\d+)\\]`).exec(path);
  if (!match) return undefined;
  const index = Number(match[1]);
  return Number.isSafeInteger(index) ? index : undefined;
}

export type VisualizationSuggestion = {
  key: string;
  type: string;
  title: string;
  description: string;
  definition: Record<string, unknown>;
};

const VISUALIZATION_TYPES = [
  "table",
  "kpi",
  "bar",
  "line",
  "area",
  "scatter",
  "pie",
  "donut",
] as const;

export function semanticTypeForPhysicalType(
  physicalType: string,
): PresentationResolvedColumn["semantic_type"] {
  const value = physicalType.trim().toLowerCase();
  if (!value) return "unknown";
  const boundary = [value.indexOf("("), value.indexOf("<"), value.indexOf("[")]
    .filter((index) => index >= 0)
    .sort((left, right) => left - right)[0];
  const base = value.slice(0, boundary ?? value.length).trim();
  const is = (...types: string[]) => types.includes(base);
  if (
    is(
      "int",
      "int2",
      "int4",
      "int8",
      "integer",
      "tinyint",
      "smallint",
      "bigint",
      "hugeint",
      "utinyint",
      "usmallint",
      "uinteger",
      "ubigint",
      "decimal",
      "numeric",
      "number",
      "real",
      "float",
      "double",
      "money",
    )
  )
    return "numeric";
  if (is("date", "time", "timetz", "timestamp", "timestamptz", "datetime", "interval"))
    return "temporal";
  if (is("bool", "boolean")) return "boolean";
  if (is("blob", "binary", "varbinary", "bytea")) return "binary";
  if (
    is("json", "jsonb", "struct", "map", "list", "array", "variant", "object") ||
    value.endsWith("[]")
  )
    return "semi_structured";
  if (is("geometry", "geography")) return "geospatial";
  if (is("varchar", "char", "character", "string", "text", "uuid", "enum")) return "categorical";
  return "unknown";
}

export function normalizedDashboardLayout(artifact: PresentationArtifact): Layout {
  const byVisualization = new Map(
    (artifact.layout ?? []).map((item) => [item.visualization, item]),
  );
  const raw: LayoutItem[] = (artifact.visualizations ?? []).map((visualization, index) => {
    const item = byVisualization.get(visualization.id);
    const width = clamp(item?.width || 6, 1, 12);
    return {
      i: visualization.id,
      x: clamp(item?.x ?? 0, 0, 12 - width),
      y: Math.max(0, item?.y ?? index * 4),
      w: width,
      h: clamp(item?.height || 4, 2, 20),
      minW: 2,
      minH: 2,
      maxW: 12,
      maxH: 20,
    };
  });
  return verticalCompactor.compact(raw, 12);
}

export function presentationLayoutFromGrid(layout: Layout): PresentationLayoutItem[] {
  return [...layout]
    .sort((left, right) => left.y - right.y || left.x - right.x || left.i.localeCompare(right.i))
    .map((item) => ({
      visualization: item.i,
      x: clamp(item.x, 0, 11),
      y: Math.max(0, item.y),
      width: clamp(item.w, 1, 12),
      height: clamp(item.h, 1, 20),
    }));
}

export function updateDashboardLayoutItem(
  layout: Layout,
  visualizationID: string,
  patch: Partial<Pick<LayoutItem, "x" | "y" | "w" | "h">>,
): PresentationLayoutItem[] {
  const next = layout.map((item) => {
    if (item.i !== visualizationID) return item;
    const width = clamp(patch.w ?? item.w, 1, 12);
    return {
      ...item,
      x: clamp(patch.x ?? item.x, 0, 12 - width),
      y: Math.max(0, patch.y ?? item.y),
      w: width,
      h: clamp(patch.h ?? item.h, 1, 20),
    };
  });
  return presentationLayoutFromGrid(verticalCompactor.compact(next, 12));
}

export function derivedDashboardSpan(width: number | undefined, mode: PresentationPreviewMode) {
  const authored = clamp(width || 6, 1, 12);
  if (mode === "mobile") return 1;
  if (mode === "tablet") return authored >= 8 ? 2 : 1;
  return authored;
}

export function visualizationSuggestions(
  columns: PresentationResolvedColumn[],
): VisualizationSuggestion[] {
  const numeric = columns.filter((column) => column.semantic_type === "numeric");
  const temporal = columns.filter((column) => column.semantic_type === "temporal");
  const categorical = columns.filter((column) =>
    ["categorical", "boolean"].includes(column.semantic_type),
  );
  const suggestions = new Map<string, VisualizationSuggestion>();

  if (temporal[0] && numeric[0]) {
    suggestions.set(
      "line",
      chartSuggestion(
        "line",
        `${humanizePresentationLabel(numeric[0].name)} over ${humanizePresentationLabel(temporal[0].name)}`,
        "Show how a measure changes over time.",
        temporal[0].name,
        numeric[0].name,
      ),
    );
    suggestions.set(
      "area",
      chartSuggestion(
        "area",
        `${humanizePresentationLabel(numeric[0].name)} over ${humanizePresentationLabel(temporal[0].name)}`,
        "Emphasize how a measure changes over time.",
        temporal[0].name,
        numeric[0].name,
      ),
    );
  }
  if (categorical[0] && numeric[0]) {
    suggestions.set(
      "bar",
      chartSuggestion(
        "bar",
        `${humanizePresentationLabel(numeric[0].name)} by ${humanizePresentationLabel(categorical[0].name)}`,
        "Compare a measure across categories.",
        categorical[0].name,
        numeric[0].name,
      ),
    );
    suggestions.set(
      "pie",
      chartSuggestion(
        "pie",
        `${humanizePresentationLabel(numeric[0].name)} share by ${humanizePresentationLabel(categorical[0].name)}`,
        "Show a categorical distribution.",
        categorical[0].name,
        numeric[0].name,
      ),
    );
    suggestions.set(
      "donut",
      chartSuggestion(
        "donut",
        `${humanizePresentationLabel(numeric[0].name)} share by ${humanizePresentationLabel(categorical[0].name)}`,
        "Show a compact categorical distribution.",
        categorical[0].name,
        numeric[0].name,
      ),
    );
  }
  if (numeric[0]) {
    suggestions.set("kpi", {
      key: `kpi:${numeric[0].name}`,
      type: "kpi",
      title: humanizePresentationLabel(numeric[0].name),
      description: "Highlight one important value.",
      definition: {
        version: 1,
        type: "kpi",
        title: humanizePresentationLabel(numeric[0].name),
        value: { field: numeric[0].name },
        presentation_limit: 1,
      },
    });
  }
  if (numeric[0] && numeric[1]) {
    suggestions.set(
      "scatter",
      chartSuggestion(
        "scatter",
        `${humanizePresentationLabel(numeric[1].name)} vs ${humanizePresentationLabel(numeric[0].name)}`,
        "Explore the relationship between two measures.",
        numeric[0].name,
        numeric[1].name,
      ),
    );
  }
  suggestions.set("table", {
    key: "table",
    type: "table",
    title: "Data table",
    description: "Start with the rows and fields as they are.",
    definition: {
      version: 1,
      type: "table",
      title: "Data table",
      columns: columns.slice(0, 8).map((column) => ({ field: column.name })),
      presentation_limit: 200,
    },
  });
  return VISUALIZATION_TYPES.map(
    (type) => suggestions.get(type) ?? fallbackVisualizationSuggestion(type, columns),
  );
}

export function visualizationSuggestionForType(
  columns: PresentationResolvedColumn[],
  type: string,
) {
  return (
    visualizationSuggestions(columns).find((suggestion) => suggestion.type === type) ??
    fallbackVisualizationSuggestion("table", columns)
  );
}

export function generatedPresentationID(title: string, prefix: string, existing: string[]) {
  const normalized = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  const base = /^[a-z]/.test(normalized) ? normalized : prefix;
  const used = new Set(existing);
  if (!used.has(base)) return base;
  for (let index = 2; ; index += 1) {
    const candidate = `${base}_${index}`;
    if (!used.has(candidate)) return candidate;
  }
}

function chartSuggestion(
  type: string,
  title: string,
  description: string,
  x: string,
  y: string,
): VisualizationSuggestion {
  return {
    key: `${type}:${x}:${y}`,
    type,
    title,
    description,
    definition: {
      version: 1,
      type,
      title,
      encoding: { x: { field: x }, y: [{ field: y }] },
      presentation_limit: 200,
    },
  };
}

function fallbackVisualizationSuggestion(
  type: (typeof VISUALIZATION_TYPES)[number],
  columns: PresentationResolvedColumn[],
): VisualizationSuggestion {
  if (type === "table") {
    return {
      key: "table",
      type,
      title: "Data table",
      description: "Start with the rows and fields as they are.",
      definition: {
        version: 1,
        type,
        title: "Data table",
        columns: columns.slice(0, 8).map((column) => ({ field: column.name })),
        presentation_limit: 200,
      },
    };
  }

  const numeric = columns.filter((column) => column.semantic_type === "numeric");
  const temporal = columns.find((column) => column.semantic_type === "temporal");
  const categorical = columns.find((column) =>
    ["categorical", "boolean"].includes(column.semantic_type),
  );
  const title = humanizePresentationLabel(type);
  if (type === "kpi") {
    const value = numeric[0];
    return {
      key: `kpi:${value?.name ?? "blank"}`,
      type,
      title,
      description: value ? `Highlight ${value.name}.` : "Choose the value in the inspector.",
      definition: {
        version: 1,
        type,
        title,
        ...(value ? { value: { field: value.name } } : {}),
        presentation_limit: 1,
      },
    };
  }

  const x =
    type === "line" || type === "area"
      ? (temporal ?? categorical ?? numeric[0])
      : type === "scatter"
        ? numeric[0]
        : (categorical ?? temporal ?? numeric[0]);
  const y = type === "scatter" ? numeric[1] : numeric[0];
  const encoding = {
    ...(x ? { x: { field: x.name } } : {}),
    ...(y ? { y: [{ field: y.name }] } : {}),
  };
  return {
    key: `${type}:${x?.name ?? "x"}:${y?.name ?? "y"}`,
    type,
    title,
    description:
      x && y ? `Start with ${x.name} and ${y.name}.` : "Choose compatible fields in the inspector.",
    definition: {
      version: 1,
      type,
      title,
      ...(Object.keys(encoding).length > 0 ? { encoding } : {}),
      presentation_limit: 200,
    },
  };
}

export function humanizePresentationLabel(value: string) {
  const result = value.replace(/[_-]+/g, " ").trim();
  return result ? result.charAt(0).toUpperCase() + result.slice(1) : "Visualization";
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(maximum, Math.max(minimum, value));
}
