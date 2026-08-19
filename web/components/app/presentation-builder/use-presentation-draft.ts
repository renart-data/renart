"use client";

import { useCallback, useEffect, useReducer, useRef } from "react";

import type {
  PresentationArtifact,
  PresentationLayoutItem,
  PresentationSection,
} from "@/lib/api-presentations";

const HISTORY_LIMIT = 60;
const TEXT_COALESCE_MS = 800;

export type PresentationDraftCommand =
  | { type: "artifact.patch"; patch: Partial<PresentationArtifact> }
  | { type: "artifact.replace"; artifact: PresentationArtifact }
  | { type: "layout.commit"; layout: PresentationLayoutItem[] }
  | { type: "sections.commit"; sections: PresentationSection[] };

export type PresentationDraftState = {
  past: PresentationArtifact[];
  present: PresentationArtifact;
  future: PresentationArtifact[];
  coalesceKey: string;
  coalesceAt: number;
};

export type PresentationDraftAction =
  | {
      type: "commit";
      artifact: PresentationArtifact;
      coalesceKey: string;
      timestamp: number;
    }
  | { type: "reset"; artifact: PresentationArtifact }
  | { type: "undo" }
  | { type: "redo" };

export function initialPresentationDraftState(
  artifact: PresentationArtifact,
): PresentationDraftState {
  return { past: [], present: artifact, future: [], coalesceKey: "", coalesceAt: 0 };
}

export function presentationDraftReducer(
  state: PresentationDraftState,
  action: PresentationDraftAction,
): PresentationDraftState {
  switch (action.type) {
    case "reset":
      return initialPresentationDraftState(action.artifact);
    case "commit": {
      if (authoredArtifactJSON(state.present) === authoredArtifactJSON(action.artifact))
        return state;
      const coalesces =
        action.coalesceKey !== "" &&
        state.coalesceKey === action.coalesceKey &&
        action.timestamp - state.coalesceAt <= TEXT_COALESCE_MS;
      const past = coalesces ? state.past : [...state.past, state.present].slice(-HISTORY_LIMIT);
      return {
        past,
        present: action.artifact,
        future: [],
        coalesceKey: action.coalesceKey,
        coalesceAt: action.timestamp,
      };
    }
    case "undo": {
      const previous = state.past.at(-1);
      if (!previous) return state;
      return {
        past: state.past.slice(0, -1),
        present: previous,
        future: [state.present, ...state.future].slice(0, HISTORY_LIMIT),
        coalesceKey: "",
        coalesceAt: 0,
      };
    }
    case "redo": {
      const next = state.future[0];
      if (!next) return state;
      return {
        past: [...state.past, state.present].slice(-HISTORY_LIMIT),
        present: next,
        future: state.future.slice(1),
        coalesceKey: "",
        coalesceAt: 0,
      };
    }
  }
}

export function applyPresentationDraftCommand(
  artifact: PresentationArtifact,
  command: PresentationDraftCommand,
): PresentationArtifact {
  switch (command.type) {
    case "artifact.patch":
      return { ...artifact, ...command.patch };
    case "artifact.replace":
      return command.artifact;
    case "layout.commit":
      return { ...artifact, layout: command.layout };
    case "sections.commit":
      return { ...artifact, sections: command.sections };
  }
}

export function usePresentationDraft(
  artifact: PresentationArtifact,
  onChange: (artifact: PresentationArtifact) => void,
) {
  const [state, dispatch] = useReducer(
    presentationDraftReducer,
    artifact,
    initialPresentationDraftState,
  );
  const externalRevision = useRef(artifact.revision);
  const lastEmittedArtifact = useRef("");

  useEffect(() => {
    const incoming = authoredArtifactJSON(artifact);
    if (externalRevision.current !== artifact.revision) {
      externalRevision.current = artifact.revision;
      lastEmittedArtifact.current = "";
      dispatch({ type: "reset", artifact });
      return;
    }
    if (lastEmittedArtifact.current === incoming) {
      lastEmittedArtifact.current = "";
      return;
    }
    if (incoming !== authoredArtifactJSON(state.present)) dispatch({ type: "reset", artifact });
    // This effect intentionally follows incoming artifacts only. Following the
    // local present state would undo a command before the parent echoes it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [artifact]);

  const commit = useCallback(
    (command: PresentationDraftCommand, options?: { coalesceKey?: string }) => {
      const next = applyPresentationDraftCommand(state.present, command);
      if (authoredArtifactJSON(state.present) === authoredArtifactJSON(next)) return;
      lastEmittedArtifact.current = authoredArtifactJSON(next);
      dispatch({
        type: "commit",
        artifact: next,
        coalesceKey: options?.coalesceKey ?? "",
        timestamp: Date.now(),
      });
      onChange(next);
    },
    [onChange, state.present],
  );

  const replace = useCallback(
    (next: PresentationArtifact, options?: { coalesceKey?: string }) =>
      commit({ type: "artifact.replace", artifact: next }, options),
    [commit],
  );

  const undo = useCallback(() => {
    const previous = state.past.at(-1);
    if (!previous) return;
    lastEmittedArtifact.current = authoredArtifactJSON(previous);
    dispatch({ type: "undo" });
    onChange(previous);
  }, [onChange, state.past]);

  const redo = useCallback(() => {
    const next = state.future[0];
    if (!next) return;
    lastEmittedArtifact.current = authoredArtifactJSON(next);
    dispatch({ type: "redo" });
    onChange(next);
  }, [onChange, state.future]);

  return {
    artifact: state.present,
    commit,
    replace,
    undo,
    redo,
    canUndo: state.past.length > 0,
    canRedo: state.future.length > 0,
  };
}

function authoredArtifactJSON(artifact: PresentationArtifact) {
  return JSON.stringify({
    version: artifact.version,
    id: artifact.id,
    kind: artifact.kind,
    title: artifact.title,
    datasets: artifact.datasets ?? [],
    filters: artifact.filters ?? [],
    visualizations: artifact.visualizations ?? [],
    layout: artifact.layout ?? [],
    sections: artifact.sections ?? [],
  });
}
