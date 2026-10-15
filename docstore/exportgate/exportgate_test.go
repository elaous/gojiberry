package exportgate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// withError is a document with one error (a picture nobody described); fine has none.
func docBytes(t *testing.T, codec docfile.Codec, items ...docfile.Item) []byte {
	t.Helper()
	b, err := codec.Marshal(docfile.Document{Content: items})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func withError(t *testing.T, codec docfile.Codec) []byte {
	return docBytes(t, codec, docfile.Item{Image: &docfile.ImageItem{URL: "a.png"}}, docfile.Item{Image: &docfile.ImageItem{URL: "b.png"}})
}

func fine(t *testing.T, codec docfile.Codec) []byte {
	return docBytes(t, codec, docfile.Item{Chars: &docfile.Chars{Text: "Hello"}}, docfile.Item{Image: &docfile.ImageItem{URL: "a.png", Alt: "A graph"}})
}

func TestDecideByMode(t *testing.T) {
	for _, codec := range []docfile.Codec{docfile.JSON, docfile.ProtoBinary, docfile.ProtoText} {
		bad, good := withError(t, codec), fine(t, codec)
		for _, tc := range []struct {
			mode  a11ycheck.GateMode
			data  []byte
			allow bool
			state string
			errs  int
		}{
			{a11ycheck.GateAllow, bad, true, "unchecked", 0},
			{a11ycheck.GateWarn, bad, true, "warned", 2},
			{a11ycheck.GateBlock, bad, false, "blocked", 2},
			{a11ycheck.GateBlock, good, true, "ok", 0},
			{a11ycheck.GateWarn, good, true, "ok", 0},
		} {
			r := tc.mode
			got := Decide(tc.data, r)
			if got.Allow != tc.allow || got.State != tc.state || got.Errors != tc.errs {
				t.Errorf("%s mode %q: %+v, want allow=%v state=%s errors=%d", codec.Name(), r, got, tc.allow, tc.state, tc.errs)
			}
		}
	}
}

func TestWhatCannotBeReadAsADocumentIsRefusedUnderBlockAndFlaggedUnderWarn(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("v1"), []byte("not a document at all, just some text"), {0x9f, 0x02, 0xff, 0x00, 0x13, 0x77}} {
		if got := Decide(data, a11ycheck.GateBlock); got.Allow || got.Checked || got.State != "unchecked" {
			t.Errorf("block %q: %+v", data, got)
		}
		if got := Decide(data, a11ycheck.GateWarn); !got.Allow || got.State != "unchecked" {
			t.Errorf("warn %q: %+v", data, got)
		}
	}
}

// fixture is a real docstore server behind the gate, with two stored documents.
func fixture(t *testing.T, mode a11ycheck.GateMode) http.Handler {
	t.Helper()
	store := docstore.NewMemoryAdapter()
	ctx := context.Background()
	for id, data := range map[string][]byte{"bad": withError(t, docfile.JSON), "good": fine(t, docfile.JSON), "cipher": {0x9f, 0x02, 0xff, 0x00, 0x13, 0x77}} {
		if err := store.Save(ctx, id, data); err != nil {
			t.Fatal(err)
		}
	}
	return Wrap(docstore.NewServer(store, "memory").Handler(), mode)
}

func get(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBlockRefusesTheExportRouteButNotTheEditorsLoad(t *testing.T) {
	h := fixture(t, a11ycheck.GateBlock)
	for _, how := range []struct {
		target string
		header map[string]string
	}{{"/docs/bad?for=export", nil}, {"/docs/bad", map[string]string{PurposeHeader: "export"}}, {"/docs/bad?for=EXPORT", nil}} {
		rec := get(h, http.MethodGet, how.target, how.header)
		if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get(GateHeader) != "blocked" || rec.Header().Get(ErrorsHeader) != "2" {
			t.Errorf("%s %v: %d %v", how.target, how.header, rec.Code, rec.Header())
		}
		if strings.Contains(rec.Body.String(), "a.png") || rec.Header().Get("ETag") != "" {
			t.Errorf("the refusal carries the document: %q %v", rec.Body, rec.Header())
		}
	}
	if rec := get(h, http.MethodHead, "/docs/bad?for=export", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HEAD is the server's own answer: %d", rec.Code)
	}
	// The editor opens a document with errors to fix them.
	if rec := get(h, http.MethodGet, "/docs/bad", nil); rec.Code != http.StatusOK || rec.Header().Get(GateHeader) != "" {
		t.Errorf("plain load: %d %v", rec.Code, rec.Header())
	}
	// A document without errors goes out, with the gate's verdict on it.
	rec := get(h, http.MethodGet, "/docs/good?for=export", nil)
	if rec.Code != http.StatusOK || rec.Header().Get(GateHeader) != "ok" || rec.Body.Len() == 0 {
		t.Errorf("good: %d %v", rec.Code, rec.Header())
	}
	// What cannot be checked is not handed over under block.
	if rec := get(h, http.MethodGet, "/docs/cipher?for=export", nil); rec.Code != http.StatusUnprocessableEntity || rec.Header().Get(GateHeader) != "unchecked" {
		t.Errorf("cipher: %d %v", rec.Code, rec.Header())
	}
	// A missing document is the server's own 404, not the gate's.
	if rec := get(h, http.MethodGet, "/docs/nope?for=export", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rec.Code)
	}
}

func TestWarnServesAndSaysHowManyErrors(t *testing.T) {
	h := fixture(t, a11ycheck.GateWarn)
	rec := get(h, http.MethodGet, "/docs/bad?for=export", nil)
	if rec.Code != http.StatusOK || rec.Header().Get(GateHeader) != "warned" || rec.Header().Get(ErrorsHeader) != "2" || rec.Body.Len() == 0 {
		t.Errorf("warn: %d %v", rec.Code, rec.Header())
	}
	if rec := get(h, http.MethodGet, "/docs/cipher?for=export", nil); rec.Code != http.StatusOK || rec.Header().Get(GateHeader) != "unchecked" {
		t.Errorf("warn cipher: %d %v", rec.Code, rec.Header())
	}
}

func TestNoGateLeavesTheHandlerAlone(t *testing.T) {
	inner := http.NewServeMux()
	if Wrap(inner, a11ycheck.GateAllow) != http.Handler(inner) {
		t.Error("GateAllow wraps the handler")
	}
}

// The gate runs after the server's own access decision: someone refused there sees that refusal, never the
// gate's count of errors.
func TestAnAnswerThatIsNotTheDocumentPassesThroughUntouched(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable} {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "refused", code) })
		rec := get(Wrap(inner, a11ycheck.GateBlock), http.MethodGet, "/docs/bad?for=export", nil)
		if rec.Code != code || rec.Header().Get(GateHeader) != "" || rec.Header().Get(ErrorsHeader) != "" {
			t.Errorf("%d became %d %v", code, rec.Code, rec.Header())
		}
	}
}

func TestOnlyGetOfOneDocumentIsExportBound(t *testing.T) {
	for target, want := range map[string]bool{
		"/docs/a?for=export": true, "/docs/a": false, "/docs?for=export": false, "/docs/a/acl?for=export": false,
		"/docs/a?for=print": false, "/health?for=export": false,
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if got := ExportBound(req); got != want {
			t.Errorf("GET %s: %v, want %v", target, got, want)
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPost} {
		if ExportBound(httptest.NewRequest(m, "/docs/a?for=export", nil)) {
			t.Errorf("%s is export bound", m)
		}
	}
}

// A host with the keys judges the document it decrypts; with the wrong key, or none, it is refused under block.
func TestDecideSealedJudgesWhatTheHostDecrypts(t *testing.T) {
	bad := docfile.Document{Content: []docfile.Item{{Image: &docfile.ImageItem{URL: "a.png"}}}}
	plain, err := docfile.JSON.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	seal := func(b []byte) []byte { out := []byte("sealed:"); return append(out, b...) }
	open := func(b []byte) ([]byte, error) {
		rest, ok := strings.CutPrefix(string(b), "sealed:")
		if !ok {
			return nil, errors.New("not sealed for this key")
		}
		return []byte(rest), nil
	}
	if r := DecideSealed(seal(plain), open, a11ycheck.GateBlock); r.Allow || !r.Checked || r.Errors == 0 {
		t.Errorf("a document with an error is refused under block: %+v", r)
	}
	if r := DecideSealed(seal(plain), open, a11ycheck.GateWarn); !r.Allow || r.State != "warned" {
		t.Errorf("under warn it goes with a warning: %+v", r)
	}
	if r := DecideSealed([]byte("garbage"), open, a11ycheck.GateBlock); r.Allow || r.Checked {
		t.Errorf("what cannot be opened is refused under block: %+v", r)
	}
	if r := DecideSealed([]byte("garbage"), open, a11ycheck.GateWarn); !r.Allow || r.State != "unchecked" {
		t.Errorf("and flagged under warn: %+v", r)
	}
	if r := DecideSealed([]byte("garbage"), open, a11ycheck.GateAllow); !r.Allow {
		t.Error("no gate, no check")
	}
}
