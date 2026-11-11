//go:build js && wasm

package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/userconfig"
	"github.com/elaous/gojiberry/xango"
)

const (
	prefsSyncKey = "showcase:a11y.sync" // "on" when the preferences are kept on the server too
	prefsItem    = "prefs.display"
	myFonts      = "My fonts"
)

type shareIncoming struct {
	info userconfig.Info
	data []byte
}

type fontDraft struct {
	file, family, licence string
	data                  []byte
}

type configExtra struct {
	synced    string // the server address the settings were last synced with
	shareNote string
	incoming  *shareIncoming
	font      fontDraft
	fontMsg   string
	prefsMsg  string
	fontsSeen map[string]int
	afterFont func() // draws the canvas again after a font arrived
}

func prefsSyncOn() bool { return lsGet(prefsSyncKey) == "on" }

func (u *ui) configURL() string {
	if u.enc == nil {
		return ""
	}
	return u.enc.dir.url
}

func (u *ui) configTick(renderUI func()) {
	u.loadStoredFonts()
	if want := u.configURL(); want != u.cfgx.synced {
		u.cfgx.synced = want
		if want != "" {
			u.syncThemes(renderUI)
		}
	}
}

func (u *ui) afterSync(renderUI func()) {
	if prefsSyncOn() {
		if _, err := u.config().Get(prefsItem); err == nil {
			u.pullPrefs()
		} else {
			u.pushPrefs(curPrefs, renderUI)
		}
	}
	u.loadStoredFonts()
}

func (u *ui) pushPrefs(p prefs, renderUI func()) {
	if !prefsSyncOn() {
		return
	}
	c, b := u.config(), encodePrefs(p)
	if old, err := c.Get(prefsItem); err == nil && bytes.Equal(old, b) {
		return
	}
	c.Set(prefsItem, b)
	u.syncThemes(renderUI)
}

func (u *ui) pullPrefs() {
	data, err := u.config().Get(prefsItem)
	if err != nil {
		return
	}
	p, err := parsePrefs(data)
	if err != nil || bytes.Equal(encodePrefs(p), encodePrefs(curPrefs)) {
		return
	}
	setPrefs(p)
	u.announce(T("prefs.fromServer", "Display preferences were updated from the server"))
}

func (u *ui) setPrefsSync(on bool, renderUI func()) {
	if !on {
		lsSet(prefsSyncKey, "")
		u.config().Delete(prefsItem)
		u.cfgx.prefsMsg = T("prefs.syncOff", "The preferences are no longer kept on the server; they stay on this device.")
		u.syncThemes(renderUI)
		return
	}
	lsSet(prefsSyncKey, "on")
	if u.configURL() == "" {
		u.cfgx.prefsMsg = T("prefs.syncNoServer", "No server is confirmed in the People tab yet; the preferences go there once one is.")
		return
	}
	u.cfgx.prefsMsg = ""
	u.syncThemes(renderUI)
}

func prefsSyncNode(u *ui, renderUI func()) xango.Node {
	kids := []xango.Node{components.Checkbox(components.CheckboxProps{
		Label:   T("prefs.sync", "Keep these preferences on the server too, so they follow me to another device"),
		Checked: prefsSyncOn(),
		OnChange: func(v bool) {
			u.setPrefsSync(v, renderUI)
			renderUI()
		}}),
		xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(T("prefs.syncHint", "Off by default: your settings can show that you have a disability. They are kept encrypted with your keys when the server store is encrypted, and are never put in a document."))),
	}
	if u.cfgx.prefsMsg != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta"), xango.A("role", "status")}, xango.Text(u.cfgx.prefsMsg)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-prefs-sync")}, kids...)
}

func (u *ui) shareTheme(item string, renderUI func()) {
	l := &u.themeLib
	data, err := u.config().Get(item)
	if err == nil {
		var b []byte
		if b, err = userconfig.Bundle(item, data, strings.TrimSpace(u.cfgx.shareNote)); err == nil {
			transfer.Download(strings.TrimPrefix(item, userconfig.KindTokens+".")+".gojiberry-theme.json", "application/json", b)
			l.status = T("share.done", "The theme was saved to a file you can send to someone.")
		}
	}
	if err != nil {
		l.status = T("share.failed", "That theme could not be put in a file.")
	}
	u.announce(l.status)
	renderUI()
}

func (u *ui) importSharedTheme(data []byte, renderUI func()) {
	l := &u.themeLib
	info, content, err := userconfig.OpenBundle(data)
	switch {
	case err != nil:
		l.status = T("share.bad", "That file cannot be used: %s", strings.TrimPrefix(err.Error(), userconfig.ErrBadBundle.Error()+": "))
	case info.Kind != userconfig.KindTokens:
		l.status = T("share.notTheme", "That file holds a %s; this page keeps shared themes only.", info.Kind)
	default:
		u.cfgx.incoming = &shareIncoming{info: info, data: content}
		l.status = ""
	}
	u.announce(l.status)
	renderUI()
}

// never replaces a theme the person has
func (u *ui) acceptSharedTheme(renderUI func()) {
	in, c := u.cfgx.incoming, u.config()
	if in == nil {
		return
	}
	name := in.info.Name
	for i := 2; ; i++ {
		if _, err := c.Get(name); err != nil {
			break
		}
		name = fmt.Sprintf("%s-%d", in.info.Name, i)
	}
	c.Set(name, in.data)
	u.cfgx.incoming = nil
	u.themeLib.status = T("share.added", "The shared theme was added to your saved themes.")
	u.announce(u.themeLib.status)
	u.syncThemes(renderUI)
	renderUI()
}

func (u *ui) shareNodes(renderUI func()) []xango.Node {
	nodes := []xango.Node{
		lineInput("theme-share-note", T("share.note", "Note to go with a theme you share (optional)"), u.cfgx.shareNote, "", func(v string) { u.cfgx.shareNote = v }),
		btn("theme-import", T("share.import", "Import a shared theme…"), func() {
			pickPrefsFile(func(data []byte) { u.importSharedTheme(data, renderUI) })
		}, false),
	}
	if in := u.cfgx.incoming; in != nil {
		review := []xango.Node{
			xango.El("p", nil, xango.Text(T("share.review", "A shared theme: %s.", strings.TrimPrefix(in.info.Name, userconfig.KindTokens+".")))),
		}
		if in.info.Note != "" {
			review = append(review, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(T("share.noteFrom", "The note with it: %s", in.info.Note))))
		}
		review = append(review,
			btn("theme-import-add", T("share.add", "Add it to my themes"), func() { u.acceptSharedTheme(renderUI) }, false),
			btn("theme-import-cancel", T("share.cancel", "Do not add it"), func() { u.cfgx.incoming = nil; renderUI() }, false))
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("class", "gj-theme-share-review"), xango.A("role", "group"), xango.A("aria-label", T("share.reviewLabel", "A shared theme waiting for your answer"))}, review...))
	}
	return nodes
}

func fontItem(family string) string {
	slug := strings.Trim(themeSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(family)), "-"), "-.")
	if slug == "" {
		return ""
	}
	if len(slug) > 56 {
		slug = slug[:56]
	}
	return "font." + slug
}

type storedFont struct {
	item, family string
	font         userconfig.Font
}

func (u *ui) storedFonts() []storedFont {
	c := u.config()
	var out []storedFont
	for _, n := range c.Names() {
		if !strings.HasPrefix(n, "font.") {
			continue
		}
		data, err := c.Get(n)
		if err != nil {
			continue
		}
		f, err := userconfig.DecodeFont(data)
		if err != nil {
			continue
		}
		out = append(out, storedFont{item: n, family: strings.TrimPrefix(n, "font."), font: f})
	}
	return out
}

func (u *ui) loadStoredFonts() {
	x := &u.cfgx
	if x.fontsSeen == nil {
		x.fontsSeen = map[string]int{}
	}
	stored := u.storedFonts()
	live := map[string]bool{}
	changed := false
	for _, s := range stored {
		live[s.family] = true
		if x.fontsSeen[s.family] == len(s.font.Data) {
			continue
		}
		x.fontsSeen[s.family] = len(s.font.Data)
		changed = true
		s := s
		fonts.LoadFace(s.family, fonts.Variant{Weight: 400}, s.font.Data, func(err error) {
			if err != nil {
				u.fontLog = append(u.fontLog, s.family+": "+err.Error())
			}
			if x.afterFont != nil {
				x.afterFont()
			}
		})
	}
	for fam := range x.fontsSeen {
		if !live[fam] {
			delete(x.fontsSeen, fam)
			changed = true
		}
	}
	if !changed {
		return
	}
	u.catalog = slices.DeleteFunc(slices.Clone(u.catalog), func(f fonts.Font) bool { return f.Source == myFonts })
	for _, s := range stored {
		u.catalog = append(u.catalog, fonts.Font{Family: s.family, Source: myFonts, Variants: []fonts.Variant{{Weight: 400}}})
	}
}

func (u *ui) isStoredFont(family string) bool {
	_, ok := u.cfgx.fontsSeen[family]
	return ok
}

func pickFile(accept string, done func(name string, data []byte)) {
	doc := js.Global().Get("document")
	in := doc.Call("createElement", "input")
	in.Set("type", "file")
	in.Set("accept", accept)
	in.Get("style").Set("display", "none")
	var onChange js.Func
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := in.Get("files")
		if files.Length() == 0 {
			return nil
		}
		file := files.Index(0)
		name := file.Get("name").String()
		var onBuf js.Func
		onBuf = js.FuncOf(func(_ js.Value, args []js.Value) any {
			arr := js.Global().Get("Uint8Array").New(args[0])
			b := make([]byte, arr.Get("length").Int())
			js.CopyBytesToGo(b, arr)
			done(name, b)
			onBuf.Release()
			return nil
		})
		file.Call("arrayBuffer").Call("then", onBuf)
		in.Call("remove")
		onChange.Release()
		return nil
	})
	in.Call("addEventListener", "change", onChange)
	doc.Get("body").Call("appendChild", in)
	in.Call("click")
}

func (u *ui) addFont(renderUI func()) {
	x := &u.cfgx
	d := x.font
	item := fontItem(d.family)
	total := 0
	for _, s := range u.storedFonts() {
		if s.item != item {
			total += len(s.font.Data)
		}
	}
	var blob []byte
	var err error
	switch {
	case d.data == nil:
		x.fontMsg = T("fonts.needFile", "Choose a font file first.")
	case item == "":
		x.fontMsg = T("fonts.needName", "Give the font a name first.")
	case len(d.data) > userconfig.DefaultMaxFont:
		x.fontMsg = T("fonts.tooBig", "That font file is over 4 MiB.")
	case total+len(d.data) > userconfig.DefaultMaxFonts:
		x.fontMsg = T("fonts.tooMany", "Your fonts together would be over 16 MiB; delete one first.")
	default:
		if blob, err = userconfig.EncodeFont(d.data, d.licence); err != nil {
			switch {
			case errors.Is(err, userconfig.ErrNoLicense):
				x.fontMsg = T("fonts.needLicense", "Write the licence note first: who made the font and under what terms it may be used.")
			case errors.Is(err, userconfig.ErrNotAFont):
				x.fontMsg = T("fonts.notFont", "That file is not a TrueType, OpenType or WOFF font.")
			default:
				x.fontMsg = T("fonts.licenseLong", "The licence note is too long (the limit is 2 KiB).")
			}
			break
		}
		u.config().Set(item, blob)
		x.fontMsg = T("fonts.added", "Font %s added. It is in the font list under My fonts.", strings.TrimPrefix(item, "font."))
		x.font = fontDraft{}
		u.loadStoredFonts()
		u.syncThemes(renderUI)
	}
	u.announce(x.fontMsg)
	renderUI()
}

func (u *ui) fontSections(renderUI func()) xango.Node {
	x := &u.cfgx
	d := &x.font
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("fonts.title", "My fonts"))),
		xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(T("fonts.hint", "Add a font file you have the right to use, for example one made for easier reading. It is kept in your settings (on this device, and on the server if you turned that on), listed under My fonts, and never put in a document or shared."))),
		btn("fonts-choose", T("fonts.choose", "Choose a font file…"), func() {
			pickFile(".ttf,.otf,.woff,.woff2,font/ttf,font/otf,font/woff,font/woff2", func(name string, data []byte) {
				d.data, d.file = data, name
				if d.family == "" {
					d.family = strings.TrimSuffix(name, name[strings.LastIndex(name, "."):])
				}
				renderUI()
			})
		}, false),
	}
	if d.file != "" {
		nodes = append(nodes, para("fonts-file", T("fonts.chosen", "Chosen: %s", d.file), false))
	}
	nodes = append(nodes,
		lineInput("fonts-family", T("fonts.name", "Name for the font"), d.family, "", func(v string) { d.family = v }),
		textarea("fonts-licence", T("fonts.licence", "Licence: who made the font and under what terms you may use it"), d.licence, "", "3", func(v string) { d.licence = v }),
		btn("fonts-add", T("fonts.add", "Add the font"), func() { u.addFont(renderUI) }, false))
	if x.fontMsg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "fonts-message"), xango.A("role", "status")}, xango.Text(x.fontMsg)))
	}
	var items []xango.Node
	for _, s := range u.storedFonts() {
		s := s
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item gj-font-stored")},
			xango.El("strong", nil, xango.Text(s.family)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(fmt.Sprintf(" %s, %d KB. ", strings.ToUpper(s.font.Format), (len(s.font.Data)+1023)/1024))),
			xango.El("span", []xango.Attr{xango.A("class", "gj-font-licence")}, xango.Text(s.font.License)),
			btn("fonts-delete-"+s.family, T("fonts.delete", "Delete %s", s.family), func() {
				u.config().Delete(s.item)
				u.loadStoredFonts()
				u.syncThemes(renderUI)
				renderUI()
			}, false)))
	}
	if len(items) == 0 {
		nodes = append(nodes, para("fonts-none", T("fonts.none", "You have added no fonts."), false))
	} else {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("aria-label", T("fonts.title", "My fonts"))}, items...))
	}
	return xango.El("section", []xango.Attr{xango.A("class", "gj-fonts-mine"), xango.A("aria-label", T("fonts.title", "My fonts"))}, nodes...)
}
