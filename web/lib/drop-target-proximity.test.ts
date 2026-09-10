import { describe, expect, it } from "vitest";
import { nearestDropTarget } from "./drop-target-proximity";

describe("drop target proximity", () => {
  const target = { id: "load", left: 100, right: 180, top: 100, bottom: 132 };
  it("expands before the pointer reaches the target but not when far away", () => {
    expect(nearestDropTarget([target], 80, 116)).toBe("load");
    expect(nearestDropTarget([target], 130, 116)).toBe("load");
    expect(nearestDropTarget([target], 20, 116)).toBeNull();
    expect(nearestDropTarget([target], 60, 60)).toBeNull();
  });
  it("chooses one nearest target and prefers a precise target inside a group", () => {
    const group = { id: "group", left: 0, right: 400, top: 0, bottom: 400 };
    expect(nearestDropTarget([group, target], 130, 116)).toBe("load");
    expect(nearestDropTarget([target, group], 130, 116)).toBe("load");
    expect(
      nearestDropTarget([target, { ...target, id: "next", left: 250, right: 330 }], 230, 116),
    ).toBe("next");
  });
  it("ignores hidden or disconnected zero-sized anchors", () => {
    expect(nearestDropTarget([{ ...target, right: 100 }], 100, 110)).toBeNull();
    expect(nearestDropTarget([], 100, 110)).toBeNull();
  });
});
