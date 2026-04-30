"use client";

import AnsiToHtml from "ansi-to-html";
import { useMemo } from "react";

export function AnsiOutput({
  output,
  className,
}: {
  output?: string;
  className?: string;
}) {
  const converter = useMemo(() => new AnsiToHtml({ escapeXML: true }), []);
  const html = useMemo(
    () => converter.toHtml((output ?? "").replace(/\r\n/g, "\n")),
    [converter, output]
  );

  return (
    <pre
      className={className}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
