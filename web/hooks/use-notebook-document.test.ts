import { describe, expect, it } from "vitest";

import type { WebNotebook } from "@/lib/types";

import { createNotebookDocumentState, notebookDocumentReducer } from "./use-notebook-document";

function notebook(id: string, revision: string): WebNotebook {
  return { id, revision } as WebNotebook;
}

describe("notebook document model", () => {
  it("keeps a mutation response until the workspace reaches its revision", () => {
    const updated = notebook("notebook-a", "revision-2");
    let state = notebookDocumentReducer(createNotebookDocumentState("notebook-a"), {
      type: "mutation_applied",
      notebookId: "notebook-a",
      notebook: updated,
    });
    state = notebookDocumentReducer(state, {
      type: "workspace_observed",
      notebookId: "notebook-a",
      revision: "revision-1",
    });
    expect(state.mutationNotebook).toBe(updated);

    state = notebookDocumentReducer(state, {
      type: "workspace_observed",
      notebookId: "notebook-a",
      revision: "revision-2",
    });
    expect(state.mutationNotebook).toBeNull();
  });

  it("uses an authoritative reload only while it differs from the workspace", () => {
    const loaded = notebook("notebook-a", "revision-3");
    let state = notebookDocumentReducer(createNotebookDocumentState("notebook-a"), {
      type: "authoritative_notebook_loaded",
      notebookId: "notebook-a",
      notebook: loaded,
      workspaceRevision: "revision-2",
    });
    expect(state.mutationNotebook).toBe(loaded);

    state = notebookDocumentReducer(state, {
      type: "authoritative_notebook_loaded",
      notebookId: "notebook-a",
      notebook: loaded,
      workspaceRevision: "revision-3",
    });
    expect(state.mutationNotebook).toBeNull();
  });

  it("ignores late loads and mutation responses after notebook navigation", () => {
    let state = notebookDocumentReducer(createNotebookDocumentState("notebook-a"), {
      type: "notebook_changed",
      notebookId: "notebook-b",
    });
    const current = state;
    state = notebookDocumentReducer(state, {
      type: "authoritative_notebook_loaded",
      notebookId: "notebook-a",
      notebook: notebook("notebook-a", "revision-2"),
    });
    state = notebookDocumentReducer(state, {
      type: "mutation_applied",
      notebookId: "notebook-a",
      notebook: notebook("notebook-a", "revision-3"),
    });

    expect(state).toBe(current);
    expect(state.mutationNotebook).toBeNull();
  });

  it("clears an action error when a new mutation starts and when it succeeds", () => {
    let state = notebookDocumentReducer(createNotebookDocumentState("notebook-a"), {
      type: "action_error_reported",
      notebookId: "notebook-a",
      message: "conflict",
    });
    expect(state.actionError).toBe("conflict");

    state = notebookDocumentReducer(state, {
      type: "mutation_started",
      notebookId: "notebook-a",
    });
    expect(state.actionError).toBe("");

    state = notebookDocumentReducer(
      { ...state, actionError: "another error" },
      {
        type: "mutation_applied",
        notebookId: "notebook-a",
        notebook: notebook("notebook-a", "revision-2"),
      },
    );
    expect(state.actionError).toBe("");
  });

  it("keeps load failures separate from action failures", () => {
    let state = notebookDocumentReducer(createNotebookDocumentState("notebook-a"), {
      type: "notebook_load_failed",
      notebookId: "notebook-a",
      message: "missing notebook",
    });
    state = notebookDocumentReducer(state, {
      type: "action_error_reported",
      notebookId: "notebook-a",
      message: "save conflict",
    });

    expect(state).toMatchObject({
      loadError: "missing notebook",
      actionError: "save conflict",
    });
  });
});
