import { WebAsset } from "@/lib/types";

/**
 * Client-side reader for the renart asset provenance stored as flat `renart_*`
 * keys in an asset's meta map (mirrors internal/web/service/assetmeta). It lets
 * the Guided cards classify dependencies and columns as inferred, manual, or
 * ignored without re-deriving anything server-side.
 */

export const RENART_META = {
  depAdd: "renart_dep_add",
  depDrop: "renart_dep_drop",
  colAdd: "renart_col_add",
  colDrop: "renart_col_drop",
  colOwn: "renart_col_own",
  colSource: "renart_col_src",
  columnManual: "renart_manual",
  columnOwned: "renart_owned",
  columnSource: "renart_source",
} as const;

export type DependencyMode = "full" | "symbolic";

export type ParsedDependencyKey = {
  key: string;
  kind: "asset" | "uri";
  value: string;
  mode: DependencyMode;
};

export type AssetProvenance = {
  depAdd: ParsedDependencyKey[];
  depDrop: ParsedDependencyKey[];
  colAdd: Set<string>;
  colDrop: Set<string>;
  colOwn: Map<string, Set<string>>;
  colSource: Map<string, string>;
};

function splitList(raw?: string): string[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

/** Parse a dependency key (a:<asset>#<mode> / u:<uri>#<mode>). */
export function parseDependencyKey(key: string): ParsedDependencyKey {
  const trimmed = key.trim();
  let kind: "asset" | "uri" = "asset";
  let rest = trimmed;
  if (trimmed.startsWith("u:")) {
    kind = "uri";
    rest = trimmed.slice(2);
  } else if (trimmed.startsWith("a:")) {
    rest = trimmed.slice(2);
  }
  let value = rest;
  let mode: DependencyMode = "full";
  const hash = rest.lastIndexOf("#");
  if (hash >= 0) {
    value = rest.slice(0, hash);
    mode = rest.slice(hash + 1) === "symbolic" ? "symbolic" : "full";
  }
  return { key: trimmed, kind, value: value.trim(), mode };
}

function parseOwn(raw?: string): Map<string, Set<string>> {
  const out = new Map<string, Set<string>>();
  if (!raw) return out;
  for (const entry of raw.split(";")) {
    const [col, fieldsRaw] = entry.split(":");
    if (!col || !fieldsRaw) continue;
    const fields = fieldsRaw
      .split("|")
      .map((f) => f.trim())
      .filter(Boolean);
    if (fields.length) out.set(col.trim().toLowerCase(), new Set(fields));
  }
  return out;
}

function parseMap(raw?: string): Map<string, string> {
  const out = new Map<string, string>();
  if (!raw) return out;
  for (const entry of raw.split(";")) {
    const separator = entry.lastIndexOf(":");
    if (separator <= 0 || separator === entry.length - 1) continue;
    const key = entry.slice(0, separator).trim().toLowerCase();
    const value = entry.slice(separator + 1).trim();
    if (key && value) out.set(key, value);
  }
  return out;
}

export function parseAssetProvenance(
  meta?: Record<string, string>,
  columns?: WebAsset["columns"],
): AssetProvenance {
  const provenance = {
    depAdd: splitList(meta?.[RENART_META.depAdd]).map(parseDependencyKey),
    depDrop: splitList(meta?.[RENART_META.depDrop]).map(parseDependencyKey),
    colAdd: new Set(splitList(meta?.[RENART_META.colAdd]).map((n) => n.toLowerCase())),
    colDrop: new Set(splitList(meta?.[RENART_META.colDrop]).map((n) => n.toLowerCase())),
    colOwn: parseOwn(meta?.[RENART_META.colOwn]),
    colSource: parseMap(meta?.[RENART_META.colSource]),
  };
  for (const column of columns ?? []) {
    const lower = column.name.toLowerCase();
    if (column.meta?.[RENART_META.columnManual]?.trim().toLowerCase() === "true") {
      provenance.colAdd.add(lower);
    }
    const owned = column.meta?.[RENART_META.columnOwned]?.trim();
    if (owned) {
      provenance.colOwn.set(
        lower,
        new Set(
          owned
            .split("|")
            .map((field) => field.trim())
            .filter(Boolean),
        ),
      );
    }
    const source = column.meta?.[RENART_META.columnSource]?.trim();
    if (source) provenance.colSource.set(lower, source);
  }
  return provenance;
}

export type DependencyRow = {
  name: string;
  key: string;
  mode: DependencyMode;
  source: "inferred" | "manual";
};

export type DependencyClassification = {
  inferred: DependencyRow[];
  manual: DependencyRow[];
  ignored: ParsedDependencyKey[];
};

/**
 * Classify an asset's upstreams into inferred, manual, and ignored, using the
 * provenance. The workspace payload lists upstream names without modes, so the
 * mode for manual deps is recovered from the dep_add key.
 */
export function classifyDependencies(asset: WebAsset): DependencyClassification {
  const provenance = parseAssetProvenance(asset.meta);
  const manualByName = new Map(provenance.depAdd.map((d) => [d.value.toLowerCase(), d]));

  const inferred: DependencyRow[] = [];
  const manual: DependencyRow[] = [];
  for (const name of asset.upstreams ?? []) {
    const lower = name.toLowerCase();
    const manualKey = manualByName.get(lower);
    if (manualKey) {
      manual.push({ name, key: manualKey.key, mode: manualKey.mode, source: "manual" });
    } else {
      inferred.push({ name, key: `a:${name}#full`, mode: "full", source: "inferred" });
    }
  }

  const present = new Set((asset.upstreams ?? []).map((n) => n.toLowerCase()));
  const ignored = provenance.depDrop.filter((d) => !present.has(d.value.toLowerCase()));

  return { inferred, manual, ignored };
}

export type ColumnStatus =
  | "inferred"
  | "manual"
  | "type-owned"
  | "table-inferred"
  | "live-inferred";

/** Best-effort status for a column row, for the Columns card markers. */
export function columnStatus(columnName: string, provenance: AssetProvenance): ColumnStatus {
  const lower = columnName.toLowerCase();
  if (provenance.colAdd.has(lower)) return "manual";
  if (provenance.colOwn.get(lower)?.has("type")) return "type-owned";
  if (provenance.colSource.get(lower) === "m") return "table-inferred";
  if (provenance.colSource.get(lower) === "l") return "live-inferred";
  return "inferred";
}
