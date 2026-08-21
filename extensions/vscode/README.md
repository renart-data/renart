# Renart SQL LSP

VS Code extension for the Go-based Renart SQL language server.

The extension starts:

```sh
renart sql-lsp --workspace <workspace>
```

It is intended for dbt and Bruin pipeline repositories. SQL parsing and
validation run in-process, so the extension does not download a parser library.
