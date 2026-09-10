import { describe, expect, it } from "vitest";

import {
  buildAssetDocumentKey,
  buildDocumentKey,
  buildDocumentReducer,
  createBuildDocumentSession,
  documentAfterClose,
  parseBuildDocumentSession,
  type BuildDocument,
} from "./build-document-state";

const first: BuildDocument = { kind: "asset", pipelineId: "orders", assetId: "raw" };
const second: BuildDocument = { kind: "asset", pipelineId: "orders", assetId: "daily" };
const adhoc: BuildDocument = {
  kind: "adhoc",
  pipelineId: "orders",
  contextAssetId: "daily",
};
const notebook: BuildDocument = { kind: "notebook", notebookId: "quarterly-review" };

describe("build document state", () => {
  it("opens a document once and preserves its original position", () => {
    const opened = buildDocumentReducer(createBuildDocumentSession("project-a"), {
      type: "document-opened",
      document: first,
    });
    const openedAgain = buildDocumentReducer(opened, {
      type: "document-opened",
      document: first,
    });

    expect(openedAgain.documents).toEqual([first]);
  });

  it("chooses the adjacent document when the active document closes", () => {
    expect(documentAfterClose([first, second, adhoc], buildDocumentKey(second))).toEqual(adhoc);
    expect(documentAfterClose([first, second, adhoc], buildDocumentKey(adhoc))).toEqual(second);
    expect(documentAfterClose([first], buildDocumentKey(first))).toBeNull();
  });

  it("prunes removed assets and notebooks without discarding pipeline ad-hoc documents", () => {
    const state = {
      ...createBuildDocumentSession("project-a"),
      documents: [first, second, adhoc, notebook],
    };
    const reconciled = buildDocumentReducer(state, {
      type: "resources-reconciled",
      assetKeys: new Set([buildAssetDocumentKey("orders", "daily")]),
      notebookIds: new Set(["quarterly-review"]),
      activeKey: buildDocumentKey(second),
    });

    expect(reconciled.documents).toEqual([second, adhoc, notebook]);

    const notebookRemoved = buildDocumentReducer(reconciled, {
      type: "resources-reconciled",
      assetKeys: new Set([buildAssetDocumentKey("orders", "daily")]),
      notebookIds: new Set(),
      activeKey: buildDocumentKey(second),
    });
    expect(notebookRemoved.documents).toEqual([second, adhoc]);

    const activeNotebookAwaitingWorkspace = buildDocumentReducer(
      { ...state, documents: [notebook] },
      {
        type: "resources-reconciled",
        assetKeys: new Set(),
        notebookIds: new Set(),
        activeKey: buildDocumentKey(notebook),
      },
    );
    expect(activeNotebookAwaitingWorkspace.documents).toEqual([notebook]);
  });

  it("rejects corrupt, cross-project, duplicate, and invalid stored entries", () => {
    expect(parseBuildDocumentSession("not json", "project-a").documents).toEqual([]);
    expect(
      parseBuildDocumentSession(
        JSON.stringify({
          ...createBuildDocumentSession("project-b"),
          documents: [first],
        }),
        "project-a",
      ).documents,
    ).toEqual([]);

    const restored = parseBuildDocumentSession(
      JSON.stringify({
        ...createBuildDocumentSession("project-a"),
        documents: [
          first,
          first,
          notebook,
          { kind: "asset", pipelineId: "", assetId: "bad" },
          { kind: "notebook", notebookId: "" },
        ],
      }),
      "project-a",
    );
    expect(restored.documents).toEqual([first, notebook]);
  });
});
