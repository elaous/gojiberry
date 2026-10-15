// Package exportgate holds the operator's minimum export gate (roadmap 62) on the server: where the
// documents are stored or sent, it runs a11ycheck.Check and GateMode.Decide on a docfile.Document, so the
// minimum no longer depends on a page the person can edit in their own browser.
//
// Two pieces. Decide judges the bytes of one stored document; use it anywhere a host sends a document on
// (a mail job, a converter, a publish step). Wrap puts the same judgement in front of the docstore
// server's one export-bound route:
//
//	GET /docs/{id}?for=export        (or the header  X-Gojiberry-Purpose: export)
//
// The docstore server has no exporter of its own: the formats are written by the client or by the host's
// own jobs, and those jobs fetch the document through this route. The editor's ordinary load and save
// (GET and PUT /docs/{id} without the purpose) are untouched, or nobody could open a document that has
// errors in order to fix it. So the gate holds for every integration that asks for the document the way
// this package documents; a client that can already read the document and fetches it without saying why
// gets the stored bytes, as it always could. The gate is a floor for the host's own export and send
// paths, not a way to stop a reader copying what they may read.
//
// The operator sets the mode with GOJIBERRY_DOCSTORE_EXPORT_GATE=warn|block (cmd/docstore). There is no
// per-request override: the page's "allow this export anyway" is for the person's own choice, and an
// operator who sets block has decided there is none (a11ycheck.OverrideAllowed).
//
// A document the server cannot read as a document (the ciphertext of an end-to-end encrypted store, or a
// file that is not one) cannot be checked. Under block it is refused, since the gate cannot say it is
// fine; under warn it is served with the header X-Gojiberry-Export-Gate: unchecked. An operator whose
// store is end-to-end encrypted should keep the gate in the client and in the host's decrypting job, which
// can make the same judgement on what it has decrypted with DecideSealed.
package exportgate

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/docfile"
)

// Headers the gate sets on an export-bound answer.
const (
	// PurposeHeader marks a request as going to an export or a send (the query ?for=export does the same).
	PurposeHeader = "X-Gojiberry-Purpose"
	// GateHeader is "ok", "warned" (errors were found and the document was served), "blocked" or
	// "unchecked" (the bytes are not a document the gate can read).
	GateHeader = "X-Gojiberry-Export-Gate"
	// ErrorsHeader is how many errors the check found.
	ErrorsHeader = "X-Gojiberry-Export-Errors"
)

// Result is the gate's judgement of one document.
type Result struct {
	// Allow says whether the document may be exported or sent.
	Allow bool
	// Errors counts the findings of severity Error; Checked is false when the bytes are not a document.
	Errors  int
	Checked bool
	// State is the value of GateHeader.
	State string
}

// Decide runs the checker on the bytes of a stored document under mode. GateAllow lets everything
// through unchecked, without reading the bytes.
func Decide(data []byte, mode a11ycheck.GateMode) Result {
	if mode == a11ycheck.GateAllow {
		return Result{Allow: true, State: "unchecked"}
	}
	doc, ok := read(data)
	if !ok {
		return Result{Allow: mode != a11ycheck.GateBlock, State: "unchecked"}
	}
	allow, errs := mode.Decide(a11ycheck.Check(doc, a11ycheck.Options{}), false)
	r := Result{Allow: allow, Errors: errs, Checked: true, State: "ok"}
	switch {
	case !allow:
		r.State = "blocked"
	case errs > 0:
		r.State = "warned"
	}
	return r
}

// DecideSealed is Decide for a host that holds the keys of an end-to-end encrypted store: open turns the stored
// bytes into the document's own bytes (the host's decrypting job: its key, its envelope format), and the same
// judgement is then made on them. If open fails, the bytes cannot be judged and the result is what Decide gives
// for bytes that are not a document (refused under block, flagged under warn). The server alone cannot do this:
// it never holds the keys, so for such a store the gate belongs here, in the job that does.
func DecideSealed(data []byte, open func([]byte) ([]byte, error), mode a11ycheck.GateMode) Result {
	if mode == a11ycheck.GateAllow {
		return Result{Allow: true, State: "unchecked"}
	}
	plain, err := open(data)
	if err != nil {
		return Result{Allow: mode != a11ycheck.GateBlock, State: "unchecked"}
	}
	return Decide(plain, mode)
}

// read decodes a stored document in whichever format it was saved. Detect guesses a format from the first
// bytes and always names one, so a failed decode of every format means the bytes are not a document (or
// are ciphertext).
func read(data []byte) (docfile.Document, bool) {
	if len(data) == 0 {
		return docfile.Document{}, false
	}
	if doc, err := docfile.Detect(data).Unmarshal(data); err == nil {
		return doc, true
	}
	return docfile.Document{}, false
}

// ExportBound reports whether the request asks for a document in order to export or send it.
func ExportBound(r *http.Request) bool {
	if r.Method != http.MethodGet { // the server has no HEAD for a document
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	if path == r.URL.Path || path == "" || strings.Contains(path, "/") {
		return false // not GET /docs/{id}
	}
	return strings.EqualFold(r.URL.Query().Get("for"), "export") || strings.EqualFold(strings.TrimSpace(r.Header.Get(PurposeHeader)), "export")
}

// Wrap puts the gate in front of next (the docstore server's Handler). It runs next first, so sign-in and
// the access list decide who may read the document at all, and a refusal of the gate is seen only by
// someone who could have read it. With mode GateAllow it returns next unchanged.
func Wrap(next http.Handler, mode a11ycheck.GateMode) http.Handler {
	if mode == a11ycheck.GateAllow {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ExportBound(r) {
			next.ServeHTTP(w, r)
			return
		}
		rec := &capture{header: http.Header{}, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status != http.StatusOK {
			rec.flush(w, r.Method) // not served anyway (missing, forbidden, expired): say what the server said
			return
		}
		res := Decide(rec.body.Bytes(), mode)
		rec.header.Set(GateHeader, res.State)
		rec.header.Set(ErrorsHeader, strconv.Itoa(res.Errors))
		if res.Allow {
			rec.flush(w, r.Method)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set(GateHeader, res.State)
		h.Set(ErrorsHeader, strconv.Itoa(res.Errors))
		w.WriteHeader(http.StatusUnprocessableEntity)
		if r.Method == http.MethodHead {
			return
		}
		if res.Checked {
			_, _ = w.Write([]byte("This document has " + strconv.Itoa(res.Errors) + " accessibility errors. This server does not hand a document over for export until they are fixed.\n"))
			return
		}
		_, _ = w.Write([]byte("This document cannot be checked on the server, so it is not handed over for export.\n"))
	})
}

// capture holds an answer so the gate can look at the body before anything is sent.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header         { return c.header }
func (c *capture) WriteHeader(code int)        { c.status = code }
func (c *capture) Write(p []byte) (int, error) { return c.body.Write(p) }

// flush sends the captured answer as the inner handler wrote it.
func (c *capture) flush(w http.ResponseWriter, method string) {
	dst := w.Header()
	for k, v := range c.header {
		dst[k] = v
	}
	w.WriteHeader(c.status)
	if method != http.MethodHead {
		_, _ = w.Write(c.body.Bytes())
	}
}
