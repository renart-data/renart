import { describe, expect, it } from "vitest";

import {
  idleQuickCreate,
  pendingNodePosition,
  quickCreateReducer,
  suggestDownstreamAssetName,
  validateQuickCreateName,
  type QuickCreateDraft,
  type QuickCreateState,
} from "@/lib/quick-create";

const draft: QuickCreateDraft = {
  sourceIds: ["events-id"],
  sourceNames: ["product.events"],
  name: "product.events_downstream",
  kind: "sql",
};

function run(...events: Parameters<typeof quickCreateReducer>[1][]): QuickCreateState {
  return events.reduce(quickCreateReducer, idleQuickCreate);
}

describe("quickCreateReducer", () => {
  it("goes from editing through creating to created, then idle once revealed", () => {
    const created = run(
      { type: "open", draft },
      { type: "edit", patch: { name: "product.daily", kind: "python" } },
      { type: "submit" },
      { type: "succeeded", assetId: "new-id" },
    );
    expect(created).toEqual({
      status: "created",
      draft: { ...draft, name: "product.daily", kind: "python" },
      assetId: "new-id",
    });
    expect(quickCreateReducer(created, { type: "revealed" })).toEqual(idleQuickCreate);
  });

  it("keeps a failed draft with its error, and an edit or retry continues from it", () => {
    const failed = run(
      { type: "open", draft },
      { type: "submit" },
      { type: "failed", error: "connection missing" },
    );
    expect(failed).toEqual({ status: "failed", draft, error: "connection missing" });
    expect(quickCreateReducer(failed, { type: "submit" })).toEqual({ status: "creating", draft });
    expect(quickCreateReducer(failed, { type: "edit", patch: { name: "product.x" } })).toEqual({
      status: "editing",
      draft: { ...draft, name: "product.x" },
    });
  });

  it("cancels anything except a request in flight", () => {
    expect(run({ type: "open", draft }, { type: "cancel" })).toEqual(idleQuickCreate);
    const creating = run({ type: "open", draft }, { type: "submit" });
    expect(quickCreateReducer(creating, { type: "cancel" })).toBe(creating);
    expect(quickCreateReducer(creating, { type: "open", draft })).toBe(creating);
    expect(quickCreateReducer(creating, { type: "edit", patch: { name: "x.y" } })).toBe(creating);
  });

  it("ignores results that do not belong to the current state", () => {
    expect(run({ type: "succeeded", assetId: "x" })).toEqual(idleQuickCreate);
    expect(run({ type: "open", draft }, { type: "failed", error: "late" })).toEqual({
      status: "editing",
      draft,
    });
    expect(run({ type: "open", draft }, { type: "revealed" })).toEqual({
      status: "editing",
      draft,
    });
  });
});

describe("suggestDownstreamAssetName", () => {
  it("joins the first source's group and stays unique", () => {
    expect(suggestDownstreamAssetName(["product.events"], new Set())).toBe(
      "product.events_downstream",
    );
    expect(
      suggestDownstreamAssetName(["product.events"], new Set(["product.events_downstream"])),
    ).toBe("product.events_downstream_2");
    expect(suggestDownstreamAssetName(["product.events", "product.users"], new Set())).toBe(
      "product.events_users",
    );
    expect(suggestDownstreamAssetName(["a.events", "b.users", "c.plans"], new Set())).toBe(
      "a.events_joined",
    );
  });
});

describe("validateQuickCreateName", () => {
  it("requires a new, prefixed name", () => {
    expect(validateQuickCreateName(" ", new Set())).toBe("Enter a name.");
    expect(validateQuickCreateName("orders", new Set())).toMatch(/prefixed/);
    expect(validateQuickCreateName("a.orders", new Set(["a.orders"]))).toMatch(/exists/);
    expect(validateQuickCreateName("a.b.orders_2", new Set())).toBeNull();
  });
});

describe("pendingNodePosition", () => {
  const size = { width: 232, height: 112 };
  it("sits one column right of the sources at their average height", () => {
    expect(
      pendingNodePosition(
        [
          { x: 0, y: 0, ...size },
          { x: 300, y: 200, ...size },
        ],
        [],
        size,
      ),
    ).toEqual({ x: 628, y: 100 });
  });

  it("takes the nearest free row, below before above", () => {
    const source = { x: 0, y: 0, ...size };
    expect(pendingNodePosition([source], [source, { x: 328, y: 0, ...size }], size)).toEqual({
      x: 328,
      y: 144,
    });
    expect(
      pendingNodePosition(
        [source],
        [
          { x: 328, y: 0, ...size },
          { x: 328, y: 144, ...size },
        ],
        size,
      ),
    ).toEqual({ x: 328, y: -144 });
  });
});
