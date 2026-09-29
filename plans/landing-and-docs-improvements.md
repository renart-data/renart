# Landing page and docs improvements

Status: decided on 27 September 2026. Phases 1–3 are implemented on branch
`landing-story-video` and recorded in `architecture/docs.md`; phases 4–5 are
open. The findings below were reviewed against the same branch, using the Astro
dev server at 1440 px and 390 px wide in dark and light themes, the page sources,
and `node scripts/check-content.mjs` (24 pages, 15 images).

This plan complements [Docs structure](docs-information-architecture.md). That
plan owns sidebar navigation and the page mapping; this one covers the landing
page, docs content gaps, media and the shared look. The positioning and
underpromise rules in `AGENTS.md` and the authoring contract in
`architecture/docs.md` apply unchanged. Every new sentence must describe
shipped, verified behavior.

## What already works — keep it

- The headline, positioning and install card are clear. The alpha disclosure is
  honest and appears in the right places.
- Media is scripted (`make landing-media`, `make docs-media`,
  `make cli-recordings`, and now `make landing-video`), comes in theme pairs, and
  has alt text.
- The workspace tour is an accessible tablist backed by real product
  screenshots. The page works without JavaScript.
- The docs pages follow the UI-first contract. SQL, Load and HTTP API pages are
  specific and accurate. The `renart type-check` link from the editor docs is a
  good bridge to CI.

## Findings

### Landing page

1. **The hero proof is hard to read.** The hero is a full-workspace capture
   scaled to 1168 px, where the UI text renders at about 4–5 px. On a 390 px phone
   it's decorative only. The new story video shows the same workflow at
   readable scale.
2. **First visits stack three overlays.** The consent dialog opens centred
   over the hero, and the Discord card and Discord button appear at the same time.
   On phones the consent sheet covers the whole first screen. The same happens on
   every docs page for a first-time reader.
3. **The page is long and uniform.** It's about 5,300 px tall on desktop and
   6,200 px on phones, with five sections that repeat eyebrow → serif H2 → three
   text columns. Only three sections carry a visual. "From getting data in to
   putting it to work" is text-only and restates the hero description.
4. **Platform chips look like links but aren't.** They're underlined text with
   no icons, even though the app already has coloured connection-type icons.
5. **Three different palettes.** The landing page uses cream and deep green. The
   docs use stock Starlight: a neutral base, system sans font and a generic green
   accent. The app uses neutral near-black surfaces with an emerald primary, and
   the story video now does too. Screenshots and video sit inside a green frame
   they don't belong to.
6. **Nothing signals freshness.** The build already knows the latest version
   (the footer shows `v0.5.8`), but nothing links to what changed in it.
7. **"Work with me" sits in the product footer.** It reads as a personal offer
   inside the product site. This is a business decision, flagged only.

### Documentation

1. **Navigation.** The problems are already described in
   [Docs structure](docs-information-architecture.md): introduction pages before
   installation, a single-page "Editing assets" group, and connections placed
   after the workflows that need them.
2. **Coverage gaps for shipped features the landing page and video promote.** A
   "features promoted" row means the landing hero video (commit `cb4a1d26`)
   currently shows the feature while the docs have no dedicated page for it.

   | Shipped capability | Current coverage |
   | --- | --- |
   | Data Browser (search, column preview, drag to canvas or notebook) | Mentioned on 4 pages; no page of its own |
   | Jinja in SQL assets (built-in run variables such as `start_date`, `end_date`, `start_timestamp`, `execution_date`, `run_id`, `this`, `var.*`; pipeline variables in **Pipeline settings**; completion and go-to-definition; the **Render** tab; checks run on the rendered SQL) | Only notebooks and HTTP API pages mention Jinja; SQL assets say nothing |
   | Environment guardrails (**Protected**, **Deployed only**, **Confirm destructive operations**; in a deployed-only environment **Review run** runs the pinned deployment) | "Execution guardrails" is named once and never explained |
   | Staleness in the workspace (**Edited**, **Upstream changed**, **Deployment differs**; building only what is stale) | Concepts and canvas explain the idea; the "build stale" flow appears only in the CLI reference |
   | Type checking as one task | Spread across 6 pages; no single how-to covering editor diagnostics → Type check tab → import source asset → `renart type-check` in CI |
   | Troubleshooting | None. Candidates to reproduce first: starting outside a Git repository (the server refuses), unavailable credentials, failed previews, a port already in use |

3. **Screenshots don't show the subject of the page.** Most pages embed a
   full-workspace 2× capture in a 720 px column. The SQL assets page's editor is
   illegible, and the docs overview shows the canvas at a zoom where nodes are
   empty boxes.
4. **The quickstart has no visuals and stops before the "developer experience".**
   It's text-only, so a reader can't confirm each step. It never shows
   completion, type checking or staleness, which are the reasons to choose
   Renart. The retail demo already provides everything needed to show them
   offline.
5. **The docs chrome is stock Starlight.** It uses a different font from the
   landing page, and there's no install entry point in the header. The
   "Renart Docs" title already links back to the landing page.

## Proposal

Principles: show the workflow instead of describing it, use one visual system,
underpromise, and keep every image or clip scripted and regenerable.

### Landing page

1. **Make the story video the hero proof.** It replaces the static workspace
   screenshot:
   - `autoplay muted loop playsinline`, `preload="metadata"`, and a poster.
   - Theme-paired sources that follow `data-theme` the same way
     `ThemedScreenshot` does.
   - `prefers-reduced-motion` shows the poster with a play button. The video
     pauses when it's off screen.
   - Label it "Illustrated walkthrough", so it's clear the video illustrates the
     product, while the workspace tour below keeps the label "Actual Renart
     interface".
   - Under the video, add a visible chapter list (Discover → … → Schedule). Each
     chapter links to its docs page. This serves as the transcript for screen
     readers and search engines.
   - Load the full-HD file only on wide screens. Serve a lighter 1280 px encode
     plus a square mobile cut (`<source media>`), rendered from the same
     `story.html` with a second camera table. A later step adds the mobile cut
     to the renderer.
2. **Shorten the page by about a quarter.** Merge "From getting data in…" and
   "Make changes with context" into one section. Its three principles (catch
   mistakes, see what a change affects, review before you run) each get a
   focused scripted crop instead of text only. Keep the workspace tour as the
   real-screenshot proof. Keep the platform, reviewability and install sections.
3. **Stop overlays from competing on first paint.** Consent becomes a non-modal
   bottom bar that doesn't cover the hero. Analytics still loads only after
   affirmative consent, and the choice stays equally easy to decline or revoke,
   as §0.7 of the docs contract requires. The Discord card waits until the reader
   has scrolled a screen and a half, without storing any engagement state, and
   never shows while the consent bar is open. Discord stays in the footer and the docs header.
4. **Give the platforms real icons and no fake-link styling.** Group them into
   Local / Warehouses / Object storage / APIs. Use the connection-type icon
   colours from `web/src/globals.css`, and check every entry against
   [supported platforms](../docs/src/content/docs/docs/reference/supported-platforms.mdx).
5. **Add a factual freshness line.** "Latest release vX.Y.Z · What changed ↗",
   linking to the GitHub release. The build already fetches the version.
6. **Align the palette with the app** (decision 2). Keep the landing
   typography (Instrument Serif headings with Geist). Move surfaces and the
   accent to the app's neutral and emerald tokens in both themes, so
   screenshots and video blend in instead of sitting in a frame of a different
   colour.

### Documentation

1. **Adopt option A from [Docs structure](docs-information-architecture.md)**,
   including the three-entry-point docs index, and keep the existing URLs. This
   plan doesn't re-decide it.
2. **Add pages for the capabilities we now market,** each verified in the app
   first and each with one scripted screenshot:
   - *Browse your data* (how-to): connected sources and project files, search,
     column preview, dragging onto the canvas or into a notebook.
   - *Use variables and Jinja in SQL* (how-to): built-in run variables, pipeline
     variables, completion, the Render tab, checks on the rendered SQL.
   - *Catch errors with type checking* (how-to hub): editor diagnostics, the
     Type check tab and quick fixes, importing a source asset, and running
     `renart type-check` in CI. Existing pages link here instead of repeating
     the explanation.
   - *Rebuild only what changed* (how-to): what each staleness badge means,
     building stale assets, and how staleness relates to deployments.
   - *Environment guardrails* (reference section on the connections and
     environments page): what each guardrail blocks, and what **Review run**
     runs in a deployed-only environment.
   - *Troubleshooting*: only failure modes reproduced in the product, each with
     its exact message and fix.
3. **Screenshots show the page's subject.** Add element-level captures
   (`locator.screenshot` at 2×) to `capture-docs-media.mjs`: the editor with
   completion on SQL assets, the Type check tab, the Render tab with Jinja, the
   Data Browser panel, the guardrail settings, and the staleness badges on the
   canvas. Keep full-workspace captures only on the tour and overview pages. The
   docs stay real product captures; illustrated video clips are for the landing
   page only.
4. **Add a "Make a change" step to the quickstart.** In the retail demo: edit
   one SQL asset, accept a column completion, introduce and fix a typo through
   Type check, and watch the downstream asset turn **Upstream changed**, then
   rebuild it. Add three focused screenshots: the welcome screen, the demo
   canvas and the schedule dialog. The step must be verified end to end in the
   retail demo before it ships.
5. **Docs chrome.** Use the same font and palette as the landing page after
   decision 2 (Starlight `customCss`; there's no custom CSS today). The Install
   header link and removing the Discord pop-up from the docs are done.

## Rollout

| Phase | Scope | Size |
| --- | --- | --- |
| 1. Quick wins — **done** | Overlays (landing and docs), non-link platform chips, docs Install link, video in the hero with poster, reduced motion and chapter list | ~1 day |
| 2. Navigation — **done** | Docs option A and the new docs index (from the structure plan) | ~0.5 day |
| 3. Coverage — **done** | The six pages or sections above, with scripted focused screenshots | 2–3 days |
| 4. Landing restructure | Merged section, palette alignment, freshness line, mobile video cut and a lighter encode | 1–2 days |
| 5. Quickstart | "Make a change" step and focused screenshots, verified in the retail demo | ~1 day |

Each phase is independently shippable.

Phase 1 as built:
- `StoryVideo.astro` in the hero. Its chapter data is written by
  `make landing-video` to `docs/src/data/story-chapters.json`.
- The consent prompt is a `bar inline` layout, with both choices on one row on
  phones.
- The Discord card is deferred on marketing pages and removed from the docs.
- Platform chips are no longer styled like links.
- The docs have a `SocialIcons` override with an Install link.

Verified in Chromium against the dev server:
- Autoplay happens only on desktop and never overrides an explicit pause.
- Reduced motion and phones show the poster with a play button.
- Chapter seeking works, and a theme switch keeps the playback position.
- The Discord card never appears together with the consent bar.
- `pnpm build` in `docs/` is green.

Phase 3 as built:
- New pages: `connections-environments/data-browser`,
  `editing-assets/variables-and-jinja`, `editing-assets/type-checking`,
  `workspace/rebuild-what-changed` and `troubleshooting`, plus an **Execution
  guardrails** section on Managing connections.
- New scripted shots: `data-browser`, `needed-assets`, `jinja-variables` and
  `type-check`, all cropped to their subject.
- The landing video's chapter links now point at these pages.

Deliberately left out until they can be reproduced:
- Dropping a project file onto the canvas to create a Load asset. The
  Data Browser offered no canvas action for project files in the staged
  workspace; the page points to Load assets instead.
- An "unavailable credentials" troubleshooting entry.
- A screenshot of the guardrail settings.

Verifying the pages also corrected the landing video. A deployed-only
environment doesn't relabel **Run** as **Deploy**: **Review run** runs the
pinned deployment, and deploying uses the separate **Deploy**/**Redeploy**
action. The video now shows that, and its type-check hover uses the real
**Unresolved column** message and quick fix.

## Acceptance

- At 390 px and 1440 px wide, in both themes, the first screen shows the
  headline, the install command and the start of the hero proof, with no overlay
  covering them.
- The hero poster is under 100 KB, and the video doesn't load on phones in
  reduced-motion or data-saver contexts. Landing LCP is measured before and after
  in a production build, not the dev server.
- The structure plan's findability test also covers two new tasks. A new reader
  reaches each of these in at most two navigation decisions: connection setup,
  first successful pipeline, notebook chart, schedule creation, Jinja variables
  in SQL, and type checking in CI.
- `pnpm build` in `docs/` is green, with no dead links or anchors.
  `rg -i bruin docs/src/content/docs docs/src/pages/index.astro` stays empty.
- Every new image or clip is the verbatim output of a make target, in both
  themes, with matching dimensions.
- The production Docker/Caddy path is smoke-tested and the consent flow is
  re-checked against `architecture/docs.md` §10.

## Decisions (27 September 2026)

1. **Hero:** the illustrated walkthrough video replaces the static screenshot.
   Done in phase 1.
2. **Palette:** align landing surfaces and accent with the app, keeping the
   landing typography. Phase 4.
3. **Docs navigation:** option A from [Docs structure](docs-information-architecture.md).
   Phase 2.
4. **Consent presentation:** a non-modal bottom bar with unchanged opt-in
   semantics. Done in phase 1. A legal review of the bar against the
   Impressum/Datenschutz setup is still recommended before release.
5. **Footer:** no change; "Work with me" stays until decided separately.
6. **Mobile video cut:** square 1080×1080. Phase 4.
