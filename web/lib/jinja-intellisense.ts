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
  // Client-owned typed namespaces augment the server render context for
  // artifact-local values such as notebook parameters.
  namespaces?: Record<string, JinjaRenderVariable[]>;
  error?: string;
};

export async function renderJinjaAsset(options: {
  assetId: string;
  content: string;
  timeWindow?: { start: string; end: string } | null;
  signal?: AbortSignal;
}) {
  return fetchJSON<JinjaRenderResponse>(`/api/assets/${options.assetId}/render-jinja`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    cache: "no-store",
    signal: options.signal,
    body: JSON.stringify({
      content: options.content,
      start_date: options.timeWindow?.start,
      end_date: options.timeWindow?.end,
    }),
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

function readJinjaOpener(
  text: string,
  offset: number,
): { type: JinjaSpanType; close: string } | null {
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

export function jinjaSpanAtPosition(
  model: MonacoNS.editor.ITextModel,
  position: MonacoNS.Position,
) {
  const offset = model.getOffsetAt(position);
  return (
    parseJinjaSpans(model.getValue()).find(
      (span) => offset >= span.startOffset && offset <= span.endOffset,
    ) ?? null
  );
}

export function registerJinjaProviders(
  monaco: Monaco,
  getRenderResult: (model: MonacoNS.editor.ITextModel) => JinjaRenderResponse | null,
  onGoToVariable?: (model: MonacoNS.editor.ITextModel, variableName: string) => void,
): MonacoNS.IDisposable {
  const disposables: MonacoNS.IDisposable[] = [];
  const definitionModels = new Set<MonacoNS.editor.ITextModel>();

  for (const language of ["sql", "yaml"]) {
    disposables.push(
      monaco.languages.registerCompletionItemProvider(language, {
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
          const renderResult = getRenderResult(model);
          const variables = renderResult?.variables ?? [];
          const namespaces = renderResult?.namespaces ?? {};

          if (/\|\s*[A-Za-z_]*$/.test(before)) {
            return { suggestions: filterSuggestions(monaco, range) };
          }

          if (/\bvar\.[A-Za-z_]*$/.test(before)) {
            return { suggestions: variablePropertySuggestions(monaco, variables, range) };
          }
          const namespaceMatch = before.match(/\b([A-Za-z_][A-Za-z0-9_]*)\.[A-Za-z_]*$/);
          if (namespaceMatch && namespaces[namespaceMatch[1]]) {
            return {
              suggestions: namespacePropertySuggestions(
                monaco,
                namespaceMatch[1],
                namespaces[namespaceMatch[1]],
                range,
              ),
            };
          }

          if (span.type === "statement") {
            const statementBefore = before.replace(/^\{%[-]?\s*/, "");
            const suggestions = statementExpressionSuggestions(
              monaco,
              variables,
              renderResult?.macros ?? [],
              namespaces,
              range,
              statementBefore,
            );
            return { suggestions };
          }

          return {
            suggestions: [
              ...builtinVariableSuggestions(monaco, range),
              ...namespaceModuleSuggestions(monaco, namespaces, range),
              ...macroSuggestions(monaco, renderResult?.macros ?? [], range),
            ],
          };
        },
      }),
    );

    disposables.push(
      monaco.languages.registerHoverProvider(language, {
        provideHover(model, position) {
          const span = jinjaSpanAtPosition(model, position);
          if (!span) return null;
          const rendered = getRenderResult(model)?.spans.find(
            (candidate) =>
              candidate.start_line === span.startLine &&
              candidate.start_column === span.startColumn,
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
      }),
    );

    disposables.push(
      monaco.languages.registerDefinitionProvider(language, {
        provideDefinition(model, position) {
          const reference = jinjaVariableReferenceAtPosition(model, position);
          if (
            !reference ||
            !getRenderResult(model)?.variables.some(
              (variable) => variable.name === reference.variableName,
            )
          ) {
            return null;
          }
          const originSelectionRange = new monaco.Range(
            reference.range.start.lineNumber,
            reference.range.start.column,
            reference.range.end.lineNumber,
            reference.range.end.column,
          );
          const uri = monaco.Uri.from({
            scheme: "renart-settings",
            authority: "pipeline-variable",
            path: `/${encodeURIComponent(reference.variableName)}`,
          });
          let definitionModel = monaco.editor.getModel(uri);
          if (!definitionModel) {
            definitionModel = monaco.editor.createModel(reference.variableName, "plaintext", uri);
            definitionModels.add(definitionModel);
          }
          const targetRange = new monaco.Range(1, 1, 1, reference.variableName.length + 1);
          return [
            {
              originSelectionRange,
              uri,
              range: targetRange,
              targetSelectionRange: targetRange,
            },
          ];
        },
      }),
    );
  }

  disposables.push(
    monaco.editor.registerEditorOpener({
      openCodeEditor(source, resource) {
        if (resource.scheme !== "renart-settings" || resource.authority !== "pipeline-variable") {
          return false;
        }
        const model = source.getModel();
        if (!model) {
          return false;
        }
        onGoToVariable?.(model, decodeURIComponent(resource.path.replace(/^\/+/, "")));
        return true;
      },
    }),
  );

  return {
    dispose: () => {
      disposables.forEach((disposable) => disposable.dispose());
      definitionModels.forEach((model) => {
        if (!model.isDisposed()) model.dispose();
      });
    },
  };
}

function jinjaVariableReferenceAtPosition(
  model: MonacoNS.editor.ITextModel,
  position: MonacoNS.Position,
) {
  if (!jinjaSpanAtPosition(model, position)) {
    return null;
  }
  const text = model.getValue();
  const offset = model.getOffsetAt(position);
  const pattern = /\bvar\.([A-Za-z_][A-Za-z0-9_]*)\b/g;
  for (const match of text.matchAll(pattern)) {
    const startOffset = match.index;
    const endOffset = startOffset + match[0].length;
    if (offset < startOffset || offset > endOffset) {
      continue;
    }
    return {
      variableName: match[1],
      range: {
        start: model.getPositionAt(startOffset),
        end: model.getPositionAt(endOffset),
      },
    };
  }
  return null;
}

function builtinVariableSuggestions(
  monaco: Monaco,
  range: MonacoNS.IRange,
): MonacoNS.languages.CompletionItem[] {
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
    kind:
      label === "var"
        ? monaco.languages.CompletionItemKind.Module
        : monaco.languages.CompletionItemKind.Variable,
    detail,
    insertText: label,
    range,
    sortText: `0${label}`,
  }));
}

function variablePropertySuggestions(
  monaco: Monaco,
  variables: JinjaRenderVariable[],
  range: MonacoNS.IRange,
) {
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

function variableNamespaceSuggestions(
  monaco: Monaco,
  variables: JinjaRenderVariable[],
  range: MonacoNS.IRange,
) {
  return variables.map((variable) => ({
    label: `var.${variable.name}`,
    kind: monaco.languages.CompletionItemKind.Variable,
    detail: variable.type ? `${variable.type} pipeline variable` : "Pipeline variable",
    documentation: variable.description,
    insertText: `var.${variable.name}`,
    range,
    sortText: `0${variable.name}`,
  }));
}

function namespacePropertySuggestions(
  monaco: Monaco,
  namespace: string,
  variables: JinjaRenderVariable[],
  range: MonacoNS.IRange,
) {
  return variables.map((variable) => ({
    label: variable.name,
    kind: monaco.languages.CompletionItemKind.Variable,
    detail: variable.type ? `${variable.type} notebook parameter` : "Notebook parameter",
    documentation:
      namespace === "parameter"
        ? "Safely rendered as a SQL literal."
        : "Typed value for Jinja conditions and source templates.",
    insertText: variable.name,
    range,
    sortText: `0${variable.name}`,
  }));
}

function namespaceModuleSuggestions(
  monaco: Monaco,
  namespaces: Record<string, JinjaRenderVariable[]>,
  range: MonacoNS.IRange,
) {
  return Object.keys(namespaces)
    .sort()
    .map((namespace) => ({
      label: namespace,
      kind: monaco.languages.CompletionItemKind.Module,
      detail:
        namespace === "parameter"
          ? "SQL-safe notebook parameters"
          : "Typed notebook parameter values",
      insertText: namespace,
      range,
      sortText: `0${namespace}`,
    }));
}

function statementExpressionSuggestions(
  monaco: Monaco,
  variables: JinjaRenderVariable[],
  macros: JinjaRenderMacro[],
  namespaces: Record<string, JinjaRenderVariable[]>,
  range: MonacoNS.IRange,
  statementBefore: string,
): MonacoNS.languages.CompletionItem[] {
  const trimmed = statementBefore.trimStart();
  const startsWithKeyword = /^(if|elif|for|set|macro)\b/.test(trimmed);
  if (/\bfor\s+\w+\s+in\s+[\w.]*$/.test(trimmed)) {
    return [
      ...variableNamespaceSuggestions(monaco, variables, range),
      ...builtinVariableSuggestions(monaco, range),
      ...namespaceModuleSuggestions(monaco, namespaces, range),
      ...macroSuggestions(monaco, macros, range),
    ];
  }

  const expressionSuggestions = [
    ...builtinVariableSuggestions(monaco, range),
    ...namespaceModuleSuggestions(monaco, namespaces, range),
    ...variableNamespaceSuggestions(monaco, variables, range),
    ...macroSuggestions(monaco, macros, range),
  ];

  if (startsWithKeyword) {
    return expressionSuggestions;
  }

  return [...statementSuggestions(monaco, range), ...expressionSuggestions];
}

function filterSuggestions(
  monaco: Monaco,
  range: MonacoNS.IRange,
): MonacoNS.languages.CompletionItem[] {
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
    insertTextRules: insertText.includes("${")
      ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
      : undefined,
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

function statementSuggestions(
  monaco: Monaco,
  range: MonacoNS.IRange,
): MonacoNS.languages.CompletionItem[] {
  return [
    "if",
    "elif",
    "else",
    "endif",
    "for",
    "endfor",
    "set",
    "raw",
    "endraw",
    "macro",
    "endmacro",
  ].map((label) => ({
    label,
    kind: monaco.languages.CompletionItemKind.Keyword,
    insertText: label,
    range,
    sortText: `0${label}`,
  }));
}
