# Shared connections and project-scoped credentials

Status: alternatives for review; no credential migration or broader secret access implemented. Audited 22 September 2026.

## Why the current behavior occurs

A parent `.bruin.yml` can define a connection visible from several launch roots. `internal/web/service/config.go` resolves secret bindings from `<workspaceRoot>/.renart/secrets.yml`. If credentials were saved while a subproject was open, that subproject owns the binding, even though the shared configuration lists the connection elsewhere.

`config_secret_restart_test.go` explicitly protects this behavior: reopening a different root must not guess at a sibling's secret binding. `config.go` already produces a missing-binding explanation, but a visible connection in the Data Browser still looks usable. Visibility and availability need separate treatment.

## Options

| Option | Behavior | Tradeoff |
| --- | --- | --- |
| A. Explicit availability | Keep current scopes. Show “Credentials needed in this project” on an unavailable connection, with Configure and the known owner/scope explanation. Avoid expanding a tree that can only fail. | Small, safe improvement; user still configures each intended project |
| B. Config-owner scope | Store bindings beside the owning shared config. Children inherit those bindings; explicitly defined child overrides win. Offer a reviewed migration from the old project binding. | Best fit for one repo with many subprojects; needs ownership, migration, rename and precedence tests |
| C. User-level shared connection profile | User explicitly makes a credential profile available across selected projects, with project-local references and revocation. | Useful across repositories; adds a durable global registry and a wider trust boundary |

**Recommendation:** ship A first, then B for shared parent configurations. Keep C as a later explicit “Share across projects” feature. Do not recursively scan sibling folders, try every stored password, or make all local secrets globally available.

## Prior art

- [DataGrip data sources](https://www.jetbrains.com/help/datagrip/managing-data-sources.html) makes project/global scope explicit, with actions to make a source global or move it into a project. That supports an intentional sharing operation rather than invisible credential discovery.
- [DBeaver preconfigured connections](https://dbeaver.com/docs/dbeaver/Admin-Manage-Connections/) keeps project connection definitions and credentials as distinct resources. Connection metadata alone is not proof that credentials are available.

These are design analogies, not drop-in storage formats. Renart must retain its local vault, Git-visible definitions and backend-only secret resolution.

## Implementation for A

Return a non-secret availability state and scope from the config/service DTO: available, missing binding, locked vault, or missing environment input. Do not probe the database on every render. Use SSE/config changes to refresh state. Data Browser, connection picker and run preflight should use the same explanation and repair route. Regenerate API types and test that passwords, secret handles and private paths do not leak into broad workspace responses.

## Migration for B

1. Canonicalize the config owner's path and define one precedence rule: child explicit binding, owner binding, configured external/env source. No ambiguous fallback between unrelated project IDs.
2. Show a concrete migration preview identifying the connection, old scope and proposed new scope without revealing the secret. Sharing changes access, so migration requires a deliberate user action.
3. Perform the vault/binding update atomically through the Go service; preserve recovery information for interrupted writes. Avoid overwriting a conflicting binding.
4. Test parent/child/sibling roots, two connections with the same name in different configs, multiple environments, symlinks, moved repos, copied worktrees, locked vault, restart and rollback. Preserve the existing no-implicit-sibling-access regression.

Estimate: A about 1 day; B 2–4 days including migration evidence. Decision needed: confirm config-owner inheritance as the intended long-term rule.
