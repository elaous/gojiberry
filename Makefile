# gojiberry / gox dev workflow.
#
# `make check` is the full local CI: regenerate every *.gox.go,
# gofmt/vet/test the root module for both native and GOOS=js
# GOARCH=wasm (domrt and friends are js/wasm-only), and run the
# vscode-gox grammar suite against every *.gox file in the repo.
.PHONY: check
check: generate fmt-check vet test wasm-vet grammar-test

.PHONY: generate
generate:
	go generate ./...

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

.PHONY: test
test:
	go test ./...

.PHONY: wasm-vet
wasm-vet:
	GOOS=js GOARCH=wasm go build ./...
	GOOS=js GOARCH=wasm go vet ./...

# coverage runs the native (non-wasm) test suite with a coverage
# profile and prints the per-function summary -- js/wasm-only packages
# (domrt and friends) have no native test suite to profile here, so
# they're not part of this number.
.PHONY: coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# coverage-html turns the same profile into a browsable, per-line
# annotated HTML report -- open coverage.out.html to see exactly which
# lines a package's tests do and don't exercise.
.PHONY: coverage-html
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.out.html
	@echo "wrote coverage.out.html"

# grammar-test-setup only needs to run once (or after upgrading
# editors/vscode-gox/test's dependencies) -- it downloads VS Code's own
# Go/HTML/CSS/SQL/JSON grammars, which aren't redistributed here.
.PHONY: grammar-test-setup
grammar-test-setup:
	cd editors/vscode-gox/test && npm install \
		&& npm run fetch-go-grammar \
		&& npm run fetch-embedded-grammars

.PHONY: grammar-test
grammar-test:
	cd editors/vscode-gox/test && npm test

# docs regenerates the plain-text `go doc -all` dump for every public
# package under docs/ -- see docs/README.md for the exact commands.
.PHONY: docs
docs:
	go doc -all ./gox > docs/gox.txt
	go doc -all ./components > docs/components.txt
	go doc -all ./devreload > docs/devreload.txt
	go doc -all ./devwatch > docs/devwatch.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt > docs/domrt.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/browser > docs/domrt-browser.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/html > docs/domrt-html.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/svg > docs/domrt-svg.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/sw > docs/domrt-sw.txt
	GOOS=js GOARCH=wasm go doc -all ./domrt/worker > docs/domrt-worker.txt
