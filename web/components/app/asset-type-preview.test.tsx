import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AssetTypePreview } from "@/components/app/asset-type-preview";

describe("AssetTypePreview", () => {
  it("uses the shared SQL glyph in the asset creation picker", () => {
    const markup = renderToStaticMarkup(<AssetTypePreview type="sql" />);

    expect(markup).toContain('data-code-type-glyph="sql"');
    expect(markup).toContain("SQL");
  });

  it("uses the shared Python logo in the asset creation picker", () => {
    const markup = renderToStaticMarkup(<AssetTypePreview type="python" />);

    expect(markup).toContain('data-code-type-glyph="python"');
    expect(markup).toContain("<svg");
    expect(markup).toContain('fill="none"');
    expect(markup).toContain('stroke="currentColor"');
  });
});
