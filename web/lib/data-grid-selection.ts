export type DataGridCell = {
  row: number;
  column: number;
};

export type DataGridBounds = {
  rows: number;
  columns: number;
};

export type DataGridSelection = {
  anchor: DataGridCell | null;
  active: DataGridCell | null;
  selected: ReadonlySet<string>;
};

export type DataGridSelectionMode = "replace" | "extend" | "toggle";

export const EMPTY_DATA_GRID_SELECTION: DataGridSelection = {
  anchor: null,
  active: null,
  selected: new Set<string>(),
};

export function dataGridCellKey(cell: DataGridCell): string {
  return `${cell.row}:${cell.column}`;
}

export function dataGridCellSelected(selection: DataGridSelection, cell: DataGridCell): boolean {
  return selection.selected.has(dataGridCellKey(cell));
}

export function selectDataGridCell(
  selection: DataGridSelection,
  cell: DataGridCell,
  mode: DataGridSelectionMode = "replace",
): DataGridSelection {
  if (mode === "extend") {
    const anchor = selection.anchor ?? selection.active ?? cell;
    return {
      anchor,
      active: cell,
      selected: dataGridRectangle(anchor, cell),
    };
  }

  if (mode === "toggle") {
    const key = dataGridCellKey(cell);
    const selected = new Set(selection.selected);
    if (selected.has(key)) selected.delete(key);
    else selected.add(key);
    return {
      anchor: selection.anchor ?? cell,
      active: cell,
      selected,
    };
  }

  return {
    anchor: cell,
    active: cell,
    selected: new Set([dataGridCellKey(cell)]),
  };
}

export function moveDataGridSelection(
  selection: DataGridSelection,
  delta: Pick<DataGridCell, "row" | "column">,
  bounds: DataGridBounds,
  extend = false,
): DataGridSelection {
  if (bounds.rows <= 0 || bounds.columns <= 0) return EMPTY_DATA_GRID_SELECTION;
  const current = selection.active ?? { row: 0, column: 0 };
  const next = {
    row: clamp(current.row + delta.row, 0, bounds.rows - 1),
    column: clamp(current.column + delta.column, 0, bounds.columns - 1),
  };
  return selectDataGridCell(selection, next, extend ? "extend" : "replace");
}

export function selectAllDataGridCells(bounds: DataGridBounds): DataGridSelection {
  if (bounds.rows <= 0 || bounds.columns <= 0) return EMPTY_DATA_GRID_SELECTION;
  const anchor = { row: 0, column: 0 };
  const active = { row: bounds.rows - 1, column: bounds.columns - 1 };
  return {
    anchor,
    active,
    selected: dataGridRectangle(anchor, active),
  };
}

export function constrainDataGridSelection(
  selection: DataGridSelection,
  bounds: DataGridBounds,
): DataGridSelection {
  if (bounds.rows <= 0 || bounds.columns <= 0) return EMPTY_DATA_GRID_SELECTION;
  const inBounds = (cell: DataGridCell | null) =>
    cell !== null &&
    cell.row >= 0 &&
    cell.row < bounds.rows &&
    cell.column >= 0 &&
    cell.column < bounds.columns;
  const selected = new Set(
    [...selection.selected].filter((key) => {
      const cell = parseDataGridCellKey(key);
      return inBounds(cell);
    }),
  );
  if (selected.size === 0) return EMPTY_DATA_GRID_SELECTION;
  return {
    anchor: inBounds(selection.anchor) ? selection.anchor : null,
    active: inBounds(selection.active) ? selection.active : null,
    selected,
  };
}

export function selectedDataGridBounds(
  selection: DataGridSelection,
): { start: DataGridCell; end: DataGridCell } | null {
  if (selection.selected.size === 0) return null;
  let minRow = Number.POSITIVE_INFINITY;
  let maxRow = Number.NEGATIVE_INFINITY;
  let minColumn = Number.POSITIVE_INFINITY;
  let maxColumn = Number.NEGATIVE_INFINITY;
  for (const key of selection.selected) {
    const cell = parseDataGridCellKey(key);
    if (!cell) continue;
    minRow = Math.min(minRow, cell.row);
    maxRow = Math.max(maxRow, cell.row);
    minColumn = Math.min(minColumn, cell.column);
    maxColumn = Math.max(maxColumn, cell.column);
  }
  if (!Number.isFinite(minRow)) return null;
  return {
    start: { row: minRow, column: minColumn },
    end: { row: maxRow, column: maxColumn },
  };
}

export function parseDataGridCellKey(key: string): DataGridCell | null {
  const separator = key.indexOf(":");
  if (separator < 1) return null;
  const row = Number(key.slice(0, separator));
  const column = Number(key.slice(separator + 1));
  return Number.isInteger(row) && Number.isInteger(column) ? { row, column } : null;
}

function dataGridRectangle(start: DataGridCell, end: DataGridCell): ReadonlySet<string> {
  const selected = new Set<string>();
  const minRow = Math.min(start.row, end.row);
  const maxRow = Math.max(start.row, end.row);
  const minColumn = Math.min(start.column, end.column);
  const maxColumn = Math.max(start.column, end.column);
  for (let row = minRow; row <= maxRow; row += 1) {
    for (let column = minColumn; column <= maxColumn; column += 1) {
      selected.add(dataGridCellKey({ row, column }));
    }
  }
  return selected;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(Math.max(value, minimum), maximum);
}
