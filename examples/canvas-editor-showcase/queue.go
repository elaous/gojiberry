//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/crdt"
)

// localQueue keeps the changes of a shared document that have not reached the others in this browser's
// localStorage, so closing the tab or reloading the page with changes waiting (offline, a hub that is
// down) does not lose them: the next visit sends them first. The key is per document and name, the
// stored text is what the person typed, and a browser that refuses to store it (private window, full)
// just does without.
type localQueue struct{ key string }

func newLocalQueue(doc, name string) localQueue {
	return localQueue{key: "showcase-queue:" + doc + ":" + name}
}

// Load implements client.Queue.
func (q localQueue) Load() (ops []crdt.Op) {
	defer func() {
		if recover() != nil {
			ops = nil
		}
	}()
	v := js.Global().Get("localStorage").Call("getItem", q.key)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	if json.Unmarshal([]byte(v.String()), &ops) != nil {
		return nil
	}
	return ops
}

// Save implements client.Queue.
func (q localQueue) Save(ops []crdt.Op) {
	defer func() { _ = recover() }() // storage unavailable or full
	ls := js.Global().Get("localStorage")
	if len(ops) == 0 {
		ls.Call("removeItem", q.key)
		return
	}
	if b, err := json.Marshal(ops); err == nil {
		ls.Call("setItem", q.key, string(b))
	}
}
