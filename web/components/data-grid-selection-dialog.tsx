import { useMemo, useState, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { parseDataGridCellKey, type DataGridSelection } from "@/lib/data-grid-selection";

// Full values, bounded DOM: a large selection is paged locally, never re-queried.
export function DataGridSelectionDialog({
  selection,
  columns,
  columnKeys,
  rows,
  renderValue,
  copied,
  onCopy,
  onClose,
  onRestoreFocus,
}: {
  selection: DataGridSelection;
  columns: string[];
  columnKeys: string[];
  rows: Record<string, unknown>[];
  renderValue: (value: unknown) => ReactNode;
  copied: boolean;
  onCopy: () => void;
  onClose: () => void;
  onRestoreFocus: () => void;
}) {
  const [page, setPage] = useState(0);
  const indices = useMemo(() => {
    const rowSet = new Set<number>();
    const columnSet = new Set<number>();
    for (const key of selection.selected) {
      const cell = parseDataGridCellKey(key);
      if (cell) {
        rowSet.add(cell.row);
        columnSet.add(cell.column);
      }
    }
    return {
      rows: [...rowSet].sort((a, b) => a - b),
      columns: [...columnSet].sort((a, b) => a - b),
    };
  }, [selection]);
  const pageSize = Math.max(
    1,
    Math.min(100, Math.floor(1000 / Math.max(1, indices.columns.length))),
  );
  const pageCount = Math.ceil(indices.rows.length / pageSize);
  const currentPage = Math.min(page, Math.max(0, pageCount - 1));
  const visible = indices.rows.slice(currentPage * pageSize, (currentPage + 1) * pageSize);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent
        className="flex h-dvh w-screen max-w-none flex-col rounded-none sm:max-w-none"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          onRestoreFocus();
        }}
      >
        <DialogHeader className="shrink-0 pr-10">
          <DialogTitle>Selected cells</DialogTitle>
          <DialogDescription>
            {selection.selected.size} cells · full values from the current result
          </DialogDescription>
        </DialogHeader>
        <div className="flex shrink-0 items-center justify-between gap-2">
          <Button variant="outline" size="sm" onClick={onCopy}>
            {copied ? <Check data-icon="inline-start" /> : <Copy data-icon="inline-start" />}
            {copied ? "Copied" : "Copy selection"}
          </Button>
          {pageCount > 1 ? (
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="sm"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                Previous
              </Button>
              <span className="text-xs text-muted-foreground">
                {currentPage + 1} / {pageCount}
              </span>
              <Button
                variant="ghost"
                size="sm"
                disabled={currentPage + 1 >= pageCount}
                onClick={() => setPage(currentPage + 1)}
              >
                Next
              </Button>
            </div>
          ) : null}
        </div>
        <ScrollArea key={currentPage} className="min-h-0 flex-1">
          <Table aria-label="Selected cell values">
            <TableHeader>
              <TableRow>
                <TableHead>Row</TableHead>
                {indices.columns.map((column) => (
                  <TableHead key={column}>{columns[column]}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.map((row) => (
                <TableRow key={row}>
                  <TableCell>{row + 1}</TableCell>
                  {indices.columns.map((column) => (
                    <TableCell
                      key={column}
                      className="min-w-48 max-w-xl align-top whitespace-normal"
                    >
                      {selection.selected.has(`${row}:${column}`)
                        ? renderValue(rows[row]?.[columnKeys[column]])
                        : null}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
