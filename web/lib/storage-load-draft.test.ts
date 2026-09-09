import { expect, it } from "vitest";
import { storageLoadDraft } from "./storage-load-draft";

it("creates reviewed Load defaults for files and prefixes, respecting read-only storage", () => {
  const object = {
    connection_name: "lake",
    reference_text: "s3://bucket/events/",
    kind: "prefix",
    capabilities: {
      list_namespaces: false,
      list_objects: false,
      describe_columns: false,
      preview_rows: false,
      query: false,
      load_source: true,
      load_destination: true,
    },
  };
  expect(storageLoadDraft(object)).toEqual({
    sourceConnection: "lake",
    sourceTable: "s3://bucket/events/*",
  });
  const upstream = { name: "analytics.orders", connection: "warehouse" };
  expect(storageLoadDraft(object, upstream)).toEqual({
    connection: "lake",
    sourceConnection: "warehouse",
    destinationObject: "s3://bucket/events/orders.parquet",
  });
  const file = { ...object, kind: "file", reference_text: "s3://bucket/a.csv" };
  expect(storageLoadDraft(file, upstream)?.destinationObject).toBe("s3://bucket/a.csv");
  const readOnly = { ...file, capabilities: { ...file.capabilities, load_destination: false } };
  expect(storageLoadDraft(readOnly)?.sourceTable).toBe("s3://bucket/a.csv");
  expect(storageLoadDraft(readOnly, upstream)).toBeNull();
});

it("uses the local Load source for project files without making them overwrite targets", () => {
  const object = {
    address: { source_kind: "local_files", path: "data/orders.csv" },
    connection_name: "Project files",
    reference_text: "data/orders.csv",
    kind: "file",
    capabilities: {
      list_namespaces: false,
      list_objects: false,
      describe_columns: false,
      preview_rows: false,
      query: false,
      load_source: true,
      load_destination: false,
    },
  };
  expect(storageLoadDraft(object)).toEqual({
    sourceConnection: "local",
    sourceTable: "data/orders.csv",
  });
  expect(
    storageLoadDraft(object, { name: "analytics.orders", connection: "duckdb-default" }),
  ).toBeNull();
  expect(
    storageLoadDraft({ ...object, capabilities: { ...object.capabilities, load_source: false } }),
  ).toBeNull();
});
