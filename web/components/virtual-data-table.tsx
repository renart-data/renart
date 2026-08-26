"use client";

import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Check,
  Copy,
  Loader2,
  Maximize2,
  X,
} from "lucide-react";
import {
  KeyboardEvent,
  PointerEvent,
  UIEvent,
  WheelEventHandler,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { Button } from "@/components/ui/button";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { copyTextToClipboard } from "@/lib/copy-to-clipboard";
import {
  constrainDataGridSelection,
  DataGridCell,
  DataGridSelection,
  dataGridCellKey,
  dataGridCellSelected,
  EMPTY_DATA_GRID_SELECTION,
  moveDataGridSelection,
  selectAllDataGridCells,
  selectDataGridCell,
  selectedDataGridBounds,
} from "@/lib/data-grid-selection";
import { cn } from "@/lib/utils";

const tableScrollPositions = new Map<string, { top: number; left: number }>();
const VIRTUALIZE_AFTER_ROWS = 50;
const VIRTUAL_ROW_OVERSCAN = 6;

export type VirtualTableRenderMeasurement = {
  durationMs: number;
  totalRows: number;
  renderedRows: number;
};

export type VirtualRowWindow = {
  start: number;
  end: number;
  topSpacerHeight: number;
  bottomSpacerHeight: number;
};

export function virtualRowWindow({
  rowCount,
  scrollTop,
  viewportHeight,
  rowHeight,
  overscan = VIRTUAL_ROW_OVERSCAN,
}: {
  rowCount: number;
  scrollTop: number;
  viewportHeight: number;
  rowHeight: number;
  overscan?: number;
}): VirtualRowWindow {
  if (rowCount <= 0) {
    return { start: 0, end: 0, topSpacerHeight: 0, bottomSpacerHeight: 0 };
  }

  const safeRowHeight = Math.max(1, rowHeight);
  const safeViewportHeight = Math.max(safeRowHeight, viewportHeight);
  const firstVisible = Math.min(rowCount - 1, Math.floor(Math.max(0, scrollTop) / safeRowHeight));
  const visibleCount = Math.ceil(safeViewportHeight / safeRowHeight);
  const start = Math.max(0, firstVisible - Math.max(0, overscan));
  const end = Math.min(rowCount, firstVisible + visibleCount + Math.max(0, overscan));

  return {
    start,
    end,
    topSpacerHeight: start * safeRowHeight,
    bottomSpacerHeight: Math.max(0, rowCount - end) * safeRowHeight,
  };
}

type Props = {
  columns: string[];
  columnKeys?: string[];
  rows: Record<string, unknown>[];
  height?: number | string;
  dense?: boolean;
  loading?: boolean;
  canLoadMore?: boolean;
  onLoadMore?: () => void;
  emptyLabel?: string;
  autoLoadMore?: boolean;
  scrollKey?: string;
  onWheelCapture?: WheelEventHandler<HTMLDivElement>;
  frameless?: boolean;
  ariaLabel?: string;
  viewportClassName?: string;
  onRenderMeasured?: (measurement: VirtualTableRenderMeasurement) => void;
};

export function VirtualDataTable({
  columns,
  columnKeys,
  rows,
  height = 224,
  dense = false,
  loading = false,
  canLoadMore = false,
  onLoadMore,
  emptyLabel = "No rows returned.",
  autoLoadMore = false,
  scrollKey,
  onWheelCapture,
  frameless = false,
  ariaLabel,
  viewportClassName,
  onRenderMeasured,
}: Props) {
  const fillAvailableHeight = typeof height === "string";
  const rowHeight = dense ? 23 : 27;
  const scrollContainerRef = useRef<HTMLDivElement | null>(null);
  const loadMoreRequestedRef = useRef(false);
  const scrollSnapshotRef = useRef({ top: 0, left: 0, height: 0 });
  const measuredRowsRef = useRef<Record<string, unknown>[] | null>(null);
  const renderStartedAtRef = useRef<number | null>(null);
  const onRenderMeasuredRef = useRef(onRenderMeasured);
  const cellRefs = useRef(new Map<string, HTMLButtonElement>());
  const draggingRef = useRef(false);
  const dragAnchorRef = useRef<DataGridCell | null>(null);
  const [copied, setCopied] = useState(false);
  const [nearBottom, setNearBottom] = useState(true);
  const [selection, setSelection] = useState<DataGridSelection>(() => EMPTY_DATA_GRID_SELECTION);
  const selectionRef = useRef(selection);
  const [viewportMetrics, setViewportMetrics] = useState({
    scrollTop: scrollKey ? (tableScrollPositions.get(scrollKey)?.top ?? 0) : 0,
    height: typeof height === "number" ? height : 224,
  });

  onRenderMeasuredRef.current = onRenderMeasured;
  if (
    measuredRowsRef.current !== rows &&
    typeof performance !== "undefined" &&
    typeof performance.now === "function"
  ) {
    measuredRowsRef.current = rows;
    renderStartedAtRef.current = performance.now();
  }

  const fallbackColumns = useMemo(() => {
    if (columns.length > 0) {
      return columns;
    }
    const firstRow = rows[0];
    return firstRow ? Object.keys(firstRow) : [];
  }, [columns, rows]);
  const shouldVirtualize = rows.length > VIRTUALIZE_AFTER_ROWS;
  const resolvedColumnKeys = useMemo(
    () => (columnKeys?.length === fallbackColumns.length ? [...columnKeys] : [...fallbackColumns]),
    [columnKeys, fallbackColumns],
  );
  const rowWindow = useMemo(
    () =>
      shouldVirtualize
        ? virtualRowWindow({
            rowCount: rows.length,
            scrollTop: viewportMetrics.scrollTop,
            viewportHeight: viewportMetrics.height,
            rowHeight,
          })
        : {
            start: 0,
            end: rows.length,
            topSpacerHeight: 0,
            bottomSpacerHeight: 0,
          },
    [rowHeight, rows.length, shouldVirtualize, viewportMetrics.height, viewportMetrics.scrollTop],
  );
  const visibleRows = rows.slice(rowWindow.start, rowWindow.end);
  selectionRef.current = selection;

  useEffect(() => {
    setSelection((current) =>
      constrainDataGridSelection(current, {
        rows: rows.length,
        columns: fallbackColumns.length,
      }),
    );
  }, [fallbackColumns.length, rows.length]);

  useEffect(() => {
    const stopDragging = () => {
      draggingRef.current = false;
      dragAnchorRef.current = null;
    };
    window.addEventListener("pointerup", stopDragging);
    window.addEventListener("pointercancel", stopDragging);
    return () => {
      window.removeEventListener("pointerup", stopDragging);
      window.removeEventListener("pointercancel", stopDragging);
    };
  }, []);

  const triggerLoadMore = () => {
    if (!canLoadMore || !onLoadMore || loading || loadMoreRequestedRef.current) {
      return;
    }

    loadMoreRequestedRef.current = true;
    onLoadMore();
  };

  const updateNearBottom = (element: HTMLDivElement) => {
    const remaining = element.scrollHeight - element.scrollTop - element.clientHeight;
    setNearBottom(remaining < 96);
    return remaining;
  };

  const copyTable = async (selectedOnly = selectionRef.current.selected.size > 0) => {
    const currentSelection = selectionRef.current;
    const serialized = selectedOnly
      ? serializeSelectedCells(currentSelection, resolvedColumnKeys, rows)
      : {
          text: serializeRowsAsTsv(fallbackColumns, resolvedColumnKeys, rows),
          html: serializeRowsAsHtmlTable(fallbackColumns, resolvedColumnKeys, rows),
        };
    if (!serialized) return;

    if (await writeTableToClipboard(serialized)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    }
  };

  const focusCell = (cell: DataGridCell) => {
    const viewport = scrollContainerRef.current;
    if (viewport) {
      const rowTop = (cell.row + 1) * rowHeight;
      const rowBottom = rowTop + rowHeight;
      if (rowTop < viewport.scrollTop + rowHeight) {
        viewport.scrollTop = Math.max(0, rowTop - rowHeight);
      } else if (rowBottom > viewport.scrollTop + viewport.clientHeight) {
        viewport.scrollTop = rowBottom - viewport.clientHeight;
      }
      setViewportMetrics((current) => ({
        scrollTop: viewport.scrollTop,
        height: viewport.clientHeight || current.height,
      }));
    }

    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        cellRefs.current.get(dataGridCellKey(cell))?.focus({ preventScroll: true });
      });
    });
  };

  const adjustSelection = (movement: { row: number; column: number }) => {
    const bounds = { rows: rows.length, columns: fallbackColumns.length };
    const current = selectionRef.current.active
      ? selectionRef.current
      : selectDataGridCell(selectionRef.current, { row: 0, column: 0 });
    const next = moveDataGridSelection(current, movement, bounds, true);
    selectionRef.current = next;
    setSelection(next);
  };

  const selectAllCells = () => {
    const next = selectAllDataGridCells({
      rows: rows.length,
      columns: fallbackColumns.length,
    });
    selectionRef.current = next;
    setSelection(next);
  };

  const clearSelection = () => {
    selectionRef.current = EMPTY_DATA_GRID_SELECTION;
    setSelection(EMPTY_DATA_GRID_SELECTION);
  };

  const handleCellPointerDown = (event: PointerEvent<HTMLButtonElement>, cell: DataGridCell) => {
    if (event.button !== 0) return;
    const mode = event.shiftKey ? "extend" : event.metaKey || event.ctrlKey ? "toggle" : "replace";
    const next = selectDataGridCell(selectionRef.current, cell, mode);
    selectionRef.current = next;
    setSelection(next);
    draggingRef.current = true;
    dragAnchorRef.current = mode === "extend" ? next.anchor : cell;
  };

  const handleCellPointerEnter = (event: PointerEvent<HTMLButtonElement>, cell: DataGridCell) => {
    const anchor = dragAnchorRef.current;
    if (!draggingRef.current || !anchor || (event.buttons & 1) === 0) return;
    const next = selectDataGridCell({ ...selectionRef.current, anchor }, cell, "extend");
    selectionRef.current = next;
    setSelection(next);
  };

  const handleCellKeyDown = (event: KeyboardEvent<HTMLButtonElement>, cell: DataGridCell) => {
    const bounds = { rows: rows.length, columns: fallbackColumns.length };
    const movement =
      event.key === "ArrowUp"
        ? { row: -1, column: 0 }
        : event.key === "ArrowDown"
          ? { row: 1, column: 0 }
          : event.key === "ArrowLeft"
            ? { row: 0, column: -1 }
            : event.key === "ArrowRight"
              ? { row: 0, column: 1 }
              : null;
    if (movement) {
      event.preventDefault();
      const current = selectionRef.current.active
        ? selectionRef.current
        : selectDataGridCell(selectionRef.current, cell);
      const next = moveDataGridSelection(current, movement, bounds, event.shiftKey);
      selectionRef.current = next;
      setSelection(next);
      if (next.active) focusCell(next.active);
      return;
    }
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "a") {
      event.preventDefault();
      const next = selectAllDataGridCells(bounds);
      selectionRef.current = next;
      setSelection(next);
      return;
    }
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "c") {
      event.preventDefault();
      void copyTable(true);
      return;
    }
    if (event.key === " ") {
      event.preventDefault();
      const next = selectDataGridCell(selectionRef.current, cell, "toggle");
      selectionRef.current = next;
      setSelection(next);
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      selectionRef.current = EMPTY_DATA_GRID_SELECTION;
      setSelection(EMPTY_DATA_GRID_SELECTION);
    }
  };

  const handleScroll = (event: UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    const scrollTop = element.scrollTop;
    const viewportHeight = element.clientHeight;
    scrollSnapshotRef.current = {
      top: scrollTop,
      left: element.scrollLeft,
      height: element.scrollHeight,
    };
    setViewportMetrics((current) => {
      const next = {
        scrollTop,
        height: viewportHeight || current.height,
      };
      return current.scrollTop === next.scrollTop && current.height === next.height
        ? current
        : next;
    });

    if (scrollKey) {
      tableScrollPositions.set(scrollKey, {
        top: scrollTop,
        left: element.scrollLeft,
      });
    }

    if (!autoLoadMore || !canLoadMore || !onLoadMore) {
      updateNearBottom(element);
      return;
    }

    const remaining = updateNearBottom(element);
    if (remaining < 64) {
      triggerLoadMore();
    }
  };

  if (!loading) {
    loadMoreRequestedRef.current = false;
  }

  useLayoutEffect(() => {
    const element = scrollContainerRef.current;
    if (!element) {
      return;
    }

    if (!loadMoreRequestedRef.current && !scrollKey) {
      return;
    }

    const savedPosition = scrollKey ? tableScrollPositions.get(scrollKey) : null;

    if (savedPosition) {
      element.scrollTop = savedPosition.top;
      element.scrollLeft = savedPosition.left;
      setViewportMetrics((current) => ({
        scrollTop: savedPosition.top,
        height: element.clientHeight || current.height,
      }));
      return;
    }

    if (loadMoreRequestedRef.current) {
      element.scrollTop = scrollSnapshotRef.current.top;
      element.scrollLeft = scrollSnapshotRef.current.left;
    }
  }, [rows.length, scrollKey]);

  useLayoutEffect(() => {
    const element = scrollContainerRef.current;
    if (!element) {
      return;
    }
    const updateSize = () => {
      setViewportMetrics((current) => {
        const height = element.clientHeight || current.height;
        return current.height === height ? current : { ...current, height };
      });
    };
    updateSize();
    if (typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(updateSize);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const element = scrollContainerRef.current;
    if (element) {
      updateNearBottom(element);
    }
  }, [rows.length, loading]);

  useEffect(() => {
    const startedAt = renderStartedAtRef.current;
    if (startedAt === null || !onRenderMeasuredRef.current || typeof window === "undefined") {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      if (renderStartedAtRef.current !== startedAt) {
        return;
      }
      renderStartedAtRef.current = null;
      onRenderMeasuredRef.current?.({
        durationMs: performance.now() - startedAt,
        totalRows: rows.length,
        renderedRows: visibleRows.length,
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [rows, visibleRows.length]);

  const showLoadMoreControl = Boolean(
    onLoadMore && (canLoadMore || loading) && (nearBottom || loading),
  );

  return (
    <div
      className={cn(
        "group/table relative overflow-hidden bg-background",
        !frameless && "rounded border",
        fillAvailableHeight && "flex h-full min-h-0 flex-col",
      )}
    >
      {rows.length > 0 && fallbackColumns.length > 0 ? (
        <Button
          aria-label={selection.selected.size > 0 ? "Copy selected cells" : "Copy table"}
          className="absolute right-2 top-2 z-30 h-7 gap-1.5 bg-background/90 px-2 text-[11px] opacity-0 shadow-sm backdrop-blur transition-opacity group-hover/table:opacity-100 focus-visible:opacity-100"
          onClick={() => void copyTable()}
          size="sm"
          type="button"
          variant="outline"
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
          <span>
            {copied
              ? "Copied"
              : selection.selected.size > 0
                ? `Copy ${selection.selected.size}`
                : "Copy"}
          </span>
        </Button>
      ) : null}

      {selection.selected.size > 0 ? (
        <div
          aria-label="Table selection controls"
          className="mobile-data-grid-selection-controls absolute bottom-2 right-2 z-30 items-center gap-0.5 rounded-lg border bg-background/95 p-1 shadow-md backdrop-blur"
          data-testid="mobile-table-selection-controls"
          role="toolbar"
        >
          <span
            className="px-1.5 text-[10px] tabular-nums text-muted-foreground"
            aria-live="polite"
          >
            {selection.selected.size} selected
          </span>
          <Button
            aria-label="Adjust selection up"
            onClick={() => adjustSelection({ row: -1, column: 0 })}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <ArrowUp />
          </Button>
          <Button
            aria-label="Adjust selection down"
            onClick={() => adjustSelection({ row: 1, column: 0 })}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <ArrowDown />
          </Button>
          <Button
            aria-label="Adjust selection left"
            onClick={() => adjustSelection({ row: 0, column: -1 })}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <ArrowLeft />
          </Button>
          <Button
            aria-label="Adjust selection right"
            onClick={() => adjustSelection({ row: 0, column: 1 })}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <ArrowRight />
          </Button>
          <Button
            aria-label="Select all cells"
            onClick={selectAllCells}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <Maximize2 />
          </Button>
          <Button
            aria-label="Copy selection"
            onClick={() => void copyTable(true)}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            {copied ? <Check /> : <Copy />}
          </Button>
          <Button
            aria-label="Clear selection"
            onClick={clearSelection}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <X />
          </Button>
        </div>
      ) : null}

      {loading ? (
        <div className="pointer-events-none absolute right-2 top-10 z-20 rounded bg-background/90 p-1 text-muted-foreground shadow-sm">
          <Loader2 className="size-3.5 animate-spin" />
        </div>
      ) : null}

      {showLoadMoreControl ? (
        <Button
          className="absolute bottom-3 left-1/2 z-30 h-8 -translate-x-1/2 gap-2 bg-background/95 px-3 text-[11px] shadow-md backdrop-blur disabled:opacity-70"
          disabled={!canLoadMore || loading}
          onClick={triggerLoadMore}
          size="sm"
          type="button"
          variant="outline"
        >
          {loading ? <Loader2 className="size-3 animate-spin" /> : null}
          {loading ? "Loading more rows..." : "Load more rows"}
        </Button>
      ) : null}

      <ScrollArea
        className={fillAvailableHeight ? "min-h-0 flex-1" : "h-fit"}
        horizontalScrollBarClassName="left-12 w-[calc(100%-3rem)]"
        viewportClassName={cn(
          !fillAvailableHeight && !viewportClassName && "max-h-56",
          viewportClassName,
        )}
        viewportStyle={
          !fillAvailableHeight && typeof height === "number" ? { maxHeight: height } : undefined
        }
        viewportRef={scrollContainerRef}
        onViewportScroll={handleScroll}
        onWheelCapture={onWheelCapture}
      >
        <table
          aria-label={ariaLabel}
          aria-multiselectable="true"
          aria-rowcount={rows.length + 1}
          className="min-w-full border-collapse text-xs"
          role="grid"
        >
          <thead className="sticky top-0 z-10 bg-muted/70 backdrop-blur">
            <tr aria-rowindex={1}>
              <th
                className={cn(
                  "sticky left-0 z-30 w-12 min-w-12 border-b border-r bg-muted/95 text-right font-medium text-muted-foreground backdrop-blur",
                  dense ? "px-2 py-1" : "px-2 py-1.5",
                )}
              >
                #
              </th>
              {fallbackColumns.map((column, columnIndex) => (
                <th
                  className={cn(
                    "sticky top-0 z-20 w-56 min-w-32 max-w-56 border-b border-r bg-muted/90 text-left font-medium whitespace-nowrap backdrop-blur last:border-r-0",
                    dense ? "px-2 py-1" : "px-2 py-1.5",
                  )}
                  key={`${column}-${columnIndex}`}
                >
                  <div className="w-56 max-w-56 truncate">{column}</div>
                </th>
              ))}
            </tr>
          </thead>
          <tbody data-virtualized={shouldVirtualize || undefined}>
            {rows.length > 0 ? (
              <>
                {rowWindow.topSpacerHeight > 0 ? (
                  <tr aria-hidden="true" role="presentation">
                    <td
                      className="border-0 p-0"
                      colSpan={Math.max(1, fallbackColumns.length + 1)}
                      style={{ height: rowWindow.topSpacerHeight }}
                    />
                  </tr>
                ) : null}
                {visibleRows.map((row, visibleRowIndex) => {
                  const rowIndex = rowWindow.start + visibleRowIndex;
                  return (
                    <tr
                      aria-rowindex={rowIndex + 2}
                      className={cn(rowIndex % 2 === 0 && "bg-muted/20")}
                      data-row-index={rowIndex}
                      key={rowIndex}
                      style={{ height: rowHeight }}
                    >
                      <td
                        className={cn(
                          "sticky left-0 z-10 w-12 min-w-12 border-b border-r bg-muted/75 text-right font-mono text-[11px] text-muted-foreground backdrop-blur",
                          dense ? "px-2 py-0.5" : "px-2 py-1",
                        )}
                      >
                        {rowIndex + 1}
                      </td>
                      {fallbackColumns.map((column, columnIndex) => {
                        const cell = formatCell(row[resolvedColumnKeys[columnIndex]]);
                        const coordinate = { row: rowIndex, column: columnIndex };
                        const selected = dataGridCellSelected(selection, coordinate);
                        const active =
                          selection.active?.row === rowIndex &&
                          selection.active.column === columnIndex;

                        return (
                          <td
                            aria-selected={selected}
                            data-grid-cell-selected={selected || undefined}
                            key={`${rowIndex}-${columnIndex}`}
                            className={cn(
                              "w-56 min-w-32 max-w-56 border-b border-r p-0 align-top last:border-r-0",
                              selected && "bg-primary/10 ring-1 ring-inset ring-primary/35",
                            )}
                            role="gridcell"
                          >
                            <TableCellContent
                              active={active}
                              cell={cell}
                              column={column}
                              coordinate={coordinate}
                              dense={dense}
                              selected={selected}
                              setRef={(element) => {
                                const key = dataGridCellKey(coordinate);
                                if (element) cellRefs.current.set(key, element);
                                else cellRefs.current.delete(key);
                              }}
                              tabIndex={
                                active || (!selection.active && rowIndex === 0 && columnIndex === 0)
                                  ? 0
                                  : -1
                              }
                              onFocus={() => {
                                if (selectionRef.current.selected.size > 0) return;
                                const next = selectDataGridCell(selectionRef.current, coordinate);
                                selectionRef.current = next;
                                setSelection(next);
                              }}
                              onKeyDown={(event) => handleCellKeyDown(event, coordinate)}
                              onPointerDown={(event) => handleCellPointerDown(event, coordinate)}
                              onPointerEnter={(event) => handleCellPointerEnter(event, coordinate)}
                            />
                          </td>
                        );
                      })}
                    </tr>
                  );
                })}
                {rowWindow.bottomSpacerHeight > 0 ? (
                  <tr aria-hidden="true" role="presentation">
                    <td
                      className="border-0 p-0"
                      colSpan={Math.max(1, fallbackColumns.length + 1)}
                      style={{ height: rowWindow.bottomSpacerHeight }}
                    />
                  </tr>
                ) : null}
              </>
            ) : (
              <tr>
                <td
                  className="p-3 text-xs text-muted-foreground"
                  colSpan={Math.max(1, fallbackColumns.length + 1)}
                >
                  {emptyLabel}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </ScrollArea>
    </div>
  );
}

type FormattedCell = {
  value: string;
  className: string;
  detailValue: string;
  detailKind: "json" | "text";
};

function TableCellContent({
  active,
  cell,
  column,
  coordinate,
  dense,
  selected,
  setRef,
  tabIndex,
  onFocus,
  onKeyDown,
  onPointerDown,
  onPointerEnter,
}: {
  active: boolean;
  cell: FormattedCell;
  column: string;
  coordinate: DataGridCell;
  dense: boolean;
  selected: boolean;
  setRef: (element: HTMLButtonElement | null) => void;
  tabIndex: number;
  onFocus: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
  onPointerDown: (event: PointerEvent<HTMLButtonElement>) => void;
  onPointerEnter: (event: PointerEvent<HTMLButtonElement>) => void;
}) {
  const [detailOpen, setDetailOpen] = useState(false);

  useEffect(() => {
    if (!selected) setDetailOpen(false);
  }, [selected]);

  const trigger = (
    <button
      aria-label={`${column}, row ${coordinate.row + 1}: ${cell.value}`}
      className={cn(
        "block w-full min-w-0 select-none truncate text-left outline-none",
        dense ? "px-2 py-0.5" : "px-2 py-1",
        cell.className,
        active && "ring-2 ring-inset ring-primary/70",
      )}
      data-grid-column-index={coordinate.column}
      data-grid-cell
      data-grid-row-index={coordinate.row}
      ref={setRef}
      tabIndex={tabIndex}
      type="button"
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      onPointerDown={onPointerDown}
      onPointerEnter={onPointerEnter}
      onPointerLeave={() => setDetailOpen(false)}
    >
      {cell.value}
    </button>
  );

  return (
    <HoverCard
      closeDelay={80}
      open={selected && detailOpen}
      openDelay={350}
      onOpenChange={(open) => setDetailOpen(selected && open)}
    >
      <HoverCardTrigger asChild>{trigger}</HoverCardTrigger>
      <HoverCardContent
        align="start"
        className="w-max min-w-32 max-w-[min(36rem,calc(100vw-2rem))] p-0"
      >
        <ScrollArea
          className="max-h-[min(24rem,calc(100vh-4rem))]"
          viewportClassName="max-h-[min(24rem,calc(100vh-4rem))] p-3"
        >
          {cell.detailKind === "json" ? (
            <JsonPreview value={cell.detailValue} />
          ) : (
            <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">
              {cell.detailValue}
            </pre>
          )}
        </ScrollArea>
      </HoverCardContent>
    </HoverCard>
  );
}

async function writeTableToClipboard({ html, text }: { html: string; text: string }) {
  try {
    if (typeof ClipboardItem !== "undefined" && navigator.clipboard?.write) {
      await navigator.clipboard.write([
        new ClipboardItem({
          "text/html": new Blob([html], { type: "text/html" }),
          "text/plain": new Blob([text], { type: "text/plain" }),
        }),
      ]);
      return true;
    }
  } catch {
    // Fall back to plain text below.
  }

  return copyTextToClipboard(text);
}

function JsonPreview({ value }: { value: string }) {
  return (
    <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">
      {tokenizeJson(value).map((token, index) => (
        <span className={token.className} key={index}>
          {token.value}
        </span>
      ))}
    </pre>
  );
}

function formatCell(value: unknown): FormattedCell {
  const base = formatCellValue(value);
  const detail = formatCellDetail(value, base.value);

  return {
    ...base,
    detailValue: detail.value,
    detailKind: detail.kind,
  };
}

function formatCellValue(value: unknown): { value: string; className: string } {
  if (value === null || value === undefined) {
    return { value: "null", className: "text-muted-foreground italic" };
  }

  if (typeof value === "number") {
    return { value: String(value), className: "text-primary font-medium" };
  }

  if (typeof value === "boolean") {
    return {
      value: value ? "true" : "false",
      className: value ? "text-primary" : "text-destructive",
    };
  }

  if (typeof value === "string" && looksLikeDate(value)) {
    return { value, className: "text-sky-700 dark:text-sky-300" };
  }

  if (value instanceof Date) {
    return {
      value: value.toISOString(),
      className: "text-sky-700 dark:text-sky-300",
    };
  }

  if (typeof value === "object") {
    return {
      value: JSON.stringify(value),
      className: "text-muted-foreground",
    };
  }

  return { value: String(value), className: "text-foreground" };
}

function formatCellDetail(
  value: unknown,
  fallback: string,
): { value: string; kind: "json" | "text" } {
  if (value !== null && typeof value === "object") {
    return { value: JSON.stringify(value, null, 2), kind: "json" };
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (
      (trimmed.startsWith("{") && trimmed.endsWith("}")) ||
      (trimmed.startsWith("[") && trimmed.endsWith("]"))
    ) {
      try {
        return { value: JSON.stringify(JSON.parse(trimmed), null, 2), kind: "json" };
      } catch {
        return { value, kind: "text" };
      }
    }
    return { value, kind: "text" };
  }

  return { value: fallback, kind: "text" };
}

export function serializeSelectedCells(
  selection: DataGridSelection,
  columnKeys: string[],
  rows: Record<string, unknown>[],
): { text: string; html: string } | null {
  const bounds = selectedDataGridBounds(selection);
  if (!bounds) return null;
  const selectedRows: string[][] = [];
  for (let rowIndex = bounds.start.row; rowIndex <= bounds.end.row; rowIndex += 1) {
    const values: string[] = [];
    for (
      let columnIndex = bounds.start.column;
      columnIndex <= bounds.end.column;
      columnIndex += 1
    ) {
      if (!selection.selected.has(dataGridCellKey({ row: rowIndex, column: columnIndex }))) {
        values.push("");
        continue;
      }
      const value = rows[rowIndex]?.[columnKeys[columnIndex]];
      values.push(formatCellDetail(value, formatCellValue(value).value).value);
    }
    selectedRows.push(values);
  }
  return {
    text: selectedRows.map((row) => row.map(escapeTsvValue).join("\t")).join("\n"),
    html: `<table><tbody>${selectedRows
      .map((row) => `<tr>${row.map((value) => `<td>${escapeHtml(value)}</td>`).join("")}</tr>`)
      .join("")}</tbody></table>`,
  };
}

function serializeRowsAsTsv(
  columns: string[],
  columnKeys: string[],
  rows: Record<string, unknown>[],
) {
  const header = columns.map(escapeTsvValue).join("\t");
  const body = rows.map((row) =>
    columnKeys
      .map((columnKey) =>
        escapeTsvValue(
          formatCellDetail(row[columnKey], formatCellValue(row[columnKey]).value).value,
        ),
      )
      .join("\t"),
  );
  return [header, ...body].join("\n");
}

function serializeRowsAsHtmlTable(
  columns: string[],
  columnKeys: string[],
  rows: Record<string, unknown>[],
) {
  const header = `<tr>${columns.map((column) => `<th>${escapeHtml(column)}</th>`).join("")}</tr>`;
  const body = rows
    .map(
      (row) =>
        `<tr>${columnKeys.map((columnKey) => `<td>${escapeHtml(formatCellDetail(row[columnKey], formatCellValue(row[columnKey]).value).value)}</td>`).join("")}</tr>`,
    )
    .join("");
  return `<table><thead>${header}</thead><tbody>${body}</tbody></table>`;
}

function escapeTsvValue(value: string) {
  return value.replace(/\r?\n/g, " ").replace(/\t/g, " ");
}

function escapeHtml(value: string) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function tokenizeJson(value: string) {
  const tokens: Array<{ value: string; className?: string }> = [];
  const pattern =
    /("(?:\\.|[^"\\])*"\s*:)|("(?:\\.|[^"\\])*")|\b(true|false)\b|\b(null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;
  let lastIndex = 0;
  for (const match of value.matchAll(pattern)) {
    if (match.index > lastIndex) {
      tokens.push({ value: value.slice(lastIndex, match.index) });
    }
    const token = match[0];
    tokens.push({
      value: token,
      className: match[1]
        ? "text-sky-700 dark:text-sky-300"
        : match[2]
          ? "text-emerald-700 dark:text-emerald-300"
          : match[3]
            ? "text-violet-700 dark:text-violet-300"
            : match[4]
              ? "text-muted-foreground italic"
              : "text-primary",
    });
    lastIndex = match.index + token.length;
  }
  if (lastIndex < value.length) {
    tokens.push({ value: value.slice(lastIndex) });
  }
  return tokens;
}

function looksLikeDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}([ tT]\d{2}:\d{2}(:\d{2})?)?/.test(value)) {
    return false;
  }

  return !Number.isNaN(Date.parse(value));
}
