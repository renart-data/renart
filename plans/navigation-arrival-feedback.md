# Navigation arrival feedback

Status: in progress, 10 September 2026.

## Implemented slice

The shared committed-arrival provider, explicit intent tokens and silent local
reflection are implemented. Connection fields, asset column fields and
materialization, validated asset source ranges, notebook cells and Data Browser
columns use the shared visual treatment. Cold/legacy links wait for their real
owner; navigation IDs also work over plain HTTP on a LAN. The old notebook-only
jump animation is removed. DOM cleanup is generation-safe and reduced motion
uses a static temporary outline.

Remaining: migrate presentation component/inspector reveals and run event/timeline
locations, extend whole-section feedback beyond the pilot materialization surface,
and finish the acceptance matrix for those adapters. Keep their existing reveal
behavior until migrated; do not claim every addressable place highlights yet.

## Goal

After following a deep link, make the destination immediately recognizable with
one quiet, short highlight. Repeatedly clicking the same link must work even when
its owner and field are already open. Preserve independent panels, drafts,
execution context, selection and normal keyboard focus indicators.

This extends the real owner routes described in
[diagnostic navigation](../architecture/diagnostic-navigation.md); it does not
introduce another details view or overlay renderer.

## Current boundaries

- `resourceDestination` resolves semantic targets to their existing owners.
- `ResourceLink` emits real anchors. `useResourceNavigation.open` navigates;
  `reflect` updates a locator from local interaction without revealing anything.
- Arrival behavior is currently distributed: connection fields remember the last
  field name; asset column refs focus controls; Monaco reveals source ranges;
  notebook, presentation and run owners have their own reveal lifecycles.
- Remembering only the target value cannot distinguish a new visit to the same
  place. A CSS selector or an effect tied only to `detail` would miss those visits
  and could highlight stale/hidden copies of an editor.

## Proposed contract

### 1. Separate an address from an arrival

Keep canonical URLs unchanged. Introduce one shell-owned, ephemeral arrival
sequence associated with a **committed** navigation and its validated target.
An explicit `open` or ordinary ResourceLink activation supplies a fresh intent
token through navigation history state, including same-URL replacement. A cold
URL, reload or Back/Forward receives a fresh arrival from the committed router
transition. An intent rejected by an unsaved-change guard produces no arrival.

New-tab and modifier clicks remain native anchors: the URL alone is sufficient;
the new document creates its own arrival. No tokens, animation state, credentials
or DOM selectors are added to the URL or persistent application storage.

`reflect` remains explicitly silent. Audit local onFocus/onSelection handlers
that currently call `open` (notably connection fields) and switch those to
reflection. Otherwise focusing a highlighted control could retrigger navigation
and create an animation loop. Ordinary search typing, SSE updates and background
loads must never generate arrival feedback.

### 2. Let the existing owner reveal and acknowledge the target

A small `useNavigationArrival` adapter consumes the current arrival only when the
project, owner and semantic target match. The owner retains responsibility for
opening the necessary section and resolving a stable field/cell/component ID.
It acknowledges readiness through a ref or explicit ready callback, **after**
lazy content is mounted and the necessary panel transition has completed.

The shared hook deduplicates by arrival ID, cancels obsolete work on navigation
or unmount, and applies feedback once. There is no document-wide selector search,
unbounded MutationObserver, polling, or global store of DOM nodes. Virtualized
owners use their existing scroll-to-item/reveal-range APIs before acknowledging.
Unavailable, ambiguous or stale-fingerprint targets retain the existing notice;
never highlight a similarly named fallback as if it were the requested field.

### 3. One visual treatment, two render adapters

- **DOM:** a temporary `data-navigation-arrival` attribute on the smallest useful
  existing field/row/block. Shared CSS uses the theme's primary color for a
  subtle inset outline/background that fades once over roughly 700 ms. It does
  not affect layout, click targets or the normal focus-visible ring.
- **Monaco:** a temporary decoration using the same theme tokens and duration;
  reuse the editor's source-range validation and reveal policy. Never manipulate
  Monaco's internal DOM as if it were an ordinary form field.

The shared helper owns cleanup on completion, replacement and unmount. Respect
`prefers-reduced-motion`: briefly show a static outline instead of animation and
do not request smooth scrolling. Do not pulse repeatedly or add a toast. Existing
accessible labels/focus identify the destination; announcements are reserved for
failure to reach it, not every pointer click.

## Delivery

1. Build pure arrival identity/deduplication tests and integrate committed-router
   arrivals with `open`, `ResourceLink` and silent `reflect`.
2. Pilot connection fields and asset columns. Verify same-field repeat clicks,
   disclosure, dirty guards and mobile owners before migrating further.
3. Add adapters for notebook cells, presentation components, Data Browser
   objects/columns, run event/timeline targets and validated Monaco ranges.
4. Remove each migrated owner's last-target focus suppression, not its semantic
   reveal rules. Document the as-built extension contract and retire this plan.

## Acceptance

Desktop/mobile, light/dark and reduced-motion checks must cover:

- Cold/new-tab links; already-open targets; repeated identical links; history.
- Collapsed/lazy/virtualized destinations, rapid successive links and unmount.
- Stay/failed Save produces no highlight; successful navigation produces one.
- Local reflection, editor typing and SSE updates produce none.
- Inspect/Query/Materialize choice, sidebar width/collapse/filter, notebook
  drafts and execution environment remain unchanged unless revealing the
  addressed owner genuinely requires a specific change.
- No duplicate hidden owner is focused; stale source fingerprints and missing
  semantic IDs show notices; animation never authorizes an execution or save.

Use disposable workspaces and retain screenshots/traces locally. This is not a
claim that every currently non-addressable UI control becomes routable.
