import { describe, expect, it } from "vitest";
import { LocalResourceReflection } from "./local-resource-reflection";

describe("local resource reflection", () => {
  it("suppresses only this mounted editor's own address replacement", () => {
    const reflection = new LocalResourceReflection();
    expect(reflection.matches(undefined)).toBe(false);
    expect(reflection.matches("cold-link")).toBe(false);
    reflection.begin("selection");
    // React can render local selection before the replacement URL commits.
    expect(reflection.active).toBe(true);
    reflection.observe("REPLACE", "selection");
    expect(reflection.matches("selection")).toBe(true);
    expect(new LocalResourceReflection().matches("selection")).toBe(false);
  });

  it.each(["PUSH", "BACK", "FORWARD", "GO"])("reveals again after %s", (action) => {
    const reflection = new LocalResourceReflection();
    reflection.begin("selection");
    reflection.observe(action, "selection");
    expect(reflection.active).toBe(false);
    expect(reflection.matches("selection")).toBe(false);
  });

  it("does not mistake an unrelated replacement or old selection for local input", () => {
    const reflection = new LocalResourceReflection();
    reflection.begin("first");
    reflection.begin("second");
    expect(reflection.matches("first")).toBe(false);
    reflection.observe("REPLACE", undefined);
    expect(reflection.matches("second")).toBe(false);
  });
});
