//go:build !(js && wasm)

package fonts

import "errors"

func LoadFace(family string, v Variant, data []byte, done func(error)) {
	if done != nil {
		done(errors.New("fonts: LoadFace needs a browser (js/wasm)"))
	}
}
