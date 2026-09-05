import type { ResourceTarget } from "./generated/api-types";

export type ColumnTarget = ResourceTarget & {
  kind: "asset-column";
  field: "type";
  asset_id: string;
  column: string;
};
export type DataTarget = ResourceTarget & {
  kind: "data-object";
  address: NonNullable<ResourceTarget["address"]>;
  section: "schema" | "rows";
};
export type SectionTarget = ResourceTarget & {
  kind: "asset-section";
  asset_id: string;
  section: "columns" | "checks" | "dependencies" | "materialization" | "identity" | "source";
};
export type ConnectionTarget = ResourceTarget & { kind: "connection"; connection: string };
export type DocumentTarget = ResourceTarget &
  (
    | { kind: "notebook-cell"; notebook_id: string; cell_id: string }
    | { kind: "presentation"; presentation_id: string }
  );
export type NavigableTarget =
  | ColumnTarget
  | DataTarget
  | SectionTarget
  | ConnectionTarget
  | DocumentTarget;
export type ResourceDetail = { v: 1; environment: string; target: NavigableTarget };
export type ResourceSearch = { project?: string; detail?: ResourceDetail };

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function boundedString(value: unknown, max: number): value is string {
  return (
    typeof value === "string" &&
    value.trim().length > 0 &&
    value.length <= max &&
    Array.from(value).every((character) => character.charCodeAt(0) >= 32)
  );
}

// Search is untrusted input, including values decoded by the router's JSON codec.
// Only implemented target handlers are accepted; no arbitrary routes or selectors.
export function parseDetail(value: unknown): ResourceDetail {
  if (
    !record(value) ||
    value.v !== 1 ||
    !boundedString(value.environment, 256) ||
    !record(value.target)
  ) {
    throw new Error("This detail link is invalid or uses an unsupported version.");
  }
  const target = value.target;
  const envelope = { v: 1 as const, environment: value.environment };
  if (
    target.kind === "notebook-cell" &&
    boundedString(target.notebook_id, 4096) &&
    boundedString(target.cell_id, 1024)
  )
    return {
      ...envelope,
      target: { kind: "notebook-cell", notebook_id: target.notebook_id, cell_id: target.cell_id },
    };
  if (
    target.kind === "presentation" &&
    boundedString(target.presentation_id, 4096) &&
    (target.block_id === undefined || boundedString(target.block_id, 1024))
  )
    return {
      ...envelope,
      target: {
        kind: "presentation",
        presentation_id: target.presentation_id,
        ...(target.block_id ? { block_id: target.block_id as string } : {}),
      },
    };
  if (
    target.kind === "data-object" &&
    record(target.address) &&
    ["schema", "rows"].includes(String(target.section))
  ) {
    const a = target.address;
    const column =
      target.column === undefined
        ? undefined
        : boundedString(target.column, 1024)
          ? target.column
          : null;
    if (column === null) throw new Error("Invalid column in data link.");
    const result = (address: DataTarget["address"]): ResourceDetail => ({
      ...envelope,
      target: {
        kind: "data-object",
        address,
        section: target.section as DataTarget["section"],
        ...(column === undefined ? {} : { column }),
      },
    });
    if (
      a.source_kind === "local_files" &&
      boundedString(a.path, 4096) &&
      !a.path.startsWith("/") &&
      !a.path.includes("\\") &&
      a.path
        .split("/")
        .every(
          (p) => p && !p.startsWith(".") && !["node_modules", "dist", "__pycache__"].includes(p),
        ) &&
      [a.connection, a.connection_type, a.database, a.schema, a.name].every(
        (v) => v === undefined || v === "",
      )
    )
      return result({ source_kind: "local_files", path: a.path });
    if (
      a.source_kind === "warehouse" &&
      boundedString(a.connection, 256) &&
      boundedString(a.connection_type, 256) &&
      boundedString(a.name, 1024) &&
      [a.database, a.schema].every((v) => v === undefined || v === "" || boundedString(v, 1024)) &&
      (a.path === undefined || a.path === "")
    )
      return result({
        source_kind: "warehouse",
        connection: a.connection,
        connection_type: a.connection_type,
        name: a.name,
        ...(a.database ? { database: a.database as string } : {}),
        ...(a.schema ? { schema: a.schema as string } : {}),
      });
    throw new Error("Invalid data address.");
  }
  if (
    target.kind === "asset-section" &&
    boundedString(target.asset_id, 4096) &&
    ["columns", "checks", "dependencies", "materialization", "identity", "source"].includes(
      String(target.section),
    )
  ) {
    const anchor =
      target.source_fingerprint === undefined
        ? {}
        : target.section === "source" &&
            typeof target.source_fingerprint === "string" &&
            /^fnv1a64:[a-f0-9]{16}$/.test(target.source_fingerprint) &&
            Number.isInteger(target.line) &&
            Number.isInteger(target.end_line) &&
            (target.line as number) > 0 &&
            (target.end_line as number) >= (target.line as number) &&
            (target.end_line as number) <= 1000000
          ? {
              source_fingerprint: target.source_fingerprint,
              line: target.line as number,
              end_line: target.end_line as number,
            }
          : null;
    if (!anchor) throw new Error("Invalid source anchor.");
    const check =
      target.column === undefined && target.check_name === undefined
        ? {}
        : target.section === "checks" &&
            boundedString(target.column, 1024) &&
            boundedString(target.check_name, 256)
          ? { column: target.column, check_name: target.check_name }
          : null;
    if (!check) throw new Error("Invalid check destination.");
    return {
      ...envelope,
      target: {
        kind: "asset-section",
        asset_id: target.asset_id,
        section: target.section as SectionTarget["section"],
        ...anchor,
        ...check,
      },
    };
  }
  if (
    target.kind === "connection" &&
    boundedString(target.connection, 256) &&
    (target.field === undefined || boundedString(target.field, 256))
  ) {
    return {
      ...envelope,
      target: {
        kind: "connection",
        connection: target.connection,
        ...(target.field === undefined ? {} : { field: target.field as string }),
      },
    };
  }
  if (
    target.kind !== "asset-column" ||
    target.field !== "type" ||
    !boundedString(target.asset_id, 4096) ||
    !boundedString(target.column, 1024)
  ) {
    throw new Error("This detail destination is not supported.");
  }
  return {
    v: 1,
    environment: value.environment,
    target: {
      kind: "asset-column",
      asset_id: target.asset_id,
      column: target.column,
      field: "type",
    },
  };
}

export function resourceLabel(target: NavigableTarget): string {
  switch (target.kind) {
    case "asset-column":
      return "Edit type";
    case "asset-section":
      return target.section === "source" ? "View source" : `Open ${target.section}`;
    case "data-object":
      return "Inspect data";
    case "connection":
      return "Open connection";
    case "notebook-cell":
      return "View saved cell";
    case "presentation":
      return target.block_id ? "View visualization definition" : "View presentation definition";
  }
}

export function normalizeResourceSearch(search: Record<string, unknown>): ResourceSearch {
  if (search.project !== undefined && !boundedString(search.project, 256))
    throw new Error("Invalid project in link.");
  const project = search.project as string | undefined;
  const detail = search.detail === undefined ? undefined : parseDetail(search.detail);
  if (detail && !project) throw new Error("A detail link must identify its project.");
  return { project, detail };
}

export function detailSearch(
  search: Record<string, unknown>,
  project: string,
  detail: ResourceDetail,
) {
  return { ...search, ...normalizeResourceSearch({ project, detail }) };
}

// For surfaces such as Monaco whose links are real hrefs rather than JSX.
export function resourceHref(base: string, project: string, detail: ResourceDetail) {
  const url = new URL(base);
  if (!["http:", "https:"].includes(url.protocol)) throw new Error("Invalid resource link origin.");
  const normalized = normalizeResourceSearch({ project, detail });
  url.searchParams.set("project", normalized.project!);
  url.searchParams.set("detail", JSON.stringify(normalized.detail));
  return url.href;
}

export function resolveColumn<T extends { name: string }>(
  columns: T[],
  name: string,
): T | undefined {
  const matches = columns.filter((column) => column.name === name);
  return matches.length === 1 ? matches[0] : undefined;
}
