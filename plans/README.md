# Plans

Ephemeral design documents: proposals, evaluations, and implementation plans
for work that has **not shipped** (or only partially). The current state of
what *is* built lives in [`../architecture/`](../architecture/).

When a plan is implemented, fold the as-built reality (including deviations)
into the relevant `architecture/` doc and delete the plan — git history keeps
the original.

| Doc | Status |
| --- | --- |
| [security-and-build-vs-buy-audit.md](security-and-build-vs-buy-audit.md) | audit complete — reported security findings and phased remediation/library-adoption plan; critical and high actions open |
| [dbt-assets.md](dbt-assets.md) | evaluation — enabling renart intelligence on existing dbt projects |
| [materialization-reach.md](materialization-reach.md) | proposed reach — guided advanced SQL modes, coverage timeline, Python pre-run diagnostic |
| [remote-table-intellisense.md](remote-table-intellisense.md) | proposal — surface warehouse tables with no backing asset via the LSP |
| [python-asset-sdk.md](python-asset-sdk.md) | phases 1–2 + upstream refresh + PyPI publication implemented — credential-free `query()`, ingestr-free uploads, editor/notebook parity; policy and protocol reach items open |
| [python-cross-connection-policy.md](python-cross-connection-policy.md) | proposal — opt-in per-environment connection scopes for Python SDK queries |
| [questions.md](questions.md) | open questions for the maintainer |

Recently folded away (git history keeps them): `docs-alpha.md` and
`landing-page.md` → `architecture/docs.md`; `notebook-intellisense.md` →
`architecture/sql-lsp.md`; `ingestr-feature-flag.md`,
`project-settings-and-workspaces.md`, and `cli-v1.md` →
`architecture/backend.md` + `architecture/frontend.md`.
