//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/tokens"
	"github.com/elaous/gojiberry/userconfig"
	cclient "github.com/elaous/gojiberry/userconfig/client"
	"github.com/elaous/gojiberry/xango"
)

const themeCacheKey = "showcase:config"

type themeCache struct{}

func (themeCache) Load() map[string]cclient.Entry {
	m := map[string]cclient.Entry{}
	_ = json.Unmarshal([]byte(lsGet(themeCacheKey)), &m)
	return m
}

func (themeCache) Save(m map[string]cclient.Entry) {
	b, _ := json.Marshal(m)
	lsSet(themeCacheKey, string(b))
}

type themeLibState struct {
	c      *cclient.Client
	url    string // the server the client talks to ("" none)
	draft  string // the name being typed
	status string
}

var themeSlug = regexp.MustCompile(`[^a-z0-9._-]+`)

func themeItem(name string) string {
	slug := strings.Trim(themeSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-.")
	if slug == "" {
		return ""
	}
	if len(slug) > 56 {
		slug = slug[:56]
	}
	return userconfig.KindTokens + "." + slug
}

func (u *ui) config() *cclient.Client {
	want := ""
	if u.enc != nil {
		want = u.enc.dir.url
	}
	l := &u.themeLib
	if l.c == nil || l.url != want {
		l.url = want
		l.c = cclient.New(want, themeCache{})
		if want != "" && u.enc != nil && u.enc.dir.token != "" {
			tok := u.enc.dir.token
			l.c.Header = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
		}
	}
	return l.c
}

func (u *ui) syncThemes(renderUI func()) {
	c := u.config()
	if u.themeLib.url == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		switch err := c.Sync(ctx); err {
		case nil:
			u.themeLib.status = T("theme.synced", "Saved themes are in step with the server.")
			u.afterSync(renderUI)
		case cclient.ErrSignedOut:
			u.themeLib.status = T("theme.signedOut", "Sign in to the server to keep saved themes there; they stay on this device.")
		default:
			u.themeLib.status = T("theme.offline", "The server could not be reached; saved themes stay on this device and are sent later.")
		}
		renderUI()
	}()
}

func (u *ui) savedThemes() []struct {
	item string
	set  tokens.Set
} {
	c := u.config()
	var out []struct {
		item string
		set  tokens.Set
	}
	for _, n := range c.Names() {
		if !strings.HasPrefix(n, userconfig.KindTokens+".") {
			continue
		}
		data, err := c.Get(n)
		if err != nil {
			continue
		}
		var s tokens.Set
		if json.Unmarshal(data, &s) != nil {
			continue
		}
		if s.Name == "" {
			s.Name = strings.TrimPrefix(n, userconfig.KindTokens+".")
		}
		out = append(out, struct {
			item string
			set  tokens.Set
		}{n, s})
	}
	slices.SortFunc(out, func(a, b struct {
		item string
		set  tokens.Set
	}) int {
		return strings.Compare(a.item, b.item)
	})
	return out
}

func (u *ui) themeLibrary(use func(tokens.Set), renderUI func()) xango.Node {
	l := &u.themeLib
	var rows []xango.Node
	for _, st := range u.savedThemes() {
		st := st
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-theme-saved")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-theme-saved-name")}, xango.Text(st.set.Name)),
			components.Button(components.ButtonProps{Content: []xango.Node{xango.Text(T("theme.use", "Use")), xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(" "+st.set.Name))}, Variant: components.ButtonSecondary, OnClick: func(xango.Event) {
				if len(st.set.Validate()) > 0 {
					l.status = T("theme.cannotUse", "That theme cannot be read comfortably; fix its colours first.")
					renderUI()
					return
				}
				use(st.set)
			}}),
			components.Button(components.ButtonProps{Content: []xango.Node{xango.Text(T("theme.share", "Share")), xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(" "+st.set.Name))}, Variant: components.ButtonSecondary, OnClick: func(xango.Event) {
				u.shareTheme(st.item, renderUI)
			}}),
			components.Button(components.ButtonProps{Content: []xango.Node{xango.Text(T("theme.delete", "Delete")), xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(" "+st.set.Name))}, Variant: components.ButtonSecondary, OnClick: func(xango.Event) {
				u.config().Delete(st.item)
				u.syncThemes(renderUI)
				renderUI()
			}})))
	}
	children := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("theme.savedTitle", "Saved themes"))),
		xango.El("label", []xango.Attr{xango.A("class", "gj-theme-row"), xango.A("for", "gj-theme-name")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-theme-label")}, xango.Text(T("theme.name", "Name for this theme"))),
			xango.El("input", []xango.Attr{xango.A("id", "gj-theme-name"), xango.A("type", "text"), xango.A("value", l.draft), xango.A("class", "gj-theme-input"), xango.A("autocomplete", "off"),
				xango.On("input", func(ev xango.Event) { l.draft = xango.EventValue[string](ev) })})),
		components.Button(components.ButtonProps{Label: T("theme.save", "Save this theme"), Variant: components.ButtonSecondary, OnClick: func(xango.Event) {
			item := themeItem(l.draft)
			set := u.theme.current()
			switch {
			case item == "":
				l.status = T("theme.needName", "Give the theme a name first.")
			case len(set.Validate()) > 0:
				l.status = T("theme.notSaved", "Fix what is listed above before saving the theme.")
			default:
				set.Name = strings.TrimSpace(l.draft)
				b, _ := json.Marshal(set)
				u.config().Set(item, b)
				l.status = T("theme.saved", "Theme saved on this device.")
				l.draft = ""
				u.syncThemes(renderUI)
			}
			renderUI()
		}}),
	}
	if len(rows) == 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(T("theme.noneSaved", "No saved themes yet."))))
	} else {
		children = append(children, xango.El("ul", []xango.Attr{xango.A("class", "gj-theme-saved-list"), xango.A("aria-label", T("theme.savedTitle", "Saved themes"))}, rows...))
	}
	children = append(children, u.shareNodes(renderUI)...)
	if l.status != "" {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta"), xango.A("role", "status")}, xango.Text(l.status)))
	}
	if n := u.config().Pending(); n > 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(Tn("theme.pending", "%d change waits to be sent to the server.", "%d changes wait to be sent to the server.", n))))
	}
	return xango.El("section", []xango.Attr{xango.A("class", "gj-theme-library"), xango.A("aria-label", T("theme.savedTitle", "Saved themes"))}, children...)
}
