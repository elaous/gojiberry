//go:build js && wasm

package main

import (
	"syscall/js"

	"github.com/elaous/gojiberry/export/remote/serve"
)

func main() {
	handle := js.FuncOf(func(_ js.Value, args []js.Value) any {
		m := args[0]
		id, op, format := m.Get("id").Int(), m.Get("op").String(), m.Get("format").String()
		in := make([]byte, m.Get("data").Get("length").Int())
		js.CopyBytesToGo(in, m.Get("data"))
		out, err := serve.Handle(op, format, in)
		reply := js.Global().Get("Object").New()
		reply.Set("id", id)
		if err != nil {
			reply.Set("ok", false)
			reply.Set("err", err.Error())
			js.Global().Call("postMessage", reply)
			return nil
		}
		arr := js.Global().Get("Uint8Array").New(len(out))
		js.CopyBytesToJS(arr, out)
		reply.Set("ok", true)
		reply.Set("data", arr)
		js.Global().Call("postMessage", reply, []any{arr.Get("buffer")})
		return nil
	})
	js.Global().Call("__filesStart", handle)
	select {}
}
