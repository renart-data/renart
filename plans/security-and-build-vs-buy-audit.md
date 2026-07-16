# Security and build-vs-buy audit

Status: audit complete on 2026-07-16; remediation and selected migrations are
open. Audited revision: `c7c156a2c174dc941d96eea95ae787578a8f6eb8` on
`release/v0.3.0` (the checkout also contained unrelated user changes, which
were not included in this audit or modified).

This document records both the reported findings and the action plan. The
short version is that Renart has three release-blocking trust-boundary defects:

1. the HTTP API's process token is optional, so DNS rebinding and unauthenticated
   non-browser clients can reach an API that writes files and executes code;
2. caller-controlled pipeline and asset IDs are treated as filesystem
   authority, including a pipeline ID that resolves to the workspace root; and
3. a repository-local `web/dist` overrides the embedded UI by default, giving
   an opened repository same-origin access to the entire privileged API.

The most valuable library substitutions are also security-relevant: replace
lexical path checks with Go's `os.Root`, replace the custom OpenAPI parser and
validator with a maintained implementation, reuse Bruin's sensitive-field
metadata, reuse the embedded `ty` engine for Python imports, and replace the
custom Go-to-TypeScript extractor. The DAG layout is a good migration candidate
after a measured spike. Renart-specific graph, provenance, reconciliation,
staleness, notebook, and deterministic-wheel semantics should remain custom.

## 1. Scope and method

### In scope

- process startup, listener configuration, middleware, routing, SSE, and static
  asset serving;
- every HTTP surface that reads or mutates workspace files, executes SQL or
  Python, starts subprocesses, stores snapshots, or handles credentials;
- frontend rendering sinks, Markdown, charts, development proxying, and API
  clients;
- Go, JavaScript, Python, Rust, release, and GitHub Actions supply chains;
- custom implementations that overlap with maintained libraries or an already
  embedded Renart/Bruin capability.

The audit was read-first against the current code, tests, dependency locks,
[`architecture/backend.md`](../architecture/backend.md),
[`architecture/frontend.md`](../architecture/frontend.md),
[`architecture/asset-editing.md`](../architecture/asset-editing.md),
[`architecture/notebooks.md`](../architecture/notebooks.md), and
[`architecture/sql-lsp.md`](../architecture/sql-lsp.md). Git history and plans
were used for intent, but current behavior won when they differed.

### Checks performed

- Manual source review of trust boundaries, filesystem resolution, mutation
  ownership, SQL validation, outbound HTTP, subprocesses, browser sinks, and
  release workflows.
- `gosec` v2.28.0 over 191 Go files / 53,795 lines. It reported 97 raw alerts.
  Most were expected or false positives (Wasm ABI casts, parameterized SQL
  placeholder construction, Git-authored `0644` files, argument-vector
  subprocesses, and multipart paths already protected by `MaxBytesReader`). It
  did corroborate that filesystem safety needs to be enforced at the file
  operation, not through string checks.
- `govulncheck` v1.6.0 module scan: 29 advisories existed in the dependency
  graph. A fresh binary built from this revision with Go 1.26.0 had **22
  reachable vulnerabilities**: 21 in the Go standard library and one in AWS
  EventStream/S3. The fixed Go release is 1.26.5; the AWS findings identify
  EventStream 1.7.8 and S3 1.97.3 as fixed versions.
- OSV Scanner v2.4.0 over `web/pnpm-lock.yaml`: 725 packages, one affected
  package (`dompurify@3.2.7`, bundled by `monaco-editor@0.55.1`), and 16
  advisories with no high or critical result (13 moderate and 3 low). The
  observed Monaco call path passes strings and does not use DOMPurify's
  `IN_PLACE` mode, so the relevant low advisory is not exploitable through that
  path. Other sanitizer advisories still need an upstream or vendored fix.
- `pnpm audit --prod --audit-level=high` could not run because npm's quick and
  fallback audit endpoints returned HTTP 410. The existing `pnpm audit` script
  is therefore not a functioning security gate in this environment.
- Targeted browser reproduction confirmed that a crafted database column name
  can escape the generated chart CSS declaration and alter global page styles.

This was a source audit plus focused local reproduction, not an independent
penetration test, warehouse configuration review, or full audit of Bruin and
all transitive source. Findings that depend on a race or crafted scheduler DB
should receive a dedicated exploit regression test during remediation.

## 2. Threat model and severity

Renart is local-first, but it is not low privilege. The server can read
connection credentials, modify the Git working tree, query databases, and run
repository Python. The relevant adversaries are:

- an untrusted repository opened before the user has made a trust decision;
- a malicious browser origin, including DNS rebinding to loopback;
- another host or process that can reach a non-loopback listener or exposed
  development proxy;
- a compromised or attacker-influenced HTTP API response, redirect, or
  pagination link;
- a compromised package, toolchain, action, or mutable build image; and
- accidental credentials or private keys located below a snapshotted pipeline.

The following are intentional capabilities, not findings by themselves:

- a trusted user may configure an HTTP asset for an internal API;
- materializing a trusted Python asset runs repository code with the user's
  filesystem and network privileges; and
- a trusted user can deliberately run materializations that write data.

The security boundary is the transition into those capabilities. Opening a
repository, viewing the UI, loading a preview, or receiving a pagination URL
must not silently grant equivalent authority.

Severity is based on impact in that model, not a formal CVSS calculation:

- **Critical:** unauthenticated or open-repository compromise of host code,
  secrets, or the entire workspace;
- **High:** direct workspace/database mutation, secret disclosure, or automatic
  code execution with a narrower prerequisite;
- **Medium:** denial of service, browser-content injection without script
  execution, or supply-chain weaknesses requiring an additional failure; and
- **Low:** defense-in-depth or dependency drift with limited immediate impact.

## 3. Security findings

| ID     | Severity           | Finding                                                                                                      | Confidence                               | Disposition                            |
| ------ | ------------------ | ------------------------------------------------------------------------------------------------------------ | ---------------------------------------- | -------------------------------------- |
| SEC-01 | Critical           | API token is optional; same-origin check is DNS-rebinding vulnerable                                         | Confirmed by code                        | Release blocker                        |
| SEC-02 | Critical           | Pipeline/asset IDs are filesystem authority; root deletion, arbitrary overwrite, and symlink escape          | Confirmed by code                        | Release blocker                        |
| SEC-03 | Critical           | Workspace `web/dist` replaces the privileged UI and may serve host files through symlinks                    | Confirmed by code                        | Release blocker                        |
| SEC-04 | High               | Read-only SQL query/inspect invariant fails open                                                             | Confirmed by code and connector behavior | Release blocker                        |
| SEC-05 | High               | Connection secrets are serialized to every browser config client                                             | Confirmed by code                        | Release blocker                        |
| SEC-06 | High               | API redirects and `next_url` pagination can forward credentials across origins                               | Confirmed by code                        | Fix before broad API-asset use         |
| SEC-07 | High               | Snapshots can persist and disclose `.env`, keys, and other pipeline-local secrets                            | Confirmed by code                        | Fix before relying on deploy snapshots |
| SEC-08 | High               | Tracked scheduler state can preload work before reconciliation; repository trust is implicit                 | High-confidence code path                | Fix alongside workspace trust          |
| SEC-09 | High               | Sling is installed from an unpinned package spec at execution time                                           | Confirmed by code                        | Pin before release                     |
| SEC-10 | High               | Exposed Vite dev server proxies the full privileged API and rewrites hostile origins as trusted              | Confirmed by code                        | Fix developer workflow                 |
| SEC-11 | Medium             | Current Go/AWS/Monaco dependency set has known vulnerabilities and no reliable JS audit gate                 | Scanner-confirmed                        | Immediate dependency work              |
| SEC-12 | Medium             | Request, result, execution, and SSE resource limits are incomplete                                           | Confirmed by code                        | Hardening                              |
| SEC-13 | Medium             | Markdown previews automatically load remote images, enabling tracking and data-bearing requests              | Confirmed by code                        | Browser hardening                      |
| SEC-14 | Medium             | Untrusted column names escape chart CSS and can alter global UI styles                                       | Dynamically reproduced                   | Browser hardening                      |
| SEC-15 | Medium             | Production responses lack a browser security-header baseline                                                 | Confirmed by code                        | Browser hardening                      |
| SEC-16 | High (conditional) | Release/CI jobs expose a write token to same-repository PR code; actions, toolchains, and images are mutable | Confirmed by workflow review             | Split permissions before release       |
| SEC-17 | Low                | Embedded Python SDK dependencies have open-ended minimum versions                                            | Confirmed by metadata                    | Release reproducibility                |
| SEC-18 | Low                | Sensitive/workspace API responses do not set `Cache-Control: no-store`                                       | Confirmed by code                        | Response hardening                     |
| SEC-19 | Low                | TSV copy can become a spreadsheet formula when pasted                                                        | Confirmed by code                        | Clipboard hardening                    |
| SEC-20 | Low                | Workspace-controlled object keys can crash grouping views through inherited properties                       | Confirmed by code                        | Data-structure hardening               |

### SEC-01 — mandatory API authentication and DNS-rebinding protection

Evidence:

- `internal/web/httpapi/middleware.go:101-158` treats the process token as an
  optional bypass, permits every `GET`/`HEAD`/`OPTIONS`, permits state-changing
  requests with no `Origin`, and accepts `Origin.Host == Request.Host`.
- `cmd/web.go:90-107` permits arbitrary bind hosts and optional TLS, while
  `cmd/web.go:146-167` starts the listener without a mandatory public-mode
  security policy.
- There is no listener-derived `Host` allowlist and no middleware test suite for
  the token/origin behavior.
- The admitted API includes workspace/config reads, file creation and mutation,
  SQL, pipeline execution, Python materialization, and project-directory
  browsing/opening.

A rebinding domain can remain both the browser's `Origin` and request `Host`
while its DNS answer changes to `127.0.0.1`. A direct client can omit `Origin`.
Binding to `0.0.0.0` makes the same API available to network peers.

Action:

1. Require the per-process secret for **every** `/api/**` request, including
   reads and SSE. A missing token must deny, not fall back to origin semantics.
2. Bootstrap the browser without putting the secret in a request URL. One
   viable design is a URL fragment consumed once by the same-origin app and
   exchanged for an `HttpOnly`, `SameSite=Strict` session cookie plus a CSRF
   token; CLI clients continue to use `Authorization: Bearer`.
3. Validate `Host` against names derived from the actual listener. Maintain an
   explicit development origin list instead of trusting all loopback origins.
4. Refuse non-loopback binding unless an explicit network mode enables required
   authentication and TLS. Fail startup if secure token generation fails
   (`cmd/server.go:480-489` currently falls back).
5. Add tests for rebinding-style `Host`/`Origin`, wrong host, missing origin,
   missing/invalid token, authenticated GET/POST/SSE, Vite development, and CLI
   discovery.

Done when an attacker-controlled origin and an origin-less client cannot read
or mutate any API route without the process secret, and the normal browser,
CLI delegation, SSE reconnect, and Vite flows still work.

### SEC-02 — decoded IDs must be references, never path authority

Evidence:

- `internal/web/service/helpers.go:32-42` returns the workspace root for `.` and
  performs only lexical traversal checks.
- `internal/web/service/pipeline.go:242-252` decodes a pipeline ID and calls
  `RemoveAll` without first resolving a canonical pipeline. The encoded ID for
  `.` therefore selects the entire workspace; `.git` and `.renart` can also be
  addressed.
- Content-only asset update in `internal/web/service/asset.go:469-509` reads the
  decoded file but resolves an asset only when metadata fields are present; it
  writes the path at `internal/web/service/asset.go:672-683`.
- Asset deletion at `internal/web/service/asset.go:742-778` treats resolution as
  best-effort. Formatting and Python resolution contain similar extension-plus-
  path checks. The route pipeline ID is not used to prove ownership.
- `SafeJoin` plus `afero` operations follows repository symlinks, so a path that
  is lexically under the workspace can address a target outside it.
- Asset creation can overwrite an existing path rather than using an exclusive
  create operation.

Action:

1. Resolve every mutation through the canonical current-workspace pipeline or
   asset graph. Use the resolved definition/executable path, and verify that it
   belongs to the route's pipeline.
2. Reject empty, dot/root, reserved, non-pipeline, and non-asset IDs before any
   filesystem call. Treat `.git`, `.renart`, and the workspace root as
   unaddressable through asset/pipeline APIs.
3. Replace `SafeJoin` as the enforcement mechanism with Go's `os.OpenRoot` /
   `os.Root` on patched Go 1.26.5. Perform read, create, rename, remove, and
   directory creation through the root so symlink and TOCTOU protection applies
   at operation time.
4. Use create-exclusive semantics and atomic replacement without following an
   attacker-controlled final symlink.
5. Add adversarial tests for `.`, `.git`, unrelated files, parent/final
   symlinks, symlinked directories, concurrent link replacement, malformed IDs,
   wrong-pipeline ownership, and collisions on creation.

This should become a shared root-scoped filesystem service used by assets,
pipelines, seeds, notebooks, source control, snapshots, config, and project
selection rather than another family of path helper functions.

### SEC-03 — embedded UI must be the default privileged UI

Evidence:

- `cmd/server.go:57-63` defaults `--static-dir` to `web/dist`, and
  `cmd/server.go:105-108` resolves that relative to the opened workspace.
- `internal/web/static/handler.go:24-36` gives the override precedence over the
  embedded application.
- `internal/web/static/handler.go:64-72` and `:102-114` use `os.DirFS`, `Stat`,
  and `ReadFile`, which can follow symlinks outside the override directory.
- The web command automatically opens the resulting origin in the user's
  browser.

An untrusted repository can provide `web/dist/index.html`, obtain same-origin
API authority, read connection credentials, write a Python asset, and execute
it. A symlinked static entry can additionally expose a host file through HTTP.

Action:

- Make the embedded build the only production default. An override must be an
  explicit development flag with no default.
- Refuse workspace-relative overrides; resolve an explicitly supplied directory
  independently and serve it through a symlink-confined root.
- Add tests proving that a workspace `web/dist` is ignored and that neither a
  symlinked root nor a child symlink can expose outside content.
- Add a workspace-trust decision before any repository code or persisted
  schedule can execute; UI safety is necessary but not sufficient.

### SEC-04 — every preview/query path must fail closed as one SELECT

Evidence:

- `/api/sql/query` and `/api/sql/column-values` accept arbitrary SQL at
  `internal/web/httpapi/sql.go:39-95`.
- `internal/web/service/sql.go:81-102` forwards it unchanged, and
  `internal/web/service/direct_query.go:190-218` passes it to connector
  `SelectWithSchema`. Bruin connectors can execute non-result statements.
- Asset inspect skips enforcement for unsupported dialects and rejects only
  when parsing succeeds and returns false (`internal/web/service/direct_query.go:55-78`).
- `internal/web/service/execution.go:393-419` treats query extraction or parser
  errors as permission to continue.
- Non-SQL inspect interpolates `asset.Name` into SQL at
  `internal/web/service/execution.go:345`; `QuoteQualifiedIdentifier` already
  exists at `internal/web/service/helpers.go:195-204`.

Action:

- Introduce one shared `ValidateReadOnlySingleSelect` boundary used by ad-hoc
  query, column values, SQL asset inspect, notebook SQL, and broker queries.
  Unsupported dialect, extraction failure, and parser failure must reject.
- Quote every generated identifier. Never validate by prefix or regular
  expression.
- Use read-only credentials, transactions, or connector session settings as
  defense in depth where the destination supports them.
- Add a dialect corpus covering multiple statements, DDL/DML, comments,
  semicolons in strings, Jinja, `COPY`, `CALL`, `PRAGMA`, parser errors, and
  connector integration tests that prove no write occurs.

### SEC-05 — connection secrets must be write-only

Evidence:

- `WorkspaceConfigConnection.Values` is a generic response map
  (`internal/web/service/config.go:35-43`).
- `internal/web/service/config.go:429-470` derives fields but drops Bruin's
  `sensitive:"true"` and `sensitive_file:"true"` tags.
- `internal/web/service/config.go:494-600` reflects all selected values into the
  response.
- The frontend guesses sensitivity from a small name list
  (`web/components/workspace-connection-form-fields.tsx:397-405`), missing
  examples such as `service_account_json`, `grant_key`, `key_base64`, and some
  provider-specific credential names.

Action:

- Preserve Bruin's sensitive metadata in `WorkspaceConfigFieldDef` and generated
  web types. Do not duplicate the provider list in TypeScript.
- Return a non-secret marker such as `{configured: true}` for stored sensitive
  values. Omission during update preserves the existing secret; an explicit
  replacement changes it; an explicit clear action removes it.
- Keep connection testing server-side. Never serialize passwords, tokens,
  private keys, service-account JSON, or secret-file contents to the browser.
- Add table-driven tests across all Bruin connection types and a response test
  that fails if any sensitive field's plaintext appears.

### SEC-06 — bind outbound credentials to the intended origin

Evidence:

- `internal/web/service/api_asset.go:596` uses a default `http.Client`.
- `newAPIHTTPRequest` applies arbitrary user headers and authentication
  (`internal/web/service/api_asset.go:1026-1061`, `:1146-1192`).
- A response-controlled `next_url` becomes authoritative at
  `internal/web/service/api_asset.go:1263-1268` and `:1308-1314`; the next
  request reapplies all credentials.
- Go strips standard `Authorization`/cookies on some cross-origin redirects,
  but not arbitrary configured headers such as `X-API-Key`. Redirects are not
  otherwise constrained.
- OpenAPI suggestion fetching and API assets intentionally accept user-supplied
  URLs. Under SEC-01, that is also an unauthenticated SSRF surface.

Action:

- Create one outbound HTTP policy shared by OpenAPI suggestions, sampling,
  inference, and execution.
- Record the initial normalized origin. Reject cross-origin redirects and
  pagination by default. If a project explicitly permits another origin, strip
  all configured authentication, secret headers, and credential query
  parameters unless that exact origin is allowlisted for them.
- Re-resolve and re-check every redirect/dial to prevent DNS rebinding within a
  request. Always block cloud metadata endpoints. Because internal APIs are a
  valid local-first use case, make private-network access an explicit trusted
  project/user policy rather than silently removing the capability.
- Bound and sanitize non-2xx bodies before reflecting them into UI errors.
- Test relative and absolute pagination, 30x chains, custom API-key headers,
  query credentials, hostname case/default ports, DNS answer changes, loopback,
  link-local, and explicit internal-API opt-in.

### SEC-07 — snapshots need a source/secret inclusion policy

Evidence:

- `internal/web/snapshot/store.go:52-62` skips selected directories and database
  extensions but not `.env`, PEM/private keys, service-account files, or other
  likely secrets.
- `internal/web/snapshot/store.go:106-145` stores every other regular file below
  the pipeline.
- `internal/web/httpapi/deploy.go:82-108` returns any manifest file verbatim.

Action:

- Respect Git ignore rules and add a dedicated snapshot-ignore mechanism.
- Deny or require an explicit reviewed override for likely secret filenames and
  key material. Show the exact manifest before deployment.
- Authenticate every snapshot route and audit snapshot-content reads.
- Prefer an allowlist of execution-relevant pipeline files where Bruin can
  determine it without breaking Python dependencies and seed sidecars.
- Test ignored files, dotfiles, keys, symlinks (currently skipped), nested
  pipelines, and previously persisted snapshots after policy upgrades.

### SEC-08 — opening a repository must not start repository-supplied work

Evidence:

- Python assets execute unsandboxed as the Renart OS user
  (`internal/web/service/python_operator.go:168-207`, `:529-541`). This is
  expected after an explicit run, not on open.
- The scheduler database is workspace-local and scheduling is enabled by
  default (`cmd/server.go:284-314`, `:440-443`). A repository can force-add the
  normally ignored `.renart/state.db`.
- River workers start at `internal/web/scheduler/service.go:208` before schedule
  reconciliation at `:216`; startup recovery also inspects persisted runs
  before worker start. A crafted or copied state DB can therefore preload or
  replay work before current files have established trustworthy state.

Action:

- Add a project-identity-based trust decision before enabling the scheduler,
  running Python, invoking hooks/tools, or accepting repository-supplied UI
  content. Trust must not be inferred merely because the path is a Git repo.
- Refuse a tracked or repository-supplied scheduler DB on first open. Prefer
  machine-local state outside the checkout, keyed by the stable Renart project
  ID; at minimum quarantine a pre-existing tracked DB until trust is granted.
- Reconcile and validate persisted jobs before starting River workers. A job
  must resolve to a current pipeline and allowed environment at execution time.
- Add a crafted-state integration test proving that opening an untrusted clone
  cannot run a pipeline or Python process.
- Document clearly that trusted Python assets have host filesystem/network
  access. A real untrusted-code mode would require a separate OS/container
  sandbox project, not an in-process parsing library.

### SEC-09 — pin Sling and the installer chain

Evidence:

- `internal/web/service/load.go:354-359` defaults the package specification to
  plain `sling`.
- `internal/web/service/load.go:371-389` executes `uv tool run --from sling
sling ...`, resolving whatever release is current at runtime with host-user
  privileges.
- The inherited Bruin uv bootstrap downloads and executes an installer without
  independent artifact verification.

Action:

- Pin the exact tested Sling version in one release manifest shared by load,
  seed, API, and Python staging paths. Prefer a lock/hash or a Renart-managed
  tool environment over resolving per run.
- Keep explicit `RENART_SLING_BINARY` / package overrides for development, but
  make non-default provenance visible in diagnostics.
- Download a pinned uv release artifact and verify its published digest; do not
  execute a floating installer script.
- Add a release test that starts from an empty cache and proves the resolved
  Sling/uv versions and hashes.

### SEC-10 — the development proxy is a network boundary

Evidence:

- `scripts/dev.sh:70-98` supports `HOST=0.0.0.0`, exposes Vite, and proxies the
  full API to the loopback backend.
- `web/vite.config.ts:11-23` rewrites any forwarded `Origin` to the backend
  target, and `web/vite.config.ts:111-133` exposes all `/api` routes. The
  comment that the executing server is not network-exposed is therefore false
  in security terms: Vite is the exposed API gateway.

Action:

- Default Vite to loopback and require an explicit authenticated remote-dev
  mode before binding publicly.
- Do not rewrite arbitrary origins into trusted ones. Forward the browser
  session/CSRF mechanism from SEC-01 and allow only the configured dev origin.
- Print a prominent warning and refuse plaintext non-loopback remote-dev mode
  unless the user opts into the documented risk or configures TLS.
- Add a LAN-origin integration test against the proxy.

### SEC-11 — remediate reachable dependencies and install a real audit gate

Evidence and triage:

- The current build uses Go 1.26.0. A fresh `govulncheck -mode=binary` found 22
  reachable vulnerabilities. Twenty-one are fixed by Go 1.26.5; the remaining
  result is `GO-2026-5764` in AWS EventStream 1.6.10 / S3 1.80.2, fixed in
  EventStream 1.7.8 / S3 1.97.3.
- `.github/workflows/release.yml` uses
  `goreleaser/goreleaser-cross:v1.26.2-3`, also behind 1.26.5.
- OSV found only Monaco's bundled DOMPurify 3.2.7 in the pnpm lock, with no high
  or critical advisory. A pnpm override alone may not update code vendored into
  Monaco's emitted assets. Latest stable Monaco was still 0.55.1 during the
  audit, so this needs upstream tracking or a tested vendor patch.
- The existing `pnpm audit` script currently receives HTTP 410 and no dependency
  audit runs in CI.

Action:

1. Pin development and release builds to Go 1.26.5 or newer patched 1.26.x;
   rebuild every release artifact and rerun binary `govulncheck`.
2. Upgrade the Bruin/AWS dependency path until EventStream and S3 reach the
   scanner's fixed versions. Verify runtime integrations rather than forcing an
   incompatible transitive version blindly.
3. Add pinned OSV Scanner jobs for `go.mod` and `web/pnpm-lock.yaml`; keep
   `govulncheck` for Go reachability. Fail on new high/critical findings and
   require an expiry/reason for exceptions.
4. Track Monaco/DOMPurify upstream. If the fix cannot wait, patch the bundled
   sanitizer in a reproducible `pnpm patch` with Monaco sanitizer tests; do not
   assume a dependency override changes bundled code.
5. Remove or replace the broken audit command so local and CI documentation
   describes a check that actually runs.

### SEC-12 — enforce resource limits before allocation/execution

Evidence:

- `cmd/server.go:492-501` sets `ReadHeaderTimeout` and `IdleTimeout` but no body
  deadline or general request limits.
- Most JSON handlers decode directly from `r.Body` without `MaxBytesReader`.
- Each SSE client creates a goroutine and buffered channel without a subscriber
  cap (`internal/web/events/hub.go:40-47`).
- Seed multipart requests are capped at 256 MiB but read the file fully into
  memory.
- `/api/sql/query` fetches a full result before applying the UI row limit
  (`internal/web/service/sql.go:100-109`).

Action:

- Apply route-specific `MaxBytesReader` limits through shared decode helpers and
  deadlines through request context.
- Stream seed uploads into a root-confined temporary file while enforcing the
  byte cap; never hold 256 MiB per request in memory.
- Push `LIMIT limit+1` into validated SQL before connector execution and cap
  bytes as well as rows.
- Add authenticated per-session/global concurrency limits for runs, expensive
  inference, and SSE; add heartbeat, subscriber cap, and reconnect/backpressure
  tests. Keep long-lived SSE out of a blunt global `WriteTimeout`.

### SEC-13 — Markdown must not make implicit remote requests

Evidence:

- Asset Markdown can interpolate database row values into a template
  (`web/lib/asset-visualization.ts:25-35`, `:164-194`).
- `web/components/asset-inspect-view.tsx:51-88` renders it with ReactMarkdown
  without an image/URL policy. Notebook Markdown does the same at
  `web/components/app/notebook-page.tsx:1832-1835`.
- Monaco hover/completion content is another Markdown renderer: SQL and Python
  intelligence can pass raw column descriptions through
  `internal/sqllsp/analyzer.go:1520-1533`, `web/hooks/use-sql-lsp.ts:407-426`,
  and `web/hooks/use-python-query-intellisense.ts:338-358`. Monaco permits
  HTTP(S) Markdown images even when the Markdown is not marked trusted.
- No raw-HTML plugin was found, which prevents direct HTML/script injection,
  but normal Markdown images still cause browser `http(s)` requests. A template
  can place row data in the URL; an untrusted notebook can track opens or issue
  blind GETs to local devices.

Action:

- Create one shared locked-down Markdown renderer. Disable remote images by
  default, or require an explicit click/allowlist before fetching them. Restrict
  link schemes and add safe `target`/`rel` behavior.
- Sanitize Monaco hover/completion/signature-help Markdown separately (or render
  database/repository descriptions as plaintext); the ReactMarkdown component
  does not control Monaco's renderer.
- Enforce production CSP `img-src 'self' data:` unless the user explicitly
  enables a proxied/allowlisted image source. Use a no-referrer policy.
- Add tests for `javascript:`, `data:`, remote images, loopback images, data in
  query strings, and normal local/data images.

### SEC-14 — chart keys must not become CSS source text

Evidence:

- Database column names become `ChartConfig` keys in
  `web/lib/asset-visualization.ts:55-89` and notebook viz keys at
  `web/components/app/notebook-viz.tsx:111-143`.
- `web/components/ui/chart.tsx:68-93` interpolates each key into a raw `<style
dangerouslySetInnerHTML>` block.
- A key shaped like `x: red; } body { opacity:0 } /*` was reproduced in Chromium
  and changed global body styles. This is CSS injection/UI spoofing, not a
  demonstrated JavaScript execution primitive.

Action:

- Map external column names to generated internal IDs (`series-0`, `series-1`)
  for CSS variables. Keep the original name only as a React label/data key.
- Prefer React style properties or a fixed stylesheet over generated raw CSS;
  do not rely on `CSS.escape` alone for declaration values.
- Add hostile keys containing braces, colons, semicolons, quotes, newlines,
  Unicode, and custom-property syntax to chart tests.

### SEC-15 — add a production browser security baseline

The static handler currently sets content type and cache control but not a
content security policy, framing control, MIME-sniffing protection, referrer
policy, or permissions policy (`internal/web/static/handler.go:102-114`). This
magnifies SEC-03, SEC-13, and SEC-14 and permits clickjacking.

Action:

- Add a production CSP compatible with Monaco workers and embedded Wasm, with
  at least `default-src 'self'`, restrictive `connect-src`, `img-src`,
  `object-src 'none'`, `base-uri 'none'`, and `frame-ancestors 'none'`.
- Add `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, a
  minimal `Permissions-Policy`, and `X-Frame-Options: DENY` as legacy defense.
- Keep a separate explicit development policy for Vite/HMR; do not weaken the
  production policy to accommodate development.
- Roll CSP out in report-only mode first. If the current Monaco AMD loader
  requires dynamic evaluation, migrate to a CSP-compatible loader or document a
  narrowly scoped, time-limited exception; do not silently add a broad
  `unsafe-eval` allowance to the final policy.
- Add response-header and smoke tests for Monaco, Wasm, downloads, and SSE.

### SEC-16 — make CI/release least-privilege and reproducible

Evidence:

- `.github/workflows/release.yml:3-18` runs on pull requests while the
  GoReleaser job has `contents: write`.
- The non-tag verification path passes `GITHUB_TOKEN` into a container that has
  the PR checkout and Docker socket (`.github/workflows/release.yml:126-140`).
- Many release/E2E actions use mutable major/stable tags; Rust uses
  `dtolnay/rust-toolchain@stable`; the GoReleaser image is tag-pinned but not
  digest-pinned.
- `scripts/build_rustsqlparser_release_lib.sh:22-24` executes the floating
  rustup installer and `:72-73` builds with the floating stable toolchain.
- Dependabot covers Go/npm but not GitHub Actions.

Action:

- Split pull-request verification from tag publication. PR jobs get read-only
  permissions and no publication token; tag jobs receive the minimum write/OIDC
  permissions only at the publish step.
- Pin third-party actions to full commit SHAs, build containers to digests, Go/
  Node/pnpm/Rust to exact supported versions, and record upgrade ownership.
- Add `rust-toolchain.toml`; preinstall the pinned toolchain/targets in the
  release builder and remove curl-pipe execution.
- Add Dependabot for `github-actions`. Generate checksums and an SBOM; add
  provenance/attestation once the release split is stable.
- Treat Docker-socket access as code execution on the runner and never combine
  it with an unnecessary write token.

### SEC-17 — bound embedded SDK runtime dependency drift

`internal/pysdk/wheel.go:43-59` declares `pyarrow>=15.0.0` and `pandas>=1.5`,
and uv resolves them at runtime. The Renart wheel bytes are deterministic, but
the environment produced from that wheel is not.

Action:

- Define release-tested compatible ranges or a constraints/lock strategy with
  hashes for Renart-managed runtime dependencies. Do not over-pin user-owned
  asset projects across platforms.
- Build the runtime wheel and published PyPI wheel through the same version and
  metadata path; keep `twine check` and isolated install/import tests.

### SEC-18 — sensitive API responses should not be browser-cached

`internal/web/api/response.go:24-29` applies no cache policy. Today `/api/config`
contains full connection values, and `/api/workspace`, logs, query results, and
execution responses can also contain repository or data content. Secret
redaction in SEC-05 is the primary fix, but it does not make browser/proxy
caching desirable.

Action: set `Cache-Control: no-store` on authenticated API responses containing
workspace content, configuration, logs, snapshots, or data. Add response tests;
avoid disabling useful immutable caching for hashed static assets.

### SEC-19 — copied result data needs a spreadsheet-safe mode

`web/components/virtual-data-table.tsx:411-435` serializes copied TSV by removing
tabs/newlines, but string values beginning with `=`, `+`, `-`, or `@` remain
formulas in spreadsheet applications. Clipboard HTML escapes cells correctly;
the risk is formula interpretation after paste.

Action: either make the default TSV copy spreadsheet-safe by prefixing dangerous
string cells with an apostrophe, or offer an explicit clearly named safe-copy
mode while preserving a raw-data option. Add tests for all formula prefixes,
leading whitespace/control characters, numbers, and ordinary negative numeric
values so data is not corrupted unnecessarily.

### SEC-20 — do not group untrusted keys in prototype-bearing objects

Workspace-controlled group names are accumulated in ordinary objects at
`web/components/app/build-page.tsx:1864-1870` and
`web/components/app/lineage-canvas.tsx:377-384`. Keys such as `__proto__`,
`constructor`, or `toString` resolve inherited values and can crash the
reduce/spread path. No code execution was demonstrated.

Action: use `Map` or null-prototype records for workspace/database-controlled
keys and sweep similar reducers. Add fixtures using prototype names to the
workspace and lineage tests.

## 4. Positive controls and triaged non-findings

The audit also found several boundaries worth preserving:

- The Python query broker is loopback-only, uses a random 256-bit token,
  constant-time comparison, and a 4 MiB body limit. Production wiring supplies
  a fail-closed single-SELECT validator. Make a missing validator invalid broker
  configuration so future call sites cannot accidentally become permissive.
- Snapshot collection skips symlinks/non-regular files, and snapshot extraction
  checks destination containment into a fresh temporary directory. No current
  archive traversal exploit was found in that path.
- Seed filenames are reduced to a basename and upload size is capped. The
  remaining issue is parent-symlink confinement and full-memory buffering.
- API response/OpenAPI downloads have size caps, and displayed/logged URLs
  redact configured query credentials.
- Sling and other subprocesses use argument vectors rather than a shell; no
  shell-command injection was found in those call sites.
- ANSI conversion uses XML escaping; ReactMarkdown does not enable raw HTML; no
  application-authored dynamic-evaluation sink or browser secret persistence
  was found. Monaco's checked-in AMD loader does use `eval`/`new Function`, so
  its replacement or tightly scoped transition policy belongs to SEC-15.
- The discovery token is written through a mode-0600 temporary file and
  atomically renamed. The defect is that the main server does not require it.
- Polyglot downloads are versioned and checksum-verified. The release chain
  still needs independently pinned toolchain/actions as described above.
- Git operations use `go-git`; scheduling uses River/SQLite/cron/flock;
  filesystem watching uses `fsnotify`. Those are appropriate library
  boundaries rather than reinventions.

## 5. Build-vs-buy decision framework

Custom code is not automatically a problem. Replace it when at least one is
true:

- Renart is implementing a changing public specification (OpenAPI, JSON Schema,
  packaging metadata) where incomplete behavior creates correctness/security
  risk;
- the code is a generic algorithm with maintained, test-rich implementations;
- Renart already embeds a stronger parser/analyzer and maintains a second weaker
  implementation; or
- the custom generator can silently produce an incomplete contract.

Keep it when the behavior is Renart's product model, preserves reviewable Git
diffs/provenance, or is a small adapter over an established library. A migration
must reduce **net** complexity after accounting for bundle size, Wasm startup,
cross-platform release behavior, library update cadence, and compatibility
fixtures. No big-bang framework rewrite is recommended.

## 6. Build-vs-buy findings

| ID    | Area                                | Current custom surface                                                  | Decision                                                                           | Candidate / reuse path                                                                                            |
| ----- | ----------------------------------- | ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| BB-01 | Workspace filesystem confinement    | `SafeJoin` plus scattered `afero` operations                            | Adopt now                                                                          | Go 1.26.5 `os.Root`                                                                                               |
| BB-02 | OpenAPI parse/ref/schema validation | 927-line parser/validator + 513-line suggestion layer                   | Adopt                                                                              | `kin-openapi` for current Swagger 2/OAS 3.0 needs; `libopenapi` if 3.1/3.2 is required                            |
| BB-03 | Go-to-TypeScript DTO generation     | 265-line brace/regex extractor, manually enumerated types               | Adopt now                                                                          | `tygo` short-term; typed OpenAPI contract longer-term                                                             |
| BB-04 | DAG layout                          | 830-line layered/force implementation; unused legacy Dagre dependency   | Evaluate, then adopt algorithms                                                    | `elkjs` for layered/compound layout, `d3-force` for organic layout; current Dagre only as a baseline              |
| BB-05 | Python missing-package detection    | 283-line regex parser + hard-coded Python 3.11 stdlib                   | Remove duplicate                                                                   | Reuse embedded `ty` diagnostics/import extraction                                                                 |
| BB-06 | HTTP API schema/client              | 3,536 backend handler LOC + 26 frontend API modules / 2,280 LOC         | Incremental evaluation                                                             | Huma + `openapi-typescript`/`openapi-fetch`, or spec-first `oapi-codegen`                                         |
| BB-07 | SQL LSP token/scope parsing         | about 4,715 production LOC, including custom scanners                   | Keep product layer; consume existing token seam now, evaluate semantic spans later | Polyglot `Tokenize` spans; upstream AST spans only if needed                                                      |
| BB-08 | Notebook SQL rename lexer           | 266-line hand lexer/splicer                                             | Reuse existing token seam                                                          | Polyglot `Tokenize` spans; retain positional splice/Jinja mapping                                                 |
| BB-09 | SSE hub/event bus                   | 122 + 140 LOC                                                           | Keep and harden                                                                    | `net/http` + current small abstractions                                                                           |
| BB-10 | YAML/provenance/reconciliation      | node codec 215 LOC + `assetmeta` 837 LOC                                | Keep                                                                               | `yaml.v3` node model plus Bruin render, fuzz/property tests                                                       |
| BB-11 | Python wheel assembly               | 237 LOC                                                                 | Keep                                                                               | Python wheel spec conformance tests                                                                               |
| BB-12 | PyPI discovery                      | 173-line PEP 691 client + 195-line package service                      | Keep                                                                               | Official PyPI Index API; no better supported search API                                                           |
| BB-13 | Scheduler/watch/Git/registry        | substantial scheduler domain layer; thinner watch/Git/registry adapters | Keep                                                                               | River, cron, flock, fsnotify, go-git; retain reconciliation/history/snapshot policy and strengthen recovery tests |
| BB-14 | Renart domain engines               | graph, staleness, fingerprints, transactions, notebook viz              | Keep custom                                                                        | These are the differentiating product model                                                                       |

### BB-01 — use `os.Root`, not a more elaborate secure-join helper

This is the clearest security/build-vs-buy win. Lexical containment cannot
prevent symlink traversal or check/use races. Go added traversal-resistant
root-scoped filesystem operations in 1.24. Use the patched 1.26.5
implementation because earlier releases themselves had `os.Root` fixes. Build
one adapter around it for testability; do not recreate `openat` traversal in
Renart.

### BB-02 — replace custom OpenAPI semantics, keep Renart orchestration

`internal/web/service/api_openapi.go` is 927 lines and implements document
shapes, Swagger conversion details, path matching, local `$ref`, `allOf`, and
partial response validation. `api_openapi_suggestions.go` adds 513 lines. The
current model omits large parts of JSON Schema/OpenAPI behavior, including many
format/pattern/bound constraints, discriminator/external-ref semantics, and
newer unevaluated/dependent keywords.

Recommended seam:

- library owns parsing, version conversion, reference resolution under Renart's
  outbound policy, schema composition, and validation;
- Renart keeps document caching, operation selection UX, path/parameter
  suggestions, response-record selection, column mapping, and warning/error
  presentation; and
- external references are disabled by default or fetched only through SEC-06's
  bounded client.

Start with a fixture corpus made from current tests and real Swagger 2/OpenAPI
3 documents. First write a small ADR declaring Renart's supported OpenAPI
versions and compatibility policy; that product decision chooses the library.
If current scope stays Swagger 2 + OAS 3.0, `kin-openapi` is the simpler first
candidate. If OAS 3.1/3.2 and full JSON Schema semantics are a near-term
contract, evaluate `libopenapi` plus its separate validator instead. Record
license, maintenance, CVE response, binary size, and parse latency before the
choice.

### BB-03 and BB-06 — generated types now, typed API framework only by slice

`web/scripts/generate-api-types.mjs` manually lists source files/types, counts
braces, parses fields with regular expressions, and silently skips shapes it
does not understand. Its 597-line output is imported broadly, so a missed field
is a contract bug.

Use `tygo` as the bounded first replacement: it reads Go types/tags through Go's
parser/type system, supports type mappings, and does not require changing every
handler. Replace the generator against the existing packages first, with exact
golden/diff parity and a CI dirty-tree check. Consolidating wire DTOs may still
be useful, but it is a separate cleanup or part of the later typed-route
decision; combining it with generator replacement would obscure parity review.

Longer term, a typed route contract could remove duplicated handler decode/
validate/envelope code and hand-maintained frontend clients. Huma supports Chi
and OpenAPI 3.1; `openapi-typescript` plus `openapi-fetch` can produce a small
typed client. This deserves a two-or-three-endpoint spike covering normal JSON,
an error envelope, SSE, multipart upload, and project-prefixed routes. Do not
rewrite all routes until the spike demonstrates:

- the one-DTO/one-error/one-envelope convention remains clear;
- request size/auth middleware cannot be bypassed;
- generated clients work with project routing and abort signals; and
- streaming/SSE and multipart retain explicit specialized handlers.

If Renart prefers spec-first ownership, evaluate `oapi-codegen` instead of Huma.
Do not combine both approaches or adopt deprecated generators.

### BB-04 — replace layout algorithms, retain Renart's layout policy

`web/lib/app-lineage-layout.ts` is 830 lines: topology/rank construction,
dummy nodes, median ordering, transposition, placement, and a 260-tick
quadratic force simulation. The installed legacy `dagre@0.8.x` dependency is
not used by product layout code.

Keep Renart's recommendation scoring and semantic modes (`strict`, `bands`,
`force`), but delegate coordinates:

1. spike `elkjs` as the primary engine for strict/banded layered and compound
   layout in a web worker;
2. compare the installed legacy Dagre only as a simple-DAG baseline; it cannot
   directly preserve Renart's explicit prefix/layer-band constraints;
3. use `d3-force` for organic layout with deterministic seeding and bounded
   iterations; and
4. retain the current implementation until snapshot, crossing-count, runtime,
   bundle-size, and first-paint comparisons show a clear win.

Fixtures must include long thin DAGs, wide joins/fan-outs, disconnected
components, skip/intra-layer edges, cycles, and graphs large enough to expose
main-thread stalls. Preserve horizontal centering and stable positions when
content has not changed. Remove the unused legacy Dagre package and its types as
soon as the benchmark chooses an engine, including if the result is to retain
the current implementation.

### BB-05 — ask `ty` which imports are unresolved

`web/lib/notebook-python-deps.ts` maintains a Python 3.11 standard-library list,
parses imports/requirements with regular expressions, and owns a small alias
table. Renart already embeds the `ty` engine for Python diagnostics and module
resolution. Extend the backend result with structured unresolved-import/module
information (or a dedicated import-extraction call), then let the frontend map
that to the PyPI picker. Before presenting a suggestion, reconcile unresolved
modules against both declared distributions and installed modules so the
current "declared but not installed" suppression remains intact. Keep any
curated import-name/distribution-name aliases in one backend table.

This removes drift across Python syntax, multiline imports, conditional imports,
future stdlib versions, and notebook venvs. A curated alias table can reduce but
cannot eliminate import-name/PyPI-distribution drift.

### BB-07 and BB-08 — strengthen the existing parser seam; do not replace the LSP

The SQL LSP contains Renart-specific canonical graph resolution, notebook
scope, templating projection, fixpoint column inference, diagnostics,
navigation, and rename UX. Replacing it with a generic LSP would discard the
product value.

The first reusable problem is already solved upstream: Polyglot v0.5.15 exposes
`Tokenize(sql, dialect)`, and its tokens include raw text, byte-offset spans, and
line/column positions. Add a typed JSON wrapper and consume that existing stream
for notebook rename and lexical string/comment/identifier handling. Keep
host-document to embedded-SQL/Jinja projection and positional splicing in
Renart; do not AST-reprint user SQL.

The harder semantic scope/subquery work may still need AST-node spans that the
current token stream does not provide. Evaluate that separately and prefer an
upstream Polyglot addition over a Renart fork. Share one fixture corpus covering
dollar-quoted strings, bracket/backtick identifiers, nested comments, scalar
subqueries, table functions, and Jinja.

### BB-09 through BB-14 — custom code that is justified

- The SSE hub and internal event bus are small and encode Renart's snapshot/
  revision behavior. A broker library would not remove the required heartbeat,
  authentication, cap, and reconnect policy. Consolidate the duplicate browser
  `EventSource` ownership instead.
- The YAML node codec and `assetmeta` provenance preserve comments, ownership,
  reviewable diffs, and reconciliation across SQL/YAML files. Those are product
  semantics. Add fuzz/property tests and evaluate the maintained `go.yaml.in`
  module path when compatibility permits.
- The deterministic in-Go wheel assembler is justified because the same bytes
  ship embedded and to PyPI. Validate it against the wheel specification,
  `twine check`, and isolated installs rather than introducing Python into the
  Go release path.
- The PyPI client already uses the official PEP 691 Index API. PyPI deliberately
  has no general supported search endpoint that makes a third-party search
  client obviously better; keep the cache/fuzzy UX and optimize exact lookup.
- River, cron, and flock already own the scheduler's generic queue/cron/locking
  mechanisms, while Renart's roughly 2.9k-line scheduler layer justifiably owns
  reconciliation, history, snapshot, recovery, and environment policy. Fsnotify
  and go-git likewise own the generic watch/Git mechanisms. Keep these domain
  layers, but do not understate their recovery and policy test surface.
- Staleness, fingerprints/coverage, graph modeling, asset transactions,
  provenance reconciliation, notebook sessions/viz, and DuckDB coordination are
  Renart's differentiating layer and should remain custom.

## 7. Implementation plan and PR boundaries

### P0 — security release gate

Do not hide these in a broad refactor; each change needs a focused regression
suite and reviewable commit/PR boundary.

1. **Static UI boundary:** embedded-only default, explicit root-confined dev
   override, hostile-workspace tests (SEC-03).
2. **Root-scoped filesystem authority:** introduce the `os.Root` adapter, make
   IDs resolve through canonical pipeline/asset ownership, migrate destructive
   and create/update operations first, and add adversarial tests (SEC-02,
   BB-01).
3. **Authenticated server boundary:** mandatory API/SSE auth, listener-derived
   Host/origin policy, secure browser bootstrap, non-loopback mode rules, and
   middleware/E2E coverage (SEC-01). Update the Vite flow in the same contract,
   though its remote exposure can be a follow-up PR.
4. **Write-only connection secrets:** preserve Bruin sensitive tags, redact all
   responses, and implement preserve/replace/clear update semantics (SEC-05).
5. **Read-only query boundary:** shared fail-closed validator, quoted generated
   identifiers, connector defense-in-depth, and adversarial corpus (SEC-04).
6. **Toolchain/runtime pins:** Go 1.26.5, fixed AWS path, exact Sling/uv, current
   binary `govulncheck`, and cold-cache resolution tests (SEC-09, SEC-11).
7. **Open-repository execution boundary:** trust decision, reject/quarantine
   tracked scheduler state, reconcile before River starts, crafted-state test
   (SEC-08).
8. **HTTP/snapshot secret boundaries:** origin-bound outbound auth and explicit
   internal-network policy; snapshot ignore/secret policy and authenticated
   reads (SEC-06, SEC-07).
9. **PR/release privilege split:** make pull-request verification read-only and
   stop passing publication credentials into the Docker-socket build; reserve
   write/OIDC authority for tag publication (the conditional High part of
   SEC-16).

P0 exit criteria:

- all Critical findings have regression tests and are closed;
- every High finding is closed or explicitly accepted in a dated maintainer
  decision with scope/expiry;
- a fresh release-equivalent binary has no reachable `govulncheck` finding with
  an available upgrade;
- an untrusted clone cannot replace the UI, access the API, delete/overwrite
  non-assets, preload scheduled execution, or reveal connection/snapshot
  secrets merely by being opened; and
- inspect/ad-hoc/notebook/broker preview paths prove they cannot write.

### P1 — browser, resource, and release hardening

1. Route body limits, streamed seed staging, bounded SQL results, execution/SSE
   quotas, heartbeat/backpressure tests (SEC-12).
2. Shared locked-down Markdown renderer, synthetic chart IDs/no raw key CSS,
   production CSP/header policy (SEC-13 through SEC-15).
3. Authenticated loopback-default Vite proxy and remote-development policy
   (SEC-10).
4. Finish full SHA/digest/toolchain pins, GitHub Actions Dependabot, checksums,
   and SBOM after the P0 PR/release permission split (SEC-16).
5. Pinned OSV/govuln CI and tracked Monaco exception with owner/expiry
   (SEC-11).
6. Runtime Python dependency constraints and wheel release checks (SEC-17).
7. API `no-store`, spreadsheet-safe copy, and prototype-safe grouping
   (SEC-18 through SEC-20).

### P2 — high-confidence library substitutions

1. Replace the Go-to-TypeScript script with `tygo` against existing packages;
   require exact output parity and reproducible/checked generation. Keep DTO
   consolidation out of this PR (BB-03).
2. Declare the supported OpenAPI versions/compatibility policy in an ADR, select
   the matching library, then replace custom parse/ref/validation semantics
   behind the existing service interface and fixture corpus (BB-02).
3. Replace frontend Python import/stdlib regexes with structured `ty` results,
   preserving declared and installed dependency suppression (BB-05).
4. Consume Polyglot's existing token spans for notebook rename and lexical SQL
   handling, without forking or extending Polyglot (BB-07, BB-08).
5. Run the ELK-first DAG benchmark spike; migrate only after quality,
   performance, and bundle gates pass, then remove unused Dagre (BB-04).

### P3 — bounded architectural evaluations

1. Huma plus OpenAPI TypeScript client spike on representative route types;
   choose code-first or spec-first ownership and document a go/no-go decision
   (BB-06).
2. Evaluate whether semantic SQL scope analysis needs Polyglot AST-node spans;
   if it does, propose the smallest upstream addition and migrate one scope
   scanner before expanding the seam (BB-07).
3. Reassess the remaining custom inventory after those migrations.

## 8. Validation matrix

| Boundary           | Required automated evidence                                                                                                    |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| Server auth        | unit matrix for method/token/Host/Origin; browser bootstrap; CLI delegation; SSE reconnect; DNS-rebinding and LAN-negative E2E |
| Filesystem         | root/dot/reserved IDs; wrong owner; final/parent symlinks; concurrent replacement; create collision; Windows path variants     |
| Static UI          | malicious workspace `web/dist` ignored; override opt-in; root/child symlink escape denied                                      |
| SQL                | per-dialect mutation corpus plus live connectors proving no DDL/DML side effect                                                |
| Secrets            | all Bruin sensitive tags covered; API response/log/snapshot golden tests contain no plaintext credentials                      |
| Outbound HTTP      | redirect/next-url origin matrix; custom headers/query auth; DNS changes; metadata/private ranges; explicit internal opt-in     |
| Scheduler trust    | force-added crafted state DB cannot execute before trust/reconciliation                                                        |
| Browser content    | Markdown URL policy; CSS hostile-key corpus; CSP/header smoke tests with Monaco/Wasm/SSE                                       |
| Resources          | body/result/upload caps; cancellation; concurrent run/SSE limits; slow-client tests                                            |
| Supply chain       | pinned versions/digests; cold-cache build; `govulncheck`; OSV; actionlint; checksums/SBOM                                      |
| Library migrations | fixture parity, output golden tests, latency/memory/bundle metrics, rollback seam                                              |

For broad Go validation, use sequential execution (`go test -p 1 ./...`) if the
host hits build pressure. Frontend security changes require `pnpm build` and
live Playwright coverage. Release changes require a release-equivalent build,
not only a module graph scan.

## 9. Reference material

Primary references used for the recommendations:

- [Go security best practices](https://go.dev/doc/security/best-practices),
  [Go release history](https://go.dev/doc/devel/release), and
  [`os.Root` documentation](https://pkg.go.dev/os)
- [`GO-2026-5764`](https://pkg.go.dev/vuln/GO-2026-5764) and the
  [Go vulnerability database](https://vuln.go.dev/)
- [OWASP CSRF prevention guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
- [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use)
  and [Dependabot for Actions](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/auto-update-actions)
- [Rust toolchain override files](https://rust-lang.github.io/rustup/overrides.html)
- [OSV Scanner supported lockfiles](https://google.github.io/osv-scanner/supported-languages-and-lockfiles/)
  and [DOMPurify advisory example](https://github.com/advisories/GHSA-x4vx-rjvf-j5p4)
- [`kin-openapi`](https://github.com/getkin/kin-openapi) and
  [`libopenapi`](https://github.com/pb33f/libopenapi)
- [`tygo`](https://github.com/gzuidhof/tygo),
  [Huma](https://github.com/danielgtaylor/huma),
  [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen), and
  [`openapi-fetch`](https://openapi-ts.dev/openapi-fetch/)
- [React Flow layout guidance](https://reactflow.dev/learn/layouting/layouting),
  [`elkjs`](https://github.com/kieler/elkjs), and
  [`d3-force`](https://github.com/d3/d3-force)
- [Polyglot SQL parser/SDK](https://github.com/tobilg/polyglot)
- [Python wheel specification](https://packaging.python.org/en/latest/specifications/binary-distribution-format/)
  and [PyPI Index API](https://docs.pypi.org/api/index-api/)

## 10. Final recommendation

Treat the next security work as boundary repair, not cosmetic hardening. The
combination of mandatory authentication, canonical ID resolution through
`os.Root`, an embedded-only privileged UI, write-only credentials, and
fail-closed SQL removes the shortest paths from "open repo / visit page" to
host compromise.

Then make four targeted build-vs-buy changes: OpenAPI, DTO generation, Python
import detection, and (after measurement) DAG layout; also consume Polyglot's
existing token stream before writing more SQL lexing code. Do not spend
migration budget replacing the product-specific graph/provenance/staleness/
notebook layers or the justified scheduler policy built over River/cron/flock.
That split reduces security and maintenance risk without turning Renart into a
framework rewrite.
