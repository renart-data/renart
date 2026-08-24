import { describe, expect, it } from "vitest";

import {
  constrainDataGridSelection,
  dataGridCellSelected,
  EMPTY_DATA_GRID_SELECTION,
  moveDataGridSelection,
  selectAllDataGridCells,
  selectDataGridCell,
  selectedDataGridBounds,
} from "@/lib/data-grid-selection";

describe("data grid selection", () => {
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
