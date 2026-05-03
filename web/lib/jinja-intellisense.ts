import type * as MonacoNS from "monaco-editor";

import { fetchJSON } from "@/lib/api-core";

type Monaco = typeof MonacoNS;

export type JinjaSpanType = "expression" | "statement" | "comment";

export type JinjaSpan = {
  type: JinjaSpanType;
  startOffset: number;
  endOffset: number;
  startLine: number;
  startColumn: number;
  endLine: number;
  endColumn: number;
  content: string;
};

export type JinjaRenderSpan = {
  start_line: number;
  start_column: number;
  end_line: number;
  end_column: number;
  expression: string;
  rendered_text: string;
  error?: string;
};

export type JinjaRenderVariable = {
  name: string;
  type?: string;
  default_value?: unknown;
  description?: string;
};

export type JinjaRenderMacro = {
  name: string;
  parameters: string[];
};

export type JinjaRenderResponse = {
  status: "ok" | "error";
  rendered?: string;
  spans: JinjaRenderSpan[];
  variables: JinjaRenderVariable[];
  macros: JinjaRenderMacro[];
  error?: string;
};

export async function renderJinjaAsset(options: {
  assetId: string;
  content: string;
  signal?: AbortSignal;
}) {
  return fetchJSON<JinjaRenderResponse>(`/api/assets/${options.assetId}/render-jinja`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    cache: "no-store",
    signal: options.signal,
    body: JSON.stringify({ content: options.content }),
  });
}

export function parseJinjaSpans(text: string): JinjaSpan[] {
  const spans: JinjaSpan[] = [];
  let line = 1;
  let column = 1;
  let offset = 0;

  while (offset < text.length) {
    const opener = readJinjaOpener(text, offset);
    if (!opener) {
      ({ line, column } = advancePosition(text[offset] ?? "", line, column));
      offset++;
      continue;
    }

    const startOffset = offset;
    const startLine = line;
    const startColumn = column;
    offset += 2;
    column += 2;
    const contentStart = offset;

    while (offset < text.length) {
      if (text.startsWith(opener.close, offset)) {
        const content = text.slice(contentStart, offset).trim();
        offset += 2;
        column += 2;
        spans.push({
          type: opener.type,
          startOffset,
          endOffset: offset,
          startLine,
          startColumn,
          endLine: line,
          endColumn: column,
          content,
        });
        break;
      }

      ({ line, column } = advancePosition(text[offset] ?? "", line, column));
      offset++;
    }
  }

  return spans;
}

function readJinjaOpener(text: string, offset: number): { type: JinjaSpanType; close: string } | null {
  if (text.startsWith("{{", offset)) return { type: "expression", close: "}}" };
  if (text.startsWith("{%", offset)) return { type: "statement", close: "%}" };
  if (text.startsWith("{#", offset)) return { type: "comment", close: "#}" };
  return null;
}

function advancePosition(char: string, line: number, column: number) {
  if (char === "\n") {
    return { line: line + 1, column: 1 };
  }
  return { line, column: column + 1 };
}

export function jinjaSpanAtPosition(model: MonacoNS.editor.ITextModel, position: MonacoNS.Position) {
  const offset = model.getOffsetAt(position);
  return parseJinjaSpans(model.getValue()).find(
    (span) => offset >= span.startOffset && offset <= span.endOffset,
  ) ?? null;
}

export function registerJinjaProviders(
  monaco: Monaco,
  getRenderResult: () => JinjaRenderResponse | null,
): MonacoNS.IDisposable {
  const disposables: MonacoNS.IDisposable[] = [];

  disposables.push(monaco.languages.registerCompletionItemProvider("sql", {
    triggerCharacters: ["{", ".", "|", "%", " "],
    provideCompletionItems(model, position) {
      const span = jinjaSpanAtPosition(model, position);
      if (!span || span.type === "comment") {
        return { suggestions: [] };
      }

      const word = model.getWordUntilPosition(position);
      const range = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: word.startColumn,
        endColumn: word.endColumn,
      };
      const before = model.getValueInRange({
        startLineNumber: span.startLine,
        startColumn: span.startColumn,
        endLineNumber: position.lineNumber,
        endColumn: position.column,
      });

      if (span.type === "statement") {
        return { suggestions: statementSuggestions(monaco, range) };
      }

      if (/\|\s*[A-Za-z_]*$/.test(before)) {
        return { suggestions: filterSuggestions(monaco, range) };
      }

      if (/\bvar\.[A-Za-z_]*$/.test(before)) {
        return { suggestions: variablePropertySuggestions(monaco, getRenderResult()?.variables ?? [], range) };
      }

      return {
        suggestions: [
          ...builtinVariableSuggestions(monaco, range),
          ...macroSuggestions(monaco, getRenderResult()?.macros ?? [], range),
        ],
      };
    },
  }));

  disposables.push(monaco.languages.registerHoverProvider("sql", {
    provideHover(model, position) {
      const span = jinjaSpanAtPosition(model, position);
      if (!span) return null;
      const rendered = getRenderResult()?.spans.find(
        (candidate) => candidate.start_line === span.startLine && candidate.start_column === span.startColumn,
      );
      const contents = [
        { value: span.type === "expression" ? "**Jinja expression**" : "**Jinja statement**" },
        { value: `\`${span.content}\`` },
      ];
      if (rendered?.rendered_text) {
        contents.push({ value: `Rendered: \`${rendered.rendered_text}\`` });
      } else if (rendered?.error) {
        contents.push({ value: `Render error: \`${rendered.error}\`` });
      }
      return {
        range: new monaco.Range(span.startLine, span.startColumn, span.endLine, span.endColumn),
        contents,
      };
    },
  }));

  return { dispose: () => disposables.forEach((disposable) => disposable.dispose()) };
}

function builtinVariableSuggestions(monaco: Monaco, range: MonacoNS.IRange): MonacoNS.languages.CompletionItem[] {
  return [
    ["start_date", "Run start date"],
    ["end_date", "Run end date"],
    ["start_datetime", "Run start datetime"],
    ["end_datetime", "Run end datetime"],
    ["start_timestamp", "Run start timestamp"],
    ["end_timestamp", "Run end timestamp"],
    ["execution_date", "Run execution date"],
    ["full_refresh", "Full refresh flag"],
    ["pipeline", "Current pipeline name"],
    ["run_id", "Current run id"],
    ["this", "Current asset name"],
    ["var", "Pipeline variables"],
  ].map(([label, detail]) => ({
    label,
    kind: label === "var" ? monaco.languages.CompletionItemKind.Module : monaco.languages.CompletionItemKind.Variable,
    detail,
    insertText: label,
    range,
    sortText: `0${label}`,
  }));
}

function variablePropertySuggestions(monaco: Monaco, variables: JinjaRenderVariable[], range: MonacoNS.IRange) {
  return variables.map((variable) => ({
    label: variable.name,
    kind: monaco.languages.CompletionItemKind.Variable,
    detail: variable.type ? `${variable.type} pipeline variable` : "Pipeline variable",
    documentation: variable.description,
    insertText: variable.name,
    range,
    sortText: `0${variable.name}`,
  }));
}

function filterSuggestions(monaco: Monaco, range: MonacoNS.IRange): MonacoNS.languages.CompletionItem[] {
  return [
    ["add_days", "add_days(${1:0})", "Add/subtract days"],
    ["add_hours", "add_hours(${1:0})", "Add/subtract hours"],
    ["add_minutes", "add_minutes(${1:0})", "Add/subtract minutes"],
    ["add_seconds", "add_seconds(${1:0})", "Add/subtract seconds"],
    ["add_months", "add_months(${1:0})", "Add/subtract months"],
    ["add_years", "add_years(${1:0})", "Add/subtract years"],
    ["date_format", "date_format('${1:%Y-%m-%d}')", "Format date string"],
    ["tojson", "tojson", "Serialize as JSON"],
    ["default", "default(${1:''})", "Default value if undefined"],
    ["join", "join('${1:, }')", "Join iterable"],
    ["upper", "upper", "Uppercase string"],
    ["lower", "lower", "Lowercase string"],
    ["int", "int", "Convert to integer"],
    ["float", "float", "Convert to float"],
  ].map(([label, insertText, detail]) => ({
    label,
    kind: monaco.languages.CompletionItemKind.Function,
    detail,
    insertText,
    insertTextRules: insertText.includes("${") ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
    range,
    sortText: `0${label}`,
  }));
}

function macroSuggestions(monaco: Monaco, macros: JinjaRenderMacro[], range: MonacoNS.IRange) {
  return macros.map((macro) => ({
    label: macro.name,
    kind: monaco.languages.CompletionItemKind.Function,
    detail: `Macro: ${macro.name}(${macro.parameters.join(", ")})`,
    insertText: `${macro.name}(${macro.parameters.map((param, index) => `\${${index + 1}:${param}}`).join(", ")})`,
    insertTextRules: monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet,
    range,
    sortText: `1${macro.name}`,
  }));
}

function statementSuggestions(monaco: Monaco, range: MonacoNS.IRange): MonacoNS.languages.CompletionItem[] {
  return ["if", "elif", "else", "endif", "for", "endfor", "set", "raw", "endraw", "macro", "endmacro"].map((label) => ({
    label,
    kind: monaco.languages.CompletionItemKind.Keyword,
    insertText: label,
    range,
    sortText: `0${label}`,
  }));
}
