//go:build js && wasm

package domrt

import (
	"errors"
	"syscall/js"
)

// Await blocks the calling goroutine until the JS Promise p settles,
// without blocking the rest of the WASM runtime: Go's js/wasm scheduler
// keeps servicing the JS event loop (and other goroutines) while this
// one waits on the channel. Shared by domrt/browser and domrt/sw so
// both bridge JS Promises the same way.
func Await(p js.Value) (js.Value, error) {
	result := make(chan js.Value, 1)
	fail := make(chan error, 1)

	var then, catch js.Func
	then = js.FuncOf(func(this js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0]
		}
		result <- v
		return nil
	})
	catch = js.FuncOf(func(this js.Value, args []js.Value) any {
		msg := "promise rejected"
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			msg = args[0].Call("toString").String()
		}
		fail <- errors.New(msg)
		return nil
	})
	defer then.Release()
	defer catch.Release()

	p.Call("then", then).Call("catch", catch)
	select {
	case v := <-result:
		return v, nil
	case err := <-fail:
		return js.Value{}, err
	}
}

// ToPromise returns a JS Promise that settles once work returns,
// running work on its own goroutine so the calling event handler can
// return the Promise immediately (as event.waitUntil/respondWith
// require) while the real work happens asynchronously.
func ToPromise(work func() (js.Value, error)) js.Value {
	executor := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			v, err := work()
			if err != nil {
				reject.Invoke(err.Error())
				return
			}
			resolve.Invoke(v)
		}()
		return nil
	})
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}
