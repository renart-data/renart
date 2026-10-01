// Shared by astro.config.mjs and scripts/build-agent-files.mjs, which
// orders llms.txt by the same groups.
//
// Workflow order: start → connect and load → build → analyze → run →
// look things up (plans/docs-information-architecture.md, option A).
// Slugs are unchanged; only groups and order move.
export const sidebar = [
  {
    label: 'Start here',
    items: [
      { label: 'Overview', slug: 'docs' },
      { label: 'Installation', slug: 'docs/installation' },
      { label: 'Quickstart', slug: 'docs/quickstart' },
      { label: 'Tour of the interface', slug: 'docs/workspace/interface-tour' },
      { label: 'Alpha status', slug: 'docs/alpha-status' },
    ],
  },
  {
    label: 'Connect and load data',
    items: [
      { label: 'Managing connections', slug: 'docs/connections-environments/managing-connections' },
      { label: 'Connection credentials', slug: 'docs/connections-environments/managing-credentials' },
      { label: 'Browse your data', slug: 'docs/connections-environments/data-browser' },
      { label: 'Load assets', slug: 'docs/asset-types/load-assets' },
      { label: 'HTTP API assets', slug: 'docs/asset-types/http-api-assets' },
    ],
  },
  {
    label: 'Build pipelines',
    items: [
      { label: 'The pipeline canvas', slug: 'docs/workspace/pipeline-canvas' },
      { label: 'The asset editor', slug: 'docs/editing-assets/asset-editor' },
      { label: 'SQL assets', slug: 'docs/asset-types/sql-assets' },
      { label: 'Python assets', slug: 'docs/asset-types/python-assets' },
      { label: 'Variables and Jinja in SQL', slug: 'docs/editing-assets/variables-and-jinja' },
      { label: 'Type checking', slug: 'docs/editing-assets/type-checking' },
    ],
  },
  {
    label: 'Analyze data',
    items: [
      { label: 'Notebooks', slug: 'docs/notebooks/overview' },
      { label: 'Notebook agents', slug: 'docs/notebooks/agents' },
      { label: 'Dashboards & reports', slug: 'docs/presentations/overview' },
    ],
  },
  {
    label: 'Run and operate',
    items: [
      { label: 'Runs & history', slug: 'docs/workspace/runs-and-history' },
      { label: 'Rebuild only what changed', slug: 'docs/workspace/rebuild-what-changed' },
      { label: 'Deployments & schedules', slug: 'docs/scheduling/overview' },
      { label: 'Work from the terminal', slug: 'docs/cli/work-from-terminal' },
    ],
  },
  {
    label: 'Concepts and reference',
    items: [
      { label: 'Concepts', slug: 'docs/concepts' },
      { label: 'How it works', slug: 'docs/how-it-works' },
      { label: 'Supported platforms', slug: 'docs/reference/supported-platforms' },
      { label: 'CLI reference', slug: 'docs/reference/cli' },
      { label: 'Security & privacy', slug: 'docs/reference/security-and-privacy' },
      { label: 'Troubleshooting', slug: 'docs/troubleshooting' },
    ],
  },
];
