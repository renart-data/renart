# Renart

<p align="center">
  <a href="https://github.com/renart-data/renart/blob/main/LICENSE"><img alt="Apache License 2.0" src="https://img.shields.io/github/license/renart-data/renart" /></a>
  <a href="https://github.com/renart-data/renart/tags"><img alt="Latest version" src="https://img.shields.io/github/v/tag/renart-data/renart?label=version" /></a>
  <a href="https://github.com/renart-data/renart/actions/workflows/ci.yml"><img alt="CI status" src="https://github.com/renart-data/renart/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/renart-data/renart/actions/workflows/e2e-live.yml"><img alt="Live end-to-end test status" src="https://github.com/renart-data/renart/actions/workflows/e2e-live.yml/badge.svg" /></a>
</p>

**Bring a first-class developer experience to the entire data workflow.**

Renart is an open source data platform for moving, transforming and analyzing
data across your data stack. Build and run SQL and Python pipelines alongside
notebooks and dashboards in one visual workspace, with type checking,
scheduling and everything defined as code in Git.

Renart runs on your machine and connects to the databases, warehouses, files
and APIs you already use. Your authored definitions stay in your repository
as plain, reviewable files.

> [!IMPORTANT]
> **Renart is currently in public alpha.** The core build, inspect, run,
> schedule, notebook, type-checking, and freshness workflows are available, but
> expect rough edges and changes before the first stable release. Keep your
> pipelines in Git, evaluate Renart before relying on it for critical production
> scheduling, and please report issues on
> [GitHub](https://github.com/renart-data/renart/issues).

![Renart pipeline canvas and editor](docs/public/landing/hero-workspace.webp)

## Highlights

- Move data between databases, warehouses, files and object storage, and ingest
  data from HTTP APIs.
- See assets, dependencies, lineage, and staleness on a visual pipeline canvas.
- Get pipeline-aware SQL completion and type checking, plus Python diagnostics,
  completion, hover and go-to-definition while you edit.
- Inspect data safely before materializing an asset or building a pipeline.
- Combine warehouse, file, object-storage, and HTTP data in typed notebooks,
  then promote useful work into pipeline assets.
- Build version-controlled dashboards and reports with checked visualizations
  and interactive controls.
- Run and schedule pipelines per environment, with logs and history in the UI.
- Review every authored change as an ordinary Git diff.

## Install

```bash
curl -LsSf getrenart.com/install.sh | sh
```

Start Renart inside a Git repository:

```bash
renart
```

This opens the Renart workspace in a native window, with an automatic browser
fallback when the platform webview is unavailable. The matching native-window
helper is included in each release archive and installed by the one-line installer.

Release archives support Linux x86-64/ARM64 (glibc 2.31+), macOS
Intel/Apple silicon, and Windows x86-64. See the
[installation guide](https://getrenart.com/docs/installation/) for details.

## Documentation

- [Quickstart](https://getrenart.com/docs/quickstart/)
- [Full documentation](https://getrenart.com/docs/)
- [Connection credentials](https://getrenart.com/docs/connections-environments/managing-credentials/)
- [CLI reference](https://getrenart.com/docs/reference/cli/)

## License

Renart is licensed under the Apache License 2.0. See [`LICENSE`](LICENSE).
Licenses and required notices for bundled third-party software are collected in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

See [`CONTRIBUTING.md`](CONTRIBUTING.md) to contribute. Please report suspected
vulnerabilities privately as described in [`SECURITY.md`](SECURITY.md).
