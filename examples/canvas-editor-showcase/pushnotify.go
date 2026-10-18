//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

// Mentions when the page is closed (Web Push). If the operator names a push server in push.json next to the page
// ({"server": "https://..."}, https or relative), and the browser has service workers, push and notifications, the
// Comments tab offers "Notify me of mentions when this page is closed". Turning it on asks the browser for permission
// (only then), takes the server's public key (GET key), subscribes this browser to its push service, and gives the
// subscription to the server (POST subscribe) under the person's handle; turning it off does the reverse. The service
// worker that shows the notification is sw.js (package wasmserve). Nothing is asked of the server before the person
// turns it on, except one look at push.json and, to show the box ticked, the browser's own record of a subscription.

var pushUI struct {
	started    bool
	configured bool
	server     string
	subscribed bool
	checked    bool // the browser was asked whether there is a subscription
	synced     bool // the server was given the browser's current subscription since the page opened (or since it changed)
	busy       bool
	msg        string
}

// pushSupported says the browser has what push needs.
func pushSupported() bool {
	g := js.Global()
	nav := g.Get("navigator")
	return nav.Truthy() && nav.Get("serviceWorker").Truthy() && g.Get("PushManager").Truthy() && g.Get("Notification").Truthy()
}

// ensurePush reads push.json (once) and the state of the subscription.
func ensurePush(renderUI func()) {
	if pushUI.started || !pushSupported() {
		return
	}
	pushUI.started = true
	// The worker tells the page when the browser replaced the subscription (see sw.go): the page gives the new one to
	// the server under the person's handle, or says the notifications stopped when it could not be made again.
	sw := js.Global().Get("navigator").Get("serviceWorker")
	if sw.Get("addEventListener").Truthy() {
		sw.Call("addEventListener", "message", js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			d := args[0].Get("data")
			if !d.Truthy() || d.Get("type").String() != "gj-push-changed" {
				return nil
			}
			if d.Get("subscription").IsNull() {
				pushUI.subscribed, pushUI.msg = false, T("push.lost", "Notifications for mentions stopped: the browser could not renew them. Turn them on again.")
			} else {
				pushUI.synced = false
			}
			renderUI()
			return nil
		}))
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if resp, err := domrt.Await(js.Global().Call("fetch", "push.json")); err == nil && resp.Get("ok").Bool() {
			if text, err := domrt.Await(resp.Call("text")); err == nil {
				var cfg struct {
					Server string `json:"server"`
				}
				if json.Unmarshal([]byte(text.String()), &cfg) == nil && pushServerOK(cfg.Server) {
					pushUI.configured, pushUI.server = true, strings.TrimSuffix(cfg.Server, "/")
				}
			}
		}
		if pushUI.configured {
			if sub, err := pushSubscription(ctx); err == nil {
				pushUI.subscribed = sub.Truthy()
			}
			pushUI.checked = true
		}
		renderUI()
	}()
}

// pushServerOK: https, or an address relative to the page.
func pushServerOK(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "https://") {
		return !strings.ContainsAny(s, " \t\r\n\"'<>")
	}
	return !strings.Contains(s, "://") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, " \t\r\n\"'<>")
}

// pushRegistration is the service worker registration, waited for a few seconds (there is none when the page was opened
// with ?nosw).
func pushRegistration(ctx context.Context) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := domrt.Await(js.Global().Get("navigator").Get("serviceWorker").Get("ready"))
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		return js.Undefined(), errors.New("no service worker")
	}
}

func pushSubscription(ctx context.Context) (js.Value, error) {
	reg, err := pushRegistration(ctx)
	if err != nil {
		return js.Undefined(), err
	}
	return domrt.Await(reg.Get("pushManager").Call("getSubscription"))
}

// post sends JSON to the push server and reports whether it answered with success.
func pushPost(path string, body any) error {
	b, _ := json.Marshal(body)
	resp, err := domrt.Await(js.Global().Call("fetch", pushUI.server+path, map[string]any{
		"method": "POST", "body": string(b), "headers": map[string]any{"Content-Type": "application/json"}, "credentials": "include", "referrerPolicy": "no-referrer"}))
	if err != nil {
		return err
	}
	if !resp.Get("ok").Bool() {
		return fmt.Errorf("the push server answered %d", resp.Get("status").Int())
	}
	return nil
}

func pushFail(renderUI func(), msg string) {
	pushUI.busy, pushUI.msg = false, msg
	renderUI()
}

// turnOnPush asks for permission (the call is made here, in the click, as browsers require), then subscribes.
func turnOnPush(handle string, renderUI func()) {
	pushUI.busy, pushUI.msg = true, ""
	permission := js.Global().Get("Notification").Call("requestPermission")
	renderUI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if res, err := domrt.Await(permission); err != nil || res.String() != "granted" {
			pushFail(renderUI, T("push.denied", "Notifications are not allowed for this site: allow them in the browser's settings for this site and try again."))
			return
		}
		resp, err := domrt.Await(js.Global().Call("fetch", pushUI.server+"/key", map[string]any{"credentials": "omit", "referrerPolicy": "no-referrer"}))
		if err != nil || !resp.Get("ok").Bool() {
			pushFail(renderUI, T("push.noServer", "The notification server could not be reached."))
			return
		}
		text, err := domrt.Await(resp.Call("text"))
		var k struct {
			Key string `json:"key"`
		}
		key, kerr := base64.RawURLEncoding.DecodeString("")
		if err == nil && json.Unmarshal([]byte(text.String()), &k) == nil {
			key, kerr = base64.RawURLEncoding.DecodeString(strings.TrimRight(k.Key, "="))
		}
		if err != nil || kerr != nil || len(key) != 65 {
			pushFail(renderUI, T("push.noServer", "The notification server could not be reached."))
			return
		}
		reg, err := pushRegistration(ctx)
		if err != nil {
			pushFail(renderUI, T("push.noWorker", "Notifications need the page's service worker, which is not running (it is off when the page is opened with ?nosw)."))
			return
		}
		sub, err := domrt.Await(reg.Get("pushManager").Call("subscribe", map[string]any{"userVisibleOnly": true, "applicationServerKey": u8(key)}))
		if err != nil || !sub.Truthy() {
			pushFail(renderUI, T("push.failed", "The browser could not subscribe to notifications."))
			return
		}
		raw := js.Global().Get("JSON").Call("stringify", sub)
		var subscription json.RawMessage
		if dec := json.NewDecoder(bytes.NewReader([]byte(raw.String()))); dec.Decode(&subscription) != nil {
			pushFail(renderUI, T("push.failed", "The browser could not subscribe to notifications."))
			return
		}
		if err := pushPost("/subscribe", map[string]any{"handle": handle, "subscription": subscription}); err != nil {
			_, _ = domrt.Await(sub.Call("unsubscribe"))
			pushFail(renderUI, T("push.refused", "The notification server did not accept this subscription."))
			return
		}
		pushUI.busy, pushUI.subscribed, pushUI.msg = false, true, T("push.on", "You will be notified of mentions when this page is closed.")
		renderUI()
	}()
}

func turnOffPush(handle string, renderUI func()) {
	pushUI.busy, pushUI.msg = true, ""
	renderUI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if sub, err := pushSubscription(ctx); err == nil && sub.Truthy() {
			endpoint := sub.Get("endpoint").String()
			_, _ = domrt.Await(sub.Call("unsubscribe"))
			_ = pushPost("/unsubscribe", map[string]any{"handle": handle, "endpoint": endpoint}) // the server also forgets it when the push service says it is gone
		}
		pushUI.busy, pushUI.subscribed, pushUI.msg = false, false, T("push.off", "You will no longer be notified when this page is closed.")
		renderUI()
	}()
}

// pushResync gives the server the browser's current subscription under the person's handle: it may be a new one (the
// browser replaced it while the page was closed), and the server keeps a subscription once per endpoint, so repeating a
// known one changes nothing.
func pushResync(handle string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sub, err := pushSubscription(ctx)
	if err != nil || !sub.Truthy() {
		return
	}
	var subscription json.RawMessage
	if json.Unmarshal([]byte(js.Global().Get("JSON").Call("stringify", sub).String()), &subscription) != nil {
		return
	}
	if err := pushPost("/subscribe", map[string]any{"handle": handle, "subscription": subscription}); err != nil {
		pushUI.synced = false // tried again the next time the box is drawn
	}
}

// pushSection is the box in the Comments tab; nothing when the operator did not name a server or the browser cannot.
func pushSection(u *ui, renderUI func()) xango.Node {
	ensurePush(renderUI)
	h := u.myHandle()
	if !pushUI.configured || !pushUI.checked || h == "" {
		return xango.Frag()
	}
	if pushUI.subscribed && !pushUI.synced && !pushUI.busy {
		pushUI.synced = true
		go pushResync(h)
	}
	nodes := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("id", "push-toggle")}, components.Checkbox(components.CheckboxProps{
			Label: T("push.label", "Notify me of mentions when this page is closed"), Checked: pushUI.subscribed, Disabled: pushUI.busy,
			OnChange: func(on bool) {
				if on {
					turnOnPush(h, renderUI)
				} else {
					turnOffPush(h, renderUI)
				}
			}})),
		para("push-about", T("push.about", "A notification says who mentioned you and in which document, never what they wrote. It goes through your browser's push service, encrypted."), false),
	}
	if pushUI.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "push-message"), xango.A("role", "status")}, xango.Text(pushUI.msg)))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "push-section")}, nodes...)
}
