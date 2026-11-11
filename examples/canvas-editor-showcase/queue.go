//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/crdt"
)

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
