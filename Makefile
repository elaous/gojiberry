# gojiberry / xango dev workflow.
#
# `make check` is the full local CI: regenerate every *.xg.go,
# gofmt/vet/test the root module for both native and GOOS=js
# GOARCH=wasm (domrt and friends are js/wasm-only), and run the
# vscode-xango grammar suite against every *.xg file in the repo.
.PHONY: check
check: generate generate-check docs docs-check fmt-check vet test wasm-vet lint wasm-lint modules lint-modules grammar-test-setup grammar-test

.PHONY: piper-runtime-test
# The reference Piper runtime (examples/piper-runtime): the parts that need no model. With PIPER_RUNTIME_MODELS and onnxruntime-web
# installed it runs the real models too (see that folder's README). Not part of check: it needs node.
piper-runtime-test:
	node --test examples/piper-runtime/piper-runtime.test.js

.PHONY: generate
generate:
	go generate ./...

# generate-check fails when `generate` changed a tracked file: a generated file was committed stale
# (a .css or .xg changed and its .xg.go was not regenerated).
.PHONY: generate-check
generate-check:
	@out="$$(git status --porcelain -- '*.xg.go')"; \
	if [ -n "$$out" ]; then \
		echo "generated files are stale (run make generate and commit):"; echo "$$out"; exit 1; \
	fi

# docs-check fails when regenerating the API dumps (`make docs`) changed a tracked docs/*.txt: a dump
# was committed stale, so it no longer shows the real public API. Run `make docs` and commit.
.PHONY: docs-check
docs-check:
	@out="$$(git status --porcelain -- 'docs/*.txt')"; \
	if [ -n "$$out" ]; then \
		echo "API dumps are stale (run make docs and commit):"; echo "$$out"; exit 1; \
	fi

.PHONY: fmt-check
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt would reformat:"; echo "$$out"; exit 1; \
	fi

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: vet
vet:
	go vet ./...

# The showcase's browser tests alone take about 450s on a quiet machine, so the default 10 minute
# per-package limit is too close (a full run that shared the machine with other work hit it).
.PHONY: test
test:
	go test -timeout 75m ./...  # the showcase browser tests alone take about 35 minutes

# a11y-test is the accessibility gate: axe-core over the page in every state a person meets it (the
# document, each tab, each menu, the find bar, the key directory), and the keyboard and reading-copy
# tests. It needs a Chromium and fails without one rather than skipping (GOJIBERRY_REQUIRE_BROWSER).
# A new axe violation fails it; the reviewed exceptions are listed, each with a roadmap entry, in
# examples/canvas-editor-showcase/e2e_axe_test.go (axeDisabled). Run it before a release, with the
# manual screen-reader script: docs/accessibility-testing.md.
.PHONY: a11y-test
a11y-test:
	GOJIBERRY_REQUIRE_BROWSER=1 go test -count=1 -run 'Axe|Accessib|ScreenReader|ReadingCopy|Keyboard|ArrowKeys|DialogFocus|DisplayPreferences' ./examples/canvas-editor-showcase/

# e2e-test is the whole browser suite of the showcase (every end-to-end test, about 25 minutes). It needs Chrome or
# Chromium and fails, rather than skips, without one. Run it at the full run every ten merges, or from the hosted
# workflow ("e2e"). It is not part of `make test`'s default time budget on purpose. With GOJIBERRY_E2E_JSON=file the
# output is `go test -json` written to that file (the nightly run reads it: .github/scripts/e2e-report.py).
.PHONY: e2e-test
e2e-test:
	@if [ -n "$$GOJIBERRY_E2E_JSON" ]; then \
		GOJIBERRY_REQUIRE_BROWSER=1 go test -count=1 -timeout 75m -json ./examples/canvas-editor-showcase/ > "$$GOJIBERRY_E2E_JSON"; \
	else \
		GOJIBERRY_REQUIRE_BROWSER=1 go test -count=1 -timeout 75m ./examples/canvas-editor-showcase/; \
	fi

# pdfa11y-test checks the tagged PDF the exporter writes with pdfa11y (github.com/speedata/pdfa11y, a Go
# checker for the machine-checkable part of PDF/UA). It installs the tool into .pdfa11y/ (ignored by git) and
# makes the test fail, not skip, if it is missing. veraPDF remains the reference validator (roadmap 63).
.PHONY: pdfa11y-test
pdfa11y-test:
	GOBIN=$(CURDIR)/.pdfa11y go install github.com/speedata/pdfa11y/cmd/pdfa11y@v0.0.15
	PATH="$(CURDIR)/.pdfa11y:$$PATH" GOJIBERRY_REQUIRE_PDFA11Y=1 go test -count=1 -run Pdfa11y ./export

# ci-local runs the CI job's make steps on this machine (no hosted runner needed); see
# .github/scripts/local-ci.sh. `make ci-local STEPS=a11y-test` runs only that step.
.PHONY: ci-local
ci-local:
	.github/scripts/local-ci.sh $(STEPS)

# mail-test runs the mail package's SMTP tests against real Mailpit servers (STARTTLS with sign-in, and
# plain on loopback), from the axllent/mailpit image when Docker is running, else the mailpit binary,
# else a build of it; see .github/scripts/mailpit-test.sh. It fails on a skipped test.
.PHONY: mail-test
mail-test:
	.github/scripts/mailpit-test.sh

.PHONY: wasm-vet
wasm-vet:
	GOOS=js GOARCH=wasm go build ./...
	GOOS=js GOARCH=wasm go vet ./...

# NESTED_MODULES is every directory under the root with its own go.mod; modules and
# lint-modules both walk it, so the list is kept in one place.
NESTED_MODULES := $(shell git ls-files '*/go.mod' | xargs -n1 dirname)
# tools holds the linter's pin and no Go code of its own: tidy-checked by `modules`, not linted.
LINT_MODULES := $(filter-out tools,$(NESTED_MODULES))

# modules checks every nested Go module (each directory under the root with its own
# go.mod): go.mod and go.sum are tidy, and it vets and tests. The Gio ones need
# system libraries (wayland, xkbcommon) for a native build, so they are vetted as
# GOOS=js GOARCH=wasm, which still type-checks everything but the window glue.
.PHONY: modules
modules:
	@set -e; for mod in $(NESTED_MODULES); do \
		echo "== $$mod"; \
		(cd $$mod && go mod tidy -diff && \
		case "$$mod" in tools) ;; *gio*) GOOS=js GOARCH=wasm go vet ./... ;; *) go vet ./... && go test ./... ;; esac); \
	done

# lint/wasm-lint run golangci-lint (pinned as a tool dependency of its
# own module, tools/go.mod, so its large dependency tree stays out of
# the library's module graph) the same "native, then GOOS=js GOARCH=wasm" way vet/wasm-vet
# already do -- see .golangci.yml's own doc comment for why one pass
# alone can't see both (domrt and friends are js/wasm-only, invisible
# to a plain native-build type-check). The wasm pass needs a host-
# native golangci-lint binary with GOOS/GOARCH set in its own
# environment, not `go tool` directly -- `go tool` would try to
# cross-compile the linter itself into wasm and then fail to execute
# it (wasm isn't directly runnable on the host OS). The binary is
# built from tools/ and run from the repository root.
LINT_PKG = github.com/golangci/golangci-lint/v2/cmd/golangci-lint

.PHONY: lint
lint:
	@bin="$$(mktemp -d)/golangci-lint"; trap 'rm -rf "$$(dirname "$$bin")"' EXIT; \
	(cd tools && go build -o "$$bin" $(LINT_PKG)) && "$$bin" run ./...

.PHONY: wasm-lint
wasm-lint:
	@bin="$$(mktemp -d)/golangci-lint"; trap 'rm -rf "$$(dirname "$$bin")"' EXIT; \
	(cd tools && go build -o "$$bin" $(LINT_PKG)) && GOOS=js GOARCH=wasm "$$bin" run ./...

# lint-modules runs the same pinned golangci-lint (built once from tools/go.mod,
# as lint and wasm-lint do) inside each nested module, since golangci-lint does not cross
# module boundaries and `lint`/`wasm-lint` therefore never see them. The binary is called by
# absolute path from inside each module directory so that module's own go.mod is used, with
# the root .golangci.yml via an absolute --config. The Gio modules are linted as
# GOOS=js GOARCH=wasm, mirroring `modules`. The examples are linted like the library
# (the root config has no path exclusions): the root-module ones by lint/wasm-lint, the
# nested ones (examples/*/go.mod) here.
.PHONY: lint-modules
lint-modules:
	@set -e; bin="$$(mktemp -d)/golangci-lint"; trap 'rm -rf "$$(dirname "$$bin")"' EXIT; \
	(cd tools && go build -o "$$bin" $(LINT_PKG)); \
	for mod in $(LINT_MODULES); do \
		echo "== $$mod"; \
		(cd $$mod && case "$$mod" in *gio*) GOOS=js GOARCH=wasm "$$bin" run --config "$(CURDIR)/.golangci.yml" ./... ;; *) "$$bin" run --config "$(CURDIR)/.golangci.yml" ./... ;; esac); \
	done

# coverage runs the native (non-wasm) test suite with a coverage
# profile and prints the per-function summary -- js/wasm-only packages
# (domrt and friends) have no native test suite to profile here, so
# they're not part of this number. Scoped to packages that actually
# have a _test.go file of their own (go list's own TestGoFiles/
# XTestGoFiles fields), excluding every cmd/* package (each a thin
# main() wrapper with no tests of its own, exercised indirectly through
# the library package it wraps): `go test -coverprofile` against a
# zero-test package still has to emit an (empty) counter record for it,
# which needs `go tool covdata` to merge -- a real, reproducible gap in
# this project's own toolchain setup (go.mod's "go 1.26.0" directive
# triggers a supplementary auto-fetched toolchain under GOTOOLCHAIN=auto
# whose own internal covdata invocation doesn't resolve the same way
# `go tool covdata` run directly does), not something -coverprofile
# itself should ever need for a package with nothing to cover anyway.
.PHONY: coverage
coverage:
	go test -timeout 75m -coverprofile=coverage.out $$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)
	go tool cover -func=coverage.out

# coverage-html turns the same profile into a browsable, per-line
# annotated HTML report -- open coverage.out.html to see exactly which
# lines a package's tests do and don't exercise.
.PHONY: coverage-html
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.out.html
	@echo "wrote coverage.out.html"

# coverage-badge rewrites README.md's own coverage badge line to a
# static img.shields.io URL carrying that same coverage.out profile's
# percentage -- CI-computed on every push to main (see ci.yml) so the
# badge never silently drifts from the number `make coverage` itself
# would print, but rendered by img.shields.io (works unauthenticated
# regardless of this repo's own visibility) rather than a self-hosted
# SVG served via raw.githubusercontent.com, which 404s for anyone
# without a live, signed-in github.com session on a private repo like
# this one -- see gen-coverage-badge.sh's own doc comment.
.PHONY: coverage-badge
coverage-badge: coverage
	.github/scripts/gen-coverage-badge.sh coverage.out README.md

# wasm-size builds the showcase wasm and fails when its raw or gzip size is over the budget in
# docs/wasm-budget.txt (roadmap 82). It is not part of `make check` (a build of the showcase on
# its own, a few seconds); CI runs it as its own step.
.PHONY: wasm-size
wasm-size:
	.github/scripts/wasm-size.sh examples/canvas-editor-showcase docs/wasm-budget.txt

# showcase-dist builds the showcase for deployment into dist/showcase (override with SHOWCASE_DIST=...):
# main.wasm, files.wasm (the file formats, loaded when first used), files-worker.js, index.html and wasm_exec.js, plus a precompressed .gz of each (and a .br when the brotli
# tool is installed) written by cmd/wasmdist (roadmap 82). Upload that directory; docs/deploy.md has the
# server settings (types, gzip_static or precompressed, cache headers). It needs no credentials.
SHOWCASE_DIST ?= dist/showcase
.PHONY: showcase-dist
showcase-dist:
	rm -rf $(SHOWCASE_DIST)
	mkdir -p $(SHOWCASE_DIST)
	cd examples/canvas-editor-showcase && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o $(abspath $(SHOWCASE_DIST))/main.wasm .
	cd examples/canvas-editor-showcase && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o $(abspath $(SHOWCASE_DIST))/files.wasm ./files
	cp examples/canvas-editor-showcase/index.html examples/canvas-editor-showcase/files-worker.js $(SHOWCASE_DIST)/
	cp examples/canvas-editor-showcase/static/* $(SHOWCASE_DIST)/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(SHOWCASE_DIST)/wasm_exec.js
	go run ./cmd/wasmdist $(SHOWCASE_DIST)

# feature-shots makes the pictures of docs/features.md (one screenshot of the showcase per feature, WebP, about
# 700 KB in all) in docs/features/img, by driving the showcase in headless Chromium. Run it when the editor looks
# different and commit the pictures that changed; it is not part of `make check`.
.PHONY: feature-shots
feature-shots:
	rm -rf docs/features/img
	GOJIBERRY_FEATURE_SHOTS=$(abspath docs/features/img) GOJIBERRY_REQUIRE_BROWSER=1 go test -count=1 -run TestFeatureShots ./examples/canvas-editor-showcase
	GOJIBERRY_FEATURE_SHOTS=$(abspath docs/features/img) GOJIBERRY_REQUIRE_BROWSER=1 go test -count=1 -run TestEndToEnd ./examples/canvas-table-demo

# bench runs every Go benchmark in the module: buffer's own
# GapBuffer-vs-naive-slice benchmarks (fast, native, no extra setup)
# and examples/domrt-bench's own patch/patchChildren benchmarks (drives
# a real, wasm-built page through a real headless Chromium via
# chromedp (internal/e2etest), skipping itself with b.Skip -- not failing --
# if no Chromium is available; see that example's own
# bench_test.go and Makefile for the fuller picture). -benchtime=1x
# matches examples/domrt-bench's own Makefile: each "iteration" already
# represents a full weighted operation count measured browser-side, not
# something to run for a full second of wall-clock repeats.
.PHONY: bench
bench:
	go test -bench=. -benchmem -benchtime=1x -run=^$$ ./...

# perf is bench's profiling counterpart: same benchmarks, but with a
# CPU and memory profile captured per package (Go's own -cpuprofile/
# -memprofile), for tracking *where* time/allocations actually go
# rather than just how long a benchmark took. -cpuprofile/-memprofile
# only work against a single package per go test invocation (multiple
# packages: "cannot use -cpuprofile flag with multiple packages"), so
# this discovers every package with at least one Benchmark (via `go
# test -list`) and profiles each into its own pair of files under
# $(PERF_DIR), rather than hardcoding the package list by hand.
PERF_DIR := perf
.PHONY: perf
perf:
	@mkdir -p $(PERF_DIR)
	@for pkg in $$(go list ./... | xargs -I{} sh -c 'go test -list "Benchmark" {} 2>/dev/null | grep -q Benchmark && echo {}'); do \
		name=$$(echo $$pkg | tr '/' '_'); \
		echo "profiling $$pkg -> $(PERF_DIR)/$$name.{cpu,mem}.out"; \
		go test -bench=. -benchmem -benchtime=1x -run=^$$ \
			-cpuprofile=$(PERF_DIR)/$$name.cpu.out \
			-memprofile=$(PERF_DIR)/$$name.mem.out \
			$$pkg || exit 1; \
	done
	@echo "profiles written to $(PERF_DIR)/ -- inspect with e.g. go tool pprof -top $(PERF_DIR)/<name>.cpu.out"

.PHONY: perf-clean
perf-clean:
	rm -rf $(PERF_DIR)

# grammar-test-setup only needs to run once (or after upgrading
# editors/vscode-xango/test's dependencies) -- it downloads VS Code's own
# Go/HTML/CSS/SQL/JSON grammars, which aren't redistributed here.
.PHONY: grammar-test-setup
grammar-test-setup:
	cd editors/vscode-xango/test && npm install \
		&& npm run fetch-go-grammar \
		&& npm run fetch-embedded-grammars

.PHONY: grammar-test
grammar-test:
	cd editors/vscode-xango/test && npm test

# vscode-package builds editors/vscode-xango's own installable .vsix
# (Extensions view -> "..." -> Install from VSIX...) via @vscode/vsce,
# fetched ad hoc through npx rather than tracked as a package.json
# devDependency -- vscode-xango's own package.json is a plain extension
# manifest, not an npm project with its own install step, the same
# reasoning grammar-test's own separate test/package.json already
# keeps the grammar suite's dependencies out of it.
# --allow-missing-repository: vsce's own default warns/fails without a
# package.json "repository" field, meant for a marketplace listing
# this extension has never had (it ships from this monorepo, not its
# own standalone repo) -- suppresses that warning rather than adding a
# repository field that would point nowhere real.
# .vscodeignore (editors/vscode-xango's own) keeps test/ -- its own
# node_modules/fetched grammars included, ~260KB of real weight --
# out of the packaged .vsix; without it vsce bundles the entire
# directory, tests and all, into what a user installs.
.PHONY: vscode-package
vscode-package:
	cd editors/vscode-xango && npx --yes @vscode/vsce package --allow-missing-repository

# docs regenerates the plain-text `go doc -all` dump for every public
# package under docs/ -- see docs/README.md for the exact commands.
.PHONY: docs
docs:
	go doc -all ./xango > docs/xango.txt
	go doc -all ./components > docs/components.txt
	go doc -all ./devreload > docs/devreload.txt
	go doc -all ./devwatch > docs/devwatch.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt > docs/domrt.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/browser > docs/domrt-browser.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/html > docs/domrt-html.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/svg > docs/domrt-svg.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/sw > docs/domrt-sw.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/worker > docs/domrt-worker.txt

# Runs the whole stack (docker-compose.yml). .env.local, when it exists, supplies values the shell does not
# set; the shell wins over the file. See docs/configuration.md.
.PHONY: stack-up stack-down
stack-up:
	docker compose $(if $(wildcard .env.local),--env-file .env.local) up --build
stack-down:
	docker compose down

# Ollama for the grammar check (package grammar talks to an Ollama-compatible server; the showcase's Language tab takes
# its address and model). None of this is part of `make check`: the install runs the script ollama.com publishes, as
# root where the script needs it, so it is a target you run on purpose.
#   make ollama-install           install Ollama (https://ollama.com/install.sh)
#   make ollama-pull              download the model OLLAMA_MODEL (default below)
#   make ollama-serve             start the server on OLLAMA_HOST (default 127.0.0.1:11434) in the foreground
#   make ollama-check             ask the running server one grammar question through package grammar
OLLAMA_MODEL ?= llama3.2:3b
OLLAMA_HOST ?= 127.0.0.1:11434
.PHONY: ollama-install ollama-pull ollama-serve ollama-check
ollama-install:
	curl -fsSL https://ollama.com/install.sh | sh
ollama-pull:
	OLLAMA_HOST=$(OLLAMA_HOST) ollama pull $(OLLAMA_MODEL)
ollama-serve:
	OLLAMA_HOST=$(OLLAMA_HOST) ollama serve
ollama-check:
	GOJIBERRY_OLLAMA_URL=http://$(OLLAMA_HOST) GOJIBERRY_OLLAMA_MODEL=$(OLLAMA_MODEL) go test -count=1 -run TestOllamaLive -v ./grammar
