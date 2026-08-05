import { describe, expect, it } from "vitest";

import { columnStatus, parseAssetProvenance } from "./asset-provenance";

describe("column-local asset provenance", () => {
  it("reads the Bruin column meta key and lets it override legacy asset metadata", () => {
    const provenance = parseAssetProvenance({ renart_col_src: "customer_id:l;legacy_id:m" }, [
      { name: "customer_id", meta: { renart_source: "m", semantic_type: "identifier" } },
      { name: "email", meta: { renart_source: "l" } },
      { name: "manual_note", meta: { renart_manual: "true" } },
      { name: "amount", meta: { renart_owned: "type|description" } },
    ]);

    expect(columnStatus("customer_id", provenance)).toBe("table-inferred");
    expect(columnStatus("email", provenance)).toBe("live-inferred");
    expect(columnStatus("manual_note", provenance)).toBe("manual");
    expect(columnStatus("amount", provenance)).toBe("type-owned");
    expect(columnStatus("legacy_id", provenance)).toBe("table-inferred");
  });
});
