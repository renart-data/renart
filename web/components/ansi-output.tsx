"use client";

import AnsiToHtml from "ansi-to-html";
import { useMemo } from "react";

export function normalizeAnsiOutput(output?: string) {
  return (output ?? "").replace(/\r\n/g, "\n").replaceAll("␛[", "\u001b[");
}

export function AnsiOutput({ output, className }: { output?: string; className?: string }) {
  const converter = useMemo(() => new AnsiToHtml({ escapeXML: true }), []);
  const html = useMemo(() => converter.toHtml(normalizeAnsiOutput(output)), [converter, output]);

  return <pre className={className} dangerouslySetInnerHTML={{ __html: html }} />;
}
