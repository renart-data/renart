import { describe, expect, it } from "vitest";

import { API_ASSET_TEMPLATES, buildAPIAssetTemplate } from "@/lib/api-asset-templates";

describe("API asset templates", () => {
  it("offers a JSON request-body example", () => {
    expect(API_ASSET_TEMPLATES.some((template) => template.id === "request-body")).toBe(true);

    const content = buildAPIAssetTemplate("request-body", "warehouse");
    expect(content).toContain("connection: warehouse");
    expect(content).toContain("method: POST");
    expect(content).toContain("    body:\n");
    expect(content).toContain("      source:\n");
    expect(content).toContain("    records_path: json");
  });
});
