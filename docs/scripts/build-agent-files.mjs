// Writes the Markdown companions of the built site into dist/ after
// `astro build` (architecture/docs.md §11):
//
// - a Markdown version of every docs and compare page and of /work-with-me/,
//   at the page path plus .md (/docs/quickstart/ -> /docs/quickstart.md);
// - /index.md, the landing page, from the hand-written src/markdown/home.md;
// - /llms.txt, /docs/llms.txt and /llms-full.txt, in sidebar order;
// - /404.md, which Caddy returns for a 404 requested as Markdown.
//
// The build fails when a page's advertised Markdown alternate is missing, a
// generated link points at nothing, or home.md has drifted from the landing
// page's copy.
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { select, selectAll } from 'hast-util-select';
import { toString } from 'hast-util-to-string';
import rehypeParse from 'rehype-parse';
import rehypeRemark from 'rehype-remark';
import remarkGfm from 'remark-gfm';
import remarkStringify from 'remark-stringify';
import { unified } from 'unified';
import { SKIP, visit } from 'unist-util-visit';
import { sidebar } from '../sidebar.mjs';
import { markdownTwinPath } from '../src/lib/markdown-twin.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const dist = path.join(root, 'dist');
const site = 'https://getrenart.com';
const failures = [];

/** Pages converted from their built HTML. The landing page is hand-written. */
const hasTwin = (pathname) =>
  pathname.startsWith('/docs/') || pathname.startsWith('/compare/') || pathname === '/work-with-me/';

const parser = unified().use(rehypeParse);
const markdown = unified()
  .use(rehypeRemark)
  .use(remarkGfm)
  .use(remarkStringify, { bullet: '-', emphasis: '_', fences: true, listItemIndent: 'one', rule: '-' });

const pages = new Map();
for (const file of await readdir(dist, { recursive: true })) {
  if (path.basename(file) !== 'index.html' || file.startsWith('pagefind')) continue;
  const directory = path.dirname(file).split(path.sep).join('/');
  const pathname = directory === '.' ? '/' : `/${directory}/`;
  pages.set(pathname, parser.parse(await readFile(path.join(dist, file), 'utf8')));
}

const h = (tagName, properties = {}, children = []) => ({ type: 'element', tagName, properties, children });
const text = (value) => ({ type: 'text', value });
const hasClass = (node, name) => node.properties?.className?.includes(name);
const ariaHidden = (node) => String(node.properties?.ariaHidden) === 'true';
const removedTags = new Set(['button', 'caption', 'link', 'nav', 'noscript', 'script', 'source', 'style', 'svg', 'template']);
const removedClasses = ['sl-anchor-link', 'landing-eyebrow', 'feature-matrix__legend'];
const spacedContainers = new Set(['article', 'div', 'header', 'section', 'td', 'th']);

/** Visible text, skipping aria-hidden decoration such as arrows. */
function textOf(node) {
  if (node.type === 'text') return node.value;
  if (node.type !== 'element' && node.type !== 'root') return '';
  if (node.type === 'element' && ariaHidden(node)) return '';
  return node.children.map(textOf).join('');
}
const clean = (value) => String(value ?? '').replace(/\s+/g, ' ').trim();
const description = (tree) => clean(select('meta[name="description"]', tree)?.properties.content);

function linkTarget(href) {
  if (!href || href.startsWith('#')) return href;
  const url = new URL(href, site);
  if (url.origin !== site) return href;
  if (pages.has(url.pathname) && (hasTwin(url.pathname) || (url.pathname === '/' && !url.hash)))
    return site + markdownTwinPath(url.pathname) + url.hash;
  return url.href;
}

/** Rewrites Starlight and site components into plain HTML that maps to Markdown. */
function simplify(tree) {
  visit(tree, 'element', (node, index, parent) => {
    if (!parent || index === undefined) return;
    const replace = (...nodes) => {
      parent.children.splice(index, 1, ...nodes);
      return [SKIP, index];
    };
    if (hasClass(node, 'starlight-aside')) {
      const content = select('.starlight-aside__content', node)?.children ?? [];
      return replace(h('blockquote', {}, [h('p', {}, [h('strong', {}, [text(clean(node.properties.ariaLabel))])]), ...content]));
    }
    if (hasClass(node, 'expressive-code')) {
      const language = select('pre', node)?.properties.dataLanguage;
      const lines = selectAll('.ec-line', node).map((line) => toString(select('.code', line) ?? line));
      const className = language && language !== 'plaintext' ? [`language-${language}`] : [];
      return replace(h('pre', {}, [h('code', { className }, [text(lines.join('\n'))])]));
    }
    if (hasClass(node, 'card-grid') || hasClass(node, 'sl-link-card')) {
      const cards = selectAll('.sl-link-card', node).concat(hasClass(node, 'sl-link-card') ? [node] : []);
      const items = cards.map((card) =>
        h('li', {}, [
          h('a', { href: select('a', card)?.properties.href }, [text(clean(toString(select('.title', card))))]),
          text(`: ${clean(toString(select('.description', card)))}`),
        ]),
      );
      return replace(h('ul', {}, items));
    }
    if (hasClass(node, 'terminal-recording')) {
      const source = select('[data-terminal-recording]', node)?.properties.dataSrc;
      const caption = clean(toString(select('figcaption', node) ?? text('')));
      return replace(h('p', {}, [h('a', { href: source }, [text(clean(node.properties.ariaLabel))]), text(caption ? ` ${caption}` : '')]));
    }
    if (hasClass(node, 'themed-screenshot')) return replace(...[select('img', node)].filter(Boolean));
    if (removedTags.has(node.tagName) || ariaHidden(node) || removedClasses.some((name) => hasClass(node, name))) {
      parent.children.splice(index, 1);
      return [SKIP, index];
    }
    if (hasClass(node, 'sl-heading-wrapper')) return replace(...node.children);
    if (node.tagName === 'br' && /^h[1-6]$/.test(parent.tagName)) return replace(text(' '));
    if (hasClass(node, 'feature-matrix__focus') || hasClass(node, 'feature-matrix__edition'))
      node.children.unshift(text(' · '));
    if (hasClass(node, 'comparison-source')) {
      // Displayed on its own line after the cell's sentence.
      node.properties.className = [];
      parent.children.splice(index, 0, text(' '));
      return index + 1;
    }
    // Blocks and buttons that sit side by side would otherwise run together.
    if (spacedContainers.has(node.tagName))
      for (let i = node.children.length - 1; i > 0; i--)
        if (node.children[i].type === 'element' && node.children[i - 1].type === 'element')
          node.children.splice(i, 0, text(' '));
    if (node.tagName === 'a') node.properties.href = linkTarget(node.properties.href);
    if (node.tagName === 'img') {
      node.properties.src = new URL(node.properties.src, site).href;
      delete node.properties.srcSet;
    }
  });
  // Link text loses its decorative arrow above; drop the space that preceded it.
  visit(tree, { tagName: 'a' }, (node) => {
    const first = node.children[0];
    const last = node.children.at(-1);
    if (first?.type === 'text') first.value = first.value.trimStart();
    if (last?.type === 'text') last.value = last.value.trimEnd();
  });
}

async function toMarkdown(children) {
  const tree = await markdown.run({ type: 'root', children });
  return markdown
    .stringify(tree)
    .replace(/\[([^\]:]+): \1\]/g, '[$1]') // "Not offered: Not offered" from screen-reader text
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

const twins = new Map();
for (const [pathname, tree] of pages) {
  if (!hasTwin(pathname)) continue;
  const starlightContent = select('.sl-markdown-content', tree);
  const content = starlightContent ?? select('main', tree);
  simplify(content);
  const body = await toMarkdown(content.children);
  const twin = starlightContent
    ? `# ${clean(toString(select('h1#_top', tree)))}\n\n> ${description(tree)}\n\n${body}\n`
    : `${body}\n`;
  if (!twin.startsWith('# ')) failures.push(`${pathname}: Markdown version must start with a top-level heading`);
  twins.set(pathname, twin);
}

const title = (pathname) => clean(toString(select('title', pages.get(pathname)))).replace(/ \| Renart( Docs)?$/, '');
const entry = (label, pathname) => `- [${label}](${site}${markdownTwinPath(pathname)}): ${description(pages.get(pathname))}`;

const listed = new Set();
const docsSections = sidebar
  .map(({ label, items }) => {
    const lines = items.map((item) => {
      const pathname = `/${item.slug}/`;
      listed.add(pathname);
      if (!twins.has(pathname)) failures.push(`sidebar.mjs: no built docs page for ${item.slug}`);
      return entry(item.label, pathname);
    });
    return `## ${label}\n\n${lines.join('\n')}`;
  })
  .join('\n\n');
for (const pathname of twins.keys())
  if (pathname.startsWith('/docs/') && !listed.has(pathname)) failures.push(`${pathname}: docs page is missing from sidebar.mjs`);

const docsPages = [...listed].filter((pathname) => twins.has(pathname));
const comparePages = [...twins.keys()].filter((pathname) => pathname.startsWith('/compare/')).sort();
const llmsIntro = (await readFile(path.join(root, 'src/markdown/llms-intro.md'), 'utf8')).trim();
const docsHeading = `# Renart Docs\n\n> ${description(pages.get('/docs/'))}`;
const docsIntro = `${docsHeading}\n\nEvery page is also available as Markdown at its URL with \`.md\` in place of the trailing slash. The [complete documentation](${site}/llms-full.txt) puts all pages in one file.`;

const outputs = new Map([
  ['/index.md', await readFile(path.join(root, 'src/markdown/home.md'), 'utf8')],
  [
    '/llms.txt',
    `${llmsIntro}\n\n${docsSections}\n\n## Optional\n\n${[
      `- [Complete documentation](${site}/llms-full.txt): every documentation page above in one Markdown file`,
      ...comparePages.map((pathname) => entry(title(pathname), pathname)),
      entry(title('/work-with-me/'), '/work-with-me/'),
      '- [Source code](https://github.com/renart-data/renart): the Renart repository on GitHub',
    ].join('\n')}\n`,
  ],
  ['/docs/llms.txt', `${docsIntro}\n\n${docsSections}\n`],
  [
    '/llms-full.txt',
    `${docsHeading}\n\nEvery page of the Renart documentation in sidebar order, each with the URL of its web version.\n\n---\n\n` +
      docsPages
        .map((pathname) => twins.get(pathname).replace(/^# .*\n/, (heading) => `${heading}\nSource: ${site}${pathname}\n`))
        .join('\n---\n\n'),
  ],
  [
    '/404.md',
    `# Page not found\n\nThere is no page at this address. Start from the [Renart documentation](${site}/docs.md) or the [documentation index](${site}/llms.txt).\n`,
  ],
]);
for (const [pathname, twin] of twins) outputs.set(markdownTwinPath(pathname), twin);

if (outputs.get('/llms.txt').length > 30_000) failures.push('llms.txt exceeds 30,000 characters; move detail into linked files');

// The landing page is hand-written in Markdown; fail when its copy drifts.
const landing = pages.get('/');
const home = clean(outputs.get('/index.md').replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').replace(/[*_]/g, ''));
const landingCopy = [
  ...selectAll('main h1, main h2, main h3, main article > p, .section-lede, .landing-hero__description > p, .platform-list > li', landing).map(textOf),
  ...selectAll('[data-stage-button]', landing).flatMap((tab) => [tab.properties.dataTitle, tab.properties.dataDescription]),
];
for (const copy of new Set(landingCopy.map(clean)))
  if (copy && !home.includes(copy)) failures.push(`src/markdown/home.md is missing landing copy: "${copy}"`);

for (const [pathname, content] of outputs) await writeFile(path.join(dist, pathname), content);

// Every advertised Markdown alternate and every generated same-site link must resolve.
const resolves = (pathname) =>
  existsSync(path.join(dist, decodeURIComponent(pathname), pathname.endsWith('/') ? 'index.html' : ''));
for (const file of await readdir(dist, { recursive: true })) {
  if (!file.endsWith('.html') || file.startsWith('pagefind')) continue;
  const pathname = file === 'index.html' ? '/' : `/${file.split(path.sep).join('/').replace(/(^|\/)index\.html$/, '$1').replace(/\.html$/, '/')}`;
  const tree = pages.get(pathname) ?? parser.parse(await readFile(path.join(dist, file), 'utf8'));
  const alternate = select('link[rel~="alternate"][type="text/markdown"]', tree)?.properties.href;
  const expected = hasTwin(pathname) || pathname === '/' ? markdownTwinPath(pathname) : undefined;
  if (alternate !== expected)
    failures.push(`${file}: Markdown alternate should be ${expected ?? 'absent'}, found ${alternate ?? 'none'}`);
}
for (const [output, content] of outputs)
  for (const [, href] of content.matchAll(/\]\((https:\/\/getrenart\.com[^)\s]*)\)/g)) {
    const { pathname } = new URL(href);
    if (!resolves(pathname)) failures.push(`${output}: broken link ${href}`);
  }

if (failures.length) {
  console.error(failures.join('\n'));
  process.exit(1);
}
console.log(`Agent files written: ${twins.size} Markdown pages, index.md, llms.txt, docs/llms.txt, llms-full.txt, 404.md.`);
