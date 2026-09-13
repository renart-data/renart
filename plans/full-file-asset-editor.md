# Full-file asset editing with fault-isolated intelligence

Status: proposed, 11 September 2026. Plan only; the full-file editor is not yet
implemented. Build the safety boundary before exposing unrestricted file editing.

## Experience

Add **Edit entire file** to the existing asset editor's overflow menu. It opens
the same asset, in the same editor tab and workbench, with a small **Entire file**
mode selector to return to the guided experience. Metadata is ordinary editable
text, not a generated preview. Preserve the independent sidebar, results tab,
selection and scroll positions. Make the mode routable using the existing asset
route/search contract, including source-range deep links.

SQL and Python show their complete file including the `@bruin` header. YAML
assets show the whole definition, including Load parameters, API configuration,
columns, checks, hooks and unit tests. Unknown Bruin-compatible keys and comments
remain untouched. A broken file still has a tab, a path and a repairable buffer;
it does not disappear just because it cannot currently be interpreted as an asset.

## Existing building blocks and actual gaps

- `use-asset-content-editing.ts` / `use-debounced-asset-save.ts` already own
  drafts, autosave and save coordination. `AssetService.Update` is **not** a raw
  file API: it calls `ExtractExecutableContent` / `MergeExecutableContent` and
  semantic metadata persistence. Sending a whole file there can merge or discard
  parts of the intended content. Do not overload that contract.
- `pipeline_resilient.go` creates marked placeholders when an individual task
  creator returns an error. `workspace.go` retries with that tolerant builder
  after a strict parse fails. It still depends on pipeline construction succeeding,
  does not catch non-returning parsers/process faults, and is not an independent
  file inventory. Strict asset resolution can still depend on other asset files.
- `workspacefs` supplies path IDs and atomic replacement. Its lexical `Join`
  alone is not a symlink-containment guarantee; raw access needs an explicit
  root-contained, regular-file boundary shared by reads and writes.
- `use-asset-monaco.ts` composes SQL, Jinja, Python and YAML support. The SQL
  engine already tolerates incomplete SQL and source-maps rendered Jinja, but
  currently works primarily on the executable projection and saved metadata.
- `use-yaml-intellisense.ts` has focused API/Ingestr completions, not a complete
  Bruin asset schema. `monaco-unit-test-yaml.ts` already bundles a scoped YAML
  language worker with remote schema fetching disabled. Generalize its scoping
  adapter, preserving unit-test model isolation; do not register another global
  provider that changes other editors.

## 1. Separate document existence from semantic validity

Introduce a bounded, read-only asset-document inventory below `service`:

`project + relative path → bytes/hash + document regions + parse result/diagnostics`

The path, not `name:`, is the document identity. Invalid/empty/unknown-kind files
stay in the inventory even when they contribute no runnable asset. Keep parse
status and diagnostics outside authored `meta`; the existing parse-error DTO
becomes a projection of this result, not the storage for parser state.

Parse each file independently. Build the graph from valid projections plus
explicit unknown/unavailable placeholders for invalid inputs. Duplicate names,
bad references and cycles produce addressed findings, not map overwrites or a
failed workspace response. Valid sibling pipelines, connection settings, Data
Browser and notebook navigation remain usable. A broken `pipeline.yml` cannot
provide an executable pipeline, but its directory and files must remain available
for repair; it must not hide other pipelines.

Last-good schemas may be shown as clearly stale explanatory context, never as
current validation or executable plans. Deployment/execution must reject the
invalid asset and affected dependency closure; they may not silently execute
the last-good version. Unrelated valid branches retain the existing plan rules.

### Resource and fault boundary

Bound bytes before reading, YAML depth/alias expansion, region count, graph
work and diagnostics. Initial editor limit: 1 MiB per file, with a clear
read-only/externally-editable oversized or non-UTF-8 state; never truncate and
then save. Preserve unsupported bytes on disk. Reading a definition must not
execute Python/Jinja, resolve secret values or fetch an external schema/OpenAPI
document.

A `recover` wrapper and goroutine timeout are insufficient for stack exhaustion,
OOM or a parser that never returns. For the requested strong isolation guarantee,
run content-derived parsing/analysis in a bounded worker process of the existing
Renart binary, with a framed DTO protocol, deadline, output-size limit and
platform-specific memory/process limits. No separate installation/service is
needed. The worker receives supplied text/context, has no mutation endpoints,
and never follows arbitrary include/schema URLs. Cache results by content hash
and parser version; one worker initially avoids multiplying memory usage.
Kill/restart a failed worker and report a document-level failure. Test the
Linux/macOS/Windows resource-limit implementations; do not claim a hard memory
guarantee on a platform with only a soft heap target. Audit post-parse graph
construction for the same budgets rather than moving only YAML decoding.

Browser language workers need their own bounded lifetime and restart behavior.
Editor error boundaries keep a failed Monaco/provider surface from replacing
the app shell. This protects malformed documents, not arbitrary hostile project
code executed by an explicitly started run.

## 2. One document write contract

Add `GET` and `PUT /api/asset-documents/{pathID}` through the ordinary Go HTTP
layer, using the repository's DTO generator, error envelope and project scope.

- GET returns exact supported text, content hash, encoding/EOL facts, regions,
  parse status and addressed diagnostics, without strict asset resolution.
- PUT accepts full text plus `expected_revision` (content hash). Check the hash
  under the **same per-file lock** used by semantic writers, then atomically
  replace the file through `workspacefs`. Reject stale writes with 409 while
  keeping the local draft. No implicit merge, metadata normalization, dependency
  reconciliation or name/file move on raw save. Syntax-invalid text is saveable.
- Contain resolved paths/symlinks within the selected project, allow only regular
  authored asset files, and reject `.git`, `.renart`, secrets, binary data and
  oversized requests. Recheck containment at write time; test symlink replacement
  races instead of relying only on string-prefix checks.
- Publish the normal one-file SSE update; asynchronously reparse only current
  revisions. A slower old result cannot overwrite a newer document projection.
- Route semantic transactions through the same lock/hash boundary. They may
  operate only on the current valid document. Concurrent raw/guided/external edits
  get a conflict response rather than silently winning against another draft.

Keep one document draft/save coordinator. Guided fields and SQL-body editing
become projections/transactions of that document, not independent raw and guided
autosaves. Switch modes without replacing the Monaco model on each keystroke;
retain undo, selection, dirty state and errors. Disable only guided controls that
cannot be safely projected while broken. Save-and-run continues to flush the
current draft and must fail visibly if that saved file is invalid.

## 3. Shared source regions and IntelliSense

Use a tolerant region scanner before strict parsing. SQL/Python headers, YAML
metadata and embedded SQL scalars each have ranges, a language/context and an
explicit confidence. Compose physical-file → region → rendered-Jinja source
maps. Do not identify headers with an unrestricted regex over SQL strings or
Python docstrings. For unfinished delimiters expose conservative region recovery;
when the language is uncertain, offer syntax/structural help without fabricated
semantic errors or edits.

- **YAML metadata:** derive the static schema from pinned Bruin definitions and
  existing backend asset-kind/creation/materialization capabilities. Add dynamic
  connection names, dependencies, known columns, check names and fixture schemas
  from the current context. Unknown supported extension keys stay legal. Use the
  existing `monaco-yaml` worker for completion, documentation hovers, syntax/schema
  diagnostics and formatting; server validation remains authoritative.
- **SQL body:** adapt the existing `useSQLLSP` / Golyglot stack to a request-local
  metadata projection from the unsaved header. Connection/type changes must
  affect that draft's dialect and references without mutating the saved graph.
  Keep grammar-aware completions, hover, definition and diagnostics when a clause
  or neighboring metadata is incomplete. Formatting a region must not rewrite
  the rest of the file.
- **SQL inside YAML:** project `parameters.query`, hooks and custom checks into
  virtual SQL documents with their existing context contracts. Map completion
  edits, diagnostics and definition targets back through scalar indentation and
  quoting. Begin with literal block scalars; folded/quoted/multiline forms need
  lossless edit maps before offering mutating fixes. Read-only help can degrade
  gracefully for an unsupported mapping.
- **Python:** reuse the embedded Python intelligence and SQL-in-Python support,
  with the header region excluded/mapped correctly. Do not infer runnable code
  from a partial parse and never execute code for completion.
- Key requests by project, path, model version, region/context and environment.
  Cancel obsolete work and discard late responses. Scope providers/markers by
  model URI; reuse the existing diagnostic registry and navigation-arrival hooks.
  Shared offset utilities must handle UTF-8/UTF-16, emoji, CRLF and BOMs.

Monaco's [model/URI and provider model](https://github.com/microsoft/monaco-editor)
supports this division. [Monaco YAML](https://github.com/remcohaszing/monaco-yaml)
provides the YAML schema service; Renart still owns mixed-language projections,
Bruin semantics, safe editing and workspace isolation. These are architectural
choices, not features supplied automatically by adding a Monaco textarea.

## Delivery and acceptance gates

1. **Resilience first:** adversarial corpus + bounded inventory/parser worker.
   Empty files, missing headers, malformed YAML, duplicate keys/names, alias
   bombs, deeply nested syntax, cycles, oversized/non-UTF-8 files, cancellation,
   worker crashes and invalid pipeline definitions must not hide healthy files
   or stall `/api/workspace`/SSE. Strict execution must stay strict.
2. **Raw document API:** lossless round-trip, stale-save conflict, root/symlink
   containment, concurrent semantic/external writers, interrupted atomic write
   and restart recovery. Verify exact saved bytes and Bruin compatibility for
   every valid asset kind.
3. **Single draft + editor mode:** launch from valid and broken assets; edit all
   metadata; save malformed text and repair it; switch tabs/modes/back-forward,
   reload and open a deep link. Preserve unrelated panel state and unsaved drafts.
4. **Metadata and body intelligence:** complete YAML keys/typed values and SQL
   columns from partially typed headers/queries. Test unsaved connection changes,
   source-map round trips, formatting scope and out-of-order responses. Retain
   existing API, unit-test, SQL and Python editor behavior.
5. **Embedded SQL parity + hardening:** hooks/checks/query-sensor scalar coverage,
   provider isolation, Unicode/mixed line endings, safe fixes and bounded fuzzing.
   Benchmark cold/warm completion and memory on large representative projects;
   record actual budgets rather than promising whole-workspace instant parsing.

Ship the full-file switch only after gates 1–4. Keep incomplete embedded-scalar
edit support explicit in gate 5. Run focused desktop/mobile live tests first,
then the complete release gate serially with durable `.test-artifacts` logs.
This plan does not expand the existing name/path or physical-target lifecycle
work: a raw `name:` edit changes authored text and reports resulting conflicts;
it does not silently refactor dependencies or rename a warehouse table.
