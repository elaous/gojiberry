# wasm-worker-demo dev workflow. Run `make run` for the full path from
# a clean checkout: regenerates .go from .xg, builds both the main
# page's WASM binary and the separate worker/ WASM binary it spawns
# onto a real background thread, and serves it -- then open
# http://localhost:8080/.
.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/wasm-worker-demo

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	(cd worker && GOOS=js GOARCH=wasm go build -o ../worker.wasm .)
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: clean
clean:
	rm -f main.wasm worker.wasm wasm_exec.js
