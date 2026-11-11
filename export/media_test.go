package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
)

func TestEveryExportCarriesTheTranscriptOfAClip(t *testing.T) {
	r := registry()
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\n<v Ann>Good morning to you.</v>\n"
	for name, m := range map[string]docfile.MediaItem{
		"a transcript":      {URL: "https://example.com/talk.mp4", Title: "The talk", Transcript: "Good morning to you."},
		"captions only":     {URL: "https://example.com/talk.mp4", Title: "The talk", Captions: vtt},
		"audio":             {URL: "https://example.com/talk.mp3", Kind: "audio", Title: "The talk", Transcript: "Good morning to you."},
		"captions and text": {URL: "https://example.com/talk.mp4", Title: "The talk", Captions: vtt, Transcript: "Good morning to you."},
	} {
		d := docOf(plain("Before the clip."), docfile.Item{Media: &m}, plain("After the clip.\n"))
		for _, format := range Formats {
			var out string
			if format == "pdf" {
				out, _ = pdftext.ExtractBytes(rawOne(t, r, "pdf", d))
			} else {
				out = string(exportOne(t, r, format, d))
			}
			for _, want := range []string{"The talk", "Good morning to you.", "Transcript", "After the clip."} {
				if !strings.Contains(out, want) {
					t.Errorf("%s / %s: %q is not in the file:\n%.400s", name, format, want, out)
				}
			}
			if strings.Contains(out, "WEBVTT") || strings.Contains(out, "-->") {
				t.Errorf("%s / %s: the caption file's markup is in the text", name, format)
			}
		}
	}
	d := docOf(docfile.Item{Media: &docfile.MediaItem{URL: "https://example.com/talk.mp4", Title: "The talk"}})
	if out := string(exportOne(t, r, "html", d)); !strings.Contains(out, `<video controls src="https://example.com/talk.mp4"`) || !strings.Contains(out, "<figcaption>The talk</figcaption>") || strings.Contains(out, "Transcript") {
		t.Errorf("html of a clip with no words: %s", out)
	}
	if out := string(exportOne(t, r, "markdown", d)); !strings.Contains(out, "Video: [The talk](https://example.com/talk.mp4)") {
		t.Errorf("markdown of a clip: %q", out)
	}
}

func TestFidelityReportSaysWhereAClipIsNotEmbedded(t *testing.T) {
	d := docOf(docfile.Item{Media: &docfile.MediaItem{URL: "a.mp4", Title: "Clip", Captions: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHi.\n"}})
	for _, format := range Formats {
		got := map[Feature]Loss{}
		for _, l := range Fidelity(format, d).Losses {
			got[l.Feature] = l
		}
		if format == "html" {
			if _, ok := got[FeatMedia]; ok {
				t.Errorf("html: media is reported as a loss: %+v", got[FeatMedia])
			}
			if _, ok := got[FeatMediaCaptions]; ok {
				t.Errorf("html: captions are reported as a loss: %+v", got[FeatMediaCaptions])
			}
			continue
		}
		if l := got[FeatMedia]; l.Treatment != Simplified || l.Note != NoteMediaText {
			t.Errorf("%s: media %+v", format, l)
		}
		if l := got[FeatMediaCaptions]; l.Treatment != Dropped || l.Note != NoteCaptionWords {
			t.Errorf("%s: captions %+v", format, l)
		}
	}
}
