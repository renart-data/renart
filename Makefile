GO ?= $(or $(shell command -v go 2>/dev/null),/usr/local/go/bin/go)
PNPM ?= corepack pnpm
DOCKER ?= docker
DOCS_IMAGE ?= renart-docs:local
RENART_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo local)
RENART_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
RENART_CACHE_HOME ?= $(if $(XDG_CACHE_HOME),$(XDG_CACHE_HOME),$(HOME)/.cache)
HOST_SQLPARSER_TARGET = $(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH)
BRUIN_SQLPARSER_STUB_LIB_DIR = $(RENART_CACHE_HOME)/renart/bruin-sqlparser-stub/$(HOST_SQLPARSER_TARGET)/release

.PHONY: help dev build test check release-check licenses licenses-check bruin-sqlparser-stub go-build go-test standalone-build web-install web-build web-typecheck web-test-live web-sync-polyglot-wasm docs-install docs-build docs-dev docs-preview vscode-install landing-media docs-media cli-recordings docs-docker docs-docker-run sync-install clean

help:
	@printf "Renart build targets\n\n"
	@printf "  make dev               Hot-reload dev servers (Go backend + Vite frontend)\n"
	@printf "  make build             Build web app, Go binary, and docs\n"
	@printf "  make check             Run Go tests plus web/docs builds\n"
	@printf "  make release-check     Run local alpha release checks\n"
	@printf "  make licenses          Regenerate third-party notices\n"
	@printf "  make licenses-check    Verify dependency licenses and notices\n"
	@printf "  make bruin-sqlparser-stub  Build Bruin's compatibility link shim\n"
	@printf "  make go-build          Build Renart CLI\n"
	@printf "  make standalone-build  Build Renart CLI plus the renart-gui desktop helper\n"
	@printf "  make go-test           Run Go tests\n"
	@printf "  make web-build         Build React app\n"
	@printf "  make web-sync-polyglot-wasm  Copy Polyglot SQL WASM from web dependency\n"
	@printf "  make web-typecheck     Typecheck React app\n"
	@printf "  make web-test-live     Run live Playwright tests\n"
	@printf "  make docs-build        Build Astro/Starlight docs\n"
	@printf "  make docs-dev          Start docs dev server\n"
	@printf "  make vscode-install    Install VS Code extension dependencies\n"
	@printf "  make landing-media     Regenerate landing media\n"
	@printf "  make docs-media        Regenerate docs screenshots\n"
	@printf "  make cli-recordings    Regenerate interactive CLI recordings\n"
	@printf "  make docs-docker       Build Caddy docs image\n"
	@printf "  make docs-docker-run   Serve docs image on http://127.0.0.1:8099 (legal env required)\n"

build: web-build go-build docs-build

# Hot-reload dev environment. Override the workspace with WORKSPACE=path.
dev:
	./scripts/dev.sh $(WORKSPACE)

check: go-test web-build docs-build

release-check: bruin-sqlparser-stub web-install docs-install vscode-install licenses-check
	$(GO) mod verify
	CGO_LDFLAGS="-L$(BRUIN_SQLPARSER_STUB_LIB_DIR) $(CGO_LDFLAGS)" $(GO) test -p=1 ./...
	CGO_LDFLAGS="-L$(BRUIN_SQLPARSER_STUB_LIB_DIR) $(CGO_LDFLAGS)" $(GO) vet -p=1 ./...
	$(PNPM) --dir web check
	$(PNPM) --dir web audit
	$(PNPM) --dir docs build
	$(PNPM) --dir docs audit
	$(PNPM) --dir extensions/vscode typecheck
	$(PNPM) --dir extensions/vscode audit

licenses:
	GO="$(GO)" node scripts/generate-third-party-notices.mjs

licenses-check:
	GO="$(GO)" ./scripts/check-third-party-licenses.sh

bruin-sqlparser-stub:
	./scripts/build_bruin_sqlparser_stub.sh "$(HOST_SQLPARSER_TARGET)"

test: go-test

go-build: web-build bruin-sqlparser-stub
	CGO_LDFLAGS="-L$(BRUIN_SQLPARSER_STUB_LIB_DIR) $(CGO_LDFLAGS)" $(GO) build .

# Builds the desktop helper used by `renart standalone`. Platform deps:
# Linux needs gtk3 + webkit2gtk dev packages, macOS needs the Xcode command
# line tools, Windows needs the WebView2 runtime (no build deps).
standalone-build: go-build
	GO="$(GO)" ./scripts/build_standalone_helper.sh "$(shell $(GO) env GOOS)" "$(shell $(GO) env GOARCH)" .

go-test: bruin-sqlparser-stub
	CGO_LDFLAGS="-L$(BRUIN_SQLPARSER_STUB_LIB_DIR) $(CGO_LDFLAGS)" $(GO) test -p=1 ./...

web-install:
	$(PNPM) --dir web install --frozen-lockfile

web-build:
	$(PNPM) --dir web build

web-sync-polyglot-wasm:
	$(PNPM) --dir web sync:polyglot-wasm

web-typecheck:
	$(PNPM) --dir web typecheck

web-test-live:
	$(PNPM) --dir web test:e2e:live

docs-install:
	$(PNPM) --dir docs install --frozen-lockfile

docs-build:
	$(PNPM) --dir docs build

docs-dev:
	$(PNPM) --dir docs dev

docs-preview:
	$(PNPM) --dir docs preview

vscode-install:
	$(PNPM) --dir extensions/vscode install --frozen-lockfile

landing-media:
	$(PNPM) --dir web landing:media

docs-media:
	$(PNPM) --dir web docs:media

cli-recordings: bruin-sqlparser-stub
	mkdir -p .tmp/docs-cli-recordings
	CGO_LDFLAGS="-L$(BRUIN_SQLPARSER_STUB_LIB_DIR) $(CGO_LDFLAGS)" $(GO) build -o .tmp/docs-cli-recordings/renart .
	RENART_DOCS_BINARY="$(CURDIR)/.tmp/docs-cli-recordings/renart" $(PNPM) --dir docs recordings:cli

sync-install:
	cp install.sh docs/public/install.sh

docs-docker:
	$(DOCKER) build -f Dockerfile.docs --build-arg RENART_VERSION=$(RENART_VERSION) --build-arg RENART_COMMIT=$(RENART_COMMIT) -t $(DOCS_IMAGE) .

docs-docker-run:
	$(DOCKER) run --rm -p 127.0.0.1:8099:80 \
		-e RENART_LEGAL_NAME \
		-e RENART_LEGAL_ADDRESS_LINE_1 \
		-e RENART_LEGAL_POSTAL_CODE \
		-e RENART_LEGAL_CITY \
		-e RENART_LEGAL_COUNTRY \
		-e RENART_LEGAL_EMAIL \
		-e RENART_ANALYTICS_RETENTION_DAYS \
		-e RENART_UMAMI_WEBSITE_ID \
		$(DOCS_IMAGE)

clean:
	rm -rf dist web/dist docs/dist docs/.astro
