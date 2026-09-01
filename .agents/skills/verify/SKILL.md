---
name: verify
description: Build, launch, and drive the renart web app against a scratch workspace to verify UI/API changes end-to-end.
---

# Verifying renart changes end-to-end

## Build & launch

```bash
go build -o /tmp/renart-test .                # backend (embeds web/dist via webui.DistFS)
```

Scratch workspace: a dir with a `.bruin.yml` (environments + connections) — **must be a git repo** (`git init && git commit`), the server refuses to start otherwise. Optional `.renart/environments.yml` for policies.

```bash
/tmp/renart-test web --host 127.0.0.1 --port 8178 --watch-mode poll --no-open <workspace>
```

- API is origin-guarded: curl needs `-H "Origin: http://127.0.0.1:<port>"`.
- Port 8123 is often taken by an unrelated service on this machine — pick another.

## Frontend

Two options:

1. **Dev server (fast, current source):**
   `cd web && PROXY_TARGET=http://127.0.0.1:8178 ./node_modules/.bin/vite --host 127.0.0.1 --port 5199`
   (`/api` is proxied; drive `http://127.0.0.1:5199`).
2. **Production bundle (what users run, catches minified-React errors):**
   `cd web && corepack pnpm build`, then either pass `--static-dir web/dist` or rebuild the Go binary (it embeds `web/dist`).

`npx` is not on PATH; use `./node_modules/.bin/{tsc,eslint,vite}` or `corepack pnpm`.

## Drive with Playwright

No global playwright; import from the repo's install in a plain node script:

```js
import { chromium } from "/home/lukas/git/renart/web/node_modules/@playwright/test/index.mjs";
```

Attach `page.on("pageerror", ...)` — React update-loop bugs (error #185) only show in the minified bundle as `Minified React error #185`. To widen the config-loading race window, `page.route("**/api/config", ...)` with a delay.

Verify file side effects directly: `.bruin.yml`, `.renart/environments.yml`, `.renart/project.yml` in the scratch workspace.

## Gotchas

- `pkill -f "vite --host..."` matches your own shell's command line — use a `[v]ite` bracket pattern.
- The live e2e suite (`web/playwright.live.config.ts`) spawns one server per test; `tests/e2e/live-app-fixture.ts` is the reference for server flags.
- New API DTOs must flow through `web/scripts/generate-api-types.mjs` (run `node scripts/generate-api-types.mjs` from `web/`).
