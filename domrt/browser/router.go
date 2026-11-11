//go:build js && wasm

package browser

import (
	"net/url"
	"syscall/js"
)

// Router wraps the History API and reports URL changes.
type Router struct {
	onNavigate func(u *url.URL)
	popstate   js.Func
}

// NewRouter creates a Router and starts listening for navigation.
func NewRouter() *Router {
	r := &Router{}
	r.popstate = js.FuncOf(func(this js.Value, args []js.Value) any {
		if r.onNavigate != nil {
			r.onNavigate(currentURL())
		}
		return nil
	})
	js.Global().Get("window").Call("addEventListener", "popstate", r.popstate)
	return r
}

// OnNavigate registers fn to run on any URL change.
func (r *Router) OnNavigate(fn func(u *url.URL)) {
	r.onNavigate = fn
}

// Navigate pushes path onto the history stack.
func (r *Router) Navigate(path string) {
	js.Global().Get("history").Call("pushState", js.Null(), "", path)
	if r.onNavigate != nil {
		r.onNavigate(currentURL())
	}
}

// Replace swaps the current history entry for path.
func (r *Router) Replace(path string) {
	js.Global().Get("history").Call("replaceState", js.Null(), "", path)
	if r.onNavigate != nil {
		r.onNavigate(currentURL())
	}
}

// URL returns the current URL.
func (r *Router) URL() *url.URL {
	return currentURL()
}

// Stop removes the popstate listener.
func (r *Router) Stop() {
	js.Global().Get("window").Call("removeEventListener", "popstate", r.popstate)
	r.popstate.Release()
}

func currentURL() *url.URL {
	href := js.Global().Get("location").Get("href").String()
	u, err := url.Parse(href)
	if err != nil {
		return &url.URL{}
	}
	return u
}
