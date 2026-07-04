//go:build js && wasm

package browser

import (
	"net/url"
	"syscall/js"
)

// Router wraps the History API (pushState/replaceState) and the
// browser's back/forward buttons (popstate) behind a single callback.
// pushState/replaceState fire no event of their own -- only popstate
// does -- so without this, code driving a page from Go has to
// duplicate its "update the page for this URL" logic in two places:
// right after calling pushState, and separately in a popstate
// listener, taking care to route both through the same function.
// OnNavigate fires for either case.
type Router struct {
	onNavigate func(u *url.URL)
	popstate   js.Func
}

// NewRouter creates a Router and starts listening for the browser's
// back/forward buttons. Call OnNavigate to register what runs on any
// URL change.
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

// OnNavigate registers fn to run on any URL change: Navigate, Replace,
// or the user's own back/forward navigation. Calling it again replaces
// the previous handler.
func (r *Router) OnNavigate(fn func(u *url.URL)) {
	r.onNavigate = fn
}

// Navigate pushes path onto the history stack (a new Back stop) and
// calls the OnNavigate handler immediately, since pushState itself
// fires no event.
func (r *Router) Navigate(path string) {
	js.Global().Get("history").Call("pushState", js.Null(), "", path)
	if r.onNavigate != nil {
		r.onNavigate(currentURL())
	}
}

// Replace swaps the current history entry for path -- e.g. for a
// redirect that shouldn't itself become a Back stop -- and calls the
// OnNavigate handler immediately.
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
