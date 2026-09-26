# Documentation structure: three options

Status: proposals for Lukas to choose. No public pages, URLs or sidebar have been moved. Inventory: 24 documentation pages in eight sidebar groups on 22 September 2026.

## What is awkward today

The sidebar starts with four introduction pages before installation. “Editing assets” contains one page, while “Analysis & scheduling” combines notebooks, agents, dashboards, reports, deployments and schedules. Connections/credentials come after those workflows even though readers need them earlier. The notebook overview spans imports, cells, copying results, controls, parameters, charts and promotion; the presentation overview combines two different artifacts.

Keep the existing UI-first writing contract in `architecture/docs.md`. Organize each page as a tutorial, how-to, reference or explanation, but do not require newcomers to understand those categories before finding their task. Keep Renart's open source data platform positioning and its mission; the docs should demonstrate the workflow rather than repeat marketing copy.

## A. Follow the data workflow — recommended

```text
Start here
  What is Renart?
  Install Renart
  Build your first pipeline (Quickstart)
  Tour the workspace
  Alpha status
Connect and load data
  Add a connection
  Credentials and environments
  Browse your data [new, verified workflow]
  Load data between systems
  Ingest an HTTP API
Build pipelines
  Work with the pipeline canvas
  Edit an asset
  SQL assets
  Python assets
  Checks and type checking [split from existing asset guidance]
Analyze data
  Work in a notebook
  Add data to a notebook [split]
  Parameters and controls [split]
  Create visualizations [split]
  Build a dashboard [split]
  Write a report [split]
  Notebook agents
Run and operate
  Inspect and run a pipeline [consolidate existing guidance]
  Runs and history
  Review a deployment [split]
  Create and manage schedules [split]
  Work from the terminal
Concepts and reference
  Concepts
  How Renart works
  Supported platforms
  CLI reference
  Security and privacy
```

Why: matches “connect → build → inspect → analyze → run repeatedly,” with familiar nouns and short task titles. Installation/Quickstart stay immediately visible. New/split pages are proposed, not claims of new features.

## B. Mirror the workspace

```text
Get started: overview, installation, quickstart, tour, alpha status
Data Browser and connections: browse, connections, credentials, loads/APIs
Build: canvas, editor, SQL/Python, checks
Notebooks: cells, sources, parameters, charts, agents
Dashboards and reports: dashboard, report, datasets/filters
Schedules and Runs: deployments, schedules, history
Reference: concepts, files/Git, CLI, platforms, privacy
```

Why: easiest to jump from an open UI surface to matching help. Tradeoff: users learning a complete workflow have to cross several product-area groups; new UI navigation names can force docs churn.

## C. Organize by reader intent (Diátaxis)

```text
Tutorials: quickstart, notebook-to-dashboard walkthrough [new]
How-to guides: connect, load, build, analyze, deploy, troubleshoot
Reference: CLI, platforms, credentials/security, supported capabilities
Explanation: concepts, architecture/files/Git, environments, staleness
```

Why: strong authoring discipline and clean separation of learning vs lookup. Tradeoff: “How-to guides” becomes a very large branch, and visitors may have to guess the page type. Best used within A rather than as the top-level sidebar for this product today.

## Full mapping for option A

Preserve every current URL in the first pass. This table maps all 24 existing pages, not just the most visible ones.

| Current page under `/docs/` | Destination group / later split |
| --- | --- |
| index | Start / What is Renart? |
| alpha-status | Start / Alpha status |
| installation | Start / Install Renart |
| quickstart | Start / Build your first pipeline |
| workspace/interface-tour | Start / Tour the workspace |
| connections-environments/managing-connections | Connect and load / Add a connection |
| connections-environments/managing-credentials | Connect and load / Credentials and environments |
| asset-types/load-assets | Connect and load / Load data between systems |
| asset-types/http-api-assets | Connect and load / Ingest an HTTP API |
| workspace/pipeline-canvas | Build / Work with the pipeline canvas |
| editing-assets/asset-editor | Build / Edit an asset; later extract checks/type-checking tasks |
| asset-types/sql-assets | Build / SQL assets |
| asset-types/python-assets | Build / Python assets |
| notebooks/overview | Analyze / Work in a notebook; later split sources, controls and charts |
| notebooks/agents | Analyze / Notebook agents |
| presentations/overview | Analyze / Dashboard/report hub; later split the two workflows |
| workspace/runs-and-history | Run and operate / Runs and history |
| scheduling/overview | Run and operate / Deployment/scheduling hub; later split the two tasks |
| cli/work-from-terminal | Run and operate / Work from the terminal |
| concepts | Concepts and reference / Concepts |
| how-it-works | Concepts and reference / How Renart works |
| reference/supported-platforms | Concepts and reference / Supported platforms |
| reference/cli | Concepts and reference / CLI reference |
| reference/security-and-privacy | Concepts and reference / Security and privacy |

## Rollout and acceptance

1. **Navigation only (half a day):** select A/B/C, reorder and relabel sidebar, improve the docs index with three entry points (first pipeline, bring existing data, explore in a notebook). Keep existing slugs and heading anchors.
2. **Separate overloaded guides (1–2 days):** split notebooks, presentations and scheduling using existing verified steps. Keep the original pages as useful hubs and preserve old anchored links with targeted sections/redirects when necessary. No mass URL rename for cosmetic folder consistency.
3. **Close evidence-backed gaps (1–2 days):** document Data Browser, result limits and resolving unavailable credentials once the corresponding behavior is established. Add task-focused troubleshooting based on real failure modes, not a generic FAQ.
4. **Verify:** internal links/anchors and search results, mobile sidebar, keyboard navigation, a first-time reader path from installation through a successful run, and dark/light screenshots from the documented capture workflow. Build Astro and smoke the production Docker/Caddy path; HTTP 200 alone is not a rendering test.

Review measure: can a new user find connection setup, first successful pipeline, notebook chart and schedule creation in two navigation decisions? Check those four tasks before expanding the page count further. Website metrics can help later when connected; they are not required to make this structural improvement.

Decision requested: choose A, B or C. I recommend A, with Diátaxis used to keep individual pages focused.
