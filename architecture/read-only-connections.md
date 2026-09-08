# Read-only connections

Connection access is a project/environment/alias policy, independent of asset
ownership, editor mutability, credentials, physical target identity, and locks.
Source assets still describe externally managed relations; Load reads one
connection and writes another. A full refresh affects the destination only.

## Authority and compatibility

The Renart-owned `.renart/environments.yml` extends existing environment flags:

```yaml
environments:
  production:
    connections:
      production_db:
        access_mode: read_only
      analytics_db:
        access_mode: read_write
```

The optional entry defaults to legacy unrestricted behavior. Explicit
`read_write` never grants database privileges or overrides DuckDB's native
`read_only` flag / parsed `access_mode=read_only` path option. Aliases retain
separate permissions even when they point at the same physical database.

`internal/web/policy` owns strict, revisioned loading and the pure effect
evaluator. Unknown fields/modes, duplicate YAML keys, malformed documents,
unreadable files, and interrupted configuration transactions fail closed.
Missing optional policy is valid. A snapshot hashes bytes and checks the
transaction marker both before and after reading. Connection reference checks
use the selected environment's actual configuration; the settings DTO also
reports orphaned policy references across environments.

Connection/environment rename, clone, and delete participate in the existing
config/secret compensation transaction. The transaction also saves/restores
policy. A process mutex plus `.renart/runtime/configuration.lock` flock
serializes participating writers; `.renart/runtime/configuration.pending`
blocks guarded operations while multiple files are being updated. Policy
writes use atomic replacement. Connection edits carry the policy revision;
stale saves return `connection_policy_stale` and preserve the UI draft.
Older environment-flag-only updates preserve connection entries.

After a killed transaction, stop competing writers, reconcile the local
connection config, `.renart/secrets.yml`, and `.renart/environments.yml` with
the intended aliases and restrictions, then remove the pending marker only
after verifying all three. There is no automatic permission-widening recovery.
The runtime directory is ignored by source control and deployment snapshots.

## One effect model, several enforcement boundaries

`service/connection_access.go` derives ordered read/write/unknown requirements
from actual, operation-local rendered asset definitions and canonical Load
parameters. It does not infer writes from secret purpose or coordination locks.

| Operation | Requirement |
| --- | --- |
| External Source, known table sensors | Read |
| Verified single SQL query, query sensor, hook or custom check | Read; unknown on parse/shape failure |
| Load | Read source; write destination |
| Table/view materialization, API and Seed destinations | Write |
| Metadata push | Write |
| Opaque operators, Ingestr source, injected connection credentials | Unknown |
| Source with materialization | Invalid definition, on any connection |

Read-only denies write and unknown. Ordinary unrestricted connections retain
their existing behavior. SQL classification uses the pinned SQL-intelligence
parser, not prefix/keyword heuristics. Arbitrary functions and database-side
effects remain a reason to require native least-privilege credentials.

Enforcement runs at these boundaries:

1. Authoring validates the parsed prospective definition before file writes;
   semantic connection/materialization changes are checked too. Raw drafts,
   descriptions and column metadata remain repairable.
2. Type checks and deployment review produce connection-field diagnostic
   targets. Execution planning scopes access blockers to the selected assets;
   an unrelated blocked branch does not prevent a valid selected read.
3. Admission checks all selected units/windows before starting a sibling.
4. Every physical task re-reads the originating project's current policy and
   configuration before connection preflight, target cleanup or execution.
5. Load/API entry points, ad-hoc/Inspect queries, notebook promotion and the
   Python query broker have corresponding checks. The broker also validates
   SQL against the actual selected connection dialect.

Verified SQL tasks on read-only connections use
`connection_access_sql.go`: rendered read hooks and SQL are dispatched directly
through the native query runner with normal DuckDB coordination. They never
enter materialization operators, full-refresh cleanup or shared write sessions.
Schema-prefix rewriting is retained and the rewritten query is checked before
dispatch. Scheduled checks remain separate guarded tasks.

The production connection factory applies native DuckDB read-only mode before
secret resolution/client construction. Lazy connection handles check current
policy on reuse and refuse a changed native mode or removed alias, including
before first use. Callers start a fresh operation rather than closing an
in-use handle. Sling receives an explicit native `read_only` setting for DuckDB
sources. Read-only DuckLake is deliberately rejected before initialization:
there is no audited native read-only adapter for it yet.

## Plans, deployments and privacy

Contracts carry effect/operation metadata with hashed connection keys plus a
scoped access-policy identity. Raw private connection names are not persisted
in secret-free target snapshots. These fields are review evidence only:
authorization is always re-derived from actual definitions/current config.
Policy identity does not enter data fingerprints or physical mutation locks.

Reviewed contracts change when the access of a referenced alias changes;
unrelated aliases do not invalidate them. Stale V3 contracts require replanning.
Older queued contracts and deployed source do not freeze permission: each
physical task still evaluates the originating project's live policy. Saving a
restriction does not cancel or roll back an operator already in progress: that
operator may still issue further statements. Enforcement is at task boundaries,
not a statement-level revocation fence for running writable operators.

## UI and API

Go-owned generated DTOs expose declared/effective access, policy revision and
configuration errors. The ordinary Connection dialog has one Access selector.
Read-only badges appear in settings, source pickers and Data Browser. The
backend's creation profile removes read-only write destinations, retains Load
sources and computes defaults only after eligibility filtering. Existing
SQL assets without materialization use the separate `read_target` role; their
actual SQL and hooks are still validated when changed or executed. Existing
invalid choices are not silently substituted. Read-only Data Browser tables
remain usable as Source assets; connection-to-Load-destination dragging is not
offered. Canvas `readOnly` (projected/non-editable nodes) remains unrelated.

Diagnostics navigate to the existing Connection dialog's `access_mode` field
through the normal typed navigation target, including cold tabs. Frozen dialog
snapshots retain dirty input while SSE updates arrive.

`GET /api/config/connections/access-preview` is an advisory saved-source scan.
It returns affected write/unknown assets and incomplete-scan warnings without
resolving credentials or touching a warehouse. The collapsed UI marks pipelines
that define schedules and links matching active runs from the existing SSE-fed
run list (bounded to 50). It is not an exhaustive inventory of queued/deployed
versions; those are checked on execution.

## Scope and verification

This is a Renart-managed-operation guardrail, not an ACL service or sandbox.
It does not provision accounts, constrain arbitrary subprocesses, revoke raw
credentials or prevent another writable alias from changing the same database.
No CDC/replication-slot setup, table-level ACLs or policy bypass was added.

Coverage lives in `policy/access_test.go`, `service/config_access_test.go`,
`service/connection_access*_test.go`, and the two `read-only-*.live.spec.ts`
files. Native DuckDB tests check driver denial, stale handles and Sling options;
live tests cover settings/cold-tab repair, Source/Load authoring, refresh,
downstream SQL, and a PostgreSQL source role without write/schema-create grants.
Use the resource-bounded [local verification runner](testing.md) for broader
release evidence. Logs and browser traces belong in ignored `.test-artifacts`.
