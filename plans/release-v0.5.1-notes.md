# Renart v0.5.1 release notes draft

## Deployment review

- Review saved source changes and semantic SQL impact in one compact dialog.
- Compare inferred output types as well as SQL, including downstream changes
  caused by upstream schemas. Formatting-only changes stay distinct.
- New output columns have their own category and inline labels. Ordinary query
  changes use the text diff without warning underlines across the whole query.
- Mobile reviews use an inline diff; source-backed warnings point to the
  relevant SQL. Findings without a reliable source location remain in the row.

## Navigation and authoring

- Diagnostic links open the actual column definition, connection or data object
  in the existing UI. Links work after reload and in a new tab, while unrelated
  sidebar, editor and results-panel choices stay independent.
- Drag Data Browser tables onto a pipeline to review and create Source assets.
  Drop compatible destination connections after an asset to prepare a Load.
  Keyboard and touch users can use the same workflow without dragging.
- Ingestr creation is explicitly opt-in. Existing legacy assets and connections
  remain editable without enabling new Ingestr suggestions.
- Asset creation waits for canonical workspace ownership before navigation.

## Documentation

- Updated UI guides, deployment/scheduling explanations and screenshots.
- Added a verified SQL platform reference, including ClickHouse, StarRocks and
  Trino, with explicit materialization limits.

Renart remains in public alpha. Deployment saves a version; it does not execute
the pipeline. Evaluate changes against non-critical data first.

## Release checklist (not part of public notes)

- [ ] Integrated local release gates, production docs smoke and serial live E2E.
- [ ] Push `release/v0.5.1`; CI, live E2E and cross-platform snapshot all green.
- [ ] Tag the exact checked commit, validate packaged artifacts, publish draft.
- [x] Video artifacts and video-specific scripts excluded.
