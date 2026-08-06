import { describe, expect, it } from "vitest";

import { visibleConnectionTypes } from "@/lib/features";
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
