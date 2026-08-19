import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { VirtualDataTable, virtualRowWindow } from "@/components/virtual-data-table";

describe("virtualRowWindow", () => {
  it("keeps a bounded overscanned window around the viewport", () => {
    expect(
      virtualRowWindow({
        rowCount: 1_000,
        scrollTop: 2_700,
        viewportHeight: 270,
        rowHeight: 27,
        overscan: 2,
      }),
    ).toEqual({
      start: 98,
      end: 112,
      topSpacerHeight: 2_646,
      bottomSpacerHeight: 23_976,
    });
  });

  it("clamps the window at the end of the result", () => {
    expect(
      virtualRowWindow({
        rowCount: 100,
        scrollTop: 10_000,
        viewportHeight: 270,
        rowHeight: 27,
      }),
    ).toEqual({
      start: 93,
      end: 100,
      topSpacerHeight: 2_511,
      bottomSpacerHeight: 0,
    });
  });
});

describe("VirtualDataTable", () => {
  it("renders only the initial row window for a large result", () => {
    const rows = Array.from({ length: 1_000 }, (_, index) => ({ value: `row-${index}` }));
    const markup = renderToStaticMarkup(
      <VirtualDataTable ariaLabel="Large result" columns={["value"]} rows={rows} />,
    );

    expect(markup).toContain('aria-label="Large result"');
    expect(markup).toContain('aria-rowcount="1001"');
    expect(markup).toContain('data-virtualized="true"');
    expect(markup).toContain("row-0");
    expect(markup).not.toContain("row-500");
    expect(markup.match(/data-row-index=/g)?.length).toBeLessThan(30);
  });

  it("keeps small results as ordinary semantic tables", () => {
    const markup = renderToStaticMarkup(
      <VirtualDataTable
        ariaLabel="Small result"
        columns={["value"]}
        rows={[{ value: "one" }, { value: "two" }]}
      />,
    );

    expect(markup).not.toContain("data-virtualized");
    expect(markup).toContain("one");
    expect(markup).toContain("two");
  });

  it("keeps duplicate display columns mapped to distinct values", () => {
    const markup = renderToStaticMarkup(
      <VirtualDataTable
        ariaLabel="Duplicate columns"
        columns={["value", "value"]}
        columnKeys={["column_0", "column_1"]}
        rows={[{ column_0: "first", column_1: "second" }]}
      />,
    );

    expect(markup.match(/>value</g)).toHaveLength(2);
    expect(markup).toContain("first");
    expect(markup).toContain("second");
  });
});
