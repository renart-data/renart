# Distributed warehouse freshness journal

Status: investigated proposal — implement only after the receipt/trust contract is accepted

## Decision summary

Storing a second copy of Renart's successful materialization evidence in the
warehouse is worthwhile, primarily for recovery and for handing freshness
between Renart installations. It should **not** replace `.renart/state.db`, and
it should not initially coordinate live writers.

The recommended design is an opt-in, per-connection, append-only **freshness
journal**:

- each completed physical output produces one canonical, content-addressed
  receipt;
- a receipt names the previous receipt(s) that the writer observed for that
  physical target, forming a small DAG rather than a mutable “latest” row;
- a receipt also names the exact upstream writer receipts visible when the
  asset ran, preserving the causal read set already captured by Renart;
- Renart keeps its existing local target claims, latest-writer rows, coverage,
  completion outbox, and scheduler state authoritative for execution;
- local SQLite facts replicate asynchronously to the output warehouse, and
  validated remote receipts may later be imported into a new local mirror;
- one valid journal head may contribute freshness evidence; concurrent heads,
  gaps, incompatible versions, and unverifiable writers fail closed; and
- no SQL, variable values, credentials, endpoints, local paths, run errors, or
  user data enter the warehouse journal.

This is Git-like in the useful sense—immutable objects, parent links, offline
replicas, explicit divergence—not in the misleading sense of automatic global
consensus. Two disconnected writers can create two valid heads. Renart must
show that divergence and reconcile it; timestamps must not silently choose a
winner.

## 1. Why do this?

Today the complete operational truth is local. `.renart/state.db` contains the
completion outbox, immutable materialization facts, compacted interval coverage,
the current writer and generation for each physical target, active/dirty write
claims, latest attempts, runs, deployments, and schedules. This is internally
coherent and lets one Renart process fail closed, but it has three limits:

1. deleting or losing the local state database loses observed freshness even
   when the warehouse tables are intact;
2. another clone or machine starts without the first machine's materialization
   evidence; and
3. independent Renart installations writing the same warehouse target cannot
   exchange completion evidence after they reconnect.

A warehouse journal can help with:

- restoring positive materialization evidence after local-state loss;
- moving scheduled execution between machines without copying a live SQLite
  database;
- sharing observed coverage between trusted clones of one project;
- detecting that a different Renart writer replaced a target; and
- explaining which code/configuration/window produced a table even when its
  original run history has expired locally.

It cannot prove that an external tool has not modified a table. A successful
receipt says what Renart observed and wrote; it is not a warehouse-wide change
data capture system.

## 2. Current invariants that must survive

The implementation must reuse, not weaken, the contracts documented in
[`architecture/staleness.md`](../architecture/staleness.md):

- project and pipeline UUIDs, canonical asset IDs, versioned fingerprints, and
  full variables hashes identify the logical result;
- a secret-free exact `target_identity` identifies a physical result without
  exposing connection coordinates;
- completion ID plus ordinal is stable and replay-safe;
- target generations prevent old A -> B -> A coverage from becoming current;
- equal-time or otherwise unordered writers become ambiguous;
- interval coverage distinguishes union-safe windows from replacing writes;
- active or dirty physical-write claims suppress freshness;
- the execution snapshot captures what actually ran and what upstream writers
  it read; and
- physical completion reaches a durable local outbox before derived state is
  acknowledged.

The remote design must not reconstruct these values from current workspace
files after a run. It must receive the captured completion evidence.

## 3. Why a mirrored “latest freshness” table is unsafe

`UPSERT target = newest timestamp` loses information in exactly the cases where
distributed state matters:

- clocks can differ between machines;
- two writers may complete at the same time or replicate out of order;
- a delayed receipt can overwrite a newer head;
- a warehouse clone or time-travel restore can rewind the metadata table;
- a target can be replaced by another asset or environment and later switch
  back; and
- one warehouse transaction generally cannot cover an entire multi-warehouse
  pipeline.

Warehouse transaction semantics are not uniform either. Snowflake executes DDL
as its own transaction and implicitly commits an active transaction; BigQuery
does not allow permanent-table DDL inside a multi-statement transaction. A
portable design therefore cannot promise that arbitrary physical DDL and a
journal insert commit atomically. See the official
[Snowflake transaction behavior](https://docs.snowflake.com/en/sql-reference/transactions)
and [BigQuery transaction limitations](https://cloud.google.com/bigquery/docs/transactions).

## 4. The Git analogy, precisely

Use these concepts:

| Git concept | Freshness-journal equivalent |
| --- | --- |
| repository | one Renart project UUID |
| object | one canonical completion receipt |
| object ID | SHA-256 of the versioned canonical receipt body |
| branch namespace | project + exact physical target identity |
| parent commit | target receipt(s) visible immediately before the write |
| clone/fetch | copy validated receipts into a local mirror |
| divergent heads | concurrent or disconnected target writers |
| merge decision | explicit reconciliation after observing physical target state |

Do **not** borrow these assumptions:

- there is no automatically selected default branch;
- commit time does not resolve divergent writes;
- a content hash proves integrity, not who was authorized to write;
- each output warehouse contains only a partial replica of a cross-warehouse
  project; and
- the data table itself is not reconstructed from the journal.

Delta Lake and Iceberg validate the general shape: both use immutable,
versioned snapshots plus optimistic conflict detection. Delta serializes
concurrent table writes and supports application transaction IDs, while Iceberg
atomically swaps the current metadata pointer and assigns sequence numbers at
commit. Those guarantees belong to their individual table formats, however;
they are not available for every Renart target. Renart should optionally record
a native Delta/Iceberg version, not make an open table format a universal
dependency. See [Delta concurrency](https://docs.delta.io/concurrency-control/),
[Delta idempotent writes](https://docs.delta.io/kernel/rust/writing/idempotent_writes),
and the [Iceberg specification](https://iceberg.apache.org/spec/).

## 5. Receipt contract

Define a strict canonical codec in a provider-neutral package. The JSON below
is illustrative; field names and canonical encoding need a versioned spec and
golden tests before implementation.

```json
{
  "version": 1,
  "project_id": "uuid",
  "pipeline_id": "uuid",
  "asset_id": "pipeline-uuid:asset.name",
  "environment": "production",
  "target_identity": "sha256 digest",
  "fingerprint": "v3:...",
  "own_content": "...",
  "vars_hash": "...",
  "source_snapshot_id": "optional deployment version",
  "coverage": {
    "mode": "marker | union_interval | replace_interval",
    "start": "optional RFC3339Nano",
    "end": "optional RFC3339Nano"
  },
  "completion_id": "stable opaque id",
  "completion_ordinal": 3,
  "completed_at": "RFC3339Nano",
  "parents": ["receipt sha256"],
  "inputs": [
    {
      "asset_id": "upstream pipeline-uuid:asset.name",
      "target_identity": "sha256 digest",
      "writer_receipt_id": "receipt sha256"
    }
  ],
  "native_target_version": {
    "kind": "optional provider-defined kind",
    "value": "optional opaque version"
  },
  "producer": {
    "instance_id": "random installation id",
    "renart_version": "semantic version"
  }
}
```

The persisted row adds `receipt_id = sha256(canonical_body)` and optionally a
signature. Parents and inputs are sorted and unique. `parents` describe the
history of this receipt's own target; `inputs` describe the physical upstream
writers this output actually read. A Renart-produced physical upstream needs a
`writer_receipt_id`; a declared external source is represented by an explicit
source-boundary marker instead of an invented materialization receipt. Receipt
construction therefore runs in dependency order and reuses the version-five
execution snapshot rather than re-reading the graph after completion. If that
causal evidence is unavailable, the receipt may be mirrored for diagnostics but
cannot restore freshness.

The canonical body must reject unknown fields for a known version,
non-canonical times, whitespace-padded identities, unsupported fingerprint
versions, dangling input receipt references, and mismatched content hashes.

### Deliberately excluded

- rendered or authored SQL and Python;
- variable names or values;
- connection names, hosts, accounts, database paths, and principals;
- raw physical table coordinates (the existing opaque target digest is
  sufficient);
- logs, errors, failed-check text, and row samples; and
- schedule, deployment, and complete run-history records.

OpenLineage remains a useful interoperability model for emitting run, job,
input, and output observations, and its custom facets could carry a Renart
receipt ID later. It is not a freshness database: its run events do not define
Renart's target generations or interval-coverage merge rules. See the
[OpenLineage object model](https://openlineage.io/docs/spec/object-model/) and
[facet extension contract](https://openlineage.io/docs/spec/facets/).

## 6. Warehouse schema

Prefer one small metadata schema per enabled connection, e.g. `renart_meta`,
with provider-specific quoting and types. Keep the logical schema portable:

### `freshness_receipts`

| Column | Purpose |
| --- | --- |
| `receipt_id` | canonical SHA-256; logical unique key |
| `project_id` | isolates unrelated repositories sharing a warehouse |
| `target_identity` | exact secret-free target digest |
| `asset_id`, `environment` | logical writer scope |
| `completed_at`, `completion_ordinal` | diagnostics and deterministic replay evidence |
| `fingerprint_version` | allows indexed compatibility filtering |
| `body` | canonical receipt as text, not provider-specific JSON |
| `body_hash` | duplicates `receipt_id` only if an adapter needs a projected check |
| `inserted_at` | warehouse observation time; never execution ordering |

Parents can either be a canonical JSON string in the receipt body or live in a
second append-only `freshness_receipt_parents` table. Start with the body only:
head calculation normally fetches the small target-specific receipt set, and a
second table creates a cross-table consistency problem on warehouses without
portable transactions.

The logical project/asset IDs in a receipt are operational metadata and may
reveal naming conventions even though physical coordinates are omitted. Treat
the journal as access-controlled metadata, not anonymized data. Before fixing
the v1 contract, compare raw logical IDs with project-scoped SHA-256 asset keys:
the latter reduce casual disclosure and can be resolved by a clone that has the
same workspace, but they do not provide secrecy against dictionary guessing.

Do not make a mutable head table authoritative. An optional
`freshness_head_cache` may accelerate reads only if it can always be rebuilt
from receipts and carries an explicit cache version/watermark.

### Permissions

Use a dedicated schema and least-privilege roles:

- migration owner: create/alter the metadata schema and table;
- writer: insert receipts, with no update/delete permission;
- reader: select receipts for its project; and
- optional maintenance role: apply configured retention.

Some deployments will not grant `CREATE SCHEMA` or `CREATE TABLE`. Enabling the
feature must distinguish **managed** bootstrap from **pre-provisioned** mode
and provide exact DDL for an administrator. Failure to provision the journal
must not break ordinary materialization while the feature is advisory.

## 7. Write and replication flow

The base flow remains local-first:

```text
local target claim
    -> physical write
    -> durable completion envelope
    -> atomic local fact/coverage/writer update
       + remote-replication outbox row
    -> asynchronous idempotent warehouse insert
```

Add a dedicated `renart_freshness_replication_outbox` rather than making the
existing completion dispatcher wait on a remote warehouse. Enqueue the remote
receipt in the same SQLite transaction that records the local fact. A remote
outage then delays replication but cannot make successful physical work look
failed or block unrelated completion recovery.

The outbox needs a stable routing contract. The current execution snapshot
persists only opaque connection keys, which are intentionally insufficient to
reopen a connection. The least surprising solution is explicit per-environment
configuration that maps a stable connection name to its journal settings:

```yaml
freshness_journals:
  warehouse:
    enabled: true
    schema: renart_meta
    mode: managed # or pre_provisioned
```

The outbox may retain that non-secret connection name plus the captured opaque
connection key. On retry, Renart re-resolves the name and requires its key to
match; configuration drift pauses replication instead of writing to a different
warehouse.

An adapter may later support an **atomic co-commit** capability when it owns the
same transaction as the data write. That is an optimization, not the portable
contract. It must be proven per materializer/provider and must not be inferred
merely because the warehouse supports transactions.

### Batching

Adapters need different delivery policies. PostgreSQL can cheaply insert a
small receipt with idempotent `ON CONFLICT` handling and is the best first live
implementation; see the official
[PostgreSQL `INSERT` contract](https://www.postgresql.org/docs/current/sql-insert.html).
BigQuery explicitly discourages point-style DML in its
[compute best practices](https://cloud.google.com/bigquery/docs/best-practices-performance-compute),
so its adapter should batch receipts or use a load path rather than issue one
`INSERT` per asset. Snowflake DML can batch too, but journal bootstrap DDL cannot
share a transaction with active DML. Trino support must be connector-capability
based rather than assumed from the SQL frontend.

### Remote synchronization cadence

Never query a warehouse journal in a canvas or HTTP request path. A backend
worker should fetch on startup, after a local receipt is queued, before an
admission decision that opts into remote evidence, and on an explicit CLI/UI
refresh. If automatic cross-installation discovery is enabled, use a bounded,
jittered interval per connection and publish ordinary local SSE events only
after the mirror changes; the browser must not poll the warehouse.

Fetch only the configured project and currently relevant targets. Prefer a
warehouse-generated `inserted_at` plus `receipt_id` keyset cursor, with an
overlapping page and periodic target-head rescan so a cursor bug or equal-time
batch cannot silently hide a receipt. Keep retry concurrency, page size, query
budget, and last successful sync visible in diagnostics.

Do not prune remote receipts in v1. The rows are small, while deleting an old
parent can make a later head unverifiable. A future retention design needs a
signed/validated checkpoint or continuity watermark before any adapter enables
deletion. Local mirrors may compact derived indexes, but must keep every object
needed to validate their retained heads.

## 8. Fetch, validation, and local reconciliation

Remote data first enters separate local mirror tables. It never writes directly
into `renart_coverage` or `renart_latest_successful_writers`.

For each receipt:

1. decode the declared version strictly;
2. recompute and compare the receipt ID;
3. require the configured project UUID and an exact target identity;
4. validate completion, fingerprint, coverage, and parent invariants;
5. require every physical input receipt or an explicit external-source marker;
6. evaluate the writer trust policy;
7. insert idempotently into the local mirror; and
8. derive target heads from parent links.

A remote head can be promoted into ordinary freshness evidence only when all of
these are true:

- exactly one valid maximal head exists for the target;
- there is no local active/dirty claim and no unresolved remote divergence;
- project, asset/environment writer, target, fingerprint version, current
  fingerprint, variables hash, and requested interval match;
- the receipt's coverage semantics are supported and cover the request;
- any referenced parents needed to establish ordering are present; and
- every input receipt is trusted and satisfies the existing achieved-
  fingerprint/read-set rules, or the input is an explicit source boundary;
- the trust policy permits that producer.

The input edge is causal provenance, not a new “same completion ID” freshness
rule. If an upstream was rebuilt later with the same current fingerprint,
variables, target writer, and supported coverage, preserve Renart's existing
semantic-freshness behavior. Do not make a downstream stale merely because an
equivalent upstream completion has a newer receipt ID.

Promotion should create a provenance-bearing local fact (`origin = remote`,
`receipt_id = ...`) through the same store logic, not a second staleness path.
The UI can then explain “Observed from warehouse journal” and link to a receipt
summary.

If the target has multiple heads, mark it **Freshness ambiguous** and show the
two writers. Do not compare timestamps. A reconciliation operation must first
inspect the actual target and then append a receipt whose parents include every
divergent head. Reconciliation acknowledges the observed physical winner; it
must never pretend that both data states were merged.

## 9. Trust and integrity

A SHA-256 receipt ID detects corruption and conflicting reuse; it does not
authenticate the writer. There are three viable trust levels:

1. **Warehouse ACL trust** — accept receipts because only the configured role
   can insert. Lowest ceremony; suitable for a first opt-in implementation.
2. **Observed-only** — display foreign receipts but never grant freshness from
   them. Safest experimental rollout.
3. **Signed producers** — Ed25519-sign the canonical receipt and keep trusted
   public keys in a Git-tracked project policy. Strongest provenance, but adds
   key registration, rotation, revocation, and clone bootstrap UX.

Recommendation: ship the first adapter in observed-only mode, validate the
operational model, then allow explicit ACL-trusted promotion. Design the receipt
with an optional signature envelope from version one so signed producers can be
added without changing its content identity.

This is not a security boundary against a principal that can modify the target
table itself. Such a principal can make the data disagree with any receipt.
Native table versions, where available, provide useful negative evidence: if
the current version differs from the receipt's captured version, invalidate it.
They still must not make an otherwise untrusted receipt fresh.

## 10. Expected failure behavior

| Situation | Required result |
| --- | --- |
| journal unavailable during/after a run | local success remains valid; replication stays queued with bounded retry |
| exact receipt replay | idempotent no-op |
| same completion/receipt ID with different body | corruption/conflict; quarantine and alert |
| receipts arrive out of order | retain them; recompute heads when parents arrive |
| missing parent beyond retention/watermark | incomplete history; no freshness promotion |
| two offline writers replace one target | two heads; freshness ambiguous |
| external tool overwrites target | not generally detectable; native-version mismatch invalidates where supported |
| metadata schema is cloned or time-travelled | treat as a separate/forked observation until target identity and continuity are verified |
| project UUID reused accidentally | receipts can collide logically; detect multiple incompatible project roots and fail closed |
| connection name now resolves elsewhere | opaque connection-key mismatch pauses replication/fetch |
| unsupported warehouse/connector | feature unavailable for that connection; local freshness unchanged |

## 11. Provider assessment

| Family | Initial assessment |
| --- | --- |
| PostgreSQL | Best pilot: ordinary transactional DML, inexpensive indexed lookup, clear schema privileges. Co-commit still requires owning the materializer transaction. |
| Snowflake | Viable append/query adapter. Bootstrap DDL is independently committed, so managed setup and receipt writes are separate operations. |
| BigQuery | Viable but not with per-asset point DML. Batch/load receipts; permanent DDL cannot be inside the DML transaction and permissions/cost need explicit UX. |
| Databricks/Delta | Strong native per-table version evidence and idempotent transaction concepts. Journal integration still depends on the SQL endpoint/catalog and must not assume every target is Delta. |
| Trino | Connector-dependent write/transaction semantics; capability probe and allowlist required. |
| DuckDB file | Useful for contract tests but not distributed across machines unless users independently synchronize the file, which Renart must not assume. |
| object/file outputs | No warehouse journal on the output connection. A separately configured metadata connection would be a later extension. |
| remaining SQL families | Require explicit insert, idempotency, schema bootstrap, quoting, batching, and consistency tests before enablement. |

## 12. Alternatives considered

| Alternative | Why it is not the recommended authority |
| --- | --- |
| copy or share `.renart/state.db` | It also contains scheduler/deployment/run state, is not a multi-writer database, and exposes more local operational metadata than freshness exchange needs. Back up the file, but do not turn it into a distributed protocol. |
| one mandatory “home” warehouse | Easier to query, but becomes a control plane and single point of availability for otherwise independent output connections. An optional metadata connection can be a later convenience, not the only topology. |
| mutable table comments/tags on every output | Provider-specific, size-limited, often DDL-gated, and overwrite history instead of detecting concurrent heads. They can expose a receipt ID as a pointer only. |
| OpenLineage backend | Valuable for interoperable run lineage, but it does not implement Renart target generations, exact writer selection, or interval-coverage rules. Emit receipt IDs to it later rather than delegating freshness. |
| provider-native table history only | Excellent negative evidence for Delta/Iceberg and selected warehouses, but unavailable or differently retained elsewhere and unable to carry the full cross-warehouse causal graph. |
| object-storage object log | Plausible for file outputs and cheap append-only storage, but listing consistency, object ACLs, and lack of portable compare-and-swap need a separate adapter contract. It does not satisfy “inside each warehouse” by itself. |

## 13. Implementation phases

### Phase 0 — contract spike, no user-visible freshness change

- Define the receipt schema, canonical encoder, receipt ID, parent rules, and
  causal-input rules, and strict decoder in a standalone package.
- Add golden vectors and property tests across map ordering, time zones,
  duplicates, unknown fields, malformed parents, and future versions.
- Model two disconnected local stores and prove that divergence produces two
  heads rather than a timestamp winner.
- Decide the project-root receipt, retention/watermark contract, and initial
  trust mode before adding warehouse writes.

Exit: the receipt and merge rules can be reviewed independently of a provider.

### Phase 1 — local mirror and simulation

- Add mirror, quarantine, sync-watermark, and replication-outbox tables to
  SQLite.
- Enqueue canonical receipts atomically with successful local materialization
  facts.
- Implement head derivation and explanatory diagnostics without letting remote
  receipts affect `fresh` yet.
- Add `renart freshness journal status` diagnostics for pending, diverged,
  quarantined, and unsupported state.

Exit: every distributed failure mode is reproducible with two SQLite stores.

### Phase 2 — opt-in PostgreSQL adapter

- Add managed and pre-provisioned schema modes.
- Implement batched idempotent append, target-scoped fetch, schema-version
  negotiation, and retry/backoff.
- Require captured connection-key equality on every retry.
- Expose replication health without turning a completed run red.
- Test two independent Renart projects/processes against one PostgreSQL
  warehouse, including offline divergence and lost-local-state recovery.

Exit: remote receipts are durable and observable, but advisory only.

### Phase 3 — reviewed import and reconciliation

- Add a preview that explains which remote receipts would restore facts and
  why each excluded receipt was rejected.
- Promote only explicit ACL-trusted receipts through the existing materialized
  fact/coverage model with remote provenance.
- Add divergence reconciliation that positively inspects the target and appends
  a multi-parent receipt.
- Add backup/recovery documentation and a CLI path suitable for a headless
  scheduler handoff.

Exit: a fresh clone can recover supported freshness without copying
`state.db`, and conflicts remain fail-closed.

### Phase 4 — warehouse reach

- Add Snowflake with separate DDL bootstrap semantics.
- Add BigQuery with batching/load delivery and cost-conscious fetches.
- Add Databricks, capturing a native Delta version only when positively known.
- Add provider conformance tests; unsupported Trino connectors and remaining
  families stay unavailable rather than falling back to generic SQL.

### Phase 5 — optional signed trust

- Add per-installation signing keys in Renart's secret-provider system.
- Add a Git-tracked trusted-writer policy, rotation/revocation workflow, and
  signature status in reconciliation UI.
- Never auto-trust a key merely because its receipt exists in the warehouse.

### Separate future proposal — distributed execution leases

Do not extend the receipt journal into live locking incidentally. Cross-machine
write exclusion requires leases, fencing tokens, expiry/clock rules, crash
recovery, and provider-specific compare-and-swap guarantees. Keep the existing
local execution coordinator until that protocol has its own threat model and
conformance suite.

## 14. Test matrix

At minimum:

- canonical receipt compatibility and unknown-version quarantine;
- exact replay, conflicting replay, delayed parent, and missing-parent cases;
- A -> B -> A target writer generations;
- marker, union interval, replace interval, and full-refresh coverage;
- two simultaneous heads and explicit multi-parent reconciliation;
- a downstream receipt whose upstream was rebuilt in the same completion, an
  unchanged upstream from an earlier completion, and a declared external
  source boundary;
- missing, untrusted, and divergent upstream receipt graphs;
- local crash before and after replication-outbox commit;
- remote outage, permission loss, schema drift, and connection-key drift;
- bounded sync pagination, equal-time batches, overlap replays, and an unchanged
  mirror that emits no SSE churn;
- pruning with a continuity watermark so an incomplete history cannot look
  complete;
- external target replacement with and without a native version signal;
- warehouse clone/time-travel fork behavior;
- project/pipeline/asset rename and identity reuse;
- mixed local and remote provenance across a cross-warehouse pipeline; and
- one live provider test per supported adapter, in addition to SQL-shape unit
  tests.

## 15. Decisions to confirm before implementation

Recommendations are included so these do not need to block the investigation:

1. **Authority:** keep `.renart/state.db` authoritative; remote receipts are a
   replicated evidence source. **Recommended: yes.**
2. **Placement:** one journal in every explicitly enabled output connection,
   not one mandatory home warehouse. **Recommended: yes.**
3. **Default:** disabled until configured because it creates warehouse objects
   and costs queries/storage. **Recommended: yes.**
4. **First trust mode:** observed-only, followed by explicit ACL-trusted import.
   **Recommended: yes.**
5. **Conflicts:** expose divergent heads and require positive reconciliation;
   never newest-timestamp-wins. **Recommended: yes.**
6. **Scope:** successful physical-output receipts and coverage only; do not
   replicate schedules, logs, quality errors, or full run history.
   **Recommended: yes.**
7. **First provider:** PostgreSQL before Snowflake/BigQuery/Databricks.
   **Recommended: yes.**

The plan should remain a proposal until these decisions and the version-one
receipt contract are accepted.
