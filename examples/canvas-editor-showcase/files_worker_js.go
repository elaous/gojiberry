//go:build js && wasm

package main

import (
	"errors"
	"sync"
	"syscall/js"
	"time"
)

// The heavy file formats (Word, OpenDocument, PDF, RTF, EPUB and HTML in and out, and the fidelity reports) are
// done by a second wasm, files.wasm, in a web worker that is started the first time one is needed (see
// ./files). That keeps their code out of the program every visitor downloads. A request blocks until the
// worker answers, so callFiles must be called from a goroutine of its own, never from a handler that
// JavaScript invoked.

var filesWorker struct {
	mu      sync.Mutex
	w       js.Value
	started bool
	next    int
	pending map[int]chan filesReply
	onMsg   js.Func
	onErr   js.Func
}

type filesReply struct {
	data []byte
	err  error
}

var errFilesTimeout = errors.New("the file worker did not answer")

// startFilesWorker starts the worker once.
func startFilesWorker() {
	filesWorker.mu.Lock()
	defer filesWorker.mu.Unlock()
	if filesWorker.started {
		return
	}
	filesWorker.started = true
	filesWorker.pending = map[int]chan filesReply{}
	filesWorker.w = js.Global().Get("Worker").New("files-worker.js")
	filesWorker.onMsg = js.FuncOf(func(_ js.Value, args []js.Value) any {
		d := args[0].Get("data")
		id := d.Get("id").Int()
		rep := filesReply{}
		if d.Get("ok").Bool() {
			if data := d.Get("data"); !data.IsUndefined() && !data.IsNull() {
				rep.data = make([]byte, data.Get("length").Int())
				js.CopyBytesToGo(rep.data, data)
			}
		} else {
			rep.err = errors.New(d.Get("err").String())
		}
		filesWorker.mu.Lock()
		ch := filesWorker.pending[id]
		delete(filesWorker.pending, id)
		filesWorker.mu.Unlock()
		if ch != nil {
			ch <- rep
		}
		return nil
	})
	filesWorker.onErr = js.FuncOf(func(_ js.Value, args []js.Value) any {
		// The worker could not start or died: every request waiting fails, and the next one starts it again.
		filesWorker.mu.Lock()
		pending := filesWorker.pending
		filesWorker.pending, filesWorker.started = map[int]chan filesReply{}, false
		filesWorker.mu.Unlock()
		filesWorker.w.Call("terminate")
		for _, ch := range pending {
			ch <- filesReply{err: errors.New("the file worker could not run")}
		}
		return nil
	})
	filesWorker.w.Set("onmessage", filesWorker.onMsg)
	filesWorker.w.Set("onerror", filesWorker.onErr)
}

// callFiles sends one request to the worker and waits for its answer (see package export/remote).
func callFiles(op, format string, in []byte) ([]byte, error) {
	startFilesWorker()
	filesWorker.mu.Lock()
	filesWorker.next++
	id := filesWorker.next
	ch := make(chan filesReply, 1)
	filesWorker.pending[id] = ch
	w := filesWorker.w
	filesWorker.mu.Unlock()

	arr := js.Global().Get("Uint8Array").New(len(in))
	js.CopyBytesToJS(arr, in)
	msg := js.Global().Get("Object").New()
	msg.Set("id", id)
	msg.Set("op", op)
	msg.Set("format", format)
	msg.Set("data", arr)
	w.Call("postMessage", msg, []any{arr.Get("buffer")})

	select {
	case rep := <-ch:
		return rep.data, rep.err
	case <-time.After(5 * time.Minute):
		filesWorker.mu.Lock()
		delete(filesWorker.pending, id)
		filesWorker.mu.Unlock()
		return nil, errFilesTimeout
	}
}
