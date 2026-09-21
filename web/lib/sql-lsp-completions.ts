import type * as MonacoNS from "monaco-editor";
import type { SQLLSPCompletionItem } from "@/lib/api-sql-lsp";

export function isSQLCompletionKind(item: Pick<SQLLSPCompletionItem, "kind">): boolean {
  return item.kind === 5 || item.kind === 18 || item.kind === 3 || item.kind === 2;
}

/** One ranking/transport contract for SQL editors and SQL embedded in Python. */
export function sqlCompletionToMonaco(
  monaco: typeof MonacoNS,
  item: SQLLSPCompletionItem,
  range: MonacoNS.IRange,
  embedded = false,
): MonacoNS.languages.CompletionItem {
  const kinds = monaco.languages.CompletionItemKind;
  const kind =
    item.kind === 5
      ? kinds.Field
      : item.kind === 18
        ? kinds.Reference
        : item.kind === 3
          ? kinds.Function
          : item.kind === 2
            ? kinds.Keyword
            : kinds.Text;
  // Local notebook columns already use groups 0/1. Backend columns join them;
  // functions must sort after both sources, not just after backend columns.
  const group = item.kind === 5 ? "0" : item.kind === 18 ? "4" : item.kind === 3 ? "6" : "9";
  const insertText = item.insertText || item.label;
  return {
    label: item.label,
    kind,
    detail: embedded ? [item.detail, "SQL in query()"].filter(Boolean).join(" · ") : item.detail,
    documentation: item.documentation ? { value: item.documentation, isTrusted: false } : undefined,
    insertText,
    range,
    sortText: `${group}-sql-${item.sortText || item.label}`,
    command: insertText.endsWith(".")
      ? { id: "editor.action.triggerSuggest", title: "Show column suggestions" }
      : undefined,
  };
}

export function dedupeSQLCompletions(
  suggestions: MonacoNS.languages.CompletionItem[],
): MonacoNS.languages.CompletionItem[] {
  const seen = new Set<string>();
  return suggestions.filter((suggestion) => {
    const label = typeof suggestion.label === "string" ? suggestion.label : suggestion.label.label;
    const key = `${suggestion.kind}::${label.toLowerCase()}::${suggestion.insertText.toLowerCase()}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
