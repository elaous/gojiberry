# wasm-components-demo dev workflow. Run `make run` for the full path
# from a clean checkout: regenerates .go from .xg, builds the WASM
# client, stages the components package's CSS alongside it, and serves
# it -- then open http://localhost:8080/.
.PHONY: run
run: generate wasm
	$(MAKE) serve

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/wasm-components-demo

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o main.wasm .
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .
	cp ../../components/*.css .

.PHONY: serve
serve:
	@echo "open http://localhost:8080/"
	python3 -m http.server 8080

.PHONY: screenshot
screenshot: generate wasm
	@python3 -m http.server 8080 >/tmp/wasm-components-demo-server.log 2>&1 & echo $$! > /tmp/wasm-components-demo-server.pid
	@timeout 10 bash -c 'until curl -sf http://localhost:8080/index.html >/dev/null; do sleep 0.5; done'
	NODE_PATH="$$(npm root -g)" node screenshot.js
	@kill "$$(cat /tmp/wasm-components-demo-server.pid)" 2>/dev/null || true
	@echo "wrote screenshot.png"

.PHONY: clean
clean:
	rm -f main.wasm wasm_exec.js *.css screenshot.png
