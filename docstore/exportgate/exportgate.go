package exportgate

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/docfile"
)

// Headers the gate sets on an export-bound response.
const (
	// PurposeHeader marks a request as going to an export.
	PurposeHeader = "X-Gojiberry-Purpose"
	// GateHeader is ok, warned, or blocked.
	GateHeader = "X-Gojiberry-Export-Gate"
	// ErrorsHeader is how many errors the check found.
	ErrorsHeader = "X-Gojiberry-Export-Errors"
)

// Result is the gate's judgement of one document.
type Result struct {
	// Allow says whether the document may be exported.
	Allow bool
	// Errors counts the findings of severity error.
	Errors  int
	Checked bool
	// State is the value of GateHeader.
	State string
}

// Decide runs the checker on the document's bytes.
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

// DecideSealed is Decide for a sealed document.
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

func read(data []byte) (docfile.Document, bool) {
	if len(data) == 0 {
		return docfile.Document{}, false
	}
	if doc, err := docfile.Detect(data).Unmarshal(data); err == nil {
		return doc, true
	}
	return docfile.Document{}, false
}

// ExportBound reports whether the request is an export.
func ExportBound(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	if path == r.URL.Path || path == "" || strings.Contains(path, "/") {
		return false // not GET /docs/{id}
	}
	return strings.EqualFold(r.URL.Query().Get("for"), "export") || strings.EqualFold(strings.TrimSpace(r.Header.Get(PurposeHeader)), "export")
}

// Wrap puts the gate in front of next.
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
			rec.flush(w, r.Method)
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

type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header         { return c.header }
func (c *capture) WriteHeader(code int)        { c.status = code }
func (c *capture) Write(p []byte) (int, error) { return c.body.Write(p) }

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
