# Local secret lifecycle follow-ups

Status: active reliability follow-ups. Write-only credential fields, native OS
storage, the encrypted local vault, environment references, binding identity,
operation-scoped connection resolution, schedule convergence, and local CLI
administration are implemented. See [backend](../architecture/backend.md) and
[frontend](../architecture/frontend.md) for the current contracts.

Team and hosted providers are parked in
[secret-providers.md](secret-providers.md); they are not available features or
requirements for local secret reliability.

## 1. Provider-backed sensitive-file leases

Sensitive-file fields currently accept write-only paths. Design a provider
lease that materializes private file contents for exactly one operation:

- Resolve only the selected project/environment/reference and purpose.
- Create private, owner-restricted temporary files outside Git and workspace
  discovery; never place contents in argv, API responses, snapshots, or logs.
- Define ownership, lifetime, cancellation, cleanup failure, and restart recovery
  before adding file-provider choices to the UI.
- Feed the lease through the existing resolved-connection factory and child
  process boundary. Do not build a separate provider translator.
- Distinguish a user-authored file path from a managed temporary lease; never
  delete a user's original file during cleanup.

Test successful use, cancellation, provider refusal, subprocess failure,
interrupted cleanup, and concurrent operations in different environments.
Exact private paths must not leak in public errors.

## 2. Migration preview and crash consistency

The current provider/manifest/config writes use compensating transactions.
Add an explicit migration preview for legacy inline credentials and strengthen
crash-injection evidence across provider writes and both filesystem updates.

Preserve the old working credential until the new value and binding have been
verified. A failed operation must restore the prior usable state; restart must
detect an interrupted migration rather than silently reinterpret a placeholder.
Keep secret-free provenance/status in connection settings even if the provider
is locked or unavailable.

Verify replace, keep, clear, rename, clone, and delete with fault injection at
each write boundary. Include native-store unavailable/locked/unlocked states,
encrypted-vault restart/re-unlock, and parent-config/child-workspace layouts.
Never borrow a sibling project's bindings.

## 3. Remaining subprocess and redaction audit

Pipeline and notebook SDK queries already use the Go broker; do not reimplement
them. Inventory remaining explicit legacy Python secret injection and subprocess
paths. Preserve Bruin compatibility while ensuring resolved bundles seed
redaction and values are scoped to one child environment, never process-global
environment variables or argv.

Test canary secrets across errors, stdout/stderr, config responses, SSE, plans,
snapshots, and durable records. Lock/unavailability must be an actionable
provider state, not a silent switch to environment references.

## Trust boundary and closure

This protects against accidental disclosure and cross-environment mistakes, not
a compromised process running as the same OS user or arbitrary code explicitly
given credentials. Redaction is defense in depth, not permission to expose
values at another boundary.

Every new resolution remains purpose-tagged and short-lived. A binding change
invalidates reviewed identity; rotating a value behind the same binding does
not alone change freshness. No plaintext fallback is acceptable.

Fold completed lease/migration contracts into architecture and delete this plan
when these local slices have evidence. Hosted-provider work does not keep it open.
