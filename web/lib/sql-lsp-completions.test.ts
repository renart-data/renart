import { describe, expect, it } from "vitest";
import type * as MonacoNS from "monaco-editor";
import {
  dedupeSQLCompletions,
  isSQLCompletionKind,
  sqlCompletionToMonaco,
} from "./sql-lsp-completions";

const monaco = {
  languages: { CompletionItemKind: { Field: 4, Reference: 17, Function: 1, Keyword: 16, Text: 0 } },
} as unknown as typeof MonacoNS;
const range = { startLineNumber: 1, endLineNumber: 1, startColumn: 8, endColumn: 10 };

describe("shared SQL completion adapter", () => {
  it("keeps Function-kind items in SQL and embedded Python", () => {
    expect(isSQLCompletionKind({ kind: 3 })).toBe(true);
    for (const embedded of [false, true]) {
      const item = sqlCompletionToMonaco(
        monaco,
        { label: "count", kind: 3, insertText: "count", documentation: "engine catalog" },
        range,
        embedded,
      );
      expect(item.kind).toBe(monaco.languages.CompletionItemKind.Function);
      expect(item.insertText).toBe("count");
      expect(item.range).toBe(range);
      expect(item.documentation).toMatchObject({ value: "engine catalog", isTrusted: false });
    }
  });
  it("ranks canonical and notebook-runtime columns before functions", () => {
    const column = sqlCompletionToMonaco(
      monaco,
      { label: "cost", kind: 5, sortText: "0001" },
      range,
    );
    const fn = sqlCompletionToMonaco(
      monaco,
      { label: "count", kind: 3, sortText: "2-count" },
      range,
    );
    expect(column.sortText! < fn.sortText!).toBe(true);
    expect("1" < fn.sortText!).toBe(true); // runtime column rank
  });
  it("does not deduplicate a function against a same-named column", () => {
    const column = sqlCompletionToMonaco(monaco, { label: "count", kind: 5 }, range);
    const fn = sqlCompletionToMonaco(monaco, { label: "count", kind: 3 }, range);
    expect(dedupeSQLCompletions([column, fn, column])).toEqual([column, fn]);
  });
});
