# Headerless CSV for Load and Seed

Status: proposed; no reader or file-format changes implemented. Audited 22 September 2026.

## Problem and current evidence

A CSV's first record must be loadable as data. Discovery, Inspect, execution and clipboard/upload authoring must agree about that choice.

- `internal/web/service/seed_sling.go` builds Seed source options in `resolveSlingSeedSource`. It currently supplies only `format`; it does not pass an authored header setting.
- `internal/web/service/asset_columns_seed.go` discovers columns through Sling's local-file discovery path. Its cache fingerprint follows source file contents, not a header policy.
- `internal/web/service/asset_columns_load.go` links upstream asset schemas, while `load_discover.go` owns Load discovery. Execution uses `load.go` and the Bruin/Sling adapter. Load currently persists flat source/destination/parallelism parameters, with no CSV header field in `loadRunParams`. Treat these as one reader-contract change, not merely a checkbox.
- `web/lib/seed-clipboard.ts` preserves CSV records and converts TSV delimiters. It should keep preserving records, including the first one. `semantic-parameters-editor.tsx` owns the Seed file authoring UI.

## Recommended contract

Expose **First row contains column names** for CSV sources, with the existing behavior as the default for existing files. An explicit false means every record is data. Do not silently guess from a numeric-looking first row, or modify the uploaded file to insert a synthetic header.

Before naming a persisted parameter, check Bruin's accepted Seed/Load metadata and Sling's native `header` option on the versions Renart ships. Prefer a compatible existing parameter; if Seed cannot express it compatibly, document the Renart extension and make CLI behavior explicit. Use a boolean with an absent/default state, not a second competing `header_mode` format.

The inspected Sling reader (`core/dbio/iop/csv.go`) supports `NoHeader` and generates names such as `col_001`; if it cannot detect the record width it requests `fields_per_rec`. Verify those details against the binary Renart ships. Headerless discovery returns stable positional names from that actual reader. Show those names in the column card and allow explicit source-to-output mapping. Do not guess that another reader uses the same `column_1` convention: test the real output first. Reordering or removing a positional column must remain an explicit mapping operation.

## Implementation sequence

1. Add one typed CSV reader-options resolver used by Seed and Load adapters. Validate format applicability and convert it to Sling options in one place.
2. Thread those options through discovery, preview and execution. Include normalized options in the discovery cache fingerprint; changing the checkbox must invalidate inferred columns. Preserve metadata edits separately from inferred source columns.
3. Add the control to local-file/URL upload and Load source settings. Preview the first few records with generated column names so the user can see whether the first record is being consumed.
4. Preserve clipboard content and header choice independently. Keep TSV conversion lossless and make JSON/Parquet unaffected.
5. Verify native CLI parity before describing it in user docs. Add UI-first documentation only once the full path works.

## Acceptance

Use the same fixtures for discovery, Inspect and materialization: numeric first row, string first row, one record, empty file, quoted commas/newlines, UTF-8 BOM, duplicate/empty header names, ragged records, and explicit source-column renames. Both header settings must produce identical columns and row counts across those paths. Confirm changing the option without changing the file refreshes inference. Cover a local DuckDB destination plus a stubbed remote execution contract; remote credentials are not needed for initial coverage.

Estimate: 2–3 focused implementation days, including adapter/version checks and end-to-end evidence. This is larger than a safe UI-only fix.
