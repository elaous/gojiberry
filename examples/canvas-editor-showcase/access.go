//go:build js && wasm

package main

import (
	"strconv"
	"syscall/js"
	"time"
)

const accessCSS = `
[data-gj-dwelling]{outline:3px solid #2563eb;outline-offset:2px;animation:gj-dwell var(--gj-dwell-ms,1200ms) linear forwards}
@keyframes gj-dwell{from{outline-color:rgba(37,99,235,.15)}to{outline-color:#2563eb}}
@media (prefers-reduced-motion:reduce){[data-gj-dwelling]{animation:none}}
.gj-scan-on{outline:4px solid #f59e0b !important;outline-offset:2px;background-image:linear-gradient(rgba(245,158,11,.18),rgba(245,158,11,.18))}
`

func dwellMillis(v string) int { return map[string]int{"fast": 700, "medium": 1200, "slow": 2000}[v] }
func scanMillis(v string) int  { return map[string]int{"fast": 1000, "medium": 1500, "slow": 2500}[v] }

const clickable = `button, a[href], [role="tab"], [role="menuitem"], [role="option"], summary, select, input[type="checkbox"], input[type="radio"], input[type="button"], input[type="submit"]`

type accessUI struct {
	started    bool
	dwell      string // the setting in force
	scan       string
	dwellOn    js.Value // the element being dwelled on
	dwellFired bool
	dwellGen   int
	scanList   []js.Value
	scanAt     int
	scanGen    int
	scanPaused bool // the document has the keyboard
	label      string
}

var access accessUI

func (a *accessUI) doc() js.Value { return js.Global().Get("document") }

func usable(el js.Value) bool {
	if el.IsUndefined() || el.IsNull() || el.Get("disabled").Truthy() || el.Call("hasAttribute", "aria-disabled").Truthy() && el.Call("getAttribute", "aria-disabled").String() == "true" {
		return false
	}
	if el.Get("id").String() == "input" {
		return false
	}
	r := el.Call("getBoundingClientRect")
	return r.Get("width").Float() > 0 && r.Get("height").Float() > 0
}

func setAccess(p prefs) {
	a := &access
	a.dwell, a.scan = p.Dwell, p.Scan
	if (a.dwell != "" || a.scan != "") && !a.started {
		a.start()
	}
	a.cancelDwell()
	if a.scan != "" {
		a.startScan()
	} else {
		a.stopScan()
	}
	if a.dwell != "" {
		a.doc().Get("documentElement").Get("style").Call("setProperty", "--gj-dwell-ms", strconv.Itoa(dwellMillis(a.dwell))+"ms")
	}
}

func (a *accessUI) start() {
	a.started = true
	d := a.doc()
	st := d.Call("createElement", "style")
	st.Set("textContent", accessCSS)
	d.Get("head").Call("appendChild", st)
	over := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if a.dwell == "" {
			return nil
		}
		el := args[0].Get("target")
		if el.IsUndefined() || el.IsNull() || el.Get("closest").IsUndefined() {
			return nil
		}
		t := el.Call("closest", clickable)
		if t.IsNull() || !usable(t) {
			a.cancelDwell()
			a.dwellFired = false
			return nil
		}
		if a.dwellOn.Equal(t) {
			return nil // still on it
		}
		a.cancelDwell()
		a.dwellFired = false
		a.beginDwell(t)
		return nil
	})
	d.Call("addEventListener", "mouseover", over, true)
	out := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if a.dwellOn.IsUndefined() || a.dwellOn.IsNull() {
			return nil
		}
		to := args[0].Get("relatedTarget")
		if !to.IsNull() && !to.IsUndefined() && a.dwellOn.Call("contains", to).Bool() {
			return nil
		}
		a.cancelDwell()
		a.dwellFired = false
		return nil
	})
	d.Call("addEventListener", "mouseout", out, true)
	key := js.FuncOf(func(_ js.Value, args []js.Value) any {
		e := args[0]
		if a.scan == "" {
			return nil
		}
		k := e.Get("key").String()
		if a.scanPaused {
			if k == "Escape" {
				a.scanPaused = false
				a.step() // publishes
			}
			return nil
		}
		if (k == " " || k == "Enter") && !e.Get("ctrlKey").Truthy() && !e.Get("metaKey").Truthy() && !e.Get("altKey").Truthy() {
			e.Call("preventDefault")
			e.Call("stopPropagation")
			a.choose()
		}
		return nil
	})
	d.Call("addEventListener", "keydown", key, true)
}

func (a *accessUI) beginDwell(t js.Value) {
	a.dwellOn = t
	a.dwellGen++
	gen := a.dwellGen
	t.Call("setAttribute", "data-gj-dwelling", "")
	time.AfterFunc(time.Duration(dwellMillis(a.dwell))*time.Millisecond, func() {
		if gen != a.dwellGen || a.dwellFired || a.dwellOn.IsUndefined() || !a.dwellOn.Equal(t) || !usable(t) {
			return
		}
		a.dwellFired = true
		t.Call("removeAttribute", "data-gj-dwelling")
		t.Call("click")
	})
}

func (a *accessUI) cancelDwell() {
	a.dwellGen++
	if !a.dwellOn.IsUndefined() && !a.dwellOn.IsNull() {
		a.dwellOn.Call("removeAttribute", "data-gj-dwelling")
	}
	a.dwellOn = js.Undefined()
}

func (a *accessUI) startScan() {
	a.scanGen++
	a.scanPaused = false
	a.scanAt = -1
	gen := a.scanGen
	var tick func()
	tick = func() {
		if gen != a.scanGen || a.scan == "" {
			return
		}
		if !a.scanPaused {
			a.step()
		}
		time.AfterFunc(time.Duration(scanMillis(a.scan))*time.Millisecond, tick)
	}
	a.step()
	time.AfterFunc(time.Duration(scanMillis(a.scan))*time.Millisecond, tick)
}

func (a *accessUI) stopScan() {
	a.scanGen++
	a.unlight()
	a.label = ""
	a.scanPaused = false
	a.publish()
}

func (a *accessUI) unlight() {
	list := a.doc().Call("querySelectorAll", ".gj-scan-on")
	for i := 0; i < list.Length(); i++ {
		list.Index(i).Get("classList").Call("remove", "gj-scan-on")
	}
}

func (a *accessUI) step() {
	a.unlight()
	els := a.doc().Call("querySelectorAll", clickable)
	a.scanList = a.scanList[:0]
	for i := 0; i < els.Length(); i++ {
		if el := els.Index(i); usable(el) {
			a.scanList = append(a.scanList, el)
		}
	}
	n := len(a.scanList) + 1 // the document is the last stop
	a.scanAt = (a.scanAt + 1) % n
	if a.scanAt == n-1 {
		a.label = "document"
		ed := a.doc().Call("querySelector", ".gj-canvas-host, canvas")
		if !ed.IsNull() {
			ed.Get("classList").Call("add", "gj-scan-on")
		}
		a.publish()
		return
	}
	el := a.scanList[a.scanAt]
	el.Get("classList").Call("add", "gj-scan-on")
	el.Call("scrollIntoView", map[string]any{"block": "nearest", "inline": "nearest"})
	a.label = scanName(el)
	a.publish()
}

func scanName(el js.Value) string {
	if v := el.Call("getAttribute", "data-tab"); !v.IsNull() {
		return "tab:" + v.String()
	}
	if id := el.Get("id").String(); id != "" {
		return "#" + id
	}
	if t := el.Get("textContent").String(); t != "" {
		return t
	}
	return el.Get("tagName").String()
}

func (a *accessUI) choose() {
	if a.scanAt < 0 {
		return
	}
	if a.scanAt >= len(a.scanList) {
		a.scanPaused = true
		a.unlight()
		if in := a.doc().Call("getElementById", "input"); !in.IsNull() {
			in.Call("focus")
		}
		a.label = "document (Escape returns to the scan)"
		a.publish()
		return
	}
	el := a.scanList[a.scanAt]
	el.Call("click")
	a.scanAt = -1
	a.step()
}

func (a *accessUI) publish() {
	root := a.doc().Get("documentElement")
	if a.label == "" {
		root.Call("removeAttribute", "data-gj-scan")
	} else {
		root.Call("setAttribute", "data-gj-scan", a.label)
	}
	if a.scanPaused {
		root.Call("setAttribute", "data-gj-scan-paused", "")
	} else {
		root.Call("removeAttribute", "data-gj-scan-paused")
	}
}
