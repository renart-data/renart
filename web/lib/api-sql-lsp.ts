import { fetchJSONWithBody } from "@/lib/api-core";

export type SQLLSPPosition = {
  line: number;
  character: number;
};

export type SQLLSPRange = {
  start: SQLLSPPosition;
  end: SQLLSPPosition;
};

export type SQLLSPDiagnostic = {
  range: SQLLSPRange;
  severity: number;
  code?: string;
  source?: string;
  message: string;
  scope?: string;
  confidence?: string;
};

export type SQLLSPCompletionItem = {
  label: string;
  kind?: number;
  detail?: string;
  documentation?: string;
  insertText?: string;
  sortText?: string;
};

export type SQLLSPLocation = {
  uri: string;
  range: SQLLSPRange;
  asset_id?: string;
};

export type SQLLSPTextEdit = {
  range: SQLLSPRange;
  newText: string;
};

export type SQLLSPWorkspaceEdit = {
  changes?: Record<string, SQLLSPTextEdit[]>;
};

export type SQLLSPCodeAction = {
  title: string;
  kind?: string;
  diagnostics?: SQLLSPDiagnostic[];
  edit?: SQLLSPWorkspaceEdit;
  isPreferred?: boolean;
};

export type SQLLSPHover = {
  contents: string;
  range?: SQLLSPRange;
};

export type SQLLSPSemanticTokens = {
  data: number[];
};

export type SQLLSPSemanticTokensLegend = {
  tokenTypes: string[];
  tokenModifiers: string[];
};

export type SQLLSPDocumentSymbol = {
  name: string;
  detail?: string;
  kind: number;
  range: SQLLSPRange;
  selectionRange: SQLLSPRange;
  children?: SQLLSPDocumentSymbol[];
};

export type SQLLSPSignatureHelp = {
  signatures: Array<{
    label: string;
    documentation?: string;
    parameters?: Array<{ label: string; documentation?: string }>;
    activeParameter?: number;
  }>;
  activeSignature?: number;
  activeParameter?: number;
};

export type SQLLSPRequest = {
  asset_id: string;
  content: string;
  connection?: string;
  environment?: string;
  document_context?: "asset" | "adhoc" | "custom_check" | "hook";
  position?: SQLLSPPosition;
  include_declaration?: boolean;
  new_name?: string;
  formatting_options?: {
    tabSize?: number;
    insertSpaces?: boolean;
  };
};

export type SQLLSPResponse = {
  status: "ok" | "error";
  diagnostics?: SQLLSPDiagnostic[];
  completions?: SQLLSPCompletionItem[];
  locations?: SQLLSPLocation[];
  hover?: SQLLSPHover;
  edit?: SQLLSPWorkspaceEdit;
  code_actions?: SQLLSPCodeAction[];
  tokens?: SQLLSPSemanticTokens;
  token_legend?: SQLLSPSemanticTokensLegend;
  symbols?: SQLLSPDocumentSymbol[];
  signature?: SQLLSPSignatureHelp;
  error?: string;
};

export function getSQLLSPDiagnostics(request: SQLLSPRequest, signal?: AbortSignal) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/diagnostics", "POST", request, {
    signal,
  });
}

export function getSQLLSPCompletions(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/completions", "POST", request);
}

export function getSQLLSPDefinition(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/definition", "POST", request);
}

export function getSQLLSPReferences(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/references", "POST", request);
}

export function getSQLLSPRename(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/rename", "POST", request);
}

export function getSQLLSPCodeActions(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/code-actions", "POST", request);
}

export function getSQLLSPHover(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/hover", "POST", request);
}

export function getSQLLSPSemanticTokens(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/semantic-tokens", "POST", request);
}

export function getSQLLSPDocumentSymbols(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/document-symbols", "POST", request);
}

export function getSQLLSPFormatting(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/formatting", "POST", request);
}

export function getSQLLSPSignatureHelp(request: SQLLSPRequest) {
  return fetchJSONWithBody<SQLLSPResponse>("/api/sql/lsp/signature-help", "POST", request);
}
