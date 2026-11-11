//go:build !(js && wasm)

package main

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func recordPhrase(d *e2etest.Tab) {
	d.Click("#speech-record")
	d.WaitFor("recording", `document.getElementById('speech-record').textContent.includes('Stop')`)
	d.Sleep(1200)
	d.Click("#speech-record")
}

func TestSpeechRecordingsAreKeptSealedAndRemoved(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-keep-box input")
	d.OK(!d.Bool(`document.querySelector('#speech-keep-box input').checked`), "keeping is off by default")

	recordPhrase(d)
	d.WaitFor("the take", `document.getElementById('speech-progress').textContent.includes('Recorded: 1')`)
	d.ReloadPage()
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-count")
	d.OK(strings.HasPrefix(d.Text("#speech-count"), "0"), "nothing was kept without asking: "+d.Text("#speech-count"))

	d.Click("#speech-keep-box input")
	d.WaitFor("keeping is on", `document.getElementById('speech-message').textContent.includes('kept on this device')`)
	recordPhrase(d)
	d.WaitFor("the take", `document.getElementById('speech-progress').textContent.includes('Recorded: 1')`)
	d.Sleep(800) // sealed and written in the background
	readable := d.Str(`(async () => {
		const open = indexedDB.open('gojiberry-showcase-search');
		const db = await new Promise((ok, no) => { open.onsuccess = () => ok(open.result); open.onerror = no; });
		const all = await new Promise((ok, no) => { const r = db.transaction('sealed').objectStore('sealed').getAll(); r.onsuccess = () => ok(r.result); r.onerror = no; });
		let text = '';
		for (const x of all) { if (x.id.startsWith('speech/')) text += new TextDecoder('latin1').decode(x.data); }
		return text.includes('RIFF') || text.includes('WAVE') || text.includes('Good') ? 'readable' : 'sealed:' + all.filter((x) => x.id.startsWith('speech/')).length;
	})()`)
	d.OK(readable == "sealed:3", "the vault holds the index, one recording and the corrections, none readable: "+readable)
	d.ReloadPage()
	d.Click(`button[data-tab="speech"]`)
	d.WaitFor("the kept recording is back", `document.getElementById('speech-count') && document.getElementById('speech-count').textContent.startsWith('1 recording')`)
	d.WaitFor("it says so", `document.getElementById('speech-message').textContent.includes('opened from this device')`)

	d.Click("#speech-keep-box input")
	d.WaitFor("off", `document.getElementById('speech-message').textContent.includes('Nothing is kept')`)
	d.ReloadPage()
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-count")
	d.Sleep(500)
	d.OK(strings.HasPrefix(d.Text("#speech-count"), "0"), "nothing is back after turning it off: "+d.Text("#speech-count"))
	d.NoErrors()
}

func TestSpeechLearnsFromACorrection(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	var mu sync.Mutex
	var prompts []string
	rec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FormName() == "prompt" {
				b, _ := io.ReadAll(p)
				mu.Lock()
				prompts = append(prompts, string(b))
				mu.Unlock()
			}
		}
		_, _ = w.Write([]byte(`{"text":"meet me at the whether station"}`))
	}))
	defer rec.Close()

	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-address")
	d.Fill("#speech-address", rec.URL+"/transcribe")
	dictate := func() {
		d.Click("#speech-dictate")
		d.WaitFor("recording for dictation", `document.getElementById('speech-dictate').textContent.includes('send')`)
		d.Sleep(900)
		d.Click("#speech-dictate")
	}
	dictate()
	d.WaitFor("the dictation is in the document", `JSON.parse(document.getElementById('state').textContent).text.includes('whether station')`)
	d.WaitSelector("#speech-last")
	d.OK(!d.Has("#speech-fixes"), "nothing is offered before anything was corrected")

	d.Focus("#input")
	d.Press("Control+a")
	d.Type("meet me at the weather station")
	d.Focus("#input")
	d.Press("Control+a")
	d.Click("#speech-learn")
	d.WaitFor("learned", `document.getElementById('speech-message').textContent.includes('Remembered 1 correction')`)
	d.OK(strings.Contains(d.Text("#speech-corrections"), "1 correction remembered"), "counted: "+d.Text("#speech-corrections"))

	d.Focus("#input")
	d.Press("Control+a")
	d.Press("Delete")
	d.Click(`button[data-tab="speech"]`)
	dictate()
	d.WaitSelector("#speech-fix-0")
	d.OK(strings.Contains(d.Text("#speech-fixes"), "whether") && strings.Contains(d.Text("#speech-fixes"), "weather"), "the offer names both words: "+d.Text("#speech-fixes"))
	d.Click("#speech-fix-0")
	d.WaitFor("the fix is in the document", `JSON.parse(document.getElementById('state').textContent).text.includes('weather station') && !JSON.parse(document.getElementById('state').textContent).text.includes('whether')`)
	mu.Lock()
	got := append([]string(nil), prompts...)
	mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "weather") {
		t.Errorf("the recogniser's hints = %q: only the second dictation has one (the first had nothing learned yet), and it carries the word the person meant", got)
	}

	d.Click("#speech-forget")
	d.WaitFor("forgotten", `document.getElementById('speech-corrections').textContent.includes('0 corrections')`)
	d.NoErrors()
}

func TestSpeechContributionIsShownConfirmedAndWithdrawable(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	var mu sync.Mutex
	var posts, deletes []string
	var body []byte
	var cookie, referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Contribution-Token, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPost:
			posts = append(posts, r.Header.Get("X-Contribution-Token"))
			body, _ = io.ReadAll(r.Body)
			cookie, referer = r.Header.Get("Cookie"), r.Header.Get("Referer")
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="speech"]`)
	d.WaitSelector("#speech-research")
	d.OK(d.Bool(`document.getElementById('speech-prepare').disabled`), "nothing to prepare without an address or a recording")
	recordPhrase(d)
	d.WaitFor("the take", `document.getElementById('speech-progress').textContent.includes('Recorded: 1')`)
	d.Fill("#speech-research", srv.URL+"/contribute")
	d.WaitFor("prepare is on", `!document.getElementById('speech-prepare').disabled`)
	d.Click("#speech-prepare")
	d.WaitSelector("#speech-plan")
	plan := d.Text("#speech-plan")
	for _, want := range []string{"1 recordings", "Each recording carries only", "audio (16 kHz mono WAV)", "No name, account, address, device or date", "only the research team can delete it"} {
		d.OK(strings.Contains(plan, want), "the plan says "+want+": "+plan)
	}
	mu.Lock()
	n := len(posts)
	mu.Unlock()
	d.OK(n == 0, "nothing was sent before the person confirmed")

	d.Click("#speech-send")
	d.WaitFor("sent", `document.getElementById('speech-message').textContent.startsWith('Sent.')`)
	mu.Lock()
	if len(posts) != 1 || len(posts[0]) < 16 {
		t.Fatalf("posts = %q", posts)
	}
	tok, sent, ck, ref := posts[0], append([]byte(nil), body...), cookie, referer
	mu.Unlock()
	if ck != "" || ref != "" {
		t.Errorf("the request carried a cookie %q or a referrer %q", ck, ref)
	}
	zr, err := zip.NewReader(bytes.NewReader(sent), int64(len(sent)))
	if err != nil {
		t.Fatalf("the package is no zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "manifest.json") {
		t.Errorf("package files = %v", names)
	}

	d.Click("#speech-withdraw")
	d.WaitFor("deleted", `document.getElementById('speech-message').textContent.includes('was deleted')`)
	mu.Lock()
	defer mu.Unlock()
	if len(deletes) != 1 || deletes[0] != "/contribute/"+tok {
		t.Errorf("deletes = %q, want /contribute/%s", deletes, tok)
	}
	d.NoErrors()
}
