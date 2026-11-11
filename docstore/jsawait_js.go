//go:build js && wasm

package docstore

import (
	"context"
	"fmt"
	"syscall/js"
)

type jsError struct{ Name, Message string }

func (e *jsError) Error() string { return e.Name + ": " + e.Message }

func toJSError(v js.Value) error {
	if v.IsUndefined() || v.IsNull() {
		return &jsError{Name: "Error", Message: "rejected"}
	}
	name, msg := v.Get("name"), v.Get("message")
	e := &jsError{Name: "Error"}
	if name.Truthy() {
		e.Name = name.String()
	}
	if msg.Truthy() {
		e.Message = msg.String()
	} else {
		e.Message = fmt.Sprint(v)
	}
	return e
}

// not for a JS callback: it must not block
func await(ctx context.Context, p js.Value) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	ch := make(chan result, 1)
	var then, fail js.Func
	release := func() { then.Release(); fail.Release() }
	then = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		v := js.Undefined()
		if len(args) > 0 {
			v = args[0]
		}
		ch <- result{v: v}
		return nil
	})
	fail = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		v := js.Undefined()
		if len(args) > 0 {
			v = args[0]
		}
		ch <- result{err: toJSError(v)}
		return nil
	})
	p.Call("then", then, fail)
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return js.Undefined(), ctx.Err()
	}
}

func jsCall(v js.Value, method string, args ...any) (out js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(js.Error); ok {
				err = toJSError(e.Value)
			} else {
				err = fmt.Errorf("docstore: %v", r)
			}
		}
	}()
	return v.Call(method, args...), nil
}

func jsAwait(ctx context.Context, v js.Value, method string, args ...any) (js.Value, error) {
	p, err := jsCall(v, method, args...)
	if err != nil {
		return js.Undefined(), err
	}
	return await(ctx, p)
}

func jsBytes(b js.Value) []byte {
	arr := js.Global().Get("Uint8Array").New(b)
	out := make([]byte, arr.Get("length").Int())
	js.CopyBytesToGo(out, arr)
	return out
}

func jsUint8(data []byte) js.Value {
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	return arr
}
