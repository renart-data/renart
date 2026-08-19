import { describe, expect, it } from "vitest";

import { visualizationPaletteColors } from "./visualization-palettes";

describe("visualization palettes", () => {
  it("resolves named palettes and falls back to workspace colors", () => {
    expect(visualizationPaletteColors("ocean")[0]).toBe("#0f6b9e");
    expect(visualizationPaletteColors("unknown")[0]).toBe("var(--chart-1)");
  });
});
