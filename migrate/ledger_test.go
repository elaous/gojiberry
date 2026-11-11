package migrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
)

type stubSource struct {
	t        *testing.T
	modified map[string]time.Time
	fetched  []string
}

func (s *stubSource) Label() string { return "Stub Drive" }
func (s *stubSource) List(context.Context, string) ([]Item, error) {
	return []Item{{ID: "a", Name: "A.docx", Modified: s.modified["a"]}, {ID: "b", Name: "B.docx", Modified: s.modified["b"]}}, nil
}
func (s *stubSource) Fetch(_ context.Context, it Item) ([]byte, error) {
	s.fetched = append(s.fetched, it.ID)
	return wordFile(s.t, "text of "+it.ID), nil
}
func (s *stubSource) Versions(_ context.Context, it Item, max int) ([]Version, error) {
	if it.ID != "a" {
		return nil, nil
	}
	v := []Version{{Saved: time.Unix(100, 0), Data: wordFile(s.t, "old a")}, {Saved: time.Unix(200, 0), Data: []byte("not a word file")}, {Saved: time.Unix(300, 0), Data: wordFile(s.t, "newer a")}}
	return v[:min(max, len(v))], nil
}

type captureSink struct{ docs map[string]docfile.Document }

func (c *captureSink) Save(_ context.Context, _ []string, name, _ string, d docfile.Document) error {
	if c.docs == nil {
		c.docs = map[string]docfile.Document{}
	}
	c.docs[name] = d
	return nil
}

func TestARunLeavesWhatAnEarlierRunBroughtAndTakesWhatChanged(t *testing.T) {
	ctx := context.Background()
	sink, sv := newSink(t)
	src := &stubSource{t: t, modified: map[string]time.Time{"a": time.Unix(1000, 0), "b": time.Unix(1000, 0)}}
	items, _ := src.List(ctx, "")
	rep, err := Run(ctx, src, items, sink, Options{Tag: "stub"})
	if err != nil || rep.Imported != 2 || rep.Skipped != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	led, err := NewSaverLedger(ctx, sv, "mem")
	if err != nil {
		t.Fatal(err)
	}
	src.fetched = nil
	rep, err = Run(ctx, src, items, sink, Options{Ledger: led})
	if err != nil || rep.Imported != 0 || rep.Skipped != 2 || len(src.fetched) != 0 {
		t.Fatalf("%+v %v fetched %v", rep, err, src.fetched)
	}
	src.modified["b"] = time.Unix(2000, 0)
	items, _ = src.List(ctx, "")
	rep, err = Run(ctx, src, items, sink, Options{Ledger: led})
	if err != nil || rep.Imported != 1 || rep.Skipped != 1 || len(src.fetched) != 1 || src.fetched[0] != "b" {
		t.Fatalf("%+v %v fetched %v", rep, err, src.fetched)
	}
	src.fetched = nil
	rep, err = Run(ctx, src, items, sink, Options{Ledger: led, Again: true})
	if err != nil || rep.Imported != 2 || rep.Skipped != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, ok := led.Brought(tagOf("Other Drive") + ":a"); ok {
		t.Error("another platform's file is known")
	}
}

func TestOriginTagsRoundTrip(t *testing.T) {
	src := &stubSource{}
	it := Item{ID: "x:y", Name: "n", Modified: time.Unix(1234, 0)}
	led := ParseOrigins([]string{"work", OriginTag(src, it), "from:broken", "from:a:b:notanumber", OriginTag(src, Item{ID: "x:y", Modified: time.Unix(99, 0)})})
	if at, ok := led.Brought(OriginKey(src, it)); !ok || at.Unix() != 1234 {
		t.Errorf("%v %v: the latest time counts, and an ID with a colon survives", at, ok)
	}
	if len(led) != 1 {
		t.Errorf("%v", led)
	}
}

func TestEarlierVersionsComeAlongAsHistory(t *testing.T) {
	ctx := context.Background()
	src := &stubSource{t: t, modified: map[string]time.Time{}}
	items, _ := src.List(ctx, "")
	sink := &captureSink{}
	rep, err := Run(ctx, src, items, sink, Options{History: 5})
	if err != nil || rep.Imported != 2 || rep.Versions != 2 || rep.VersionsFailed != 1 {
		t.Fatalf("%+v %v: two versions read, one that was not a Word file counted", rep, err)
	}
	h := sink.docs["A"].History
	if len(h) != 2 || h[0].Saved != time.Unix(100, 0).UnixNano() || h[1].Saved != time.Unix(300, 0).UnixNano() || !strings.Contains(docText(*h[0].Doc), "old a") {
		t.Errorf("history: %+v", h)
	}
	if len(sink.docs["B"].History) != 0 {
		t.Error("a document with no versions has no history")
	}
	sink = &captureSink{}
	if rep, _ = Run(ctx, src, items, sink, Options{}); rep.Versions != 0 || len(sink.docs["A"].History) != 0 {
		t.Errorf("%+v", rep)
	}
}

func TestGoogleDocsRevisionsAreExportedNewestFewOldestFirstAndTheTokenStaysHome(t *testing.T) {
	foreign := 0
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreign++ }))
	defer other.Close()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/drive/v3/files/d1/revisions":
			link := func(id string) map[string]string { return map[string]string{docxMIME: srv.URL + "/export/" + id} }
			_ = json.NewEncoder(w).Encode(map[string]any{"revisions": []map[string]any{
				{"id": "1", "modifiedTime": "2026-01-01T00:00:00Z", "exportLinks": link("1")},
				{"id": "2", "modifiedTime": "2026-01-02T00:00:00Z", "exportLinks": map[string]string{docxMIME: other.URL + "/steal"}},
				{"id": "3", "modifiedTime": "2026-01-03T00:00:00Z", "exportLinks": link("3")},
				{"id": "4", "modifiedTime": "2026-01-04T00:00:00Z", "exportLinks": link("4")}, // the current one
			}})
		case strings.HasPrefix(r.URL.Path, "/export/"):
			_, _ = w.Write(wordFile(t, "revision "+strings.TrimPrefix(r.URL.Path, "/export/")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := &GoogleDocs{Token: "tok", BaseURL: srv.URL}
	vs, err := g.Versions(context.Background(), Item{ID: "d1"}, 10)
	if err != nil || len(vs) != 2 || vs[0].Saved.Day() != 1 || vs[1].Saved.Day() != 3 {
		t.Fatalf("%+v %v: the current revision is left out, the one linked at another host is not fetched", vs, err)
	}
	if foreign != 0 {
		t.Errorf("the token was sent to another host %d times", foreign)
	}
	if vs, _ = g.Versions(context.Background(), Item{ID: "d1"}, 1); len(vs) != 1 || vs[0].Saved.Day() != 3 {
		t.Errorf("only the newest of the earlier ones: %+v", vs)
	}
	var _ Versioned = g
}

func TestSessionSkipsWhatIsThereAndOffersHistoryForGoogleDocs(t *testing.T) {
	srv := fakeDrive(t)
	sink, sv := newSink(t)
	s := NewSession([]SourceChoice{{Name: "google", Label: "Google Docs", New: func(tok string) Source { return &GoogleDocs{Token: tok, BaseURL: srv.URL} }}}, func(c docfile.Codec) Sink { sink.Codec = c; return sink })
	s.Pending.Inline = true
	s.Known = func(ctx context.Context) (Ledger, error) { return NewSaverLedger(ctx, sv, "mem") }
	s.SetSource("google")
	s.SetToken("tok")
	s.Props(nil).OnConnect()
	p := s.Props(nil)
	if p.OnAgain == nil || p.OnHistory == nil || p.History || p.Again || p.HistoryMax != 20 {
		t.Fatalf("the choices: %+v", p)
	}
	p.OnToggle("d1")
	s.Props(nil).OnStart()
	if p = s.Props(nil); p.Imported != 1 || p.Skipped != 0 {
		t.Fatalf("%+v", p)
	}
	s.Props(nil).OnStart() // d1 is still selected
	if p = s.Props(nil); p.Imported != 0 || p.Skipped != 1 {
		t.Fatalf("the second time it is left: %+v", p)
	}
	s.Props(nil).OnAgain(true)
	s.Props(nil).OnStart()
	if p = s.Props(nil); p.Imported != 1 || p.Skipped != 0 || !p.Again {
		t.Fatalf("asked to bring it again: %+v", p)
	}
}

func TestWordFilesFromTheComputerCarryNoOriginAndAreNeverSkipped(t *testing.T) {
	ctx := context.Background()
	files := Files{{Name: "a.docx", Data: wordFile(t, "a")}}
	items, _ := files.List(ctx, "")
	sink := &captureSink{}
	if _, err := Run(ctx, files, items, sink, Options{}); err != nil {
		t.Fatal(err)
	}
	if tags := sink.docs["a"].Tags(); len(tags) != 0 {
		t.Errorf("tags %v", tags)
	}
	rep, err := Run(ctx, Files{{Name: "b.docx", Data: wordFile(t, "b")}}, items, sink, Options{Ledger: MapLedger{OriginKey(files, items[0]): time.Now()}})
	if err != nil || rep.Imported != 1 || rep.Skipped != 0 {
		t.Errorf("%+v %v", rep, err)
	}
}
