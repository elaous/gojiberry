//go:build js && wasm

package main

import "syscall/js"

// The add-ons registry. Things that need a heavy runtime or a model the program should not carry (speech, vision,
// translation) are scripts the person who runs the page adds, and they offer themselves as properties of one object,
// window.addons. The page asks for one by name and does without it when it is not there: no feature depends on an
// add-on, and nothing is fetched for one until a person asks. docs/addons.md lists the names and what each offers.

// addon is window.addons[name], or undefined when the page has no such add-on.
func addon(name string) js.Value {
	all := js.Global().Get("addons")
	if !all.Truthy() {
		return js.Undefined()
	}
	return all.Get(name)
}

// hasAddon says the page has the add-on and it offers the method.
func hasAddon(name, method string) bool {
	a := addon(name)
	return a.Truthy() && a.Get(method).Type() == js.TypeFunction
}
