//go:build js && wasm

package transfer

import (
	"errors"
	"syscall/js"
)

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
	// Not at once: some browsers cancel a download whose URL is revoked
	// before they have started it.
	var revoke js.Func
	revoke = js.FuncOf(func(this js.Value, args []js.Value) any {
		js.Global().Get("URL").Call("revokeObjectURL", url)
		revoke.Release()
		return nil
	})
	js.Global().Call("setTimeout", revoke, 10000)
}

// PickFile opens the browser's file chooser (call it from a click) for
// files matching accept (see Registry.Accept) and calls done with the
// chosen file's name and bytes, on the event loop. Nothing is called if
// the user cancels.
func PickFile(accept string, done func(name string, data []byte, err error)) {
	in := js.Global().Get("document").Call("createElement", "input")
	in.Set("type", "file")
	in.Set("accept", accept)
	var onChange, onCancel, onData, onFail js.Func
	releaseAll := func() {
		onChange.Release()
		onCancel.Release()
		if onData.Truthy() {
			onData.Release()
			onFail.Release()
		}
	}
	// A dismissed chooser fires "cancel" (current browsers), not "change".
	onCancel = js.FuncOf(func(js.Value, []js.Value) any {
		releaseAll()
		return nil
	})
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := in.Get("files")
		if files.Length() == 0 {
			releaseAll()
			return nil
		}
		file := files.Index(0)
		onData = js.FuncOf(func(_ js.Value, args []js.Value) any {
			buf := js.Global().Get("Uint8Array").New(args[0])
			data := make([]byte, buf.Length())
			js.CopyBytesToGo(data, buf)
			releaseAll()
			done(file.Get("name").String(), data, nil)
			return nil
		})
		onFail = js.FuncOf(func(_ js.Value, args []js.Value) any {
			releaseAll()
			done(file.Get("name").String(), nil, errors.New("transfer: could not read the chosen file"))
			return nil
		})
		file.Call("arrayBuffer").Call("then", onData, onFail)
		return nil
	})
	in.Call("addEventListener", "cancel", onCancel)
	in.Call("addEventListener", "change", onChange)
	in.Call("click")
}
