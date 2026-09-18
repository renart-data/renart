export const productIds = ['renart', 'bruin', 'hex', 'dlt'] as const;
export type ProductId = (typeof productIds)[number];
export type CompetitorId = Exclude<ProductId, 'renart'>;
export type FeatureStatus = 'included' | 'paid' | 'additional' | 'unavailable';

export const products: Record<ProductId, { name: string; focus: string; edition: string }> = {
  renart: { name: 'Renart', focus: 'Pipelines + analysis', edition: 'Local · public alpha' },
  bruin: { name: 'Bruin', focus: 'Pipeline engineering', edition: 'CLI + VS Code' },
  hex: { name: 'Hex', focus: 'Collaborative analytics', edition: 'Hosted · plan-dependent' },
  dlt: { name: 'dlt', focus: 'Python data ingestion', edition: 'Open-source library' },
};

export const featureStatuses: Record<FeatureStatus, { icon: string; label: string }> = {
  included: { icon: '✓', label: 'Included' },
  paid: { icon: '$', label: 'Paid offering' },
  additional: { icon: '↗', label: 'Separate tool or service' },
  unavailable: { icon: '–', label: 'Not offered' },
};

interface FeatureCell {
  status: FeatureStatus;
  label: string;
  href: string;
}
interface FeatureRow {
  name: string;
  cells: Record<ProductId, FeatureCell>;
}

// Feature scope reviewed 2026-09-11; paid availability checked 2026-09-18.
// Keep each label linked to the source supporting its scope.
// A check denotes availability, not equivalent depth or a feature score.
export const featureRows: FeatureRow[] = [
  {
    name: 'Local use, no account',
    cells: {
      renart: { status: 'included', label: 'Local workspace', href: '/docs/how-it-works/' },
      bruin: { status: 'included', label: 'CLI + VS Code', href: 'https://getbruin.com/docs/bruin/getting-started/introduction/installation.html' },
      hex: { status: 'unavailable', label: 'Hex account required', href: 'https://learn.hex.tech/docs' },
      dlt: { status: 'included', label: 'Python library', href: 'https://dlthub.com/docs/intro' },
    },
  },
  {
    name: 'Data ingestion',
    cells: {
      renart: { status: 'included', label: 'Load + API assets', href: '/docs/asset-types/load-assets/' },
      bruin: { status: 'included', label: 'Connector assets', href: 'https://getbruin.com/docs/bruin/' },
      hex: { status: 'included', label: 'Files + Python code', href: 'https://learn.hex.tech/docs/explore-data/notebook-view/develop-your-notebook' },
      dlt: { status: 'included', label: 'Python sources', href: 'https://dlthub.com/docs/intro' },
    },
  },
  {
    name: 'SQL + Python',
    cells: {
      renart: { status: 'included', label: 'Pipeline assets', href: '/docs/editing-assets/asset-editor/' },
      bruin: { status: 'included', label: 'Pipeline assets', href: 'https://getbruin.com/docs/bruin/' },
      hex: { status: 'included', label: 'Notebook cells', href: 'https://learn.hex.tech/docs/explore-data/notebook-view/develop-your-notebook' },
      dlt: { status: 'included', label: 'Python + SQL access', href: 'https://dlthub.com/docs/intro' },
    },
  },
  {
    name: 'Editing experience',
    cells: {
      renart: { status: 'included', label: 'Types + completion', href: '/docs/editing-assets/asset-editor/' },
      bruin: { status: 'included', label: 'VS Code extension', href: 'https://getbruin.com/docs/bruin/getting-started/introduction/installation.html' },
      hex: { status: 'included', label: 'Notebook editor', href: 'https://learn.hex.tech/docs/explore-data/notebook-view/develop-your-notebook' },
      dlt: { status: 'additional', label: 'Your Python editor', href: 'https://dlthub.com/docs/hub/getting-started/oss-and-dlthub' },
    },
  },
  {
    name: 'Notebooks',
    cells: {
      renart: { status: 'included', label: 'SQL + Python', href: '/docs/notebooks/overview/' },
      bruin: { status: 'additional', label: 'External notebook tool', href: 'https://getbruin.com/docs/bruin/' },
      hex: { status: 'included', label: 'SQL + Python', href: 'https://learn.hex.tech/docs/explore-data/notebook-view/develop-your-notebook' },
      dlt: { status: 'additional', label: 'Marimo', href: 'https://dlthub.com/docs/general-usage/dataset-access/marimo' },
    },
  },
  {
    name: 'Dashboards + apps',
    cells: {
      renart: { status: 'included', label: 'Local dashboards', href: '/docs/presentations/overview/' },
      bruin: { status: 'additional', label: 'Bruin Cloud', href: 'https://getbruin.com/docs/bruin/cloud/overview.html' },
      hex: { status: 'included', label: 'Published apps', href: 'https://learn.hex.tech/docs' },
      dlt: { status: 'additional', label: 'Marimo / dltHub', href: 'https://dlthub.com/docs/hub/getting-started/oss-and-dlthub' },
    },
  },
  {
    name: 'Scheduled runs',
    cells: {
      renart: { status: 'included', label: 'Your Renart server', href: '/docs/scheduling/overview/' },
      bruin: { status: 'additional', label: 'Cloud / scheduler', href: 'https://getbruin.com/docs/bruin/pipelines/definition.html#schedule' },
      hex: { status: 'paid', label: 'Paid Hex plans', href: 'https://learn.hex.tech/docs/share-insights/scheduled-runs' },
      dlt: { status: 'additional', label: 'Scheduler / dltHub', href: 'https://dlthub.com/docs/hub/getting-started/oss-and-dlthub' },
    },
  },
  {
    name: 'Git versioning',
    cells: {
      renart: { status: 'included', label: 'Live project files', href: '/docs/how-it-works/' },
      bruin: { status: 'included', label: 'Project files', href: 'https://getbruin.com/docs/bruin/' },
      hex: { status: 'paid', label: 'Export on publish', href: 'https://learn.hex.tech/docs/explore-data/projects/git-export' },
      dlt: { status: 'included', label: 'Python project files', href: 'https://dlthub.com/docs/intro' },
    },
  },
  {
    name: 'Managed hosting',
    cells: {
      renart: { status: 'unavailable', label: 'Not offered', href: '/docs/reference/security-and-privacy/' },
      bruin: { status: 'additional', label: 'Bruin Cloud', href: 'https://getbruin.com/docs/bruin/cloud/overview.html' },
      hex: { status: 'included', label: 'Hosted workspace', href: 'https://learn.hex.tech/docs' },
      dlt: { status: 'paid', label: 'dltHub', href: 'https://dlthub.com/docs/hub/getting-started/oss-and-dlthub' },
    },
  },
];
