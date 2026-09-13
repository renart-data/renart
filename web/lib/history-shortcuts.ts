type HistoryKeyEvent = Pick<
  KeyboardEvent,
  | "key"
  | "altKey"
  | "ctrlKey"
  | "metaKey"
  | "shiftKey"
  | "isComposing"
  | "defaultPrevented"
  | "preventDefault"
>;

// Webviews do not consistently supply browser-chrome shortcuts. Use the same
// router history (including unsaved-change blockers), not a second route stack.
export function handleHistoryShortcut(
  event: HistoryKeyEvent,
  history: { back: () => void; forward: () => void },
) {
  if (
    event.defaultPrevented ||
    event.isComposing ||
    !event.altKey ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey
  )
    return;
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  if (event.key === "ArrowLeft") history.back();
  else history.forward();
}
