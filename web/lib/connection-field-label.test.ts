import { describe, expect, it } from "vitest";

import { connectionFieldLabel } from "./connection-field-label";

describe("connectionFieldLabel", () => {
  it("reads config keys as labels", () => {
    expect(connectionFieldLabel("ssl_mode")).toBe("SSL mode");
    expect(connectionFieldLabel("read_only")).toBe("Read only");
    expect(connectionFieldLabel("project_id")).toBe("Project ID");
    expect(connectionFieldLabel("host")).toBe("Host");
    expect(connectionFieldLabel("service_account_json")).toBe("Service account JSON");
  });
});
