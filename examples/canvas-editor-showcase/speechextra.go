//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/speechcoach"
	"github.com/elaous/gojiberry/xango"
)

// ---- recordings kept between visits ----

// speechKeyFor is the key the Speech tab's kept data is sealed under, with the vault, or why there is none. With an
// encrypted store it is the unlocked owner key's (so a locked store keeps and reads nothing); without one, the
// per-device key the browser holds non-extractable (see speechkeep.go).
func (u *ui) speechKeyFor() ([]byte, sealedVault, string) {
	var secret []byte
	if e := u.enc; e != nil && (e.broken != nil || e.store.Enabled()) {
		if e.broken != nil {
			return nil, nil, T("speech.keepBroken", "The encrypted store cannot be read, so nothing is kept.")
		}
		s, ok := e.store.SearchSecret()
		if !ok {
			return nil, nil, T("speech.keepLocked", "Unlock the encrypted store first: recordings are kept sealed under its key.")
		}
		secret = s
	} else {
		s, err := deviceSecret()
		if err != nil {
			return nil, nil, T("speech.keepNoKey", "This browser cannot hold a key for them, so they are not kept.")
		}
		secret = s
	}
	key, err := speechSealKey(secret)
	if err != nil {
		return nil, nil, T("speech.keepNoKey", "This browser cannot hold a key for them, so they are not kept.")
	}
	return key, idbVault{}, ""
}

// loadKept reads what was kept, once a visit, when the person asked for that.
func (s *speechUI) loadKept() {
	if !s.keep || s.loaded || s.key == nil {
		return
	}
	s.loaded = true
	go func() {
		key, v, why := s.key()
		if key == nil {
			s.message = why
			s.render()
			return
		}
		n, err := loadSpeech(v, key, &s.data, s.kept)
		s.corr = loadCorrections(v, key)
		switch {
		case err != nil:
			s.message = T("speech.keptBad", "The recordings kept on this device could not be opened, so they were removed.")
		case n > 0:
			s.message = Tn("speech.keptOpened", "%d recording was opened from this device.", "%d recordings were opened from this device.", n)
		}
		s.render()
	}()
}

// persist brings the kept copy in step with the recordings and the corrections (when the person asked for it).
func (s *speechUI) persist() {
	if !s.keep || s.key == nil {
		return
	}
	go func() {
		key, v, why := s.key()
		if key == nil {
			s.message = why
			s.render()
			return
		}
		if err := keepSpeech(v, key, &s.data, s.kept); err != nil {
			s.message = T("speech.keepFailed", "The recordings could not be kept on this device.")
		} else if err := keepCorrections(v, key, s.corr); err != nil {
			s.message = T("speech.keepFailed", "The recordings could not be kept on this device.")
		}
		s.render()
	}()
}

// setKeep turns keeping on or off. Turning it on needs a key first; turning it off removes what was kept.
func (s *speechUI) setKeep(on bool) {
	if s.key == nil {
		return
	}
	go func() {
		key, v, why := s.key()
		if on {
			if key == nil {
				s.message = why
				s.render()
				return
			}
			s.keep, s.loaded = true, true
			lsSet(speechKeepKey, "on")
			s.message = T("speech.keepOn", "Your recordings and corrections are kept on this device, sealed. Only this browser can open them.")
			s.persist()
			s.render()
			return
		}
		s.keep, s.kept = false, map[string]bool{}
		lsSet(speechKeepKey, "")
		wipeSpeech(idbVault{})
		_ = v
		s.message = T("speech.keepOff", "Nothing is kept on this device any more. What is open in this page stays until you close it.")
		s.render()
	}()
}

func (s *speechUI) keepNode() xango.Node {
	return xango.El("div", []xango.Attr{xango.A("id", "speech-keep-box"), xango.A("style", "margin:8px 0")},
		components.Checkbox(components.CheckboxProps{Label: T("speech.keep", "Keep my recordings and corrections on this device between visits"), Checked: s.keep, OnChange: s.setKeep}),
		xango.El("p", []xango.Attr{xango.A("style", "font-size:12px;margin:2px 0")}, xango.Text(T("speech.keepHint", "Off by default. They are sealed with a key only this browser holds (or your encrypted store's key), kept nowhere else, and removed when you turn this off or delete them."))))
}

// ---- learning from corrections ----

// dictationAftermath is what follows a dictation: earlier corrections that apply to it, each with a button to use it,
// and the button that learns from the person's own correction of it (the text they selected).
func (s *speechUI) dictationAftermath() xango.Node {
	if s.last.text == "" {
		return xango.Frag(s.correctionsNode())
	}
	var items []xango.Node
	for i, sg := range s.corr.Suggest(s.last.text) {
		sg, i := sg, i
		items = append(items, xango.El("li", nil,
			xango.Text(T("speech.fixOffer", "Earlier you changed “%s” to “%s”. ", sg.Heard, sg.Meant)),
			btn(fmt.Sprintf("speech-fix-%d", i), T("speech.fixUse", "Use “%s”", sg.Meant), func() { s.applyFix(sg) }, !s.last.inserted)))
	}
	nodes := []xango.Node{
		xango.El("h4", nil, xango.Text(T("speech.lastHeading", "The last dictation"))),
		xango.El("p", []xango.Attr{xango.A("id", "speech-last"), xango.A("style", "font-size:13px")}, xango.Text(s.last.text)),
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "speech-fixes")}, items...))
	}
	nodes = append(nodes,
		xango.El("p", []xango.Attr{xango.A("style", "font-size:12px;margin:2px 0")}, xango.Text(T("speech.learnHint", "If you corrected it in the document, select the corrected words and press the button: the next dictation will offer the same fix."))),
		btn("speech-learn", T("speech.learn", "Learn from my correction"), s.learn, !s.last.inserted),
		s.correctionsNode())
	return xango.Frag(nodes...)
}

func (s *speechUI) correctionsNode() xango.Node {
	return xango.El("p", []xango.Attr{xango.A("id", "speech-corrections"), xango.A("style", "font-size:13px")},
		xango.Text(Tn("speech.corrCount", "%d correction remembered.", "%d corrections remembered.", s.corr.Len())+" "),
		btn("speech-forget", T("speech.forget", "Forget my corrections"), func() {
			s.corr.Clear()
			s.last = lastDictation{}
			s.message = T("speech.forgotten", "The corrections were forgotten.")
			s.persist()
			s.render()
		}, s.corr.Len() == 0))
}

// applyFix puts an earlier correction into the document where the dictation is, if the words are still there.
func (s *speechUI) applyFix(sg speechcoach.Suggestion) {
	l := &s.last
	if s.host.Slice == nil || s.host.Replace == nil || s.host.Slice(l.from+sg.Start, l.from+sg.End) != sg.Heard {
		s.message = T("speech.fixMoved", "The words are not where they were put, so nothing was changed.")
		s.render()
		return
	}
	s.host.Replace(l.from+sg.Start, l.from+sg.End, sg.Meant)
	rs := []rune(l.text)
	l.text = string(rs[:sg.Start]) + sg.Meant + string(rs[sg.End:])
	s.message = T("speech.fixed", "Changed to “%s”.", sg.Meant)
	s.render()
}

// learn compares the dictation with the words the person selected in the document and remembers what they changed.
func (s *speechUI) learn() {
	sel := ""
	if s.host.Selected != nil {
		sel = strings.TrimSpace(s.host.Selected())
	}
	if sel == "" {
		s.message = T("speech.learnSelect", "Select the corrected words in the document first.")
		s.render()
		return
	}
	n := s.corr.Learn(s.last.text, sel)
	if n == 0 {
		s.message = T("speech.learnNone", "Nothing to learn: the words you selected are the same, or only words were added or taken out.")
	} else {
		s.last.text = sel
		s.message = Tn("speech.learned", "Remembered %d correction.", "Remembered %d corrections.", n)
		s.persist()
	}
	s.render()
}

// ---- contributing to research ----

// researchState is the optional contribution to a research endpoint (speechcoach.Contribution): nothing is
// sent unless the person typed an address, was shown the exact package, and confirmed that package in this
// sitting.
type researchState struct {
	endpoint string
	plan     *speechcoach.Plan
	consent  speechcoach.Contribution
}

// noCredentials sends a request with the browser's fetch and nothing of the person's: no cookies or sign-in
// (credentials "omit"), no referrer, and no following a redirect to another address. The research server
// learns only the package and the random token.
type noCredentials struct{}

func (noCredentials) Do(r *http.Request) (resp *http.Response, err error) {
	defer func() {
		if p := recover(); p != nil {
			resp, err = nil, fmt.Errorf("the request could not be made: %v", p)
		}
	}()
	opts := map[string]any{"method": r.Method, "credentials": "omit", "referrerPolicy": "no-referrer", "redirect": "error", "cache": "no-store"}
	headers := js.Global().Get("Object").New()
	for k, v := range r.Header {
		if len(v) > 0 && !strings.EqualFold(k, "cookie") {
			headers.Set(k, v[0])
		}
	}
	opts["headers"] = headers
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if len(body) > 0 {
			arr := js.Global().Get("Uint8Array").New(len(body))
			js.CopyBytesToJS(arr, body)
			opts["body"] = arr
		}
	}
	res, err := domrt.Await(js.Global().Call("fetch", r.URL.String(), opts))
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: res.Get("status").Int(), Status: res.Get("statusText").String(), Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
}

func (s *speechUI) prepareResearch() {
	rs := &s.research
	plan, err := speechcoach.Prepare(rs.endpoint, s.data.List())
	switch {
	case err == speechcoach.ErrNothingToAdd:
		s.message = T("speech.resNothing", "Record something first: there is nothing to send.")
	case err != nil:
		s.message = T("speech.resBad", "That address cannot be used: it must be https (or this computer) with no name or password in it.")
	default:
		rs.plan = plan
		s.message = ""
	}
	s.render()
}

func (s *speechUI) sendResearch() {
	rs := &s.research
	if rs.plan == nil {
		return
	}
	rs.consent.Confirm(rs.plan) // the person pressed "send exactly this" for exactly this package
	plan := rs.plan
	s.message = T("speech.resSending", "Sending exactly what was shown…")
	s.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := rs.consent.Send(ctx, noCredentials{}, plan); err != nil {
			s.message = T("speech.resFailed", "The research server did not accept it. Nothing was kept there that you know of.")
		} else {
			s.message = T("speech.resSent", "Sent. You can withdraw, and ask them to delete it, with the button below.")
			rs.plan = nil
		}
		s.render()
	}()
}

func (s *speechUI) withdrawResearch() {
	rs := &s.research
	rs.plan = nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		n, err := rs.consent.Revoke(ctx, noCredentials{})
		switch {
		case err != nil:
			s.message = T("speech.resWithdrawnFailed", "Consent is withdrawn, but the research server did not confirm the deletion. Ask its operator to delete your package.")
		case n > 0:
			s.message = Tn("speech.resDeleted", "Consent is withdrawn and %d package was deleted.", "Consent is withdrawn and %d packages were deleted.", n)
		default:
			s.message = T("speech.resWithdrawn", "Consent is withdrawn. Nothing was sent in this visit.")
		}
		s.render()
	}()
}

func (s *speechUI) researchNode() xango.Node {
	rs := &s.research
	nodes := []xango.Node{
		xango.El("h4", nil, xango.Text(T("speech.resHeading", "Share recordings with research (optional)"))),
		xango.El("p", []xango.Attr{xango.A("style", "font-size:13px")}, xango.Text(T("speech.resIntro", "Nothing is sent unless you type the address of a research server you trust, look at exactly what would be sent, and confirm that. There is no address by default."))),
		lineInput("speech-research", T("speech.resAddress", "Research server address (https://…)"), rs.endpoint, "https://…", func(v string) { rs.endpoint = v; rs.plan = nil; s.render() }),
		btn("speech-prepare", T("speech.resPrepare", "Show exactly what would be sent"), s.prepareResearch, strings.TrimSpace(rs.endpoint) == "" || s.data.Len() == 0),
	}
	if p := rs.plan; p != nil {
		var items []xango.Node
		for _, it := range p.Items {
			items = append(items, xango.El("li", nil, xango.Text(fmt.Sprintf("“%s” — %s, %.1f s", it.Transcript, it.Lang, it.Seconds))))
		}
		var fields []xango.Node
		for _, f := range p.Fields {
			fields = append(fields, xango.El("li", nil, xango.Text(f)))
		}
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "speech-plan"), xango.A("role", "group"), xango.A("aria-label", T("speech.resPlan", "What would be sent"))},
			xango.El("p", nil, xango.Text(T("speech.resTo", "To %s: %d recordings, %d KB in all.", p.Endpoint, len(p.Items), (p.Bytes+1023)/1024))),
			xango.El("ul", nil, items...),
			xango.El("p", nil, xango.Text(T("speech.resFields", "Each recording carries only:"))),
			xango.El("ul", nil, fields...),
			xango.El("p", nil, xango.Text(T("speech.resNoId", "No name, account, address, device or date goes with it, and no cookie or sign-in. Each recording gets a new random number."))),
			xango.El("p", []xango.Attr{xango.A("style", "font-weight:600")}, xango.Text(T("speech.resWarn", "Once it is sent, only the research team can delete it. Withdrawing asks their server to, and shows whether it did."))),
			btn("speech-send", T("speech.resSend", "Send exactly this"), s.sendResearch, false),
			btn("speech-cancel-send", T("speech.resCancel", "Do not send"), func() { rs.plan = nil; s.render() }, false)))
	}
	nodes = append(nodes, btn("speech-withdraw", T("speech.resWithdraw", "Withdraw and ask them to delete"), s.withdrawResearch, rs.consent.Sent() == 0 && rs.plan == nil && rs.endpoint == ""))
	return xango.El("section", []xango.Attr{xango.A("id", "speech-research-section"), xango.A("aria-label", T("speech.resHeading", "Share recordings with research (optional)"))}, nodes...)
}
