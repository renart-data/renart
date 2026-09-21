# Renart Docs — How We Write the User Docs

Status: active. This is the authoring contract for everything under `docs/`
(Astro Starlight, served at getrenart.com/docs) — the docs pages *and* the
landing page. The canonical page set is whatever `docs/astro.config.mjs`
declares in its sidebar (see §9 for how it got that shape); this doc defines
*how* to write it. If you are writing or reviewing a docs page, this is the
checklist.

The locked decisions (§0) are the non-negotiable part. The rest is craft guidance.

Product descriptions follow the positioning in `AGENTS.md`: **open source
data platform** for moving, transforming and analyzing data, with a
first-class developer experience across the workflow. The landing page,
docs overview and metadata use this category. The editor and workspace are
individual surfaces; Jinja, lineage and staleness support the wider story.

---

## 0. Locked decisions

These were decided with the user and govern every page. Don't re-litigate them in
a PR; change them here first if they need to change.

1. **Teach Renart's own model.** Tutorials, reference pages, the landing page
   and their metadata use Renart's own UI vocabulary. They do not imply
   guaranteed compatibility with Bruin. The user-requested comparisons under
   `docs/src/pages/compare/` are a narrow exception: they may name Bruin,
   explain the actual engine relationship and compare documented editions.
   Never turn that relationship into an untested migration or round-trip
   promise. `rg -i bruin docs/src/content/docs docs/src/pages/index.astro`
   remains empty; UI-authored examples do not expose raw metadata headers.
2. **Web-UI-first.** Nothing in the docs may require — or suggest — editing an
   asset's metadata encoding by hand. Users see the SQL editor, the Python
   editor, the Load form, the API editor, and the workbench; the docs describe
   those surfaces in the UI's own vocabulary. That everything is *stored* as
   plain files is explained once, at a high level, on the **How it works**
   page (readable diffs, review, no lock-in) — as a property of the product,
   never as an editing interface. Code samples show what the user types in the
   editor (the query, the `materialize()` body), not file headers. If a
   sentence only helps someone hand-editing files, it doesn't belong in the
   user docs.
3. **Docs vs. in-app help — the line.** Docs own **conceptual and multi-step**
   content (what a thing is, why it exists, how to complete a task end to end). The
   **app** owns **field-level hints** (a tooltip on one input, an empty-state
   nudge, an inline validation message). If you're tempted to document a single
   field's meaning in prose, that's an in-app tooltip, not a docs page. If you're
   tempted to put a five-step flow in a tooltip, that's a how-to.
4. **Screenshots are scripted.** `make docs-media` regenerates the shared docs
   screenshots from scratch against a staged demo workspace (the acme project
   from `web/scripts/landing-media-workspace.mjs`), and `make landing-media`
   regenerates the landing-specific screenshots. The landing page can reuse a
   shared shot from `docs/public/docs-media/` when it shows the same product
   state. Never hand-capture: a shipped image is always the verbatim output of
   one of those scripts, so it can be regenerated after any UI change. See §5.
5. **Versioning: unversioned for now.** Docs track `main`. No Starlight versioning
   yet. Revisit when releases stabilise (the trigger lives in the concept doc, not
   here).
6. **Ownership: a "docs touched?" gate.** A user-facing change ships with its docs.
   PRs carry a docs checkbox (§7); reviewers enforce it.
7. **Security copy is literal.** The Security & privacy page describes the
   current local trust boundary only. The static build contains no tracker.
   The production site can load the explicitly configured self-hosted Umami
   script only after affirmative visitor consent; decline and later revocation
   remain equally accessible. Caddy supplies CSP, framing, MIME-sniffing,
   permissions, referrer, and HSTS headers for the deployed site.

## 1. The four modes (Diátaxis)

Every page is exactly one of these. A page that mixes modes gets split. When you
don't know where content goes, you've usually got two pages fused together.

| Mode | The reader wants to… | Shape | Renart examples |
| --- | --- | --- | --- |
| **Tutorial** | learn by doing, build confidence | one guided happy path, every step verifiable, no choices | Quickstart; "Build your first pipeline" |
| **How-to** | get a specific task done now | goal → steps → result, assumes context | "Materialize a pipeline", "Load a CSV with a Load asset" |
| **Reference** | confirm a fact fast | terse, table-first, no narrative | CLI flags, load modes, check names |
| **Explanation** | understand why/how it fits | prose, diagrams, no steps | Concepts; "How Renart stores everything as files" |

Litmus test before writing: **"Is the reader learning, doing, looking up, or
understanding?"** That answer is the page type. Put it in the frontmatter intent
(see §6) and don't drift.

## 2. Voice & tone

- **Second person, present tense, active voice.** "Click **Materialize**." Not
  "The user should click" or "Materialization will be triggered."
- **Lead with the reader's goal, not our architecture.** Open with what they get,
  not how it's built. The "why it's interesting to us" belongs in Explanation pages,
  and even there, kept short.
- **Concise.** Cut hedging ("simply", "just", "of course"), throat-clearing intros,
  and restating the heading. If a sentence doesn't change what the reader does or
  understands, delete it.
- **Concrete over abstract.** Name the real button, the real file, the real menu.
  Use the example project's real asset names.
- **The UI is the interface.** Describe what the reader clicks and types in
  the product, not the files it produces. "Add a `not_null` check in the
  Columns card", not "add a `checks:` entry".
- **Confident, not salesy.** State what Renart does. Skip adjectives like
  "powerful", "seamless", "blazing".

## 3. Terminology — use the UI's exact words

The docs and the product must use **one vocabulary**. Drift here is the fastest way
to confuse readers.

- Use the **exact labels the UI shows**: Build, Catalog, Notebooks, Schedules,
  Runs, Settings; asset, pipeline, connection, environment, the workbench,
  Inspect, Materialize, staleness. Match capitalisation to the UI.
- One concept, one term. Don't alternate "task"/"asset" or "graph"/"canvas".
  The canonical term is whatever the UI prints; the glossary (Concepts page) is the
  source of truth and every term links back to it on first use per page.
- When the product renames something, the docs rename in the **same PR** (§7).
- Prefer the product's noun for the noun and a plain verb for the action:
  "create an asset", "open the canvas", "run the pipeline".

## 4. Page templates

Author from these skeletons so pages stay uniform and scannable. Keep one `H1`,
sentence-case headings that echo UI labels.

### How-to

```
# <Verb the task>           ← task-titled: "Add a manual dependency"
<One sentence: what you'll accomplish and when you'd want to.>

## Before you start          ← prerequisites, only if real
- …

## Steps
1. <Action with the real UI label in bold>
2. …

## Result
<What the reader can now see/verify. A screenshot of the end state if it helps.>

## Related                   ← Diátaxis siblings + the concept it rests on
- …
```

### Tutorial

```
# <Build/Do something concrete>
<What you'll have built by the end, and roughly how long.>

<Numbered narrative. One happy path. No "alternatively". Every step ends in
something the reader can see on screen. Screenshots at the moments that orient.>

## What you built
<Recap + the obvious next tutorial/how-to.>
```

### Reference

```
# <Thing>
<One line of scope.>

<Tables first. Flags, keys, defaults, values. Minimal prose — only what a table
can't carry. No tutorials, no motivation.>
```

### Explanation

```
# <Concept or "How X works">
<Prose. Diagrams welcome. Answer "why does this exist / how do the pieces fit".
No step lists — link to the how-tos this motivates instead.>
```

## 5. Screenshots (scripted)

Screenshots are first-class and generated, never hand-captured. The bar is
"would this look good in a product tour" — and the way we hold it is that
every shipped image is the verbatim output of a script.

- **Two pipelines, one staged demo.** `make landing-media` regenerates
  `docs/public/landing/*`, `make docs-media` regenerates
  `docs/public/docs-media/*`. Both run Playwright against the same staged
  acme workspace (`web/scripts/landing-media-workspace.mjs` +
  `web/scripts/demo-media-lib.mjs`): a coherent e-commerce project with real
  materialized data, run history including one failed run, schedules, a
  notebook, dashboard, report, and staleness states — so no shot is ever empty
  or half-loaded.
- **Reuse the right shot.** Landing-only compositions live under `/landing/`;
  product-state screenshots shared with the docs live under `/docs-media/`.
  Referencing a shared shot from the landing page does not make it part of the
  landing-media capture pipeline.
- **Adding a shot** means adding a capture block to the matching script:
  `web/scripts/capture-docs-media.mjs` for shared product-state media or
  `web/scripts/capture-landing-media.mjs` for a landing-only composition. Each
  block owns its viewport, navigation, interactions, and `shot(page, "name")`
  call; never open a browser by hand. If a shot needs state the staged demo
  lacks (an asset type, a dialog), the script creates it through the same HTTP
  API the UI uses, after the DAG-wide shots so the canvas captures stay stable.
- **Matching themes.** Capture the actual app at `deviceScaleFactor: 2`, in
  webp. The landing hero, editor proof and eight workspace views have paired
  dark (`name.webp`) and light (`name-light.webp`) captures. `makeCapture` uses
  Renart's own theme event so Monaco, charts and canvas render their real theme
  without changing the staged view. Both variants have identical dimensions;
  pages embed explicit `width`/`height` matching the emitted files.
- **Few and high-value.** A screenshot earns its place by orienting the reader
  or proving a result — roughly one per page, not one per step.
- **Every image needs alt text** describing what it shows (not "screenshot").
  The canvas shots especially — describe the lineage being shown.
- **Maintenance.** When the UI changes a surface, rerun the make target in the
  same PR and commit the regenerated images. If a capture's dimensions change,
  update the `width`/`height` where the image is referenced.
- **Terminal recordings are generated too.** `make cli-recordings` builds the
  current binary, scaffolds a disposable project, executes the documented
  read-only CLI workflow, and writes deterministic asciicast v2 files under
  `docs/public/cli-recordings/`. The self-hosted player is loaded only by pages
  that embed `TerminalRecording.astro`; recordings are never uploaded to an
  external service.

## 6. Conventions

- **Frontmatter** on every page: `title` and `description`. The `description` is the
  search/SERP line — write it for a human deciding whether to click, in one
  sentence, no period-padding. (Optionally record the Diátaxis mode in a comment or
  sidebar group so intent stays explicit.)
- **One H1**, sentence-case headings matching UI labels.
- **Code/CLI blocks are copy-paste runnable** against the example project. Show the
  command and, where it clarifies, the expected output. Use real paths
  (`example/example`), not `<your-project>` unless the value is genuinely the
  reader's.
- **Callouts (Starlight asides)** for two things especially: the git-native
  property ("every edit is a plain file change you can commit") and
  destructive/side-effecting actions (materialize writes to the warehouse;
  full-refresh). Use `:::caution`/`:::note`/`:::tip` sparingly and meaningfully.
- **Cross-link to siblings.** Every page links to its Diátaxis neighbours under
  "Related" and to the one concept it rests on. How-tos link up to the explanation;
  explanations link down to the how-tos.
- **Lists over paragraphs** for anything enumerable. **Tables** for anything with
  parallel structure (flags, options, comparisons).

## 7. Ownership — the "docs touched?" gate

A user-facing change is not done until its docs are. Operationally:

- **PR checkbox.** Every PR template carries: *"User-facing change? → docs updated
  (or N/A because …)."* Author ticks it; reviewer verifies it's honest.
- **Same-PR or adjacent-PR.** P0/P1 docs land with the feature. P2 polish may
  follow in a tracked adjacent PR, not "someday".
- **Rename discipline.** A UI label change includes the docs + glossary edit in the
  same PR (§3).
- **Reviewer's job.** Treat a missing/limp doc the way you'd treat a missing test:
  request changes. "Was this helpful?" feedback and real reader gaps feed back into
  the backlog.

## 8. Review checklist

Before approving a docs PR, confirm:

- [ ] **One mode.** The page is purely tutorial / how-to / reference / explanation.
- [ ] **No bruin.** `grep -ri bruin` over the page comes back empty.
- [ ] **Web-UI-first.** Every instruction works for a reader who only ever
      uses the web UI; no hand-editing of metadata, no raw file headers in
      samples.
- [ ] **UI words.** Terminology and capitalisation match the product 1:1; first use
      of each term links to the glossary.
- [ ] **Task-titled** (how-tos) and ends in a verifiable result.
- [ ] **Frontmatter** `title` + `description` present and reader-useful.
- [ ] **Runnable** code/CLI against the example project.
- [ ] **Screenshots** (if any) come out of `make docs-media` or, for a
      landing-only composition, `make landing-media`; they are alt-texted and
      their capture block lives in the matching script.
- [ ] **Cross-links** to Diátaxis siblings + the resting concept.
- [ ] **Right place / right scope** — not a tooltip masquerading as a page, nor a
      flow crammed into reference.
- [ ] **The gate** is satisfied: the feature this documents actually matches what
      ships.

## 9. The shipped page set & the landing page (as-built record)

Folded here from `plans/docs-alpha.md` and `plans/landing-page.md` when they
shipped (July 2026; git history keeps the full plans).

- **The alpha page set** is deliberately small: ~18 real pages instead of the
  46-stub IA from the earlier rollout plan. Deleted stubs come back from git
  as their features stabilise, at the position the rollout IA assigned them.
  The sidebar in `docs/astro.config.mjs` is the authoritative list; every
  entry must be a real page (verification: `pnpm build` in `docs/` green, no
  dead links, no Bruin references in tutorials or reference pages). The Introduction group includes
  a concise **Alpha status** page that distinguishes shipped workflows from
  release-stage caveats without becoming a roadmap.
- **The landing page** (`docs/src/pages/index.astro`) introduces an open source
  data platform through a single product screenshot → Move / Transform /
  Analyze overview → editor proof and maintainability principles → workspace
  tour → local infrastructure and reviewable definitions → quickstart/install.
  Cream surfaces and ink text in light mode, deep green surfaces in dark mode,
  and restrained green accents keep the focus on the product. The hero install
  command is a prominent, selectable card with a copy button: labeled on desktop,
  icon-only with an accessible name on mobile. The installation command is also
  available in the closing section. It does not simulate live runtime status or promise an exact rebuild
  count in a decorative diagram. The Load / Build / Lineage / Notebook /
  Dashboard / Report / Schedule / Runs tour uses a plain tablist with keyboard
  navigation, a labeled panel and links to full-size scripted screenshots.
  `MarketingTheme.astro` initializes the system/saved theme before paint, uses
  the same `starlight-theme` preference as the docs, and keeps the accessible
  header toggle, `<picture>` sources and full-size links in sync. Storage being
  unavailable does not prevent switching themes. The comparison pages share
  this control and palette. Without JavaScript, CSS and `<picture>` follow the
  system theme. Small vanilla-JavaScript enhancements handle tabs, command copy
  and mobile navigation; the initial product view and links remain available without a
  client framework. While the product is in
  public alpha, the page title/description, hero badge, visible expectation
  note, and footer say so directly; the repository README carries the same
  status. Alpha is a release-stage disclosure, not a substitute for describing
  what currently works.
- **Landing media** combines landing-specific shots from `make landing-media`
  with existing product-state shots from `make docs-media` (§5). The hero and
  editor proof use responsive images under `docs/public/landing/`; the
  workspace tour points at the corresponding generated images
  under `docs/public/docs-media/`. The landing page selects them with `srcset`
  and `sizes`; if a capture changes dimensions, update the matching dimensions
  and source descriptors in `index.astro`. `ThemedScreenshot.astro` keeps the
  light and dark source sets together, including the workspace tour and the
  comparison screenshots. Landing and social image URLs use
  content hashes from `docs/src/lib/public-media.mjs`, evaluated during the
  docs package build, so refreshed files bypass older immutable browser caches.

## 10. Production privacy and legal configuration

The production docs container uses Caddy's custom `[[RENART ... RENART]]`
template delimiters to insert public legal identity values at request time.
The operator's name and address therefore never need to be committed. The
container fails fast when any required legal value is absent instead of serving
an incomplete Impressum.

| Environment variable | Required | Purpose |
| --- | --- | --- |
| `RENART_LEGAL_NAME` | yes | natural person or legal entity operating the site |
| `RENART_LEGAL_ADDRESS_LINE_1` | yes | street and house number |
| `RENART_LEGAL_POSTAL_CODE` | yes | postal code |
| `RENART_LEGAL_CITY` | yes | city |
| `RENART_LEGAL_COUNTRY` | yes | country |
| `RENART_LEGAL_EMAIL` | yes | direct contact address |
| `RENART_ANALYTICS_RETENTION_DAYS` | yes | published maximum Umami event retention; must be a positive integer |
| `RENART_UMAMI_WEBSITE_ID` | no | enables the optional analytics choice; in production, blank means no tracker and no dialog |

All legal values pass through the template engine's HTML escaping before they
reach the response. `PUBLIC_RENART_LEGAL_*`,
`PUBLIC_RENART_ANALYTICS_RETENTION_DAYS`, and
`PUBLIC_RENART_UMAMI_WEBSITE_ID` are build-time equivalents for a non-container
static deployment; the production container deliberately requires the runtime
legal variables.

The German legal pages live at `/impressum/` and `/datenschutz/`; their English
translations live at `/legal-notice/` and `/privacy/`. Each page links to its
counterpart and declares it as an alternate language. The English landing page
and documentation footer link to the English routes.

`PrivacyConsent.astro` is the only tracker loader. It adapts the maintained
`vanilla-cookieconsent` library to Renart's copy and visual language. The
strictly necessary `renart_consent` cookie records consent categories and
timestamps for 180 days; the optional `analytics` category creates the Umami
script only after affirmative consent. The loader excludes query strings and
hashes, respects Do Not Track, and reloads after revocation so already-installed
tracker listeners cannot continue. Do not reintroduce raw head injection such
as `RENART_TRACKING_HEAD`; it bypasses this contract.

During `astro dev`, the same real consent and preferences dialogs run even
without a website ID, and their cookie persists across reloads. Development
mode never creates the Umami script or makes an analytics request. Clear the
`renart_consent` cookie to repeat the first-visit path, or use **Privacy
settings** in the footer to revisit the preferences dialog.

`DiscordInvite.astro` provides the site-wide, bottom-right community invitation.
Its callout can be dismissed while the direct Discord button remains available.
Dismissal stores only `1` under `renart.discord-invite-dismissed.v1` in browser
local storage; both legal-language versions disclose that preference.

`RENART_ANALYTICS_RETENTION_DAYS` publishes the retention promise; it does not
delete Umami rows by itself. The deployment owner must configure and verify a
matching cleanup policy in the self-hosted Umami database before enabling the
tracker.

## Comparison pages

`/compare/` and its three authored guides share the marketing header, footer
and typography with the landing page. They are decision guides, separate from
the task-oriented Starlight docs. Each identifies the product editions, shows
the documentation review date, credits the other product's strengths and links
its factual claims to primary sources. Renart screenshots come from the same
scripted media pipeline. Do not describe these documentation-based comparisons
as hands-on benchmarks or tested migrations. Changing CSS does not advance the
content review date. Add further guides only when there is a distinct decision
and enough evidence to help the reader.

The comparison index includes a four-product feature matrix; each guide shows
the same data for its pair above the detailed workflow comparison.
`docs/src/data/comparison-features.ts` owns the product editions, short feature
labels, availability and per-cell source links. `FeatureMatrix.astro` renders
the shared semantic table. Keep included capabilities, separate tools or paid
offerings, and unavailable features distinct without assigning scores. The
four-product table scrolls with sticky feature names; paired tables fit small
screens. New claims require source review, not just a copied checkmark.
