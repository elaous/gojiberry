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

// Where the encrypted store lives.
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

const encLocationKey = "showcase-enc:location"

func useServerScope(host string) {
	sum := sha256.Sum256([]byte(host))
	suffix := "@" + hex.EncodeToString(sum[:6])
	encTarget, encServerHost = "server", host
	for _, k := range []*string{&encSlot, &encConfigKey, &encSealedKey, &recoveryCheckedKey, &recoverySnoozeKey, &trustedListsKey, &keysUsedKey} {
		*k += suffix
	}
}

func encLabel(browserText, serverFormat string) string {
	if encTarget == "browser" {
		return browserText
	}
	return T("enc.serverStoreLabel", "Server %s (end-to-end encrypted)", encServerHost)
}

func encCapacity() int64 {
	if encTarget == "browser" {
		return 5 << 20
	}
	return 0
}

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
	d := &dirUI{url: clean}
	if tok := lsGet(dirTokenKey); tok != "" && lsGet(dirTokenURLKey) == clean {
		d.token = tok
	}
	useServerScope(host)
	return clean, docstore.NewHTTPAdapter(clean, &http.Client{Transport: bearerTransport{token: func(ctx context.Context) (string, error) { return d.sessionToken(ctx) }}}), true
}

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
