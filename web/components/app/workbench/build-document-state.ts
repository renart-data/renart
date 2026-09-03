import { useEffect, useMemo, useReducer } from "react";

export const BUILD_DOCUMENT_SESSION_VERSION = 1;
const MAX_DOCUMENTS = 16;
const MAX_SESSION_BYTES = 16 * 1024;

export type BuildAssetDocument = {
  kind: "asset";
  pipelineId: string;
  assetId: string;
};

export type BuildAdhocDocument = {
  kind: "adhoc";
  pipelineId: string;
  contextAssetId: string;
};

export type BuildNotebookDocument = {
  kind: "notebook";
  notebookId: string;
};

export type BuildDocument = BuildAssetDocument | BuildAdhocDocument | BuildNotebookDocument;

export type BuildDocumentSession = {
  version: typeof BUILD_DOCUMENT_SESSION_VERSION;
  projectId: string;
  documents: BuildDocument[];
};

type BuildDocumentAction =
  | { type: "document-opened"; document: BuildDocument }
  | { type: "document-closed"; key: string }
  | {
      type: "resources-reconciled";
      assetKeys: ReadonlySet<string>;
      notebookIds: ReadonlySet<string>;
      activeKey: string | null;
    }
  | { type: "state-restored"; state: BuildDocumentSession };

export function buildDocumentKey(document: BuildDocument) {
  if (document.kind === "asset") return `asset:${document.pipelineId}:${document.assetId}`;
  if (document.kind === "adhoc") return `adhoc:${document.pipelineId}`;
  return `notebook:${document.notebookId}`;
}

export function buildAssetDocumentKey(pipelineId: string, assetId: string) {
  return buildDocumentKey({ kind: "asset", pipelineId, assetId });
}

export function createBuildDocumentSession(projectId: string): BuildDocumentSession {
  return {
    version: BUILD_DOCUMENT_SESSION_VERSION,
    projectId,
    documents: [],
  };
}

export function buildDocumentReducer(
  state: BuildDocumentSession,
  action: BuildDocumentAction,
): BuildDocumentSession {
  if (action.type === "state-restored") return action.state;

  if (action.type === "document-opened") {
    const key = buildDocumentKey(action.document);
    if (state.documents.some((document) => buildDocumentKey(document) === key)) return state;
    return {
      ...state,
      documents: [...state.documents, action.document].slice(-MAX_DOCUMENTS),
    };
  }

  if (action.type === "document-closed") {
    return {
      ...state,
      documents: state.documents.filter((document) => buildDocumentKey(document) !== action.key),
    };
  }

  const documents = state.documents.filter(
    (document) =>
      buildDocumentKey(document) === action.activeKey ||
      document.kind === "adhoc" ||
      (document.kind === "asset" && action.assetKeys.has(buildDocumentKey(document))) ||
      (document.kind === "notebook" && action.notebookIds.has(document.notebookId)),
  );
  return documents.length === state.documents.length ? state : { ...state, documents };
}

export function documentAfterClose(
  documents: readonly BuildDocument[],
  closingKey: string,
): BuildDocument | null {
  const closingIndex = documents.findIndex((document) => buildDocumentKey(document) === closingKey);
  if (closingIndex < 0) return null;
  const remaining = documents.filter((document) => buildDocumentKey(document) !== closingKey);
  return remaining[Math.min(closingIndex, remaining.length - 1)] ?? null;
}

export function parseBuildDocumentSession(
  serialized: string | null,
  projectId: string,
): BuildDocumentSession {
  const fallback = createBuildDocumentSession(projectId);
  if (!serialized || serialized.length > MAX_SESSION_BYTES) return fallback;
  try {
    const value = JSON.parse(serialized) as Partial<BuildDocumentSession>;
    if (
      value.version !== BUILD_DOCUMENT_SESSION_VERSION ||
      value.projectId !== projectId ||
      !Array.isArray(value.documents)
    ) {
      return fallback;
    }
    const documents: BuildDocument[] = [];
    const keys = new Set<string>();
    for (const candidate of value.documents) {
      if (!isBuildDocument(candidate)) continue;
      const key = buildDocumentKey(candidate);
      if (keys.has(key)) continue;
      keys.add(key);
      documents.push(candidate);
    }
    return {
      version: BUILD_DOCUMENT_SESSION_VERSION,
      projectId,
      documents: documents.slice(-MAX_DOCUMENTS),
    };
  } catch {
    return fallback;
  }
}

function isBuildDocument(value: unknown): value is BuildDocument {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<BuildDocument> & { contextAssetId?: unknown };
  if (candidate.kind === "asset") {
    return nonEmptyString(candidate.pipelineId) && nonEmptyString(candidate.assetId);
  }
  if (candidate.kind === "notebook") {
    return nonEmptyString(candidate.notebookId);
  }
  return (
    candidate.kind === "adhoc" &&
    nonEmptyString(candidate.pipelineId) &&
    nonEmptyString(candidate.contextAssetId)
  );
}

function nonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 2048;
}

function buildDocumentSessionKey(projectId: string) {
  return `renart.build-documents.v${BUILD_DOCUMENT_SESSION_VERSION}.${encodeURIComponent(projectId)}`;
}

function loadBuildDocumentSession(projectId: string) {
  if (typeof window === "undefined") return createBuildDocumentSession(projectId);
  try {
    return parseBuildDocumentSession(
      window.sessionStorage.getItem(buildDocumentSessionKey(projectId)),
      projectId,
    );
  } catch {
    return createBuildDocumentSession(projectId);
  }
}

export function useBuildDocuments({
  projectId,
  activeDocument,
  availableAssetKeys,
  availableNotebookIds,
  resourcesReady,
}: {
  projectId: string;
  activeDocument: BuildDocument | null;
  availableAssetKeys: ReadonlySet<string>;
  availableNotebookIds: ReadonlySet<string>;
  resourcesReady: boolean;
}) {
  const [state, dispatch] = useReducer(buildDocumentReducer, projectId, loadBuildDocumentSession);

  useEffect(() => {
    if (state.projectId === projectId) return;
    dispatch({ type: "state-restored", state: loadBuildDocumentSession(projectId) });
  }, [projectId, state.projectId]);

  useEffect(() => {
    if (!activeDocument || state.projectId !== projectId) return;
    dispatch({ type: "document-opened", document: activeDocument });
  }, [activeDocument, projectId, state.projectId]);

  useEffect(() => {
    if (state.projectId !== projectId || !resourcesReady) return;
    dispatch({
      type: "resources-reconciled",
      assetKeys: availableAssetKeys,
      notebookIds: availableNotebookIds,
      activeKey: activeDocument ? buildDocumentKey(activeDocument) : null,
    });
  }, [
    activeDocument,
    availableAssetKeys,
    availableNotebookIds,
    projectId,
    resourcesReady,
    state.projectId,
  ]);

  useEffect(() => {
    if (typeof window === "undefined" || state.projectId !== projectId) return;
    try {
      window.sessionStorage.setItem(buildDocumentSessionKey(projectId), JSON.stringify(state));
    } catch {
      // Document history is optional presentation state. Storage failures must
      // never prevent Build from opening the route-selected document.
    }
  }, [projectId, state]);

  return useMemo(
    () => ({
      documents: state.documents,
      closeDocument: (key: string) => dispatch({ type: "document-closed", key }),
    }),
    [state.documents],
  );
}
