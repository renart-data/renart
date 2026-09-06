import { defineConfig, fontProviders } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeRapide from 'starlight-theme-rapide';

const site = 'https://getrenart.com';
const socialImage = `${site}/landing/og-image.png`;
const siteDescription = 'Renart is the git-native data pipeline IDE: a visual canvas, editor, inspect, and execution workflow for version-controlled pipelines.';
const docsDescription = 'Documentation for Renart, the git-native data pipeline IDE for visually editing, inspecting, running, and understanding data pipelines.';

const docsStructuredData = JSON.stringify({
  '@context': 'https://schema.org',
  '@type': 'WebSite',
  name: 'Renart Docs',
  url: `${site}/docs/`,
  description: docsDescription,
  publisher: {
    '@type': 'Organization',
    name: 'Renart',
    url: site,
    logo: `${site}/icons/icon.svg`,
  },
});

export default defineConfig({
  site,
  // The docs and landing page are static and link-light. Starlight otherwise
  // enables a global prefetch observer on every page, which adds avoidable
  // startup work to the landing page for a marginal navigation benefit.
  prefetch: false,
  fonts: [
    {
      provider: fontProviders.google(),
      name: 'Geist',
      cssVariable: '--font-geist',
      weights: ['400', '500', '600'],
      styles: ['normal'],
      subsets: ['latin'],
      display: 'swap',
      fallbacks: ['system-ui', 'sans-serif'],
    },
    {
      provider: fontProviders.google(),
      name: 'Geist Mono',
      cssVariable: '--font-geist-mono',
      weights: ['400', '500'],
      styles: ['normal'],
      subsets: ['latin'],
      display: 'swap',
      fallbacks: ['ui-monospace', 'monospace'],
    },
    {
      provider: fontProviders.google(),
      name: 'Instrument Serif',
      cssVariable: '--font-instrument-serif',
      weights: ['400'],
      styles: ['normal', 'italic'],
      subsets: ['latin'],
      display: 'swap',
      fallbacks: ['Georgia', 'serif'],
    },
  ],
  integrations: [
    starlight({
      title: 'Renart Docs',
      components: {
        Footer: './src/components/Footer.astro',
        Head: './src/components/Head.astro',
      },
      logo: {
        src: '../web/public/icons/icon-64.png',
      },
      favicon: '/icons/icon-32.png',
      plugins: [starlightThemeRapide()],
      description: docsDescription,
      head: [
        { tag: 'meta', attrs: { name: 'application-name', content: 'Renart' } },
        { tag: 'meta', attrs: { name: 'apple-mobile-web-app-title', content: 'Renart' } },
        { tag: 'meta', attrs: { name: 'theme-color', content: '#0f172a' } },
        { tag: 'meta', attrs: { name: 'keywords', content: 'Renart, data pipeline IDE, git-native data pipelines, data engineering, analytics engineering, lineage canvas' } },
        { tag: 'meta', attrs: { name: 'author', content: 'Renart' } },
        { tag: 'meta', attrs: { property: 'og:type', content: 'website' } },
        { tag: 'meta', attrs: { property: 'og:site_name', content: 'Renart Docs' } },
        { tag: 'meta', attrs: { property: 'og:description', content: siteDescription } },
        { tag: 'meta', attrs: { property: 'og:locale', content: 'en_US' } },
        { tag: 'meta', attrs: { property: 'og:image', content: socialImage } },
        { tag: 'meta', attrs: { property: 'og:image:width', content: '1200' } },
        { tag: 'meta', attrs: { property: 'og:image:height', content: '675' } },
        { tag: 'meta', attrs: { property: 'og:image:alt', content: 'Renart DAG canvas showing a connected data pipeline' } },
        { tag: 'meta', attrs: { name: 'twitter:card', content: 'summary_large_image' } },
        { tag: 'meta', attrs: { name: 'twitter:title', content: 'Renart Docs' } },
        { tag: 'meta', attrs: { name: 'twitter:description', content: docsDescription } },
        { tag: 'meta', attrs: { name: 'twitter:image', content: socialImage } },
        { tag: 'meta', attrs: { name: 'twitter:image:alt', content: 'Renart DAG canvas showing a connected data pipeline' } },
        { tag: 'link', attrs: { rel: 'sitemap', type: 'application/xml', href: `${site}/sitemap-index.xml` } },
        { tag: 'script', attrs: { type: 'application/ld+json' }, content: docsStructuredData },
      ],
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/renart-data/renart',
        },
        {
          icon: 'discord',
          label: 'Discord',
          href: 'https://discord.gg/jTH758KNP8',
        },
      ],
      sidebar: [
        {
          label: 'Introduction',
          items: [
            { label: 'Overview', slug: 'docs' },
            { label: 'Alpha status', slug: 'docs/alpha-status' },
            { label: 'Concepts', slug: 'docs/concepts' },
            { label: 'How it works', slug: 'docs/how-it-works' },
          ],
        },
        {
          label: 'Getting started',
          items: [
            { label: 'Installation', slug: 'docs/installation' },
            { label: 'Quickstart', slug: 'docs/quickstart' },
            { label: 'Work from the terminal', slug: 'docs/cli/work-from-terminal' },
          ],
        },
        {
          label: 'The workspace',
          items: [
            { label: 'Tour of the interface', slug: 'docs/workspace/interface-tour' },
            { label: 'The pipeline canvas', slug: 'docs/workspace/pipeline-canvas' },
            { label: 'Runs & history', slug: 'docs/workspace/runs-and-history' },
          ],
        },
        {
          label: 'Editing assets',
          items: [
            { label: 'The asset editor', slug: 'docs/editing-assets/asset-editor' },
          ],
        },
        {
          label: 'Asset types',
          items: [
            { label: 'SQL assets', slug: 'docs/asset-types/sql-assets' },
            { label: 'Python assets', slug: 'docs/asset-types/python-assets' },
            { label: 'Load assets', slug: 'docs/asset-types/load-assets' },
            { label: 'HTTP API assets', slug: 'docs/asset-types/http-api-assets' },
          ],
        },
        {
          label: 'Analysis & scheduling',
          items: [
            { label: 'Notebooks', slug: 'docs/notebooks/overview' },
            { label: 'Notebook agents', slug: 'docs/notebooks/agents' },
            { label: 'Dashboards & reports', slug: 'docs/presentations/overview' },
            { label: 'Deployments & schedules', slug: 'docs/scheduling/overview' },
          ],
        },
        {
          label: 'Connections & environments',
          items: [
            { label: 'Managing connections', slug: 'docs/connections-environments/managing-connections' },
            { label: 'Connection credentials', slug: 'docs/connections-environments/managing-credentials' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'Supported platforms', slug: 'docs/reference/supported-platforms' },
            { label: 'CLI reference', slug: 'docs/reference/cli' },
            { label: 'Security & privacy', slug: 'docs/reference/security-and-privacy' },
          ],
        },
      ],
    }),
  ],
});
