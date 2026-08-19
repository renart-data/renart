import { describe, expect, it } from "vitest";

import { normalizeAnsiOutput } from "./ansi-output";

describe("normalizeAnsiOutput", () => {
  it("restores escaped terminal control pictures and normalizes line endings", () => {
    expect(normalizeAnsiOutput("␛[31mfailed␛[0m\r\nnext")).toBe("\u001b[31mfailed\u001b[0m\nnext");
  });

  it("preserves real ANSI escape sequences", () => {
    expect(normalizeAnsiOutput("\u001b[32mok\u001b[0m")).toBe("\u001b[32mok\u001b[0m");
  });
});
