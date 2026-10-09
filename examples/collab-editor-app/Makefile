# collab-editor-app dev workflow. Run `make run` for the full path
# from a clean checkout: regenerates client/main.xg.go, builds the
# WASM client, stages its CSS, and starts the relay server -- then
# open http://localhost:8096/?role=owner in one tab and ?role=editor
# or ?role=viewer in another.
#
# Separate Go module (like ../canvas-app): replace-ing
# github.com/elaous/gojiberry with the local checkout so it can depend
# on github.com/coder/websocket without adding that to the root module.
.PHONY: run
run: generate wasm
	go run ./cmd/server

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/collab-editor-app

.PHONY: wasm
wasm:
	GOOS=js GOARCH=wasm go build -o static/main.wasm ./client/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" static/
	cp ../../components/base.css ../../components/button.css ../../components/textarea.css ../../components/collab.css static/

.PHONY: test
test: generate
	go test ./...

.PHONY: clean
clean:
	rm -f static/main.wasm static/wasm_exec.js static/base.css static/button.css static/textarea.css static/collab.css
