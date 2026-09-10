import type { BrowserCompletion, BrowserPathSyntax } from "./data-browser-search";

/** Display only. Acceptance always uses the untouched canonical value. */
export function completionShadow(value: string, completion?: BrowserCompletion) {
  if (!completion) return "";
  const display = completion.separator
    ? completion.value.slice(0, -completion.separator.length)
    : completion.value;
  return completion.value.toLowerCase().startsWith(value.toLowerCase())
    ? display.slice(value.length)
    : ` → ${display}`;
}

// Decoration, not a second resolver: the search planner supplies the actual
// connection boundary and path grammar. Preserve literal dots and SQL quoting.
export function searchPathSegments(value: string, syntax?: BrowserPathSyntax) {
  const parts: { text: string; completed: boolean }[] = [];
  if (!syntax) return [{ text: value, completed: false }];
  let start = 0;
  const boundary = (index: number) => {
    if (index > start) parts.push({ text: value.slice(start, index), completed: true });
    parts.push({ text: value[index], completed: false });
    start = index + 1;
  };
  if (syntax.connectionEnd > 0) boundary(syntax.connectionEnd - 1);
  let quoted = false;
  for (let index = start; index < value.length; index++) {
    if (syntax.separator === "." && value[index] === '"') {
      if (quoted && value[index + 1] === '"') index++;
      else quoted = !quoted;
    } else if (!quoted && value[index] === syntax.separator) boundary(index);
  }
  if (start < value.length) parts.push({ text: value.slice(start), completed: false });
  return parts;
}
