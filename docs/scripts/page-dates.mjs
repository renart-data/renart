// Sitemap lastmod dates: the newest commit touching a page's source files.
//
// The docs image is built without .git, so `make docs-docker` first runs this
// file as a script to snapshot the same per-file dates into .page-dates.json.
// Without Git history or a snapshot, pages get no lastmod rather than a guess.
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const snapshotFile = path.join(root, '.page-dates.json');

// Sources that render into every page under a prefix, beyond the page's own file.
const sharedSources = [
  ['/compare/', ['src/data/comparison-features.ts', 'src/layouts/ComparisonLayout.astro']],
];

function datesFromGit() {
  try {
    const log = execFileSync(
      'git',
      ['log', '--relative', '--format=%x00%cI', '--name-only', '--', 'src'],
      { cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, stdio: ['ignore', 'pipe', 'ignore'] },
    );
    const dates = {};
    // Newest commit first, so the first date seen for a file is its latest.
    for (const commit of log.split('\0').slice(1)) {
      const [date, ...files] = commit.split('\n').filter(Boolean);
      for (const file of files) dates[file] ??= date;
    }
    return Object.keys(dates).length ? dates : null;
  } catch {
    return null;
  }
}

function datesFromSnapshot() {
  try {
    return JSON.parse(readFileSync(snapshotFile, 'utf8'));
  } catch {
    return null;
  }
}

let fileDates;

function sourcesFor(pathname) {
  const page = pathname.replace(/^\/|\/$/g, '');
  const candidates = page
    ? [`src/content/docs/${page}.mdx`, `src/content/docs/${page}/index.mdx`, `src/pages/${page}.astro`, `src/pages/${page}/index.astro`]
    : ['src/pages/index.astro'];
  const sources = candidates.filter((file) => existsSync(path.join(root, file)));
  for (const [prefix, files] of sharedSources) if (pathname.startsWith(prefix)) sources.push(...files);
  return sources;
}

/** Latest commit date (ISO 8601) for the page at `pathname`, or undefined. */
export function pageLastModified(pathname) {
  fileDates ??= datesFromGit() ?? datesFromSnapshot() ?? {};
  const dates = sourcesFor(pathname).map((file) => fileDates[file]).filter(Boolean);
  return dates.length ? dates.reduce((a, b) => (Date.parse(a) >= Date.parse(b) ? a : b)) : undefined;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dates = datesFromGit();
  if (!dates) {
    console.error('page-dates: no Git history found; cannot write .page-dates.json');
    process.exit(1);
  }
  writeFileSync(snapshotFile, JSON.stringify(dates, null, 2) + '\n');
  console.log(`page-dates: wrote ${Object.keys(dates).length} file dates to .page-dates.json`);
}
