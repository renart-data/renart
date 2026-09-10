# Notebook Data Browser drops — remaining work

Status: table, project-file and individual S3-file handoff implemented locally,
10 September 2026. This is not a release certification. The as-built contract is
in [notebook architecture](../architecture/notebooks.md#data-browser-source-insertion).

## Next source capabilities

1. **Prefix/pattern review.** Allow an S3 prefix to open a source-selection step
   with an explicit supported format and literal prefix/pattern. Preserve the
   selected insertion anchor and require review of the resulting normalized
   source. Do not turn a connection/bucket drop into an implicit whole-bucket
   import. Browser wildcard search results already support individual-file drops;
   the search expression itself must not become an executable selector.
2. **Connection drops.** Open a scoped source picker, retaining the intended
   insertion point. Never infer a relation, export or destination. Until then,
   users open the connection in the browser and choose an individual object.
3. **Additional transports.** GCS notebook sources exist, but the browser needs
   provider coverage before it can advertise GCS drops. SFTP stays browse-only
   for notebooks until a typed, bounded transfer adapter and capability tests
   exist. Do not depend on Ingestr or silently use JSON-row snapshots.

## Verification for each extension

- Exercise native desktop drag and the same keyboard/mobile placement flow.
- Assert no source execution or authored-file write before confirmation, and
  no transfer on confirmation. Run explicitly only in disposable fixtures.
- Cover changed/missing objects, stale notebook revisions and insertion anchors,
  scope changes, failed saves and cancelled reviews; preserve the previous good
  snapshot after execution failure/cancellation.
- Retain the save barrier, exact normalized change-set apply, existing full/sample
  budgets, normal SSE projection and durable block reveal. No parallel block store,
  save queue, transfer implementation or notebook renderer.

Fold each completed capability into architecture/user docs. Delete this plan
when these follow-ups are implemented or explicitly parked elsewhere.
