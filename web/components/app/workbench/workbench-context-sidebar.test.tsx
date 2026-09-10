import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AppContextSidebarTransition } from "./workbench-context-sidebar";

describe("AppContextSidebarTransition", () => {
  it.each([
    ["forward", "slide-in-from-right-2"],
    ["back", "slide-in-from-left-2"],
    ["replace", "duration-150"],
  ] as const)("renders the %s hierarchy motion", (direction, expectedClass) => {
    const markup = renderToStaticMarkup(
      <AppContextSidebarTransition viewKey="source:path" direction={direction}>
        Sidebar view
      </AppContextSidebarTransition>,
    );

    expect(markup).toContain(`data-direction="${direction}"`);
    expect(markup).toContain('data-transition-key="source:path"');
    expect(markup).toContain(expectedClass);
    expect(markup).toContain("motion-reduce:animate-none");
  });
});
