"use client";

import type { Monaco } from "@monaco-editor/react";
import { GripHorizontal } from "lucide-react";
import type * as MonacoNS from "monaco-editor";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { AssetCodeEditor } from "@/components/asset-code-editor";
import { useJinjaIntellisense } from "@/hooks/use-jinja-intellisense";
import { usePythonIntellisense } from "@/hooks/use-python-intellisense";
import { usePythonQueryIntellisense } from "@/hooks/use-python-query-intellisense";
import { useSQLLSP } from "@/hooks/use-sql-lsp";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { formatSQLAsset } from "@/lib/api-assets-crud";
import { usesPythonSource } from "@/lib/asset-types";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { applyExternalModelValue } from "@/lib/monaco-model-sync";
import {
  buildSchemaForAsset,
  parseQualifiedTableName,
  SchemaColumn,
  SchemaTable,
} from "@/lib/sql-schema";
import { WebAsset, WebColumn, WorkspaceState } from "@/lib/types";
import type { NotebookParameter } from "@/lib/generated/api-types";

const NOTEBOOK_EDITOR_LINE_HEIGHT = 19;
const NOTEBOOK_EDITOR_VERTICAL_PADDING = 16;
const NOTEBOOK_EDITOR_MIN_LINES = 3;
const NOTEBOOK_EDITOR_AUTO_MAX_LINES = 24;
const NOTEBOOK_EDITOR_MIN_HEIGHT =
  NOTEBOOK_EDITOR_MIN_LINES * NOTEBOOK_EDITOR_LINE_HEIGHT + NOTEBOOK_EDITOR_VERTICAL_PADDING;
const NOTEBOOK_EDITOR_MAX_HEIGHT = 800;
const NOTEBOOK_EDITOR_AUTO_MAX_HEIGHT =
  NOTEBOOK_EDITOR_AUTO_MAX_LINES * NOTEBOOK_EDITOR_LINE_HEIGHT + NOTEBOOK_EDITOR_VERTICAL_PADDING;
const NOTEBOOK_EDITOR_OPTIONS: MonacoNS.editor.IStandaloneEditorConstructionOptions = {
  scrollBeyondLastLine: false,
  scrollbar: { alwaysConsumeMouseWheel: false },
};

function clampNotebookEditorHeight(height: number) {
  return Math.min(Math.max(height, NOTEBOOK_EDITOR_MIN_HEIGHT), NOTEBOOK_EDITOR_MAX_HEIGHT);
}

/**
 * Build the completion/parse-context schema for a notebook cell.
 *
 * Sibling cells come first: those are the tables (`cell_<name>` views) that
 * actually exist in the notebook's local DuckDB session, so they are the most
 * useful completion targets. Pipeline assets on the same connection are added
 * after, mirroring the asset editor.
 */
export function buildNotebookSchemaTables(
  workspace: WorkspaceState | null,
  cells: WebAsset[],
  currentCell: WebAsset,
  resultColumnsByCell?: Map<string, string[]>,
): SchemaTable[] {
  const tables: SchemaTable[] = [];
  const seen = new Set<string>();

  // A source-native cell runs in its selected warehouse, where sibling
  // notebook relations do not exist. Connectionless cells run in the local
  // notebook DuckDB and can read every materialized sibling/source snapshot.
  for (const cell of currentCell.connection?.trim() ? [] : cells) {
    if (cell.cell_id && currentCell.cell_id && cell.cell_id === currentCell.cell_id) {
      continue;
    }
    const name = cell.name;
    if (!name || seen.has(name.toLowerCase())) {
      continue;
    }
    seen.add(name.toLowerCase());
    const parts = parseQualifiedTableName(name);
    // Prefer the cell's last-run columns: they are the cell's actual output
    // (including `select *` expansions the static parser can't see), which lets
    // parse-context resolve both the table and its columns.
    const runColumns = cell.cell_id ? resultColumnsByCell?.get(cell.cell_id) : undefined;
    const columns: SchemaColumn[] =
      runColumns && runColumns.length > 0
        ? runColumns.map((column) => ({ name: column, sourceMethods: ["notebook-run"] }))
        : toSchemaColumns(cell.columns);
    tables.push({
      name,
      shortName: parts.shortName,
      columns,
      isWorkspaceAsset: true,
      assetId: cell.id,
      assetPath: cell.path,
      databaseName: parts.databaseName,
      sourceMethods: ["notebook-cell"],
    });
  }

  if (workspace) {
    for (const table of buildSchemaForAsset(workspace, currentCell)) {
      if (seen.has(table.name.toLowerCase())) {
        continue;
      }
      seen.add(table.name.toLowerCase());
      tables.push(table);
    }
  }

  return tables;
}

function toSchemaColumns(columns?: WebColumn[]): SchemaColumn[] {
  if (!columns || columns.length === 0) {
    return [];
  }
  return columns.map((column) => ({
    name: column.name,
    type: column.type,
    description: column.description,
    primaryKey: column.primary_key,
    sourceMethods: ["notebook-cell"],
  }));
}

/**
 * Monaco editor for a single notebook SQL cell. Mirrors the ad hoc editor's
 * intellisense wiring, but the cell is a real (backend-resolvable) asset, so
 * parse-context diagnostics, completion, and go-to work against the cell id.
 * Content is a controlled draft owned by the cell card.
 */
export function NotebookCellMonaco({
  cell,
  value,
  schemaTables,
  onChange,
  onCommit,
  onRun,
  onRename,
  onGoToAsset,
  onGoToCell,
  parameters = [],
}: {
  cell: WebAsset;
  value: string;
  schemaTables: SchemaTable[];
  onChange: (value: string) => void;
  onCommit: () => void;
  onRun: () => void;
  onRename: () => void;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  onGoToCell?: (cellId: string) => void;
  parameters?: NotebookParameter[];
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const [monacoInstance, setMonacoInstance] = useState<Monaco | null>(null);
  const [editorInstance, setEditorInstance] =
    useState<MonacoNS.editor.IStandaloneCodeEditor | null>(null);
  const [resizedHeight, setResizedHeight] = useState<number | null>(null);
  const [measuredContentHeight, setMeasuredContentHeight] = useState<number | null>(null);
  const resizeDragRef = useRef<{
    pointerId: number;
    startY: number;
    startHeight: number;
  } | null>(null);
  // Monaco owns local typing. Keep every not-yet-observed model snapshot so a
  // concurrent React render cannot mistake an older keystroke for an external
  // update and replay it over newer text.
  const localModelValuesRef = useRef(new Set<string>());
  const applyingExternalValueRef = useRef(false);

  const cellId = cell.cell_id ?? cell.id;
  const isPython = usesPythonSource(cell);
  const ext = isPython ? "py" : "sql";

  // SQL intellisense (completion, parse-context, Jinja) is SQL-only;
  // passing null monaco/editor disables the hooks for Python cells, which fall
  // back to Monaco's built-in Python highlighting.
  const sqlMonaco = isPython ? null : monacoInstance;
  const sqlEditor = isPython ? null : editorInstance;
  useSQLLSP(sqlMonaco, sqlEditor, cell, value, schemaTables, onGoToAsset, onGoToCell, {
    includeNotebookRuntimeColumns: true,
  });
  useJinjaIntellisense(sqlMonaco, sqlEditor, cell, value, undefined, {
    parameter: parameters.map((parameter) => ({
      name: parameter.id,
      type: parameter.type,
      default_value: parameter.default,
      description: parameter.label,
    })),
    parameters: parameters.map((parameter) => ({
      name: parameter.id,
      type: parameter.type,
      default_value: parameter.default,
      description: parameter.label,
    })),
  });

  // Python intellisense (ty: diagnostics, completion, hover, signature, goto,
  // format) is the mirror of the SQL hooks for Python cells; null monaco/editor
  // disables it for SQL cells.
  usePythonIntellisense(
    isPython ? monacoInstance : null,
    isPython ? editorInstance : null,
    cell,
    value,
  );
  usePythonQueryIntellisense(
    isPython ? monacoInstance : null,
    isPython ? editorInstance : null,
    cell,
    value,
    schemaTables,
    onGoToAsset,
    onGoToCell,
  );

  // React never controls the Monaco model after creation. A server/format/UI
  // snapshot is reduced to a minimal model edit, preserving selections and
  // giving a future collaborative layer an operation-shaped integration point.
  useLayoutEffect(() => {
    if (!editorInstance || !monacoInstance) {
      return;
    }
    const model = editorInstance.getModel();
    if (!model) {
      return;
    }
    if (model.getValue() === value) {
      localModelValuesRef.current.clear();
      return;
    }
    if (localModelValuesRef.current.delete(value)) {
      return;
    }

    applyingExternalValueRef.current = true;
    try {
      applyExternalModelValue(editorInstance, monacoInstance, value);
      localModelValuesRef.current.clear();
    } finally {
      applyingExternalValueRef.current = false;
    }
  }, [cellId, editorInstance, monacoInstance, value]);

  const formatSQL = useCallback(() => {
    if (!editorInstance || isPython) {
      return;
    }
    const content = editorInstance.getValue();
    void formatSQLAsset(cell.id, content, { persist: false })
      .then((response) => {
        if (response.status === "ok") {
          onChange(response.content);
        }
      })
      .catch(() => undefined);
  }, [cell.id, editorInstance, onChange]);

  useEffect(() => {
    if (!editorInstance || !monacoInstance) {
      return;
    }

    const keySub = editorInstance.onKeyDown((event) => {
      if (event.keyCode === monacoInstance.KeyCode.F2) {
        event.preventDefault();
        event.stopPropagation();
        onRename();
        return;
      }

      const ctrlOrCmd = event.ctrlKey || event.metaKey;
      if (!ctrlOrCmd) {
        return;
      }

      if (event.keyCode === monacoInstance.KeyCode.Enter) {
        event.preventDefault();
        event.stopPropagation();
        onCommit();
        onRun();
        return;
      }

      if (event.shiftKey && event.keyCode === monacoInstance.KeyCode.KeyI) {
        event.preventDefault();
        event.stopPropagation();
        formatSQL();
      }
    });

    const blurSub = editorInstance.onDidBlurEditorText(() => {
      onCommit();
    });

    return () => {
      keySub.dispose();
      blurSub.dispose();
    };
  }, [editorInstance, formatSQL, monacoInstance, onCommit, onRename, onRun]);

  useLayoutEffect(() => {
    if (!editorInstance) {
      setMeasuredContentHeight(null);
      return;
    }
    const syncHeight = () => {
      setMeasuredContentHeight(
        Math.min(
          Math.max(Math.ceil(editorInstance.getContentHeight()), NOTEBOOK_EDITOR_MIN_HEIGHT),
          NOTEBOOK_EDITOR_AUTO_MAX_HEIGHT,
        ),
      );
    };
    syncHeight();
    const subscription = editorInstance.onDidContentSizeChange(syncHeight);
    return () => subscription.dispose();
  }, [cellId, editorInstance]);

  const handleBeforeMount = useCallback((monaco: Monaco) => {
    defineBruinMonacoThemes(monaco);
  }, []);

  const handleMount = useCallback(
    (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: Monaco) => {
      defineBruinMonacoThemes(monaco);
      setEditorInstance(editor);
      setMonacoInstance(monaco);
    },
    [],
  );

  // Grow with content until the user takes ownership with the resize handle.
  // A double-click on the handle returns to content-driven sizing.
  const lineCount = value.split("\n").length;
  const contentHeight =
    Math.min(Math.max(lineCount, NOTEBOOK_EDITOR_MIN_LINES), NOTEBOOK_EDITOR_AUTO_MAX_LINES) *
      NOTEBOOK_EDITOR_LINE_HEIGHT +
    NOTEBOOK_EDITOR_VERTICAL_PADDING;
  const editorHeight = resizedHeight ?? measuredContentHeight ?? contentHeight;

  const setEditorHeight = (height: number) => {
    setResizedHeight(clampNotebookEditorHeight(height));
  };

  const handleResizePointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    resizeDragRef.current = {
      pointerId: event.pointerId,
      startY: event.clientY,
      startHeight: editorHeight,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const handleResizePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = resizeDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    setEditorHeight(drag.startHeight + event.clientY - drag.startY);
  };

  const finishResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = resizeDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    resizeDragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const handleResizeKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    let nextHeight: number | null = null;
    if (event.key === "ArrowUp") {
      nextHeight = editorHeight - NOTEBOOK_EDITOR_LINE_HEIGHT;
    } else if (event.key === "ArrowDown") {
      nextHeight = editorHeight + NOTEBOOK_EDITOR_LINE_HEIGHT;
    } else if (event.key === "Home") {
      nextHeight = NOTEBOOK_EDITOR_MIN_HEIGHT;
    } else if (event.key === "End") {
      nextHeight = NOTEBOOK_EDITOR_MAX_HEIGHT;
    } else if (event.key === "Enter") {
      setResizedHeight(null);
      event.preventDefault();
      return;
    }
    if (nextHeight === null) {
      return;
    }
    event.preventDefault();
    setEditorHeight(nextHeight);
  };

  return (
    <div className="overflow-hidden">
      <div data-slot="notebook-cell-editor" style={{ height: editorHeight }}>
        <AssetCodeEditor
          asset={cell}
          containerClassName="h-full"
          editorModelPath={`inmemory://bruin/notebook/${cellId}.${ext}`}
          editorOptions={NOTEBOOK_EDITOR_OPTIONS}
          editorValue={value}
          editorValueMode="initial"
          editorHighlighted={false}
          helpMode={false}
          isSqlAsset={!isPython}
          formatShortcutLabel="⌘ + ⇧ + I"
          mobile={false}
          monacoTheme={monacoTheme}
          onChange={(next) => {
            const nextValue = next ?? "";
            if (applyingExternalValueRef.current) {
              return;
            }
            localModelValuesRef.current.add(nextValue);
            onChange(nextValue);
          }}
          onBeforeMount={handleBeforeMount}
          onFormat={formatSQL}
          onMount={handleMount}
        />
      </div>
      <div
        data-slot="notebook-cell-resize-handle"
        role="separator"
        tabIndex={0}
        aria-label={`Resize ${cell.name} cell`}
        aria-orientation="horizontal"
        aria-valuemin={NOTEBOOK_EDITOR_MIN_HEIGHT}
        aria-valuemax={NOTEBOOK_EDITOR_MAX_HEIGHT}
        aria-valuenow={Math.round(editorHeight)}
        aria-valuetext={`${Math.round(editorHeight)} pixels high`}
        title="Drag or use arrow keys to resize; double-click or press Enter to fit content"
        className="group flex h-3 touch-none cursor-row-resize select-none items-center justify-center border-t bg-muted/20 text-muted-foreground outline-none transition-colors hover:bg-muted/50 hover:text-foreground focus-visible:bg-muted/50 focus-visible:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
        onPointerDown={handleResizePointerDown}
        onPointerMove={handleResizePointerMove}
        onPointerUp={finishResize}
        onPointerCancel={finishResize}
        onLostPointerCapture={() => {
          resizeDragRef.current = null;
        }}
        onDoubleClick={() => setResizedHeight(null)}
        onKeyDown={handleResizeKeyDown}
      >
        <GripHorizontal aria-hidden className="size-3.5" />
      </div>
    </div>
  );
}
