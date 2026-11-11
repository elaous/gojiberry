//go:build js && wasm

package docstore

import (
	"context"
	"syscall/js"
)

// Lock implements Locker with the Web Locks API.
func (a *BrowserAdapter) Lock(ctx context.Context) (func(), error) {
	return a.lock(ctx, "gojiberry-docstore:"+a.prefix)
}

func (a *BrowserAdapter) lock(ctx context.Context, name string) (func(), error) {
	return webLock(ctx, name)
}

func webLock(ctx context.Context, name string) (func(), error) {
	locks := js.Global().Get("navigator").Get("locks")
	if !locks.Truthy() {
		return func() {}, nil
	}
	acquired, release := make(chan struct{}), make(chan struct{})
	failed := make(chan struct{}) // the request was refused (an opaque origin, a sandbox)
	var cb js.Func
	cb = js.FuncOf(func(js.Value, []js.Value) any {
		close(acquired)
		// held until the returned promise settles
		var executor js.Func
		executor = js.FuncOf(func(_ js.Value, p []js.Value) any {
			resolve := p[0]
			go func() {
				<-release
				resolve.Invoke()
				executor.Release()
				cb.Release()
			}()
			return nil
		})
		return js.Global().Get("Promise").New(executor)
	})
	var refused js.Func
	refused = js.FuncOf(func(js.Value, []js.Value) any {
		refused.Release()
		close(failed)
		return nil
	})
	var request js.Value
	func() {
		defer func() {
			if recover() != nil { // a synchronous throw
				request = js.Undefined()
			}
		}()
		request = locks.Call("request", name, cb)
	}()
	if !request.Truthy() {
		cb.Release()
		refused.Release()
		return func() {}, nil // no exclusion, as without the API
	}
	request.Call("catch", refused)
	select {
	case <-failed:
		cb.Release()
		return func() {}, nil
	case <-acquired:
	case <-ctx.Done():
		// still queued: give the lock back the moment it is granted
		go func() { <-acquired; close(release) }()
		return nil, ctx.Err()
	}
	return func() { close(release) }, nil
}

// RequestPersistence asks the browser not to evict storage.
func RequestPersistence() bool {
	storage := js.Global().Get("navigator").Get("storage")
	if !storage.Truthy() || !storage.Get("persist").Truthy() {
		return false
	}
	done := make(chan bool, 1)
	var then, fail js.Func
	release := func() { then.Release(); fail.Release() }
	then = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		done <- len(args) > 0 && args[0].Bool()
		return nil
	})
	fail = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		done <- false
		return nil
	})
	storage.Call("persist").Call("then", then, fail)
	return <-done
}

// Persisted says whether the browser granted persistence.
func Persisted() bool {
	storage := js.Global().Get("navigator").Get("storage")
	if !storage.Truthy() || !storage.Get("persisted").Truthy() {
		return false
	}
	done := make(chan bool, 1)
	var then, fail js.Func
	release := func() { then.Release(); fail.Release() }
	then = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		done <- len(args) > 0 && args[0].Bool()
		return nil
	})
	fail = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		done <- false
		return nil
	})
	storage.Call("persisted").Call("then", then, fail)
	return <-done
}
