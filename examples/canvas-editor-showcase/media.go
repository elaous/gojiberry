//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/xango"
)

// Audio and video in the Pictures tab (roadmap 68): insert a clip by its address, which is refused until it has the
// text a reader needs (captions for a video, words for audio); edit each clip's title, address, transcript and
// caption track (components.CaptionEditor); and see what is still missing, in words. The editor has no player: a
// clip is a card in the text, and the HTML export writes the player.

// mediaForm is the clip being typed in the tab, the caption drafts of the clips already in the document, and the
// messages.
type mediaForm struct {
	kind, url, title, captionsURL, transcript string
	msg                                       string
	drafts                                    map[int]components.CaptionDraft
	capMsg                                    map[int]string
}

// mediaRef is a clip in the text: where it is (a caret position) and the object to edit.
type mediaRef struct {
	offset int
	obj    *canvas.MediaObject
}

// documentMedia lists the clips of the flow in order.
func documentMedia(fl *canvas.TextFlow) []mediaRef {
	if fl == nil {
		return nil
	}
	var out []mediaRef
	offset := 0
	for _, it := range fl.Slice(0, utf8.RuneCountInString(fl.EditText())) {
		switch it := it.(type) {
		case canvas.Chars:
			offset += utf8.RuneCountInString(it.Text)
		case canvas.Inline:
			if m, ok := it.Object.(*canvas.MediaObject); ok {
				out = append(out, mediaRef{offset, m})
			}
			offset++
		}
	}
	return out
}

// mediaItem is a clip object as the model has it, for the checker.
func mediaItem(m *canvas.MediaObject) docfile.MediaItem {
	return docfile.MediaItem{URL: m.URL, Kind: m.Kind, Title: m.Title, Captions: m.Captions, CaptionsURL: m.CaptionsURL, Transcript: m.Transcript, Width: m.Width, Height: m.Height}
}

// mediaErrors counts the errors of the clip's findings.
func mediaErrors(m docfile.MediaItem) int {
	n := 0
	for _, f := range a11ycheck.MediaFindings(m, 0, 0) {
		if f.Severity == a11ycheck.Error {
			n++
		}
	}
	return n
}

// syncMediaMarks sets the "needs text" mark of every clip card from the checker, and says whether any changed.
func syncMediaMarks(fl *canvas.TextFlow) bool {
	changed := false
	for _, r := range documentMedia(fl) {
		if need := mediaErrors(mediaItem(r.obj)) > 0; need != r.obj.Needs {
			r.obj.Needs, changed = need, true
		}
	}
	return changed
}

// safeMediaURL says an address is one a clip may be fetched from: http, https, a relative path or a data address of
// audio or video (never a script).
func safeMediaURL(u string) bool {
	u = strings.TrimSpace(strings.ToLower(u))
	switch {
	case u == "":
		return false
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "data:audio/"), strings.HasPrefix(u, "data:video/"):
		return true
	}
	return !strings.Contains(strings.SplitN(u, "/", 2)[0], ":") // no scheme: a relative path
}

// insertMedia puts the typed clip at the caret, once it has the text it needs.
func (u *ui) insertMedia(fl *canvas.TextFlow, redraw, renderUI func()) {
	f := &u.media
	kind := f.kind
	if kind == "" {
		kind = docfile.MediaVideo
	}
	item := docfile.MediaItem{URL: strings.TrimSpace(f.url), Kind: kind, Title: strings.TrimSpace(f.title), CaptionsURL: strings.TrimSpace(f.captionsURL), Transcript: strings.TrimSpace(f.transcript)}
	switch {
	case item.URL == "":
		f.msg = T("media.needURL", "Write the address of the clip first.")
	case !safeMediaURL(item.URL) || (item.CaptionsURL != "" && !safeMediaURL(item.CaptionsURL)):
		f.msg = T("media.badURL", "Use an address that starts with http:// or https://, or a path on this site.")
	default:
		for _, fi := range a11ycheck.MediaFindings(item, 0, 0) {
			if fi.Severity == a11ycheck.Error {
				f.msg = fi.Message + " " + fi.Fix + " " + T("media.afterInsert", "(Captions can also be written after the clip is in, in its caption editor.)")
				renderUI()
				return
			}
		}
		pos := fl.Cursors()[0].Caret
		obj := &canvas.MediaObject{URL: item.URL, Kind: item.Kind, Title: item.Title, CaptionsURL: item.CaptionsURL, Transcript: item.Transcript}
		fl.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: obj, Block: true}}}})
		fl.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
		*f = mediaForm{drafts: f.drafts, capMsg: f.capMsg, msg: T("media.inserted", "Clip inserted.")}
		u.announce(f.msg)
		syncMediaMarks(fl)
		redraw()
	}
	renderUI()
}

// insertMediaWithoutText puts the typed clip in with no text yet, for a person who will write it in the clip's own
// editor: the card carries the mark and the Check tab lists what is missing.
func (u *ui) insertMediaWithoutText(fl *canvas.TextFlow, redraw, renderUI func()) {
	f := &u.media
	if !safeMediaURL(f.url) {
		f.msg = T("media.badURL", "Use an address that starts with http:// or https://, or a path on this site.")
		renderUI()
		return
	}
	kind := f.kind
	if kind == "" {
		kind = docfile.MediaVideo
	}
	pos := fl.Cursors()[0].Caret
	obj := &canvas.MediaObject{URL: strings.TrimSpace(f.url), Kind: kind, Title: strings.TrimSpace(f.title), CaptionsURL: strings.TrimSpace(f.captionsURL), Transcript: strings.TrimSpace(f.transcript)}
	fl.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: obj, Block: true}}}})
	fl.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
	*f = mediaForm{drafts: f.drafts, capMsg: f.capMsg, msg: T("media.insertedNoText", "Clip inserted. It still needs its text: write it below.")}
	u.announce(f.msg)
	syncMediaMarks(fl)
	redraw()
	renderUI()
}

// mediaSections is the audio and video part of the Pictures tab.
func mediaSections(u *ui, state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) []xango.Node {
	fl := flow()
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	f := &u.media
	if f.drafts == nil {
		f.drafts, f.capMsg = map[int]components.CaptionDraft{}, map[int]string{}
	}
	kinds := [][2]string{{docfile.MediaVideo, T("media.video", "Video")}, {docfile.MediaAudio, T("media.audio", "Audio")}}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("media.title", "Audio and video"))),
		selectOf("media-kind", T("media.kind", "Kind"), f.kind, kinds, func(v string) { f.kind = v }),
		lineInput("media-url", T("media.urlLabel", "Address of the clip"), f.url, "https://…", func(v string) { f.url = v }),
		lineInput("media-title", T("media.titleLabel", "Title of the clip"), f.title, T("media.titleHint", "What it is, for a reader who cannot play it"), func(v string) { f.title = v }),
		lineInput("media-capurl", T("media.capURLLabel", "Address of a caption file (WebVTT), if there is one"), f.captionsURL, "https://…", func(v string) { f.captionsURL = v }),
		textarea("media-transcript", T("media.transcriptLabel", "Transcript: everything said, and what is shown that matters"), f.transcript, "", "4", func(v string) { f.transcript = v }),
		btn("media-insert", T("media.insert", "Insert the clip"), func() { u.insertMedia(fl, redraw, renderUI) }, !canEdit),
		btn("media-insert-later", T("media.insertLater", "Insert it now and write its text next"), func() { u.insertMediaWithoutText(fl, redraw, renderUI) }, !canEdit),
	}
	if f.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "media-message"), xango.A("role", "status")}, xango.Text(f.msg)))
	}
	clips := documentMedia(fl)
	if len(clips) == 0 {
		nodes = append(nodes, para("media-none", T("media.none", "There is no audio or video in the document."), false))
	}
	var items []xango.Node
	for i, r := range clips {
		i, r := i, r
		m := r.obj
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(T("media.n", "Clip %d", i+1))),
			btn(fmt.Sprintf("media-go-%d", i), T("media.go", "Go to it"), func() {
				fl.GotoOffset(r.offset)
				scroller().RevealPending()
				redraw()
				renderUI()
			}, false),
			selectOf(fmt.Sprintf("media-kind-%d", i), T("media.kind", "Kind"), m.Kind, kinds, func(v string) { m.Kind = v; syncMediaMarks(fl); fl.Touch(); redraw(); renderUI() }),
			lineInput(fmt.Sprintf("media-title-%d", i), T("media.titleLabelN", "Title of clip %d", i+1), m.Title, "", func(v string) { m.Title = v; fl.Touch(); redraw() }),
			lineInput(fmt.Sprintf("media-url-%d", i), T("media.urlLabelN", "Address of clip %d", i+1), m.URL, "https://…", func(v string) {
				if safeMediaURL(v) {
					m.URL = v
					fl.Touch()
				}
			}),
			lineInput(fmt.Sprintf("media-capurl-%d", i), T("media.capURLLabelN", "Caption file address of clip %d", i+1), m.CaptionsURL, "https://…", func(v string) {
				if v == "" || safeMediaURL(v) {
					m.CaptionsURL = v
					syncMediaMarks(fl)
					fl.Touch()
					redraw()
					renderUI()
				}
			}),
			textarea(fmt.Sprintf("media-transcript-%d", i), T("media.transcriptLabelN", "Transcript of clip %d", i+1), m.Transcript, "", "4", func(v string) {
				m.Transcript = v
				syncMediaMarks(fl)
				fl.Touch()
				redraw()
				renderUI()
			}),
			components.CaptionEditor(components.CaptionEditorProps{
				ID: fmt.Sprintf("media-cap-%d", i), Track: m.Captions, Disabled: !canEdit,
				OnTrack: func(v string) { m.Captions = v; syncMediaMarks(fl); fl.Touch(); redraw(); renderUI() },
				Draft:   f.drafts[i], OnDraft: func(d components.CaptionDraft) { f.drafts[i] = d },
				OnAdd: func() {
					track, msg := components.AddCaption(m.Captions, f.drafts[i])
					f.capMsg[i] = msg
					if track != m.Captions {
						m.Captions = track
						f.drafts[i] = components.CaptionDraft{}
						syncMediaMarks(fl)
						fl.Touch()
						redraw()
					}
					renderUI()
				},
				Message: f.capMsg[i],
			}),
		}
		// what the clip still needs, in words (the same rules as the Check tab)
		for j, fi := range a11ycheck.MediaFindings(mediaItem(m), r.offset, i) {
			kids = append(kids, para(fmt.Sprintf("media-missing-%d-%d", i, j), fi.Severity.String()+": "+fi.Message+" "+fi.Fix, true))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item")}, kids...))
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...))
	}
	syncMediaMarks(fl)
	return nodes
}
