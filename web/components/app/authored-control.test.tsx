import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AuthoredControlValueField } from "@/components/app/authored-control";

describe("AuthoredControlValueField", () => {
  it("keeps an explicitly empty dataset-backed select constrained", () => {
    const markup = renderToStaticMarkup(
      <AuthoredControlValueField
        control={{
          id: "region",
          label: "Region",
          type: "select",
          default: "eu",
          options: { dataset: "source01", value_field: "code" },
        }}
        value="eu"
        options={[]}
        onChange={() => undefined}
      />,
    );

    expect(markup).toContain("disabled");
    expect(markup).not.toContain('type="text"');
  });

  it("retains free text authoring before a dataset snapshot exists", () => {
    const markup = renderToStaticMarkup(
      <AuthoredControlValueField
        control={{
          id: "region",
          label: "Region",
          type: "select",
          default: "eu",
          options: { dataset: "source01", value_field: "code" },
        }}
        value="eu"
        label="Default value"
        onChange={() => undefined}
      />,
    );

    expect(markup).toContain('value="eu"');
    expect(markup).toContain('type="text"');
  });
});
