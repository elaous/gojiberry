package speechcoach

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestCorrectionsLearnPairs(t *testing.T) {
	var c Corrections
	if n := c.Learn("please call my sisters", "please call my sisters"); n != 0 {
		t.Errorf("identical: %d", n)
	}
	if n := c.Learn("tell dr shell low about the metal", "tell Dr Schellow about the medal"); n != 2 {
		t.Errorf("noted %d, pairs %+v", n, c.pairs)
	}
	c.Learn("i want to by pass", "I want to bypass")
	if n := c.Learn("open the window", "open the window please"); n != 0 {
		t.Errorf("an added word is not a correction: %d", n)
	}
	if n := c.Learn("hello samir", "Hello Samir"); n != 0 {
		t.Errorf("capitals: %d", n)
	}
	sug := c.Suggest("Then shell low said by pass is fine")
	var got []string
	for _, s := range sug {
		got = append(got, s.Heard+">"+s.Meant)
	}
	if !slices.Equal(got, []string{"shell low>Schellow", "by pass>bypass"}) {
		t.Errorf("suggestions: %v", got)
	}
	if sug[0].Start != 5 || sug[0].End != 14 {
		t.Errorf("offsets %d-%d", sug[0].Start, sug[0].End)
	}
}

func TestCorrectionsRankByCountAndOffsets(t *testing.T) {
	var c Corrections
	c.Learn("the wether is nice", "the weather is nice")
	c.Learn("the wether is nice", "the whether is nice")
	c.Learn("a wether report", "a weather report")
	sug := c.Suggest("Wether and wether")
	if len(sug) != 4 { // two stretches, two meanings each
		t.Fatalf("%+v", sug)
	}
	if sug[0].Meant != "weather" || sug[0].Count != 2 || sug[1].Meant != "whether" {
		t.Errorf("most chosen first: %+v", sug[:2])
	}
	if sug[0].Start != 0 || sug[0].End != 6 || sug[2].Start != 11 {
		t.Errorf("rune offsets %d-%d, %d", sug[0].Start, sug[0].End, sug[2].Start)
	}
	if !slices.Equal(c.Words(), []string{"weather", "whether"}) {
		t.Errorf("words %v", c.Words())
	}
	if !c.Forget("Wether", "whether") || c.Forget("wether", "whether") || len(c.Suggest("wether")) != 1 {
		t.Error("forget one pair")
	}
	c.Clear()
	if c.Len() != 0 || c.Suggest("wether") != nil {
		t.Error("clear")
	}
}

func TestCorrectionsPersistAndAreBounded(t *testing.T) {
	var c Corrections
	c.Learn("wether", "weather")
	b, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnmarshalCorrections(b)
	if err != nil || back.Len() != 1 || back.Suggest("wether")[0].Meant != "weather" {
		t.Fatalf("round trip %v", err)
	}
	junk, _ := UnmarshalCorrections([]byte(`{"version":1,"pairs":[{"heard":"Up","meant":"x","count":1},{"heard":"a","meant":"","count":1},{"heard":"a","meant":"b","count":0},{"heard":"ok","meant":"fine","count":2,"seq":9}]}`))
	if junk.Len() != 1 || junk.seq != 9 {
		t.Errorf("kept %d", junk.Len())
	}
	if _, err := UnmarshalCorrections([]byte("{")); err == nil {
		t.Error("broken json")
	}
	var many Corrections
	for i := 0; i < maxPairs+50; i++ {
		many.note("h"+strings.Repeat("x", i%40)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)), "m")
	}
	if many.Len() > maxPairs {
		t.Errorf("unbounded: %d", many.Len())
	}
}

func TestVocabularyPrompt(t *testing.T) {
	var v Vocabulary
	for _, term := range []string{"Gojiberry", "  Samir   Aouad ", "gojiberry", "", strings.Repeat("x", MaxTermLen+1), "Dr Schellow"} {
		v.Add(term)
	}
	if !slices.Equal(v.Terms(), []string{"Gojiberry", "Samir Aouad", "Dr Schellow"}) {
		t.Fatalf("terms %v", v.Terms())
	}
	var c Corrections
	c.Learn("the medal", "the Medal-X")
	p := v.Prompt(&c, 200)
	if p != "Medal-X, Gojiberry, Samir Aouad, Dr Schellow" {
		t.Errorf("prompt %q", p)
	}
	short := v.Prompt(nil, len("Samir Aouad, Dr Schellow"))
	if short != "Samir Aouad, Dr Schellow" {
		t.Errorf("short %q", short)
	}
	if v.Prompt(nil, 3) != "" {
		t.Error("nothing fits")
	}
	if !v.Remove("DR SCHELLOW") || v.Remove("nobody") {
		t.Error("remove")
	}
	b, _ := v.Marshal()
	back, err := UnmarshalVocabulary(append(b[:len(b)-1], []byte(`,"Gojiberry"]`)...))
	if err != nil || !slices.Equal(back.Terms(), []string{"Gojiberry", "Samir Aouad"}) {
		t.Errorf("round trip %v %v", back.Terms(), err)
	}
	var full Vocabulary
	for i := 0; i < MaxTerms+5; i++ {
		full.Add(strings.Repeat("a", i%30+1) + string(rune('A'+i%26)) + strings.Repeat("b", i/30))
	}
	if len(full.Terms()) > MaxTerms {
		t.Error("unbounded vocabulary")
	}
}

func TestRecogniserSendsTheContract(t *testing.T) {
	var gotAuth, gotPrompt, gotLang string
	var audio []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "multipart/form-data" {
			http.Error(w, "type", 400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "audio":
				audio = b
			case "prompt":
				gotPrompt = string(b)
			case "language":
				gotLang = string(b)
			}
		}
		_, _ = io.WriteString(w, `{"text":"  hello there \n"}`)
	}))
	defer srv.Close()
	r := &Recogniser{URL: srv.URL, Token: "k"}
	got, err := r.Transcribe(context.Background(), tone(0.5, 0.4, 0), "Gojiberry", "en")
	if err != nil || got != "hello there" || gotAuth != "Bearer k" || gotPrompt != "Gojiberry" || gotLang != "en" {
		t.Fatalf("%q %v auth %q prompt %q lang %q", got, err, gotAuth, gotPrompt, gotLang)
	}
	if pcm, err := DecodeWAV(audio); err != nil || len(pcm) != SampleRate/2 {
		t.Errorf("the audio sent is the take as WAV: %v", err)
	}
	if _, err := (&Recogniser{}).Transcribe(context.Background(), tone(1, .4, 0), "", ""); err != ErrNoEndpoint {
		t.Errorf("no address: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusServiceUnavailable) }))
	defer bad.Close()
	if _, err := (&Recogniser{URL: bad.URL}).Transcribe(context.Background(), tone(1, .4, 0), "", ""); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Errorf("a failing recogniser: %v", err)
	}
}

type research struct {
	mu       sync.Mutex
	posts    int
	deletes  []string
	headers  []http.Header
	bodyLens []int
	status   int
}

func (r *research) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := io.ReadAll(req.Body)
	r.headers = append(r.headers, req.Header.Clone())
	switch req.Method {
	case http.MethodPost:
		r.posts++
		r.bodyLens = append(r.bodyLens, len(b))
	case http.MethodDelete:
		r.deletes = append(r.deletes, req.URL.Path)
	}
	if r.status != 0 {
		w.WriteHeader(r.status)
	}
}

func TestCheckEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"https://research.example.org/upload/": "https://research.example.org/upload",
		"http://localhost:8080/c":              "http://localhost:8080/c",
		"http://127.0.0.1:9/c?x=1":             "http://127.0.0.1:9/c",
		" https://example.org ":                "https://example.org",
	} {
		if got, err := CheckEndpoint(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "  ", "http://example.org/x", "ftp://example.org", "https://user:pw@example.org", "https://example.org/#frag", "https:///nohost", "javascript:alert(1)", "http://localhost.evil.org/"} {
		if _, err := CheckEndpoint(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if _, err := CheckEndpoint(""); err != ErrNoEndpoint {
		t.Error("no endpoint is its own error")
	}
}

func TestContributionNeedsTheConfirmedPackage(t *testing.T) {
	rs := &research{}
	srv := httptest.NewServer(rs)
	defer srv.Close()
	d := sampleDataset()
	entries := d.List()

	if _, err := Prepare("", entries); err != ErrNoEndpoint {
		t.Errorf("no endpoint, no package: %v", err)
	}
	if _, err := Prepare(srv.URL, nil); err != ErrNothingToAdd {
		t.Errorf("nothing: %v", err)
	}
	plan, err := Prepare(srv.URL, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 || plan.Bytes != len(plan.Body()) || len(plan.Digest) != 64 || len(plan.Token) != 32 {
		t.Fatalf("plan %+v", plan)
	}
	for i, it := range plan.Items {
		if it.ID == entries[i].ID {
			t.Error("recording IDs are new for each package")
		}
	}
	var sent Dataset
	if res, err := sent.Import(plan.Body()); err != nil || res.Added != 3 {
		t.Fatalf("the body is an export: %+v %v", res, err)
	}
	for _, e := range sent.List() {
		if e.Created.Unix() != 0 {
			t.Errorf("a date left in: %v", e.Created)
		}
	}
	other, _ := Prepare(srv.URL, entries)
	if other.Digest == plan.Digest || other.Token == plan.Token {
		t.Error("each package has its own IDs and token")
	}

	var c Contribution
	if err := c.Send(context.Background(), srv.Client(), plan); err != ErrNoConsent || rs.posts != 0 {
		t.Fatalf("no consent, nothing sent: %v, %d posts", err, rs.posts)
	}
	c.Confirm(plan)
	if err := c.Send(context.Background(), srv.Client(), other); err != ErrNoConsent {
		t.Errorf("another package than the confirmed one: %v", err)
	}
	if err := c.Send(context.Background(), srv.Client(), plan); err != nil || rs.posts != 1 || c.Sent() != 1 {
		t.Fatalf("send: %v %d", err, rs.posts)
	}
	h := rs.headers[0]
	if h.Get("Cookie") != "" || h.Get("Authorization") != "" || h.Get("Referer") != "" || h.Get("X-Contribution-Token") != plan.Token || h.Get("Content-Type") != "application/zip" {
		t.Errorf("headers %v", h)
	}
	if rs.bodyLens[0] != plan.Bytes {
		t.Error("the body sent is the body shown")
	}

	deleted, err := c.Revoke(context.Background(), srv.Client())
	if err != nil || deleted != 1 || len(rs.deletes) != 1 || !strings.HasSuffix(rs.deletes[0], "/"+plan.Token) || c.Sent() != 0 {
		t.Fatalf("revoke: %d %v %v", deleted, err, rs.deletes)
	}
	if err := c.Send(context.Background(), srv.Client(), plan); err != ErrRevoked || rs.posts != 1 {
		t.Errorf("after revoking: %v", err)
	}
	c.Confirm(plan)
	if !c.Confirmed(plan) {
		t.Error("confirm again")
	}
}

func TestContributionFailuresKeepConsentWithdrawn(t *testing.T) {
	rs := &research{status: http.StatusInternalServerError}
	srv := httptest.NewServer(rs)
	defer srv.Close()
	plan, _ := Prepare(srv.URL, sampleDataset().List())
	var c Contribution
	c.Confirm(plan)
	if err := c.Send(context.Background(), srv.Client(), plan); err == nil || c.Sent() != 0 {
		t.Fatalf("a server error is an error and not counted: %v", err)
	}
	rs.status = 0
	_ = c.Send(context.Background(), srv.Client(), plan)
	rs.status = http.StatusInternalServerError
	deleted, err := c.Revoke(context.Background(), srv.Client())
	if err == nil || deleted != 0 || c.Confirmed(plan) {
		t.Errorf("the deletion failed but consent is withdrawn all the same: %d %v", deleted, err)
	}
	rs.status = 0
	plan2, _ := Prepare(srv.URL, sampleDataset().List())
	var c2 Contribution
	c2.Confirm(plan2)
	plan2.body[len(plan2.body)-1] ^= 1
	posts := rs.posts
	if err := c2.Send(context.Background(), srv.Client(), plan2); err != ErrNoConsent || rs.posts != posts {
		t.Errorf("tampered: %v", err)
	}
}

func TestPrepareRefusesTwoTakesOfOnePhrase(t *testing.T) {
	a := NewEntry("en", "Hello", "Hello", Prompted, tone(1, 0.4, 0.002), nil, t0)
	b := NewEntry("en", "Hello", "Hello", Prompted, tone(1.2, 0.4, 0.002), nil, t0)
	if _, err := Prepare("https://example.org/c", []Entry{a, b}); err == nil {
		t.Error("what was shown must be what is sent")
	}
}
