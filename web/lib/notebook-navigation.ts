import { usesPythonSource } from "@/lib/asset-types";
import type { NotebookParameter } from "@/lib/generated/api-types";
import type { WebAsset, WebNotebook, WebNotebookBlock } from "@/lib/types";

export type NotebookNavigationKind =
  | "sql"
  | "python"
  | "source"
  | "markdown"
  | "control"
  | "visualization";

export type NotebookNavigationEntry = {
  key: string;
  order: number;
  kind: NotebookNavigationKind;
  title: string;
  detail: string;
  searchText: string;
  block: WebNotebookBlock;
  cellId?: string;
};

export type NotebookNavigationMatch = NotebookNavigationEntry & {
  snippet?: string;
};

export type NotebookFlowNode = NotebookNavigationEntry & {
  depth: number;
  upstreamNodeIds: string[];
  downstreamNodeIds: string[];
  externalReferences: string[];
};

export type NotebookFlowModel = {
  nodes: NotebookFlowNode[];
};

export type NotebookFlowContext = {
  ancestors: Set<string>;
  descendants: Set<string>;
};

function controlForBlock(
  block: WebNotebookBlock,
  parameters: NotebookParameter[],
): NotebookParameter | undefined {
  return block.control ? parameters.find((parameter) => parameter.id === block.control) : undefined;
}

function blockKey(block: WebNotebookBlock, index: number): string {
  if (block.cell) return `cell:${block.cell}`;
  if (block.control) return `control:${block.control}`;
  if (block.id) return `block:${block.id}`;
  return `legacy-markdown:${index}`;
}

function blockTitle(
  block: WebNotebookBlock,
  cell: WebAsset | undefined,
  control: NotebookParameter | undefined,
  index: number,
): string {
  if (cell) return cell.name;
  if (control) return control.label?.trim() || control.id;
  if (block.visualization) {
    const title = block.visualization.definition.title;
    return typeof title === "string" && title.trim() ? title : "Visualization";
  }
  const firstLine = block.markdown
    ?.split("\n", 1)[0]
    ?.replace(/^#+\s*/, "")
    .trim();
  return firstLine || `Text ${index + 1}`;
}

function entryKind(block: WebNotebookBlock, cell?: WebAsset): NotebookNavigationKind {
  if (cell) {
    if (cell.notebook_source) return "source";
    return usesPythonSource(cell) ? "python" : "sql";
  }
  if (block.visualization) return "visualization";
  if (block.control) return "control";
  return "markdown";
}

function safeJSON(value: unknown): string {
  try {
    return JSON.stringify(value) ?? "";
  } catch {
    return "";
  }
}

function compact(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function entryDetail(
  kind: NotebookNavigationKind,
  cell: WebAsset | undefined,
  control: NotebookParameter | undefined,
  block: WebNotebookBlock,
): string {
  if (cell?.notebook_source) {
    const source = cell.notebook_source;
    return (
      source.connection ||
      source.uri ||
      source.request?.url ||
      source.format ||
      source.kind ||
      "Imported source"
    );
  }
  if (cell) {
    return cell.connection || cell.explicit_connection || (kind === "python" ? "Python" : "DuckDB");
  }
  if (control) {
    return control.options?.dataset
      ? `${control.type} from ${control.options.dataset}`
      : control.type;
  }
  if (block.visualization) {
    const type = block.visualization.definition.type;
    return `${typeof type === "string" ? type : "chart"} from ${block.visualization.source}`;
  }
  return compact(block.markdown ?? "").slice(0, 160);
}

export function buildNotebookNavigationEntries(notebook: WebNotebook): NotebookNavigationEntry[] {
  const cells = new Map(
    notebook.cells
      .filter((cell): cell is WebAsset & { cell_id: string } => Boolean(cell.cell_id))
      .map((cell) => [cell.cell_id, cell]),
  );
  const parameters = notebook.parameters ?? [];
  const placedControlIds = new Set(
    notebook.blocks.map((block) => block.control).filter((id): id is string => Boolean(id)),
  );
  const blocks: Array<{ block: WebNotebookBlock; blockIndex: number }> = [
    ...parameters
      .filter((parameter) => !placedControlIds.has(parameter.id))
      .map((parameter, index) => ({ block: { control: parameter.id }, blockIndex: -index - 1 })),
    ...notebook.blocks.map((block, blockIndex) => ({ block, blockIndex })),
  ];

  return blocks.map(({ block, blockIndex }, order) => {
    const cell = block.cell ? cells.get(block.cell) : undefined;
    const control = controlForBlock(block, parameters);
    const kind = entryKind(block, cell);
    const title = blockTitle(block, cell, control, order);
    const detail = entryDetail(kind, cell, control, block);
    const searchParts: unknown[] = [title, detail, kind];

    if (cell) {
      searchParts.push(
        cell.content,
        cell.path,
        cell.connection,
        cell.explicit_connection,
        ...(cell.upstreams ?? []),
        ...(cell.external_refs ?? []),
        ...(cell.columns ?? []).flatMap((column) => [column.name, column.type, column.description]),
      );
    } else if (control) {
      searchParts.push(control.id, control.label, control.type, safeJSON(control.options));
    } else if (block.visualization) {
      searchParts.push(block.visualization.source, safeJSON(block.visualization.definition));
    } else {
      searchParts.push(block.markdown);
    }

    return {
      key: blockKey(block, blockIndex >= 0 ? blockIndex : order),
      order,
      kind,
      title,
      detail,
      searchText: searchParts.filter((part) => typeof part === "string").join("\n"),
      block,
      cellId: block.cell,
    };
  });
}

function matchSnippet(searchText: string, query: string): string | undefined {
  const text = compact(searchText);
  if (!text) return undefined;
  const firstTerm = query.toLocaleLowerCase().split(/\s+/).find(Boolean) ?? "";
  const matchIndex = text.toLocaleLowerCase().indexOf(firstTerm);
  const start = Math.max(0, matchIndex >= 0 ? matchIndex - 42 : 0);
  const end = Math.min(text.length, start + 124);
  const prefix = start > 0 ? "…" : "";
  const suffix = end < text.length ? "…" : "";
  return `${prefix}${text.slice(start, end)}${suffix}`;
}

export function filterNotebookNavigationEntries(
  entries: NotebookNavigationEntry[],
  query: string,
): NotebookNavigationMatch[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return entries;
  const terms = normalized.split(/\s+/).filter(Boolean);

  return entries
    .filter((entry) => {
      const haystack = entry.searchText.toLocaleLowerCase();
      return terms.every((term) => haystack.includes(term));
    })
    .map((entry) => ({
      ...entry,
      snippet: matchSnippet(entry.searchText, normalized),
    }));
}

function normalizedReference(reference: string): string {
  return reference.trim().toLocaleLowerCase();
}

function pushUnique(target: string[], values: string[]) {
  for (const value of values) {
    if (value && !target.includes(value)) target.push(value);
  }
}

export function buildNotebookFlowModel(notebook: WebNotebook): NotebookFlowModel {
  const navigationEntries = buildNotebookNavigationEntries(notebook).filter(
    (entry) => entry.kind !== "markdown",
  );
  const cellsById = new Map(
    notebook.cells
      .filter((cell): cell is WebAsset & { cell_id: string } => Boolean(cell.cell_id))
      .map((cell) => [cell.cell_id, cell]),
  );
  const parameters = notebook.parameters ?? [];
  const aliases = new Map<string, string>();

  for (const entry of navigationEntries) {
    if (!entry.cellId) continue;
    const cell = cellsById.get(entry.cellId);
    if (!cell) continue;
    for (const alias of [entry.cellId, cell.id, cell.name, cell.path]) {
      if (alias) aliases.set(normalizedReference(alias), entry.key);
    }
  }

  const resolve = (reference: string | undefined): string | undefined => {
    if (!reference) return undefined;
    return aliases.get(normalizedReference(reference));
  };
  const mutable = new Map<
    string,
    NotebookNavigationEntry & {
      upstreamNodeIds: string[];
      downstreamNodeIds: string[];
      externalReferences: string[];
    }
  >();

  for (const entry of navigationEntries) {
    const upstreamNodeIds: string[] = [];
    const externalReferences: string[] = [];
    if (entry.cellId) {
      const cell = cellsById.get(entry.cellId);
      for (const upstream of cell?.upstreams ?? []) {
        const internal = resolve(upstream);
        if (internal && internal !== entry.key) pushUnique(upstreamNodeIds, [internal]);
        else pushUnique(externalReferences, [upstream]);
      }
      pushUnique(externalReferences, cell?.external_refs ?? []);
    } else if (entry.block.visualization) {
      const source = entry.block.visualization.source;
      const internal = resolve(source);
      if (internal) pushUnique(upstreamNodeIds, [internal]);
      else pushUnique(externalReferences, [source]);
    } else if (entry.block.control) {
      const control = controlForBlock(entry.block, parameters);
      const dataset = control?.options?.dataset;
      const internal = resolve(dataset);
      if (internal) pushUnique(upstreamNodeIds, [internal]);
      else if (dataset) pushUnique(externalReferences, [dataset]);
    }

    mutable.set(entry.key, {
      ...entry,
      upstreamNodeIds,
      downstreamNodeIds: [],
      externalReferences,
    });
  }

  for (const node of mutable.values()) {
    for (const upstreamId of node.upstreamNodeIds) {
      const upstream = mutable.get(upstreamId);
      if (upstream) pushUnique(upstream.downstreamNodeIds, [node.key]);
    }
  }

  const depths = new Map<string, number>();
  const visiting = new Set<string>();
  const depthFor = (nodeId: string): number => {
    const existing = depths.get(nodeId);
    if (existing !== undefined) return existing;
    if (visiting.has(nodeId)) return 0;
    visiting.add(nodeId);
    const node = mutable.get(nodeId);
    const depth = node?.upstreamNodeIds.length
      ? Math.max(...node.upstreamNodeIds.map((upstreamId) => depthFor(upstreamId) + 1))
      : 0;
    visiting.delete(nodeId);
    depths.set(nodeId, depth);
    return depth;
  };

  return {
    nodes: navigationEntries.map((entry) => {
      const node = mutable.get(entry.key)!;
      return { ...node, depth: depthFor(entry.key) };
    }),
  };
}

function collectRelated(
  nodes: Map<string, NotebookFlowNode>,
  start: string,
  direction: "upstreamNodeIds" | "downstreamNodeIds",
): Set<string> {
  const related = new Set<string>();
  const pending = [...(nodes.get(start)?.[direction] ?? [])];
  while (pending.length > 0) {
    const next = pending.shift()!;
    if (next === start) continue;
    if (related.has(next)) continue;
    related.add(next);
    pending.push(...(nodes.get(next)?.[direction] ?? []));
  }
  return related;
}

export function notebookFlowContext(
  model: NotebookFlowModel,
  selectedNodeId: string | null,
): NotebookFlowContext {
  if (!selectedNodeId) return { ancestors: new Set(), descendants: new Set() };
  const nodes = new Map(model.nodes.map((node) => [node.key, node]));
  return {
    ancestors: collectRelated(nodes, selectedNodeId, "upstreamNodeIds"),
    descendants: collectRelated(nodes, selectedNodeId, "downstreamNodeIds"),
  };
}
