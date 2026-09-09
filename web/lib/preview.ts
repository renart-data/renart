import type { PreviewMetadata } from "@/lib/generated/api-types";

export function previewStatus(preview: PreviewMetadata): string {
  const rows = `Showing ${preview.returned_rows.toLocaleString()} ${preview.returned_rows === 1 ? "row" : "rows"}`;
  if (preview.reason === "row_limit") return `${rows} · preview limit reached`;
  if (preview.reason === "byte_limit") return `${rows} · preview size limit reached`;
  if (!preview.has_more) return `${rows} · all rows`;
  return rows;
}

type Pending<T> = {
  limit: number;
  controller: AbortController;
  owners: Set<symbol>;
  promise: Promise<{ value: T; limit: number } | null>;
};

// Request admission only, never a second result cache. Domain owners retain
// their rows/errors. A microtask coalesces canvas/full-view requests in one turn.
export class PreviewRequests<T> {
  private pending = new Map<string, Pending<T>>();

  run(
    key: string,
    limit: number,
    fetch: (limit: number, signal: AbortSignal) => Promise<T>,
    owner?: symbol,
  ) {
    const existing = this.pending.get(key);
    if (existing && existing.limit >= limit) {
      if (owner) existing.owners.add(owner);
      return existing.promise;
    }
    const owners = new Set(existing?.owners);
    if (owner) owners.add(owner);
    this.cancel(key);
    const controller = new AbortController();
    const entry: Pending<T> = { limit, controller, owners, promise: Promise.resolve(null) };
    entry.promise = Promise.resolve().then(async () => {
      if (controller.signal.aborted) return null;
      try {
        const value = await fetch(limit, controller.signal);
        return controller.signal.aborted ? null : { value, limit };
      } catch (error) {
        if (controller.signal.aborted) return null;
        throw error;
      } finally {
        if (this.pending.get(key) === entry) this.pending.delete(key);
      }
    });
    this.pending.set(key, entry);
    return entry.promise;
  }

  cancel(key: string) {
    this.pending.get(key)?.controller.abort();
    this.pending.delete(key);
  }

  release(key: string, owner: symbol) {
    const entry = this.pending.get(key);
    if (!entry) return;
    entry.owners.delete(owner);
    if (entry.owners.size === 0) this.cancel(key);
  }
}
