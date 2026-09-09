# Connection and environment settings navigation

Status: proposed, 9 September 2026. Plan only; existing settings remain in place.

## Interaction model

Use the large contextual sidebar for finding configuration; use the center for
editing one selected item. Keep the global Build / Run / Explore header and
existing Connections / Environments rail entries. No fourth mode, duplicate
settings sidebar, or modal stacked over a full-page list.

- **Connections:** filterable connection list grouped by environment. A row shows
  type/name and a compact attention indicator only when actionable. Selecting it
  opens the existing connection editor as a center document. Environment groups
  collapse independently; selected names remain visible.
- **Environments:** environment list with explicit default/protected indicators.
  Selecting one shows its guardrails and defaults in the center, with links to
  its connections. “Use for execution” remains a separate deliberate action.
- **Details:** identity and common fields first, credentials and advanced fields
  progressively disclosed. Place Test / Save together; show relevant errors at
  their actual fields. Keep deletion in an explicit destructive-action area.
- **Create:** small type/environment selection followed by the same form body.
  Keep the quick-create dialog from Data Browser as an adapter to that form,
  returning to the requested source after a successful save.

On mobile, reuse the current workbench navigation Sheet. Selection closes it and
reveals the editor, with an obvious way to reopen the list. No full settings
form squeezed inside that Sheet or nested horizontal scrolling.

## Current code and extraction boundary

[settings-pages.tsx](../web/components/app/settings-pages.tsx) already has a
SettingsShell with a contextual portal, but it shows only section links. The
connection page repeats cards by environment and opens a sheet for editing;
environment editing also uses a sheet. This is the hierarchy to move, not a new
global navigation system.

[use-workspace-settings-data](../web/hooks/use-workspace-settings-data.ts) and
the existing config APIs remain the single state/write boundary. Extract the
connection/environment form bodies and actions from their overlay shells;
reuse them from a routed center pane and quick-create dialog. Keep schema-driven
connection metadata, capability/access policies, secret binding actions,
normalization and backend validation. Do not add a frontend type-name allowlist.

## Route and state ownership

Extend the actual `/project/connections` and `/project/environments` owners.
Keep existing connection/environment identities and diagnostic `detail` locators;
adapt old bookmarks before considering prettier path segments. Connection
identity must include environment because names can repeat. A cold link to a
field must reveal and focus the same form as an ordinary list selection.

| State | Owner / navigation behavior |
| --- | --- |
| Selected connection/environment to edit | Validated owner route; supports Back/Forward and fresh tabs |
| Focused field/section | Existing ResourceTarget and UI navigation contract, not DOM selectors embedded in errors |
| Active execution environment/time window | Existing workspace controller; editing another environment must not switch either |
| Sidebar filter, expansion, width | Disposable project-scoped presentation state; compatible navigation preserves it |
| Unsaved form/secret actions | Existing form controller, keyed by project + environment + connection; never URL/localStorage |
| Config and policies | Go service + filesystem + SSE; mutation responses reconcile through existing helpers |

Do not mount hidden duplicate forms or import settings domain state into AppShell.
Let the existing settings owner contribute its hierarchy through WorkbenchPortal.
Preserve independent notebook tabs, asset result mode, pipeline selection and
sidebar state when visiting or returning from settings.

## Safety and error handling

- Selecting an environment is **not** changing the default environment or the
  execution environment. Label these separate actions clearly.
- A locked/unavailable credential store is still the configured provider. Never
  default its editor back to environment references or overwrite bindings because
  resolution temporarily failed. Leave / replace / clear are explicit actions.
- Secret values remain write-only. Do not put credentials into list state,
  copied links, query params, logs or retained navigation drafts.
- Detect dirty form navigation and offer Save / Discard / Stay. Validation or
  failed save keeps the user on the same form; no accidental discard on selecting
  another sidebar row, Back, project change or closing a tab.
- Read-only/access-mode controls and environment guardrails keep current service
  enforcement. Ingestr-only types remain hidden unless the project opts in.
- Missing/deleted/ambiguous identities show a clear owner-page error and a link
  back to the list; do not silently edit a similarly named connection elsewhere.
- Display inherited configuration provenance. Do not turn a child-workspace edit
  into an implicit write to an unrelated parent/sibling project.

## Delivery sequence

1. **Extract without redesign.** Put form state/actions in reusable owners, retain
   current dialog behavior, and add regression tests for secrets/policies/saves.
2. **Connections hierarchy and center editor.** Adapt existing routes/deep links;
   add filter, grouping, selected-row state, dirty-navigation guard and mobile
   behavior. Keep quick-create and its return target working.
3. **Environment hierarchy and center editor.** Move guardrails/defaults without
   changing execution context. Reuse connection links and remove duplicated lists.
4. **Polish and remove obsolete wrappers.** One primary Save action, compact
   statuses, accessible field errors and deliberate destructive flows. Update
   established user-facing docs only after behavior is verified.

## Acceptance

Test cold diagnostic links/new tabs, browser history, refresh, duplicate connection
names across environments, missing identities, dirty navigation and failed saves.
Verify locked-store restart, explicit binding replacement/clear, read-only modes,
ingestr opt-in and inherited config. Assert actual config/policy files and SSE,
not just toast messages. Selecting prod for editing while dev is executing must
leave execution context, notebook drafts, inspect results and unrelated sidebar
state unchanged. Cover keyboard, narrow/mobile layouts, dark/light contrast and
large connection lists in disposable workspaces.

See [frontend architecture](../architecture/frontend.md),
[UI navigation](../architecture/diagnostic-navigation.md), and
[verification](../architecture/testing.md). When implemented, fold the as-built
contract into those documents and retire this plan.
