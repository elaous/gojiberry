package export

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

const vtt = "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\nGood morning.\n"

func mediaDoc(m docfile.MediaItem) docfile.Document {
	return docOf(plain("Before.\n"), docfile.Item{Media: &m}, plain("After.\n"))
}

// HTML embeds a clip: a player with its caption track, the title as a caption and the transcript under it.
func TestHTMLEmbedsAClipWithItsCaptionsAndTranscript(t *testing.T) {
	r := registry()
	out := string(exportOne(t, r, "html", mediaDoc(docfile.MediaItem{URL: "https://example.org/talk.mp4", Kind: "video", Title: "The mayor's talk",
		Captions: vtt, Width: 640, Height: 360})))
	for _, want := range []string{
		`<video controls src="https://example.org/talk.mp4" width="640" height="360" aria-label="The mayor's talk">`,
		`<track kind="captions" label="Captions"`, `data:text/vtt;charset=utf-8;base64,` + base64.StdEncoding.EncodeToString([]byte(vtt)), ` default>`,
		`<figcaption>The mayor's talk</figcaption>`,
		`<details>`, `<summary>Transcript</summary>`, `<p>Good morning.</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML lacks %q:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "Before."), strings.Index(out, "<video"); i < 0 || j < i {
		t.Errorf("the clip is not where it was in the text")
	}
}

func TestHTMLAudioClipHasNoSizeAndAnAddressedTrack(t *testing.T) {
	out := string(exportOne(t, registry(), "html", mediaDoc(docfile.MediaItem{URL: "talk.mp3", Kind: "audio", Title: "Radio", CaptionsURL: "talk.vtt", Transcript: "Everything said.", Width: 1, Height: 1})))
	if !strings.Contains(out, `<audio controls src="talk.mp3"`) || strings.Contains(out, "width=") || !strings.Contains(out, `<track kind="captions" label="Captions" src="talk.vtt" default>`) {
		t.Errorf("audio: %s", out)
	}
}

func TestHTMLClipWithAnUnsafeAddressHasNoSource(t *testing.T) {
	out := string(exportOne(t, registry(), "html", mediaDoc(docfile.MediaItem{URL: "javascript:alert(1)", Title: "Bad", Transcript: "Words."})))
	if strings.Contains(out, "javascript:") || strings.Contains(out, " src=") || !strings.Contains(out, "<p>Words.</p>") {
		t.Errorf("unsafe address: %s", out)
	}
}

// The other formats still write a clip as a link with its transcript.
func TestOtherFormatsStillWriteAClipAsText(t *testing.T) {
	d := mediaDoc(docfile.MediaItem{URL: "https://example.org/talk.mp4", Title: "Talk", Captions: vtt})
	for _, f := range []string{"docx", "rtf", "epub", "markdown"} {
		out := string(exportOne(t, registry(), f, d))
		if strings.Contains(out, "<video") || !strings.Contains(out, "Good morning.") {
			t.Errorf("%s: a clip is not text with its words:\n%.400s", f, out)
		}
	}
}

// The page content sits in one <main> landmark (after the contents navigation, if there is one), so a screen
// reader can jump to it and no text is outside a landmark.
func TestHTMLPutsTheContentInOneMainLandmark(t *testing.T) {
	snap := longSnapshot(1)
	snap.TOC = true
	out, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	nav, main, end := strings.Index(s, "<nav class=\"toc\""), strings.Index(s, "<main>"), strings.Index(s, "</main>")
	if strings.Count(s, "<main>") != 1 || nav < 0 || nav >= main || main >= end || !strings.Contains(s[main:end], "<h1") {
		t.Errorf("landmarks: nav %d main %d end %d", nav, main, end)
	}
}
