//go:build js && wasm

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
)

func failure(r any) error {
	if e, ok := r.(error); ok {
		return e
	}
	return errors.New("e2e selector: unexpected failure")
}

func all(sel string, root js.Value) (out js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = failure(r) // a selector the browser rejects, say
		}
	}()
	scope := root
	if scope.IsUndefined() || scope.IsNull() {
		scope = js.Global().Get("document")
	}
	type mark struct {
		el   js.Value
		name string
	}
	var marks []mark
	defer func() {
		for _, m := range marks {
			m.el.Call("removeAttribute", m.name)
		}
	}()
	i := 0
	var everything js.Value
	var css strings.Builder
	for k := 0; k < len(sel); {
		kind, arg, n := pseudoAt(sel[k:])
		if n == 0 {
			css.WriteByte(sel[k])
			k++
			continue
		}
		k += n
		want := norm(arg)
		name := "data-e2e-m" + strconv.Itoa(i)
		i++
		if everything.IsUndefined() {
			everything = scope.Call("querySelectorAll", "*")
		}
		lower := strings.ToLower(want)
		for j := range everything.Length() {
			el := everything.Index(j)
			t := norm(el.Get("textContent").String())
			if kind == "has-text" && strings.Contains(strings.ToLower(t), lower) || kind == "text-is" && t == want {
				el.Call("setAttribute", name, "")
				marks = append(marks, mark{el, name})
			}
		}
		css.WriteString("[" + name + "]")
	}
	return scope.Call("querySelectorAll", css.String()), nil
}

func visible(el js.Value) bool {
	r := el.Call("getBoundingClientRect")
	st := js.Global().Call("getComputedStyle", el)
	return r.Get("width").Float() > 0 && r.Get("height").Float() > 0 &&
		st.Get("visibility").String() != "hidden" && st.Get("display").String() != "none"
}

func list(nl js.Value) []js.Value {
	out := make([]js.Value, nl.Length())
	for i := range out {
		out[i] = nl.Index(i)
	}
	return out
}

func array(els []js.Value) js.Value {
	a := js.Global().Get("Array").New()
	for _, e := range els {
		a.Call("push", e)
	}
	return a
}

func first(sel string) (js.Value, error) {
	nl, err := all(sel, js.Undefined())
	if err != nil {
		return js.Null(), err
	}
	els := list(nl)
	for _, e := range els {
		if visible(e) {
			return e, nil
		}
	}
	if len(els) > 0 {
		return els[0], nil
	}
	return js.Null(), nil
}

func centre(sel string) (res js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = js.Null(), failure(r)
		}
	}()
	nl, err := all(sel, js.Undefined())
	if err != nil {
		return js.Null(), err
	}
	var el js.Value
	found := false
	for _, e := range list(nl) {
		if visible(e) {
			el, found = e, true
			break
		}
	}
	if !found {
		return js.Null(), nil
	}
	doc := js.Global().Get("document")
	spot := func() (float64, float64) {
		r := el.Call("getBoundingClientRect")
		return r.Get("x").Float() + r.Get("width").Float()/2, r.Get("y").Float() + r.Get("height").Float()/2
	}
	hit := func(x, y float64) bool {
		e := doc.Call("elementFromPoint", x, y)
		return !e.IsNull() && !e.IsUndefined() && el.Call("contains", e).Bool()
	}
	x, y := spot()
	if !hit(x, y) {
		opts := js.Global().Get("Object").New()
		opts.Set("block", "center")
		opts.Set("inline", "center")
		el.Call("scrollIntoView", opts)
		x, y = spot()
	}
	if !hit(x, y) {
		return js.Null(), nil
	}
	return array([]js.Value{js.ValueOf(x), js.ValueOf(y)}), nil
}

// a Go function cannot throw into the page: errors are returned
func wrap(f func(args []js.Value) (js.Value, error)) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) (ret any) {
		defer func() {
			if r := recover(); r != nil {
				ret = "e2e selector: " + fmt.Sprint(r)
			}
		}()
		v, err := f(args)
		if err != nil {
			return err.Error()
		}
		return v
	})
}

func main() {
	g := js.Global().Get("Object").New()
	g.Set("all", wrap(func(a []js.Value) (js.Value, error) {
		root := js.Undefined()
		if len(a) > 1 {
			root = a[1]
		}
		nl, err := all(a[0].String(), root)
		if err != nil {
			return js.Null(), err
		}
		return array(list(nl)), nil
	}))
	g.Set("first", wrap(func(a []js.Value) (js.Value, error) { return first(a[0].String()) }))
	g.Set("centre", wrap(func(a []js.Value) (js.Value, error) { return centre(a[0].String()) }))
	g.Set("visible", wrap(func(a []js.Value) (js.Value, error) { return js.ValueOf(visible(a[0])), nil }))
	g.Set("norm", wrap(func(a []js.Value) (js.Value, error) { return js.ValueOf(norm(a[0].String())), nil }))
	js.Global().Set("__e2eGo", g)
	select {}
}
