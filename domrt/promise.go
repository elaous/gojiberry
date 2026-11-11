//go:build js && wasm

package domrt

import (
	"errors"
	"syscall/js"
)

// Await blocks the calling goroutine until the JS Promise settles.
func Await(p js.Value) (js.Value, error) {
	// a non-promise value is its own result, as await treats it
	if (p.Type() != js.TypeObject && p.Type() != js.TypeFunction) || p.Get("then").Type() != js.TypeFunction {
		return p, nil
	}
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
		fail <- errors.New(rejectionMessage(args))
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

// ToPromise returns a JS Promise that settles when work returns.
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

func rejectionMessage(args []js.Value) (msg string) {
	msg = "promise rejected"
	if len(args) == 0 || args[0].IsUndefined() || args[0].IsNull() {
		return msg
	}
	defer func() {
		if recover() != nil {
			msg = "promise rejected"
		}
	}()
	return args[0].Call("toString").String()
}
