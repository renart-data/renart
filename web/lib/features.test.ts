import { describe, expect, it } from "vitest";

import { ingestrCreationEnabled, visibleConnectionTypes } from "@/lib/features";
import type { WorkspaceConfigConnectionType } from "@/lib/types";

function connectionType(type_name: string, category: string): WorkspaceConfigConnectionType {
  return { type_name, category, fields: [] };
}

describe("visibleConnectionTypes", () => {
  const types = [
    connectionType("postgres", "warehouse"),
    connectionType("s3", "storage"),
    connectionType("stripe", "source"),
  ];

  it("keeps object storage configurable without ingestr", () => {
    expect(visibleConnectionTypes(types, false).map((type) => type.type_name)).toEqual([
      "postgres",
      "s3",
    ]);
  });

  it("includes source connectors when ingestr is enabled", () => {
    expect(visibleConnectionTypes(types, true)).toEqual(types);
  });
});

describe("ingestrCreationEnabled", () => {
  it("requires explicit opt-in, not the presence of legacy assets", () => {
    const legacyWorkspace = { features: {}, pipelines: [{ assets: [{ type: "ingestr" }] }] };
    expect(ingestrCreationEnabled(null, legacyWorkspace)).toBe(false);
    expect(ingestrCreationEnabled(null, { features: { ingestr: true } })).toBe(true);
    expect(ingestrCreationEnabled({ features: { ingestr: true } }, null)).toBe(true);
  });

  it("uses loaded project config as authoritative over stale workspace flags", () => {
    expect(ingestrCreationEnabled({ features: {} }, { features: { ingestr: true } })).toBe(false);
  });
});
