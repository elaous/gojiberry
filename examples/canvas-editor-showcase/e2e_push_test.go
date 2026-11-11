//go:build !(js && wasm)

package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestMentionNotificationsWhenThePageIsClosed(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	key := base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
	site := e2etest.NewSite(t)
	site.Handle("/push.json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"server":"/pushapi"}`)
	}))
	site.Handle("/pushapi/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+string(body))
		mu.Unlock()
		if r.URL.Path == "/pushapi/key" {
			_ = json.NewEncoder(w).Encode(map[string]string{"key": key})
		}
	}))
	d := site.Open(t, "/d/push1?name=Ana")
	d.OnNewDocument(`
		window.__sub = null;
		const reg = { pushManager: {
			getSubscription: async () => window.__sub,
			subscribe: async (o) => { window.__opts = o.userVisibleOnly === true && o.applicationServerKey.length === 65;
				window.__sub = { endpoint: "https://push.example/abc", toJSON() { return { endpoint: this.endpoint, keys: { p256dh: "x", auth: "y" } }; },
					unsubscribe: async () => { window.__sub = null; return true; } }; return window.__sub; } } };
		Object.defineProperty(navigator, "serviceWorker", { value: { ready: Promise.resolve(reg), register: async () => reg }, configurable: true });
		window.PushManager = function () {};
		window.Notification = { requestPermission: async () => "granted", permission: "default" };`)
	d.ReloadPage()
	d.Wait("s.text.length > 100")
	d.Click(`button[data-tab="comments"]`)
	d.WaitSelector("#push-toggle")
	d.OK(!d.Checked("#push-toggle input"), "it starts unticked")
	d.SetChecked("#push-toggle input", true)
	d.WaitFor("the subscription is sent to the push server", `document.body.textContent.includes('notified of mentions when this page is closed')`)
	d.OK(d.Bool(`window.__opts === true`), "the browser was asked for a visible-only subscription with the server's key")
	mu.Lock()
	joined := strings.Join(calls, "\n")
	mu.Unlock()
	d.OK(strings.Contains(joined, "/pushapi/key") && strings.Contains(joined, `/pushapi/subscribe {"handle":"Ana"`) && strings.Contains(joined, "https://push.example/abc"), "the server was given the key request and then the subscription: "+joined)
	d.SetChecked("#push-toggle input", false)
	d.WaitFor("it is taken back", `window.__sub === null && document.body.textContent.includes('no longer be notified')`)
	mu.Lock()
	joined = strings.Join(calls, "\n")
	mu.Unlock()
	d.OK(strings.Contains(joined, `/pushapi/unsubscribe {"endpoint":"https://push.example/abc","handle":"Ana"}`) || strings.Contains(joined, `/pushapi/unsubscribe {"handle":"Ana","endpoint":"https://push.example/abc"}`), "the server was told: "+joined)
	d.NoErrors()
}

func TestAReplacedPushSubscriptionIsGivenToTheServer(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	site := e2etest.NewSite(t)
	site.Handle("/push.json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"server":"/pushapi"}`) }))
	site.Handle("/pushapi/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+string(body))
		mu.Unlock()
	}))
	d := site.Open(t, "/d/push2?name=Ana")
	d.OnNewDocument(`
		window.__sub = { endpoint: "https://push.example/new", toJSON() { return { endpoint: this.endpoint, keys: { p256dh: "x", auth: "y" } }; }, unsubscribe: async () => true };
		const reg = { pushManager: { getSubscription: async () => window.__sub } };
		const sw = new EventTarget();
		sw.ready = Promise.resolve(reg); sw.register = async () => reg;
		Object.defineProperty(navigator, "serviceWorker", { value: sw, configurable: true });
		window.PushManager = function () {};
		window.Notification = { requestPermission: async () => "granted", permission: "granted" };`)
	d.ReloadPage()
	d.Wait("s.text.length > 100")
	d.Click(`button[data-tab="comments"]`)
	d.WaitSelector("#push-toggle")
	count := func(s string) int {
		mu.Lock()
		defer mu.Unlock()
		return strings.Count(strings.Join(calls, "\n"), s)
	}
	waitCount := func(what string, atLeast int) {
		for i := 0; i < 100 && count("/pushapi/subscribe") < atLeast; i++ {
			d.Sleep(100)
		}
		d.OK(count("/pushapi/subscribe") >= atLeast, what)
	}
	waitCount("the current subscription is given to the server when the page opens", 1)
	d.OK(count(`/pushapi/subscribe {"handle":"Ana"`) >= 1 && count("https://push.example/new") >= 1, "the server was given the subscription the browser has")
	before := count("/pushapi/subscribe")
	d.Eval(`navigator.serviceWorker.dispatchEvent(Object.assign(new Event("message"), { data: { type: "gj-push-changed", subscription: { endpoint: "https://push.example/new" } } }))`, nil)
	waitCount("the changed subscription is sent again", before+1)
	d.Eval(`navigator.serviceWorker.dispatchEvent(Object.assign(new Event("message"), { data: { type: "gj-push-changed", subscription: null } }))`, nil)
	d.WaitFor("it says the notifications stopped", `document.body.textContent.includes('stopped: the browser could not renew them')`)
	d.NoErrors()
}
