# resource-imports-demo dev workflow: prints a page rendered from a
# JSON-decoded config and a CSS file imported with `import _ "<module path>/style.css"` to stdout,
# no WASM step.
.PHONY: run
run: generate
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/goxc examples/resource-imports-demo

.PHONY: test
test: generate
	cd ../.. && go test ./examples/resource-imports-demo/...
