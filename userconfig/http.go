package userconfig

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

// Service serves a signed-in person's items over HTTP, under Prefix (default "/me/config"):
//
//	GET    /me/config          {"items":["prefs","styles"]}
//	GET    /me/config/{name}   the item's bytes; ETag is its revision
//	PUT    /me/config/{name}   store the body; If-Match: "<revision>" is required ("0" for a new item)
//	DELETE /me/config/{name}   remove it; If-Match is required too
//
// A stale If-Match gets 412 with the current ETag, so the client can fetch, merge and send again.
// PUT and DELETE need If-Match so that a page on another site cannot make a signed-in browser
// change settings: sending that header cross-origin takes a CORS preflight, which the guard
// answers. Nothing is signed in: 401, and no body says whether an item exists. Answers are
// no-store. The server keeps the bytes it is sent: a client that wants them hidden from the operator
// encrypts them with the owner's keys before the PUT, as it does a document. Mount it with docstore.Server.WithRoutes(prefix + "/", svc).
type Service struct {
	A docstore.Adapter
	// Who names the person making the request (docstore.Server.UserKey).
	Who    func(*http.Request) (user string, ok bool)
	Prefix string
	// The limits are passed to the Store (zero: the defaults).
	MaxItem, MaxTotal, MaxFont, MaxFonts int
}

func (v *Service) prefix() string {
	if v.Prefix != "" {
		return strings.TrimRight(v.Prefix, "/")
	}
	return "/me/config"
}

// ServeHTTP implements http.Handler.
func (v *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := v.Who(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	st := &Store{A: v.A, User: user, MaxItem: v.MaxItem, MaxTotal: v.MaxTotal, MaxFont: v.MaxFont, MaxFonts: v.MaxFonts}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, v.prefix()), "/")
	if name == "" {
		v.list(w, r, st)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		it, err := st.Get(r.Context(), name)
		if v.fail(w, err) {
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Etag", `"`+strconv.Itoa(it.Rev)+`"`)
		_, _ = w.Write(it.Data)
	case http.MethodPut:
		base, ok := ifMatch(w, r)
		if !ok {
			return
		}
		item, _ := st.limitsFor(name)
		limit := int64(item) + 1
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		it, err := st.Put(r.Context(), name, data, base)
		if errors.Is(err, ErrConflict) {
			w.Header().Set("Etag", `"`+strconv.Itoa(it.Rev)+`"`)
		}
		if v.fail(w, err) {
			return
		}
		w.Header().Set("Etag", `"`+strconv.Itoa(it.Rev)+`"`)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		base, ok := ifMatch(w, r)
		if !ok {
			return
		}
		cur, err := st.Get(r.Context(), name)
		if v.fail(w, err) {
			return
		}
		if cur.Rev != base {
			w.Header().Set("Etag", `"`+strconv.Itoa(cur.Rev)+`"`)
			http.Error(w, "the item changed", http.StatusPreconditionFailed)
			return
		}
		if v.fail(w, st.Delete(r.Context(), name)) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (v *Service) list(w http.ResponseWriter, r *http.Request, st *Store) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	names, err := st.List(r.Context())
	if v.fail(w, err) {
		return
	}
	if names == nil {
		names = []string{}
	}
	b := []byte(`{"items":[`)
	for i, n := range names {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendQuote(b, n) // names are a-z0-9._-: a plain quoted string is valid JSON
	}
	b = append(b, ']', '}')
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// fail answers an error (and reports true), or reports false for nil. The answers carry no stored
// value and no detail of the store.
func (v *Service) fail(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		http.Error(w, "no such item", http.StatusNotFound)
	case errors.Is(err, ErrNoLicense), errors.Is(err, ErrNotAFont):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, ErrBadName):
		http.Error(w, "a name is 1 to 64 of a-z, 0-9, '.', '_' and '-'", http.StatusBadRequest)
	case errors.Is(err, ErrConflict):
		http.Error(w, "the item changed", http.StatusPreconditionFailed)
	case errors.Is(err, ErrQuota):
		http.Error(w, "over the storage limit", http.StatusRequestEntityTooLarge)
	default:
		http.Error(w, "the store could not answer", http.StatusServiceUnavailable)
	}
	return true
}

// ifMatch reads the required If-Match header as a revision.
func ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	h := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	n, err := strconv.Atoi(h)
	if h == "" || err != nil || n < 0 {
		http.Error(w, `If-Match: "<revision>" is required ("0" for a new item)`, http.StatusPreconditionRequired)
		return 0, false
	}
	return n, true
}
