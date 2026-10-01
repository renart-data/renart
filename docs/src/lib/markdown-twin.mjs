// Pages with a Markdown version advertise it with <link rel="alternate">, and
// scripts/build-agent-files.mjs writes it to this path after the build.
export function markdownTwinPath(pathname) {
  return pathname === '/' ? '/index.md' : `${pathname.replace(/\/$/, '')}.md`;
}
