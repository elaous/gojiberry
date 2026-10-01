//go:build js && wasm

package transfer

import "syscall/js"

// Download hands data to the user as a file named filename.
func Download(filename, mime string, data []byte) {
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	blob := js.Global().Get("Blob").New([]any{arr}, map[string]any{"type": mime})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", filename)
	a.Call("click")
	js.Global().Get("URL").Call("revokeObjectURL", url)
}

// PickFile opens the browser's file chooser (call it from a click) for
// files matching accept (see Registry.Accept) and calls done with the
// chosen file's name and bytes, on the event loop. Nothing is called if
// the user cancels.
func PickFile(accept string, done func(name string, data []byte, err error)) {
	in := js.Global().Get("document").Call("createElement", "input")
	in.Set("type", "file")
	in.Set("accept", accept)
	var onChange, onData js.Func
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := in.Get("files")
		if files.Length() == 0 {
			onChange.Release()
			return nil
		}
		file := files.Index(0)
		onData = js.FuncOf(func(_ js.Value, args []js.Value) any {
			buf := js.Global().Get("Uint8Array").New(args[0])
			data := make([]byte, buf.Length())
			js.CopyBytesToGo(data, buf)
			onChange.Release()
			onData.Release()
			done(file.Get("name").String(), data, nil)
			return nil
		})
		file.Call("arrayBuffer").Call("then", onData)
		return nil
	})
	in.Call("addEventListener", "change", onChange)
	in.Call("click")
}
