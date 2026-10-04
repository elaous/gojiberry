# domrt-bench dev workflow. `make bench` is the actual point: builds
# the wasm harness and runs the benchmarks against it. `make run` is
# for poking at the page by hand (open http://localhost:8080/ and call
# runBench('attrs-only', 500) from the devtools console).
.PHONY: bench
bench: generate
	go test -bench=. -benchtime=1x .

.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/domrt-bench

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: clean
clean:
	rm -f main.wasm wasm_exec.js
