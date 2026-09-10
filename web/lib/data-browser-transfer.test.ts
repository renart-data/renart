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
    destination: { kind: "pipeline" as const, id: "p1" },
    projectId: "project1",
    environment: "dev",
    method: "drag" as const,
  };
  it("keeps transfers scoped to the original project, pipeline and environment", () => {
    expect(
      acceptsDataBrowserTransfer(transfer, { kind: "pipeline", id: "p1" }, "project1", "dev"),
    ).toBe(true);
    expect(
      acceptsDataBrowserTransfer(transfer, { kind: "pipeline", id: "p2" }, "project1", "dev"),
    ).toBe(false);
    expect(
      acceptsDataBrowserTransfer(transfer, { kind: "pipeline", id: "p1" }, "project2", "dev"),
    ).toBe(false);
    expect(
      acceptsDataBrowserTransfer(transfer, { kind: "pipeline", id: "p1" }, "project1", "prod"),
    ).toBe(false);
    expect(
      acceptsDataBrowserTransfer(null, { kind: "pipeline", id: "p1" }, "project1", "dev"),
    ).toBe(false);
  });
  it("rejects foreign and stale native drags", () => {
    expect(matchesDataBrowserTransfer(transfer, "nonce")).toBe(true);
    expect(matchesDataBrowserTransfer(transfer, "other-tab")).toBe(false);
    expect(matchesDataBrowserTransfer(null, "nonce")).toBe(false);
  });
  it("isolates notebook placement from pipelines and other notebooks", () => {
    const notebook = { ...transfer, destination: { kind: "notebook" as const, id: "n1" } };
    expect(
      acceptsDataBrowserTransfer(notebook, { kind: "notebook", id: "n1" }, "project1", "dev"),
    ).toBe(true);
    expect(
      acceptsDataBrowserTransfer(notebook, { kind: "pipeline", id: "n1" }, "project1", "dev"),
    ).toBe(false);
    expect(
      acceptsDataBrowserTransfer(notebook, { kind: "notebook", id: "n2" }, "project1", "dev"),
    ).toBe(false);
    expect(
      acceptsDataBrowserTransfer(notebook, { kind: "notebook", id: "n1" }, "another", "dev"),
    ).toBe(false);
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
