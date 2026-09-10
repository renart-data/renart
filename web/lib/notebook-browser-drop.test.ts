import { describe, expect, it } from "vitest";
import { notebookBrowserDropReason } from "./notebook-browser-drop";
import type { DataBrowserNode } from "./generated/api-types";

const file: DataBrowserNode = {
  id: "id",
  label: "orders.csv",
  node_type: "object",
  object_kind: "file",
  has_children: false,
  format: "csv",
  address: { source_kind: "local_files", path: "orders.csv" },
};
describe("notebook browser drop eligibility", () => {
  it("accepts supported local and S3 files", () => {
    expect(notebookBrowserDropReason(file)).toBeUndefined();
    expect(
      notebookBrowserDropReason({
        ...file,
        address: {
          source_kind: "storage",
          connection: "lake",
          connection_type: "s3",
          path: "orders.csv",
        },
      }),
    ).toBeUndefined();
  });
  it("does not advertise unsupported transports, formats, prefixes or executable paths", () => {
    expect(notebookBrowserDropReason({ ...file, format: "txt" })).toBeTruthy();
    expect(notebookBrowserDropReason({ ...file, node_type: "namespace" })).toBeTruthy();
    expect(
      notebookBrowserDropReason({
        ...file,
        address: {
          source_kind: "storage",
          connection: "files",
          connection_type: "sftp",
          path: "orders.csv",
        },
      }),
    ).toBeTruthy();
    expect(
      notebookBrowserDropReason({
        ...file,
        address: { source_kind: "local_files", path: "{{ private }}.csv" },
      }),
    ).toBeTruthy();
  });
});
