//go:build js && wasm

package browser

import (
	"errors"
	"syscall/js"
)

// awaitFileReader blocks until a FileReader fires "load" or "error" --
// the same channel-bridge idea as awaitIDBRequest, adapted to
// FileReader's event names and its result living on reader.result.
func awaitFileReader(reader js.Value) (js.Value, error) {
	result := make(chan js.Value, 1)
	fail := make(chan error, 1)

	var onLoad, onError js.Func
	onLoad = js.FuncOf(func(this js.Value, args []js.Value) any {
		result <- reader.Get("result")
		return nil
	})
	onError = js.FuncOf(func(this js.Value, args []js.Value) any {
		fail <- fileReaderError(reader)
		return nil
	})
	defer onLoad.Release()
	defer onError.Release()

	reader.Call("addEventListener", "load", onLoad)
	reader.Call("addEventListener", "error", onError)

	select {
	case v := <-result:
		return v, nil
	case err := <-fail:
		return js.Value{}, err
	}
}

func fileReaderError(reader js.Value) error {
	if e := reader.Get("error"); !e.IsUndefined() && !e.IsNull() {
		return errors.New(e.Get("message").String())
	}
	return errors.New("filereader failed")
}

// File wraps a JS File (or Blob) object, e.g. one returned by
// dom.InputElement.Files or dom.DataTransfer.Files.
type File struct {
	Value js.Value
}

// WrapFile wraps a raw JS File/Blob object as a File.
func WrapFile(v js.Value) File { return File{Value: v} }

// WrapFiles wraps a JS FileList (or any array-like of File objects).
func WrapFiles(v js.Value) []File {
	out := make([]File, v.Length())
	for i := range out {
		out[i] = File{Value: v.Index(i)}
	}
	return out
}

// Name returns the file's name.
func (f File) Name() string { return f.Value.Get("name").String() }

// Size returns the file's size in bytes.
func (f File) Size() int64 { return int64(f.Value.Get("size").Float()) }

// Type returns the file's MIME type, or "" if unknown.
func (f File) Type() string { return f.Value.Get("type").String() }

// ReadAsText reads the file's contents decoded as UTF-8 text, blocking
// until the read completes.
func (f File) ReadAsText() (string, error) {
	reader := js.Global().Get("FileReader").New()
	reader.Call("readAsText", f.Value)
	v, err := awaitFileReader(reader)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// ReadAsArrayBuffer reads the file's raw contents as bytes, blocking
// until the read completes.
func (f File) ReadAsArrayBuffer() ([]byte, error) {
	reader := js.Global().Get("FileReader").New()
	reader.Call("readAsArrayBuffer", f.Value)
	v, err := awaitFileReader(reader)
	if err != nil {
		return nil, err
	}
	arr := js.Global().Get("Uint8Array").New(v)
	out := make([]byte, arr.Length())
	js.CopyBytesToGo(out, arr)
	return out, nil
}

// ReadAsDataURL reads the file's contents encoded as a data: URL
// (base64, with a MIME-type prefix), blocking until the read completes.
func (f File) ReadAsDataURL() (string, error) {
	reader := js.Global().Get("FileReader").New()
	reader.Call("readAsDataURL", f.Value)
	v, err := awaitFileReader(reader)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// CreateObjectURL creates a blob: URL referencing f's data, valid until
// RevokeObjectURL is called or the document unloads. Useful for e.g.
// setting an <img>/<video>'s src without reading the file into memory.
func CreateObjectURL(f File) string {
	return js.Global().Get("URL").Call("createObjectURL", f.Value).String()
}

// RevokeObjectURL releases a URL created by CreateObjectURL.
func RevokeObjectURL(url string) {
	js.Global().Get("URL").Call("revokeObjectURL", url)
}
