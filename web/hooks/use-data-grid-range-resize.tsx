import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import {
  selectedDataGridBounds,
  resizeDataGridSelection,
  type DataGridSelection,
  type DataGridSelectionEdge,
} from "@/lib/data-grid-selection";
import { cn } from "@/lib/utils";

type Props = {
  selection: DataGridSelection;
  onChange: (selection: DataGridSelection) => void;
  table: RefObject<HTMLTableElement | null>;
  viewport: RefObject<HTMLDivElement | null>;
  rows: number;
  columns: number;
  rowHeight: number;
};

export function useDataGridRangeResize({
  selection,
  onChange,
  table,
  viewport,
  rows,
  columns,
  rowHeight,
}: Props) {
  const [geometry, setGeometry] = useState<{
    left: number;
    top: number;
    width: number;
    height: number;
  } | null>(null);
  const drag = useRef<{
    initial: DataGridSelection;
    edge: DataGridSelectionEdge;
    pointer: number;
    x: number;
    y: number;
    originX: number;
    originY: number;
    moved: boolean;
    lastCell?: string;
  } | null>(null);
  const frame = useRef<number | null>(null);
  const latest = useRef({ onChange, rows, columns, rowHeight });
  latest.current = { onChange, rows, columns, rowHeight };
  const stop = () => {
    drag.current = null;
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
  };
  useEffect(() => stop, []);
  useEffect(stop, [rows, columns]);
  useLayoutEffect(() => {
    const rectangle = selectedDataGridBounds(selection);
    // A sparse selection must not silently include the holes when resized.
    if (
      !rectangle ||
      selection.selected.size !==
        (rectangle.end.row - rectangle.start.row + 1) *
          (rectangle.end.column - rectangle.start.column + 1)
    ) {
      setGeometry(null);
      return;
    }
    const element = table.current;
    if (!element) return;
    const measure = () => {
      const headers = element.tHead?.rows[0]?.cells;
      const first = headers?.[rectangle.start.column + 1];
      const last = headers?.[rectangle.end.column + 1];
      if (!first || !last) return;
      const left = first.offsetLeft;
      setGeometry({
        left,
        top: (element.tHead?.offsetHeight ?? rowHeight) + rectangle.start.row * rowHeight,
        width: last.offsetLeft + last.offsetWidth - left,
        height: (rectangle.end.row - rectangle.start.row + 1) * rowHeight,
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [selection, table, rowHeight, columns]);

  const update = () => {
    const state = drag.current;
    const element = table.current;
    const scroll = viewport.current;
    if (!state?.moved || !element || !scroll) return;
    const headers = Array.from(element.tHead?.rows[0]?.cells ?? []).slice(1);
    const rect = element.getBoundingClientRect();
    const column = Math.max(
      0,
      headers.findIndex((header) => state.x < header.getBoundingClientRect().right),
    );
    const resolvedColumn =
      headers.length && state.x >= headers[headers.length - 1].getBoundingClientRect().right
        ? headers.length - 1
        : column;
    const row = Math.floor(
      (state.y - rect.top - (element.tHead?.offsetHeight ?? rowHeight)) / latest.current.rowHeight,
    );
    const cellKey = `${Math.max(0, Math.min(latest.current.rows - 1, row))}:${resolvedColumn}`;
    if (state.lastCell === cellKey) return;
    state.lastCell = cellKey;
    latest.current.onChange(
      resizeDataGridSelection(
        state.initial,
        state.edge,
        { row, column: resolvedColumn },
        latest.current,
      ),
    );
  };
  const tick = () => {
    const state = drag.current;
    const scroll = viewport.current;
    if (!state || !scroll) return;
    const rect = scroll.getBoundingClientRect();
    const velocity = (point: number, low: number, high: number) =>
      point < low + 24
        ? -Math.min(18, (low + 24 - point) / 3)
        : point > high - 24
          ? Math.min(18, (point - high + 24) / 3)
          : 0;
    if (state.moved) {
      scroll.scrollTop += velocity(state.y, rect.top + rowHeight, rect.bottom);
      scroll.scrollLeft += velocity(state.x, rect.left + 48, rect.right);
    }
    update();
    frame.current = requestAnimationFrame(tick);
  };

  const handles = geometry ? (
    <div
      aria-label="Selection range"
      className="pointer-events-none absolute z-20 border-2 border-primary"
      style={geometry}
    >
      {(["top", "bottom", "left", "right", "start", "end"] as const).map((edge) => (
        <button
          key={edge}
          type="button"
          aria-label={`Resize selection ${edge}`}
          tabIndex={edge === "start" || edge === "end" ? 0 : -1}
          className={cn(
            "data-grid-range-handle pointer-events-auto absolute touch-none select-none outline-none focus-visible:ring-2 focus-visible:ring-ring",
            edge === "top" && "-top-2 left-4 right-4 h-4 cursor-ns-resize",
            edge === "bottom" && "-bottom-2 left-4 right-4 h-4 cursor-ns-resize",
            edge === "left" && "-left-2 top-4 bottom-4 w-4 cursor-ew-resize",
            edge === "right" && "-right-2 top-4 bottom-4 w-4 cursor-ew-resize",
            (edge === "start" || edge === "end") &&
              "grid size-7 place-items-center rounded-full cursor-nwse-resize",
            edge === "start" && "-left-3.5 -top-3.5",
            edge === "end" && "-right-3.5 -bottom-3.5",
          )}
          onPointerDown={(event) => {
            if (event.button !== 0) return;
            event.preventDefault();
            event.stopPropagation();
            stop();
            drag.current = {
              initial: selection,
              edge,
              pointer: event.pointerId,
              x: event.clientX,
              y: event.clientY,
              originX: event.clientX,
              originY: event.clientY,
              moved: false,
            };
            event.currentTarget.setPointerCapture(event.pointerId);
            frame.current = requestAnimationFrame(tick);
          }}
          onPointerMove={(event) => {
            if (drag.current?.pointer === event.pointerId) {
              drag.current.x = event.clientX;
              drag.current.y = event.clientY;
              drag.current.moved ||=
                Math.hypot(
                  event.clientX - drag.current.originX,
                  event.clientY - drag.current.originY,
                ) > 3;
              update();
            }
          }}
          onPointerUp={(event) => {
            if (drag.current?.pointer === event.pointerId) {
              update();
              stop();
              event.currentTarget.releasePointerCapture(event.pointerId);
            }
          }}
          onPointerCancel={stop}
          onLostPointerCapture={stop}
          onKeyDown={(event) => {
            const delta = {
              ArrowUp: [-1, 0],
              ArrowDown: [1, 0],
              ArrowLeft: [0, -1],
              ArrowRight: [0, 1],
            }[event.key];
            const rectangle = selectedDataGridBounds(selection);
            if (!delta || !rectangle) return;
            event.preventDefault();
            const current = edge === "start" ? rectangle.start : rectangle.end;
            onChange(
              resizeDataGridSelection(
                selection,
                edge,
                { row: current.row + delta[0], column: current.column + delta[1] },
                { rows, columns },
              ),
            );
          }}
        >
          {edge === "start" || edge === "end" ? (
            <span className="size-2.5 rounded-full border-2 border-background bg-primary shadow-sm" />
          ) : null}
        </button>
      ))}
    </div>
  ) : null;
  return handles;
}
