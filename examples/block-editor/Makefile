# block-editor dev workflow: two replicas edit the same block-based
# invoice fully offline, sync their deltas via crdt.Doc, and render
# identical HTML from two decoupled components (Editor, Output). No
# WASM step -- this prints the two-replica sync scenario to stdout.
#
# Pair with `make syncserver` to see the same Ops cross a real HTTP
# boundary instead of an in-process Drain/Apply call.
.PHONY: run
run: generate
	go run .

.PHONY: generate
generate:
	cd ../.. && go run ./cmd/xgc examples/block-editor

.PHONY: test
test: generate
	cd ../.. && go test ./examples/block-editor/...

.PHONY: syncserver
syncserver:
	GOJIBERRY_SYNC_ADDR=:8080 GOJIBERRY_SYNC_REGION=br-datacenter-1 go run ../../cmd/syncserver
