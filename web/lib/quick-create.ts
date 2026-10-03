// Quick create: a pending asset node placed on the canvas next to its sources,
// named inline and created without leaving the graph. The node moves through
// editing → creating → created (until the workspace update shows the asset);
// a failure keeps the draft with its error so it can be fixed and retried.

export type QuickCreateKind = "sql" | "python" | "load";

export type QuickCreateDraft = {
  // The sources the new asset reads. The first is its primary upstream; more
  // than one makes a SQL join.
  sourceIds: string[];
  sourceNames: string[];
  name: string;
  kind: QuickCreateKind;
  // Where a drag ended, in canvas coordinates; otherwise the node sits
  // beside its sources.
  position?: { x: number; y: number };
};

export type QuickCreateState =
  | { status: "idle" }
  | { status: "editing"; draft: QuickCreateDraft }
  | { status: "creating"; draft: QuickCreateDraft }
  | { status: "created"; draft: QuickCreateDraft; assetId: string }
  | { status: "failed"; draft: QuickCreateDraft; error: string };

export type QuickCreateEvent =
  | { type: "open"; draft: QuickCreateDraft }
  | { type: "edit"; patch: Partial<Pick<QuickCreateDraft, "name" | "kind">> }
  | { type: "submit" }
  | { type: "succeeded"; assetId: string }
  | { type: "failed"; error: string }
  | { type: "revealed" }
  | { type: "cancel" };

export const idleQuickCreate: QuickCreateState = { status: "idle" };

export function quickCreateReducer(
  state: QuickCreateState,
  event: QuickCreateEvent,
): QuickCreateState {
  switch (event.type) {
    case "open":
      // A request in flight finishes first; it already owns the pending node.
      if (state.status === "creating") return state;
      return { status: "editing", draft: event.draft };
    case "edit":
      if (state.status !== "editing" && state.status !== "failed") return state;
      return { status: "editing", draft: { ...state.draft, ...event.patch } };
    case "submit":
      if (state.status !== "editing" && state.status !== "failed") return state;
      return { status: "creating", draft: state.draft };
    case "succeeded":
      if (state.status !== "creating") return state;
      return { status: "created", draft: state.draft, assetId: event.assetId };
    case "failed":
      if (state.status !== "creating") return state;
      return { status: "failed", draft: state.draft, error: event.error };
    case "revealed":
      return state.status === "created" ? idleQuickCreate : state;
    case "cancel":
      return state.status === "creating" ? state : idleQuickCreate;
  }
}

function nameParts(name: string) {
  const parts = name.split(".").filter(Boolean);
  const leaf = parts.pop() ?? "asset";
  return { prefix: parts.join("."), leaf };
}

function uniqueName(base: string, existing: Set<string>) {
  if (!existing.has(base)) return base;
  let index = 2;
  while (existing.has(`${base}_${index}`)) index += 1;
  return `${base}_${index}`;
}

// A downstream asset joins its first source's prefix group. One source gets
// "<leaf>_downstream", two read as "<a>_<b>", more as "<first>_joined".
export function suggestDownstreamAssetName(sourceNames: string[], existing: Set<string>): string {
  const [first, second, ...rest] = sourceNames.map(nameParts);
  if (!first) return uniqueName("my_asset", existing);
  const leaf = !second
    ? `${first.leaf}_downstream`
    : rest.length === 0
      ? `${first.leaf}_${second.leaf}`
      : `${first.leaf}_joined`;
  return uniqueName(first.prefix ? `${first.prefix}.${leaf}` : leaf, existing);
}

// Mirrors the server's rules so the inline field can explain a bad name
// before a request is made.
export function validateQuickCreateName(name: string, existing: Set<string>): string | null {
  const trimmed = name.trim();
  if (!trimmed) return "Enter a name.";
  if (!/^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)+$/.test(trimmed)) {
    return "Use a prefixed name such as analytics.orders.";
  }
  if (existing.has(trimmed)) return `${trimmed} already exists.`;
  return null;
}

type Box = { x: number; y: number; width: number; height: number };

const columnGap = 96;
const rowGap = 32;

function overlaps(a: Box, b: Box) {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

// Places the pending node one column right of its rightmost source, at the
// sources' average height. When a card is in the way it tries the free row
// nearest that height, below first, so it stays close to its sources.
export function pendingNodePosition(
  sources: Box[],
  occupied: Box[],
  size: { width: number; height: number },
): { x: number; y: number } {
  if (sources.length === 0) return { x: 0, y: 0 };
  const x = Math.max(...sources.map((box) => box.x + box.width)) + columnGap;
  const centerY =
    sources.reduce((total, box) => total + box.y + box.height / 2, 0) / sources.length;
  const baseY = centerY - size.height / 2;
  const step = size.height + rowGap;
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const offset = Math.ceil(attempt / 2) * (attempt % 2 === 1 ? 1 : -1);
    const candidate = { x, y: baseY + offset * step, ...size };
    if (!occupied.some((box) => overlaps(candidate, box))) return { x, y: candidate.y };
  }
  return { x, y: baseY };
}
