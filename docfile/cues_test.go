package docfile

import (
	"strings"
	"testing"
)

func TestParseCuesReadsTimesAndTexts(t *testing.T) {
	vtt := "WEBVTT\n\nNOTE a note\n\n1\n00:00:01.000 --> 00:00:03.500 align:start\nHello there\n\n00:03.500 --> 00:05.000\n<v Ann>Second\ncue</v>\n"
	cues, problems := ParseCues(vtt)
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	if len(cues) != 2 || cues[0].Start != 1 || cues[0].End != 3.5 || cues[0].Text != "Hello there" || cues[1].Start != 3.5 || cues[1].End != 5 || !strings.Contains(cues[1].Text, "Second\ncue") {
		t.Fatalf("cues %+v", cues)
	}
}

func TestParseCuesReportsWhatIsWrong(t *testing.T) {
	has := func(ps []CueProblem, cue int, kind string) bool {
		for _, p := range ps {
			if p.Cue == cue && p.Kind == kind {
				return true
			}
		}
		return false
	}
	_, ps := ParseCues("00:00:01.000 --> 00:00:02.000\nhi\n")
	if !has(ps, 0, "no-header") {
		t.Errorf("no header: %v", ps)
	}
	_, ps = ParseCues("WEBVTT\n\n00:00:05.000 --> 00:00:02.000\nback\n\n00:00:01.000 --> 00:00:03.000\nbefore\n\n00:00:02.000 --> 00:00:04.000\noverlap\n\n00:00:09.000 --> 00:00:10.000\n\n")
	if !has(ps, 1, "backwards") || !has(ps, 3, "overlap") || !has(ps, 4, "no-text") {
		t.Errorf("problems %v", ps)
	}
	if _, ps = ParseCues("WEBVTT\n"); !has(ps, 0, "no-cues") {
		t.Errorf("empty: %v", ps)
	}
	if _, ps = ParseCues("WEBVTT\n\nxx:yy --> 00:00:02.000\ntext\n"); !has(ps, 1, "no-text") {
		t.Errorf("bad time: %v", ps)
	}
}

func TestBuildCuesRoundTrips(t *testing.T) {
	in := []Cue{{0.5, 2, "One"}, {2, 3661.25, "Two\nlines"}}
	vtt := BuildCues(in)
	if !strings.HasPrefix(vtt, "WEBVTT\n") || !strings.Contains(vtt, "01:01:01.250") {
		t.Fatalf("%q", vtt)
	}
	out, ps := ParseCues(vtt)
	if len(ps) != 0 || len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Errorf("out %+v problems %v", out, ps)
	}
	if FormatCueTime(59.9996) != "00:01:00.000" && FormatCueTime(59.9996) != "00:00:60.000" {
		t.Errorf("rounding %q", FormatCueTime(59.9996))
	}
}

func TestMediaTextSpeaksTheDocumentsLanguage(t *testing.T) {
	m := MediaItem{URL: "https://example.org/a.mp4", Title: "Interview", Transcript: "Hello."}
	text := func(items []Item) string {
		var sb strings.Builder
		for _, it := range items {
			if it.Chars != nil {
				sb.WriteString(it.Chars.Text)
			}
		}
		return sb.String()
	}
	if got := text(MediaText(m)); !strings.HasPrefix(got, "Video: Interview") || !strings.Contains(got, "Transcript") {
		t.Errorf("en: %q", got)
	}
	if got := text(MediaTextIn(m, "de-AT")); !strings.HasPrefix(got, "Video: Interview") || !strings.Contains(got, "Transkript") {
		t.Errorf("de: %q", got)
	}
	m.Kind = MediaAudio
	if got := text(MediaTextIn(m, "fr")); !strings.HasPrefix(got, "Audio : Interview") || !strings.Contains(got, "Transcription") {
		t.Errorf("fr: %q", got)
	}
	if got := text(MediaTextIn(m, "xx")); !strings.Contains(got, "Transcript") {
		t.Errorf("unknown language falls back to English: %q", got)
	}
	d := Document{Content: []Item{{Media: &m}}}
	d.SetLanguage("es")
	if got := text(d.MediaAsText().Content); !strings.Contains(got, "Transcripción") {
		t.Errorf("MediaAsText ignores the document's language: %q", got)
	}
}
