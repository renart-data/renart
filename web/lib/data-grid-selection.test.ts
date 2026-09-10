import { describe, expect, it } from "vitest";

import {
  constrainDataGridSelection,
  dataGridCellSelected,
  EMPTY_DATA_GRID_SELECTION,
  moveDataGridSelection,
  selectAllDataGridCells,
  selectDataGridCell,
  selectedDataGridBounds,
  resizeDataGridSelection,
} from "@/lib/data-grid-selection";

describe("data grid selection", () => {
  it("resizes both corners, shrinks, crosses the opposite corner and clamps", () => {
    const selection = selectDataGridCell(
      selectDataGridCell(EMPTY_DATA_GRID_SELECTION, { row: 2, column: 2 }),
      { row: 4, column: 4 },
      "extend",
    );
    const bounds = { rows: 10, columns: 10 };
    expect(
      resizeDataGridSelection(selection, "end", { row: 3, column: 3 }, bounds).selected.size,
    ).toBe(4);
    expect(
      selectedDataGridBounds(
        resizeDataGridSelection(selection, "start", { row: 6, column: 6 }, bounds),
      ),
    ).toEqual({ start: { row: 4, column: 4 }, end: { row: 6, column: 6 } });
    expect(
      selectedDataGridBounds(
        resizeDataGridSelection(selection, "end", { row: -5, column: 50 }, bounds),
      ),
    ).toEqual({ start: { row: 0, column: 2 }, end: { row: 2, column: 9 } });
  });
  it("moves only the dragged edge and preserves the other dimension", () => {
    const selection = selectDataGridCell(
      selectDataGridCell(EMPTY_DATA_GRID_SELECTION, { row: 2, column: 2 }),
      { row: 4, column: 4 },
      "extend",
    );
    const bounds = { rows: 10, columns: 10 };
    expect(
      selectedDataGridBounds(
        resizeDataGridSelection(selection, "top", { row: 0, column: 0 }, bounds),
      ),
    ).toEqual({ start: { row: 0, column: 2 }, end: { row: 4, column: 4 } });
    expect(
      selectedDataGridBounds(
        resizeDataGridSelection(selection, "right", { row: 0, column: 6 }, bounds),
      ),
    ).toEqual({ start: { row: 2, column: 2 }, end: { row: 4, column: 6 } });
  });
  it("replaces, extends, and toggles cell selections", () => {
    const initial = selectDataGridCell(EMPTY_DATA_GRID_SELECTION, { row: 1, column: 1 });
    const extended = selectDataGridCell(initial, { row: 2, column: 3 }, "extend");

    expect(extended.selected.size).toBe(6);
    expect(dataGridCellSelected(extended, { row: 1, column: 1 })).toBe(true);
    expect(dataGridCellSelected(extended, { row: 2, column: 3 })).toBe(true);

    const toggled = selectDataGridCell(extended, { row: 1, column: 2 }, "toggle");
    expect(dataGridCellSelected(toggled, { row: 1, column: 2 })).toBe(false);
    expect(toggled.selected.size).toBe(5);
  });

  it("supports bounded keyboard movement and range extension", () => {
    const initial = selectDataGridCell(EMPTY_DATA_GRID_SELECTION, { row: 0, column: 0 });
    const moved = moveDataGridSelection(initial, { row: 10, column: 1 }, { rows: 3, columns: 2 });
    expect(moved.active).toEqual({ row: 2, column: 1 });

    const extended = moveDataGridSelection(
      initial,
      { row: 2, column: 1 },
      { rows: 3, columns: 2 },
      true,
    );
    expect(extended.selected.size).toBe(6);
  });

  it("selects all loaded cells and constrains a stale selection", () => {
    const all = selectAllDataGridCells({ rows: 3, columns: 2 });
    expect(all.selected.size).toBe(6);
    expect(selectedDataGridBounds(all)).toEqual({
      start: { row: 0, column: 0 },
      end: { row: 2, column: 1 },
    });

    const constrained = constrainDataGridSelection(all, { rows: 2, columns: 1 });
    expect(constrained.selected).toEqual(new Set(["0:0", "1:0"]));
    expect(constrained.active).toBeNull();
  });
});
