//go:build js && wasm

package transfer

import (
	"errors"
	"syscall/js"
)

func Download(filename, mime string, data []byte) {
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	blob := js.Global().Get("Blob").New([]any{arr}, map[string]any{"type": mime})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", filename)
	a.Call("click")
	// not at once: some browsers cancel a download whose URL is revoked early
	var revoke js.Func
	revoke = js.FuncOf(func(this js.Value, args []js.Value) any {
		js.Global().Get("URL").Call("revokeObjectURL", url)
		revoke.Release()
		return nil
	})
	js.Global().Call("setTimeout", revoke, 10000)
}

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

// PickedFile is a file the person chose.
type PickedFile struct {
	Name string
	Data []byte
}

func PickFiles(accept string, done func(files []PickedFile, err error)) {
	in := js.Global().Get("document").Call("createElement", "input")
	in.Set("type", "file")
	in.Set("multiple", true)
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
	onCancel = js.FuncOf(func(js.Value, []js.Value) any {
		releaseAll()
		return nil
	})
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := in.Get("files")
		n := files.Length()
		if n == 0 {
			releaseAll()
			return nil
		}
		reads := js.Global().Get("Array").New()
		names := make([]string, n)
		for i := 0; i < n; i++ {
			names[i] = files.Index(i).Get("name").String()
			reads.Call("push", files.Index(i).Call("arrayBuffer"))
		}
		onData = js.FuncOf(func(_ js.Value, args []js.Value) any {
			out := make([]PickedFile, n)
			for i := range out {
				buf := js.Global().Get("Uint8Array").New(args[0].Index(i))
				data := make([]byte, buf.Length())
				js.CopyBytesToGo(data, buf)
				out[i] = PickedFile{Name: names[i], Data: data}
			}
			releaseAll()
			done(out, nil)
			return nil
		})
		onFail = js.FuncOf(func(js.Value, []js.Value) any {
			releaseAll()
			done(nil, errors.New("transfer: could not read the chosen files"))
			return nil
		})
		js.Global().Get("Promise").Call("all", reads).Call("then", onData, onFail)
		return nil
	})
	in.Call("addEventListener", "cancel", onCancel)
	in.Call("addEventListener", "change", onChange)
	in.Call("click")
}
