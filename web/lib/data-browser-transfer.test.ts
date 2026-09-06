import { describe, expect, it } from "vitest";
import {
  acceptsDataBrowserTransfer,
  matchesDataBrowserTransfer,
  canLoadDataBrowserConnection,
} from "./data-browser-transfer";

describe("Data Browser transfers", () => {
  const transfer = {
    kind: "table" as const,
    id: "table-id",
    label: "orders",
    token: "nonce",
    pipelineId: "p1",
    projectId: "project1",
    environment: "dev",
    method: "drag" as const,
  };
  it("keeps transfers scoped to the original project, pipeline and environment", () => {
    expect(acceptsDataBrowserTransfer(transfer, "p1", "project1", "dev")).toBe(true);
    expect(acceptsDataBrowserTransfer(transfer, "p2", "project1", "dev")).toBe(false);
    expect(acceptsDataBrowserTransfer(transfer, "p1", "project2", "dev")).toBe(false);
    expect(acceptsDataBrowserTransfer(transfer, "p1", "project1", "prod")).toBe(false);
    expect(acceptsDataBrowserTransfer(null, "p1", "project1", "dev")).toBe(false);
  });
  it("rejects foreign and stale native drags", () => {
    expect(matchesDataBrowserTransfer(transfer, "nonce")).toBe(true);
    expect(matchesDataBrowserTransfer(transfer, "other-tab")).toBe(false);
    expect(matchesDataBrowserTransfer(null, "nonce")).toBe(false);
  });
  it("offers Load only for an eligible local upstream and another destination", () => {
    const source = { connection: "input", readOnly: false, kind: "sql" };
    expect(canLoadDataBrowserConnection(source, "output", ["input"], ["output"])).toBe(true);
    expect(canLoadDataBrowserConnection(source, "input", ["input"], ["input"])).toBe(false);
    expect(
      canLoadDataBrowserConnection({ ...source, readOnly: true }, "output", ["input"], ["output"]),
    ).toBe(false);
    expect(
      canLoadDataBrowserConnection({ ...source, kind: "sensor" }, "output", ["input"], ["output"]),
    ).toBe(false);
    expect(canLoadDataBrowserConnection(source, "output", [], ["output"])).toBe(false);
    expect(canLoadDataBrowserConnection(source, "output", ["input"], [])).toBe(false);
  });
});
