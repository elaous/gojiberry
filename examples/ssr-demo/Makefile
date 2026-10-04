# ssr-demo dev workflow: a pure server-side render, no WASM step.
.PHONY: run
run: generate
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/ssr-demo

.PHONY: test
test: generate
	cd ../.. && go test ./examples/ssr-demo/...
