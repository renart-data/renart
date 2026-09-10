import type { editor } from "monaco-editor";

type AnnotationDiffEditor = Pick<editor.IDiffEditor, "getLineChanges" | "onDidUpdateDiff"> & {
  getModifiedEditor(): Pick<editor.ICodeEditor, "revealLineInCenter">;
};

// Inline diff computation changes view zones and synchronizes scroll positions.
// Reveal only after that first update, then leave subsequent user scrolling alone.
export function revealDiffAnnotation(
  diff: AnnotationDiffEditor,
  line: number,
  scrollType: editor.ScrollType,
): () => void {
  let cancelled = false;
  let queued = false;
  const revealWhenReady = () => {
    if (cancelled || queued || diff.getLineChanges() === null) return;
    queued = true;
    queueMicrotask(() => {
      if (!cancelled) diff.getModifiedEditor().revealLineInCenter(line, scrollType);
      subscription.dispose();
    });
  };
  const subscription = diff.onDidUpdateDiff(revealWhenReady);
  revealWhenReady();
  return () => {
    cancelled = true;
    subscription.dispose();
  };
}
