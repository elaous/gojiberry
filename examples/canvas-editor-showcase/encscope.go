//go:build js && wasm

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/xango"
)

// Where the encrypted store lives. By default it is this browser's storage and every key below is the one
// it has always been. When the person has chosen to keep it on the docstore server they confirmed
// (encLocationKey, applied at the next load), the same screens run over that server's documents: the
// target is called "server", and the things that belong to one store (its key list, the passkey made
// for it, the lists of keys the person confirmed, when keys were used and checked) get a suffix naming
// the server, so a store on a server and the one in the browser never mix.
var (
	encTarget          = "browser"
	encServerHost      = "" // the host of the server when the store lives there
	encSlot            = "showcase"
	encConfigKey       = "showcase-enc:config"
	encSealedKey       = "showcase-enc:sealed"
	recoveryCheckedKey = "showcase-enc:recovery-checked"
	recoverySnoozeKey  = "showcase-enc:recovery-snooze"
	trustedListsKey    = "showcase-enc:trusted-lists"
	keysUsedKey        = "showcase-enc:keys-used"
	deviceLabelKey     = "showcase-enc:device-label"
)

// encLocationKey holds "server" when the person chose to keep the encrypted store on the server.
const encLocationKey = "showcase-enc:location"

// useServerScope switches the keys above to the store of a server. Called once, before the page builds its
// encryption state.
func useServerScope(host string) {
	sum := sha256.Sum256([]byte(host))
	suffix := "@" + hex.EncodeToString(sum[:6])
	encTarget, encServerHost = "server", host
	for _, k := range []*string{&encSlot, &encConfigKey, &encSealedKey, &recoveryCheckedKey, &recoverySnoozeKey, &trustedListsKey, &keysUsedKey} {
		*k += suffix
	}
}

// encLabel is the name of the encrypted store's save target: the browser's, or "Server <host>" for a store
// on the server (format has the %s that the browser's own label fills).
func encLabel(browserText, serverFormat string) string {
	if encTarget == "browser" {
		return browserText
	}
	return T("enc.serverStoreLabel", "Server %s (end-to-end encrypted)", encServerHost)
}

// encCapacity is how much the store holds (a server has no limit the page knows of).
func encCapacity() int64 {
	if encTarget == "browser" {
		return 5 << 20
	}
	return 0
}

// chosenServerStore returns the server the person chose to keep the store on: its address and an adapter
// that talks to it with the sign-in token. ok is false when the choice was not made or the server is not
// the confirmed one.
func chosenServerStore() (url string, adapter docstore.Adapter, ok bool) {
	if lsGet(encLocationKey) != "server" {
		return "", nil, false
	}
	u := lsGet(dirURLKey)
	if u == "" || lsGet(dirOKKey) != u {
		return "", nil, false // only a server the person confirmed
	}
	clean, host, err := checkDirURL(u)
	if err != nil {
		return "", nil, false
	}
	d := &dirUI{url: clean} // only for the sign-in token, kept the way the page keeps it
	if tok := lsGet(dirTokenKey); tok != "" && lsGet(dirTokenURLKey) == clean {
		d.token = tok
	}
	useServerScope(host)
	return clean, docstore.NewHTTPAdapter(clean, &http.Client{Transport: bearerTransport{token: func(ctx context.Context) (string, error) { return d.sessionToken(ctx) }}}), true
}

// locationNodes is the choice of where the encrypted store lives, shown with the server settings once a
// server is confirmed: a checkbox, and once the choice differs from what is loaded, a button that
// reloads the page to apply it (the store a page works on is fixed for the whole visit).
func (d *dirUI) locationNodes(e *encUI) []xango.Node {
	want := lsGet(encLocationKey) == "server"
	nodes := []xango.Node{
		components.Checkbox(components.CheckboxProps{
			Label:   T("enc.location", "Keep the end-to-end encrypted store on this server instead of in this browser (applies after a reload; the other store stays where it is)"),
			Checked: want,
			OnChange: func(on bool) {
				if on {
					lsSet(encLocationKey, "server")
				} else {
					lsSet(encLocationKey, "")
				}
				e.render()
			},
		}),
	}
	if want != (encTarget == "server") {
		nodes = append(nodes, btn("enc-location-reload", T("enc.locationReload", "Reload to apply"), func() { js.Global().Get("location").Call("reload") }, false))
	}
	return nodes
}

// keyListNodes is where a device that has the key list hands it over: the text goes to the clipboard
// and the short code is shown to compare on the other device. Shown with the keys, for any store (the
// list is what a second device needs; it is public information).
func (u *ui) keyListNodes() []xango.Node {
	e := u.enc
	text, code, err := e.store.ExportKeyList()
	if err != nil {
		return nil
	}
	return []xango.Node{
		xango.El("div", []xango.Attr{xango.A("id", "enc-keylist"), xango.A("style", "margin:8px 0")},
			xango.El("p", []xango.Attr{xango.A("style", "font-size:12px;margin:2px 0")},
				xango.Text(T("enc.keyListNote", "To use this store on another device, copy its key list here and paste it there (it holds no secrets). Compare the code on both devices before you unlock."))),
			xango.El("p", []xango.Attr{xango.A("id", "enc-keylist-code"), xango.A("style", "font-family:monospace;margin:2px 0")}, xango.Text(T("enc.keyListCode", "Key list code: %s", code))),
			xango.El("textarea", []xango.Attr{xango.A("id", "enc-keylist-text"), xango.A("readonly", "readonly"), xango.A("rows", "3"), xango.A("spellcheck", "false"),
				xango.A("aria-label", T("enc.keyListText", "This store's key list")), xango.A("value", text),
				xango.A("style", "width:100%;box-sizing:border-box;font-family:monospace;font-size:11px")}),
			btn("enc-keylist-copy", T("enc.keyListCopy", "Copy the key list"), func() { u.saver.Copy(text) }, false)),
	}
}

// keyListImportNodes is the form that takes a key list copied on another device, on a page whose own list
// is missing: after the person has compared its code it becomes this device's list and the page loads
// again, locked, ready for one of the store's keys.
func (u *ui) keyListImportNodes() []xango.Node {
	e := u.enc
	return []xango.Node{
		para("enc-keylist-help", T("enc.keyListHelp", "Another device has this store's key list? Paste it here: it keeps all the store's keys, not just one."), false),
		textarea("enc-keylist-input", T("enc.keyListInput", "Key list from the other device"), e.keyListDraft, "{\"kind\":\"gojiberry-keylist/1\",…}", "3", func(v string) { e.keyListDraft = v; e.render() }),
		btn("enc-keylist-import", T("enc.keyListImport", "Use this key list"), func() {
			cfg, code, err := encstore.ParseKeyList(e.keyListDraft)
			if err != nil {
				e.notice = T("enc.keyListBad", "That is not a key list from another device.")
				e.render()
				return
			}
			e.notice = ""
			e.dir.ask(T("enc.keyListConfirmTitle", "Use this key list?"),
				T("enc.keyListConfirm", "Its code is %s. Use it only if the other device shows the same code.", code),
				func() {
					if err := writeEncConfig(cfg); err != nil {
						e.notice = err.Error()
						e.render()
						return
					}
					js.Global().Get("location").Call("reload")
				})
		}, strings.TrimSpace(e.keyListDraft) == ""),
	}
}
