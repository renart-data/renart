/**
 * Helpers for editing only the `parameters:` block of an API asset's YAML while
 * leaving the rest of the file (type, connection, columns, meta, comments)
 * untouched. The block is treated as verbatim text — its own `parameters:` key
 * line plus its indented body — so it round-trips without any dedent/reindent
 * math and the OpenAPI YAML intellisense (which keys off `parameters.*` paths)
 * keeps working unchanged.
 */

export type ParametersSpan = {
  found: boolean;
  /** Line index of the `parameters:` key (inclusive). */
  start: number;
  /** Line index just past the block (exclusive). */
  end: number;
  /** The block text: the `parameters:` line plus its indented body. */
  text: string;
};

/**
 * Locate the top-level `parameters:` block. The body runs up to the next
 * syntactically plausible plain top-level YAML key. Looking only for indentation
 * is unsafe while editing: a pasted JSON object commonly leaves `{`, `}`, and
 * quoted property lines at column zero. Those malformed lines must remain in
 * Monaco so the user can repair them instead of becoming an invisible tail in
 * the asset file.
 */
export function findParametersBlock(content: string): ParametersSpan {
  const lines = content.split("\n");
  let start = -1;
  for (let index = 0; index < lines.length; index += 1) {
    if (/^parameters:\s*$/.test(lines[index])) {
      start = index;
      break;
    }
  }
  if (start === -1) {
    return { found: false, start: -1, end: -1, text: "" };
  }

  let end = start + 1;
  while (end < lines.length && !/^[A-Za-z_][A-Za-z0-9_-]*\s*:/.test(lines[end])) {
    end += 1;
  }
  // Don't swallow trailing blank lines that belong to the gap before the next key.
  while (end - 1 > start && lines[end - 1].trim() === "") {
    end -= 1;
  }

  return { found: true, start, end, text: lines.slice(start, end).join("\n") };
}

/** The verbatim `parameters:` block text (empty string if the file has none). */
export function extractParametersText(content: string): string {
  return findParametersBlock(content).text;
}

/**
 * Replace the file's `parameters:` block with `editedBlock`, preserving every
 * other line. When the file has no block yet, the edited block is appended.
 */
export function spliceParametersText(content: string, editedBlock: string): string {
  const blockLines = editedBlock.replace(/\n+$/, "").split("\n");
  const span = findParametersBlock(content);

  if (!span.found) {
    if (content.trim() === "") {
      return `${blockLines.join("\n")}\n`;
    }
    const separator = content.endsWith("\n") ? "" : "\n";
    return `${content}${separator}${blockLines.join("\n")}\n`;
  }

  const lines = content.split("\n");
  const next = [...lines.slice(0, span.start), ...blockLines, ...lines.slice(span.end)];
  return next.join("\n");
}

/**
 * Cheap mid-edit guard for metadata sync. Monaco users commonly pause after
 * typing an intended mapping key such as `fields` before adding `:`; that is
 * not useful input for server-side column inference yet.
 */
export function hasIncompletePlainYAMLKeyLine(content: string): boolean {
  // Lines inside a block scalar (`key: |` / `key: >`) are literal content, not
  // mapping keys — a bare word there must not read as an incomplete key.
  let blockScalarIndent = -1;
  return content.split("\n").some((line) => {
    const trimmed = line.trim();
    if (trimmed === "") {
      return false;
    }
    const indent = line.length - line.trimStart().length;
    if (blockScalarIndent >= 0) {
      if (indent > blockScalarIndent) {
        return false;
      }
      blockScalarIndent = -1;
    }
    if (/^[^#\s][^:]*:\s*[|>][+-]?\d*\s*(#.*)?$/.test(trimmed)) {
      blockScalarIndent = indent;
      return false;
    }
    if (trimmed.startsWith("#") || trimmed.startsWith("-")) {
      return false;
    }
    if (trimmed.includes(":")) {
      return false;
    }
    return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(trimmed);
  });
}
