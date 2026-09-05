# UI navigation — canonical places and minimal state changes

Status: implemented on the semantic-impact branch. Diagnostics are consumers of
ordinary UI navigation, not a separate editor or detail-view system.

## Invariant

Navigation changes the addressed resource and only the view state necessary to
reveal it. Unspecified, independent state is preserved. For example, opening a
column changes the asset if necessary, reveals its existing Properties surface,
selects Columns and focuses that column field. It does not select Inspect,
Materialize or Query, expand collapsed results, or select a different Build
sidebar tool merely to show the field.

Opening another asset necessarily changes the main editor/model. Its prior draft
remains in the existing editor draft/save system; returning restores that owner.
Opening a field of the same asset does not replace the editor. The contract is
not a promise to keep every old page mounted across unrelated owner routes.

## Address and ownership

The existing TanStack path identifies the owning page. A validated project ID
scopes cold arrivals. The optional v1 detail search value contains environment
and a structured target; its historical name now means a locator in the existing
UI, not an auxiliary outlet. No new special-purpose detail pages are mounted.

| Target / state | Existing owner and necessary changes |
| --- | --- |
| Asset column | Asset path, existing Properties → Columns, exact row/field |
| Asset section | Properties → General, Lineage, Columns or Checks |
| Saved source location | Real asset Monaco; reveal a hidden editor if necessary |
| Connection / field | Project Connections and its normal edit Sheet |
| Data object / column | Existing /data schema/rows view and explicit row preview |
| Notebook cell | Existing notebook editor, persisted cell ID, local scroll/focus |
| Presentation component | Existing visual editor and its normal inspector |
| Presentation editor mode | presentation_editor selects Visual or Definition |
| Run tab / asset location | run_tab, run_asset and run_focus in the existing run page |

web/lib/ui-navigation.ts owns target-to-owner resolution and sparse search
updates. It uses workspace asset ownership and presentation workspace_id, never
an authored presentation ID, translated error prose, a DOM selector or an array
index. Metadata targets preserve the current Build canvas/split/code view and
result/editor choices. Source targets may reveal split and choose the repository
editor, but still do not change the results tab. Visualization targets reveal
the visual editor; changing that editor with an unsaved draft is guarded.

ResourceLink emits real anchors with canonical destination paths. The same
resolver builds Monaco diagnostic hrefs. useResourceNavigation is the ordinary
interaction adapter. Property tabs, column fields, connection fields, notebook
cell focus and presentation component selection update the address too. Runs
use their existing route search contract extended by the pure run-navigation
helpers. A timeline target preserves the independent output tab; an event target
selects Events because the requested event is rendered there.

The small ResourceNavigation component only normalizes legacy v1 bookmarks whose
path still points to their origin and reports unavailable owners. It renders no
editor. Root middleware retains project scope, not stale detail targets during
unrelated navigation. Identical address clicks replace history; meaningful
navigation pushes history. Connection field movement replaces the current entry.

## Independent state and lifecycle

State remains with the page/controller that owns it. There is no workspace-wide
snapshot encoded in every URL and no sequence of tool-selection callbacks to
reconstruct a location. Existing Build result selection, panel collapse, editor
drafts and compatible Data Browser sidebar state stay independent. The Workbench
reconciles a tool only when it represents the destination's owner/editor; it no
longer opens a collapsed sidebar solely because the route tool changed.

Responsive UI uses the same existing Properties and Connection Sheets. Only one
asset inspector is mounted for the current breakpoint. A mobile navigation
Sheet closes when its context is replaced and would otherwise cover the new
page. This is a necessary reveal, not a reset of desktop sidebar preferences.
Focus uses semantic refs and scrolls the target's own viewport. Missing,
ambiguous or capability-incompatible targets show a notice instead of selecting
a similarly named replacement. Source ranges require a matching fingerprint of
the actual current editor content, including drafts.

New tabs/reloads need no prior session state for the destination. The explicit
project is checked before workspace consumers/SSE mount; a conflicting stored
pin cannot override it. Hot cross-project cache switching remains disallowed:
use a document navigation/new tab. The target environment is used where the
owner needs it (connection, schema metadata, data object); definition navigation
does not reconfigure the global execution environment.

Connection edits retain a stable form snapshot and guard leaving dirty state,
including Close/Escape and browser unload. Changing only its addressed field
does not discard that form. Presentation selection within the same editor does
not invoke the leave guard; changing owners/editor modes does. Existing
blur/explicit asset-save semantics remain authoritative.

## Commands, contracts and compatibility

Navigation is not a command. It does not deploy, save a suggested correction,
verify a connection, synchronize schema or request a row preview. Existing
owner pages still perform their ordinary read-only loading/intelligence work.
A directly addressed presentation definition does not automatically execute its
preview; Refresh draft data remains available explicitly. Notebook runtime
initialization follows the normal editor lifecycle, not a hidden clone.

Go navigationtarget remains the generated public target/policy boundary.
authoringdiag.Subject carries semantic facts, and pipeline checking, deployment
readiness and HTTP LSP preserve the target. Every registered type-check code has
a tested exact target or honest section fallback; unregistered diagnostics do
not receive guessed destinations. Custom-check SQL remains owned by Checks.

Data object URLs contain durable metadata addresses, not revision-bound action
tokens or credentials. The read-only resolve endpoint verifies exact
connection/environment/object identity and creates a fresh operation token.
Old tokens still become stale. Local path containment and quoted-identifier
boundaries remain enforced; previews are explicit and capped.

## Coverage and extension rule

The shared contract currently covers the families above. Column fields include
type, description, primary key and supported merge fields. Presentation targets
cover artifact, dataset, filter, visualization and report-section selection.
This is a reusable foundation, not a claim that every pre-existing local UI
state is already URL-backed: notebook non-cell blocks/tool panels, arbitrary
nested visualization fields, pipeline-settings subsections and other local
creation/configuration dialogs remain follow-up coverage.

For each new place, identify the existing owner, its stable semantic ID, the
smallest state patch and the independent states it must preserve. Add a bounded
codec/owner resolver and wire the existing control bidirectionally. Do not add a
parallel renderer, persist credentials/drafts in URLs or guess targets from
free text. Actions remain buttons/commands separate from navigation links.

## Verification

Unit tests cover strict codecs, exact owner identity, minimal search changes,
all supported column fields, run location dependencies and sidebar preservation.
The focused live suite covers desktop/mobile normal UI → URL → cold tab,
conflicting project pins, retained same-asset Monaco and returning drafts,
Inspect and collapsed results, history, stale columns, explicit metadata saves,
connection discard cancellation, presentation selection/editor mode, durable
data bookmarks and run tabs without new commands. Run one browser worker locally.
Frontend checks include generated API parity, formatting, lint, unit tests,
TypeScript and production bundle gates without increasing their budgets.
