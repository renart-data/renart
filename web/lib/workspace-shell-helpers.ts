import type { AssetCreationKind } from "@/lib/asset-creation-profile";

export function buildSuggestedAssetName(
  kind: AssetCreationKind,
  existingNames: Set<string>,
  pipelineName?: string | null,
  preferredPrefix?: string | null,
): string {
  const base = `${suggestedAssetPrefix(existingNames, pipelineName, preferredPrefix)}.my_${kind}_asset_`;
  let index = 1;
  while (existingNames.has(`${base}${index}`)) {
    index += 1;
  }

  return `${base}${index}`;
}

// The prefix a new asset joins: the one the user pointed at, else the
// pipeline's most common prefix so the asset lands in an existing group. Only
// a pipeline without prefixed assets falls back to its own name.
export function suggestedAssetPrefix(
  existingNames: Iterable<string>,
  pipelineName?: string | null,
  preferredPrefix?: string | null,
): string {
  const preferred = preferredPrefix?.trim();
  if (preferred) {
    return preferred;
  }
  const counts = new Map<string, number>();
  for (const name of existingNames) {
    const prefix = assetNamePrefix(name);
    if (prefix) {
      counts.set(prefix, (counts.get(prefix) ?? 0) + 1);
    }
  }
  let best: string | undefined;
  for (const [prefix, count] of counts) {
    const bestCount = best ? (counts.get(best) ?? 0) : 0;
    if (count > bestCount || (count === bestCount && best !== undefined && prefix < best)) {
      best = prefix;
    }
  }
  return best ?? slugifyPipelinePrefix(pipelineName);
}

// Everything before an asset name's last dot: "a.b.orders" -> "a.b".
export function assetNamePrefix(name?: string | null): string {
  const trimmed = name?.trim() ?? "";
  const dot = trimmed.lastIndexOf(".");
  return dot > 0 ? trimmed.slice(0, dot) : "";
}

function slugifyPipelinePrefix(input?: string | null): string {
  const normalized = (input ?? "").trim().toLowerCase();
  if (!normalized) {
    return "default";
  }

  const slug = normalized
    .replace(/[^a-z0-9\s_-]/g, "")
    .replace(/[\s_]+/g, "_")
    .replace(/-+/g, "_")
    .replace(/^_+|_+$/g, "");

  return slug || "default";
}
