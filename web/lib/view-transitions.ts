// The desktop window (renart-gui) runs WebKitGTK on Linux with accelerated
// compositing turned off, and in that mode WebKitGTK stops painting after any
// view transition: the DOM keeps updating, but the window stays blank. Wails
// tags its webview's user agent with "wails.io", so the desktop window skips
// view transitions and browsers keep them.
export function viewTransitionsSupported(userAgent: string) {
  return !/\bwails\.io\//.test(userAgent);
}

export const viewTransitionsEnabled =
  typeof navigator === "undefined" || viewTransitionsSupported(navigator.userAgent);

// globals.css opts same-origin document navigations into a cross-document
// view transition. The page being left decides, so opting out at startup
// covers every navigation the app can make.
export function optOutOfUnsupportedViewTransitions() {
  if (viewTransitionsEnabled) return;
  const style = document.createElement("style");
  style.textContent = "@view-transition { navigation: none; }";
  document.head.append(style);
}
