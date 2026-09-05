import type { ResourceTarget } from "./generated/api-types";

export type ColumnTarget = ResourceTarget & { kind: "asset-column"; field: "type" };
export type ResourceDetail = { v: 1; environment: string; target: ColumnTarget };
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

export function resolveColumn<T extends { name: string }>(
  columns: T[],
  name: string,
): T | undefined {
  const matches = columns.filter((column) => column.name === name);
  return matches.length === 1 ? matches[0] : undefined;
}
