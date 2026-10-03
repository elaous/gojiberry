//go:build !(js && wasm)

package fonts

import "errors"

// LoadFace registers a font file with the browser so canvas text can use
// it. There is no browser outside WASM: done gets ErrUnsupported.
func LoadFace(family string, v Variant, data []byte, done func(error)) {
	if done != nil {
		done(errors.New("fonts: LoadFace needs a browser (js/wasm)"))
	}
}
