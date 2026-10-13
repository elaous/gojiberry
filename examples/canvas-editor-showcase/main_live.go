//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/ws"
	collabxango "github.com/elaous/gojiberry/collab/xango"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
	"net/http"
	"slices"
	"strings"
	"syscall/js"
	"time"
)

// liveProps makes the page a shared document when it was opened on /d/{doc} (or ?doc=):
// the Collab element does the connecting; this says where, as whom, over what
// (?transport=ws for a WebSocket) and what to do when the state changes.
func liveProps(u *ui, state *State, origin string, renderCanvas, renderUI func()) *collabxango.CollabProps {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	doc := pageDoc()
	if doc == "" {
		return nil
	}
	get := func(k, def string) string {
		if q.Call("has", k).Bool() {
			return q.Call("get", k).String()
		}
		return def
	}
	name := get("name", "guest")
	hdr := http.Header{}
	tok := get("token", "")
	if tok != "" {
		hdr.Set("Authorization", "Bearer "+tok)
	}
	var transport collab.Transport
	if get("transport", "http") == "ws" {
		transport = ws.WebSockets()
	}
	u.liveName, u.liveColor = name, get("color", "#7c5cff")
	return &collabxango.CollabProps{
		Transport: transport,
		Flow:      collabxango.Flow(get("flow", "")),
		Client:    client.Options{BaseURL: hubURL(get("hub", ""), origin), Doc: doc, Header: hdr, Retry: 300 * time.Millisecond, MaxRetry: 2 * time.Second, Queue: newLocalQueue(doc, name)},
		Replica:   fmt.Sprintf("%s-%d", name, int(js.Global().Get("Math").Call("random").Float()*1e9)),
		Name:      name,
		Color:     u.liveColor,
		OnState: func(st collabxango.State) {
			u.live = &st
			u.noteCollab(renderUI)
			if role := canvas.ParseRole(string(st.Role)); st.Role != "" && role != state.Role {
				state.Role = role
				if !role.CanEdit() {
					state.Mode = canvas.ModeView
				}
				renderCanvas()
			}
			renderUI()
		},
	}
}

// liveState summarises a shared session for the e2e test.
func liveState(u *ui, flow *canvas.TextFlow) any {
	s := u.live
	if s == nil {
		return map[string]any{"error": ""}
	}
	names := []string{}
	for _, p := range s.Peers {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	errText := ""
	if s.Err != nil {
		errText = s.Err.Error()
	}
	return map[string]any{"role": string(s.Role), "status": s.Status.String(), "peers": names, "remote": len(flow.RemoteCursors),
		"pending": s.Pending, "repairs": s.Repairs, "error": errText}
}

func titleOf(cmds []canvas.CommandInfo, c canvas.Command) string {
	for _, ci := range cmds {
		if ci.Name == c {
			return ci.Title
		}
	}
	return string(c)
}

// resetBinding puts cmd's chords back to their defaults.
func resetBinding(stage *canvas.Stage, defaults canvas.Keymap, cmd canvas.Command) {
	stage.Rebind(cmd, "")
	for _, chord := range defaults.Chords(cmd) {
		stage.Keymap[chord] = cmd
	}
}

// livePresence shows who is at a shared document and the connection.
func livePresence(u *ui, flow func() *canvas.TextFlow, renderUI func()) xango.Node {
	s := u.live
	var peers []components.PresencePeer
	for _, p := range s.Peers {
		peers = append(peers, components.PresencePeer{Name: p.Name, Color: p.Color, User: p.User, Role: string(p.Role)})
	}
	slices.SortFunc(peers, func(a, b components.PresencePeer) int { return strings.Compare(a.Name, b.Name) })
	// "Where is <name>?" buttons are a per-person setting (View > Display preferences): off, the cursors are
	// never spoken; on, the position is given only when asked.
	var where func(components.PresencePeer)
	if curPrefs.Where == "on" {
		where = func(p components.PresencePeer) { u.sayWhere(flow(), p); renderUI() }
	}
	return xango.El("div", []xango.Attr{xango.A("id", "presence"), xango.A("style", "margin-bottom:0.8em")}, components.Presence(components.PresenceProps{
		Self: components.PresencePeer{Name: u.liveName, Color: u.liveColor, Role: string(s.Role)}, Peers: peers,
		Status: s.Status.String(), Pending: s.Pending, Role: string(s.Role), OnWhere: where,
	}))
}

// hubURL is the collaboration hub's address: the page's own /collab unless ?hub= names
// another that allowedEndpoint accepts.
func hubURL(param, origin string) string {
	if u := pageEndpoint(param); u != "" {
		return u
	}
	return origin + "/collab"
}

// downloadChoices lists the formats of the "Download as" menu under headings: the
// documents people send around first, then web and text formats, then the data formats,
// and last opendocs' own file, which carries the history too.
func downloadChoices(exporters []transfer.Exporter) []components.SaverChoice {
	groups := []struct {
		title string
		names []string
	}{
		{T("download.group.documents", "Documents"), []string{"docx", "odt", "pdf", "rtf", "epub"}},
		{T("download.group.web", "Web and text"), []string{"html", "markdown", "text"}},
		{T("download.group.data", "Data"), []string{"json", "proto-text", "proto"}},
		{T("download.group.opendocs", "opendocs"), []string{"open"}},
	}
	byName := map[string]transfer.Format{}
	for _, e := range exporters {
		byName[e.Format().Name] = e.Format()
	}
	var out []components.SaverChoice
	for _, g := range groups {
		for _, n := range g.names {
			f, ok := byName[n]
			if !ok {
				continue
			}
			label := f.Label
			if n == "open" {
				label = T("download.openWithHistory", "opendocs (.open), with version history")
			}
			out = append(out, components.SaverChoice{Name: f.Name, Label: label, Group: g.title})
			delete(byName, n)
		}
	}
	for _, e := range exporters { // any other format a host added
		if f, ok := byName[e.Format().Name]; ok {
			out = append(out, components.SaverChoice{Name: f.Name, Label: f.Label, Group: T("download.group.more", "More")})
			delete(byName, f.Name)
		}
	}
	return out
}

// publishEncState brings the "encryption" part of #state up to date. The rest of #state is written
// when the canvas is drawn, which a change in the Save or People tab (unlock, restore, a directory
// address confirmed) does not do.
func (u *ui) publishEncState() {
	el := js.Global().Get("document").Call("getElementById", "state")
	if !el.Truthy() {
		return
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(el.Get("textContent").String()), &m) != nil || m == nil {
		return
	}
	b, err := json.Marshal(u.enc.state())
	if err != nil {
		return
	}
	m["encryption"] = b
	if out, err := json.Marshal(m); err == nil {
		el.Set("textContent", string(out))
	}
}
