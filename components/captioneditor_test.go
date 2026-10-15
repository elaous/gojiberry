package components

import (
	"strings"
	"testing"
)

func TestCaptionEditorShowsTheCuesAndWhatIsWrong(t *testing.T) {
	html := render(t, CaptionEditor(CaptionEditorProps{ID: "c", Track: "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n\n00:00:01.500 --> 00:00:03.000\nOverlapping\n"}))
	for _, want := range []string{`id="c-track"`, `00:00:01.000`, `Hello`, `Caption 2 starts before the caption before it has ended.`, `id="c-add"`, `<th scope="col">Start</th>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	empty := render(t, CaptionEditor(CaptionEditorProps{ID: "c"}))
	if strings.Contains(empty, "problems") {
		t.Errorf("an empty track is not a fault: %s", empty)
	}
	if !strings.Contains(render(t, CaptionEditor(CaptionEditorProps{ID: "c", Track: "no header at all"})), "does not start with the line WEBVTT") {
		t.Error("a track with no header is said so")
	}
}

func TestAddCaptionChecksTheDraft(t *testing.T) {
	track, msg := AddCaption("", CaptionDraft{Start: "0:01", End: "0:03.5", Text: "Hello"})
	if !strings.HasPrefix(track, "WEBVTT\n") || !strings.Contains(track, "00:00:01.000 --> 00:00:03.500\nHello") || msg != "Caption 1 added." {
		t.Fatalf("%q %q", track, msg)
	}
	track2, msg := AddCaption(track, CaptionDraft{Start: "2", End: "5", Text: "Too early"})
	if track2 != track || !strings.Contains(msg, "starts before the last one ends") {
		t.Errorf("overlap: %q", msg)
	}
	for _, c := range []struct {
		d    CaptionDraft
		want string
	}{
		{CaptionDraft{Start: "x", End: "3", Text: "a"}, "Write the times"},
		{CaptionDraft{Start: "5", End: "3", Text: "a"}, "must end after it starts"},
		{CaptionDraft{Start: "5", End: "6", Text: "  "}, "Write what is said"},
	} {
		if got, msg := AddCaption(track, c.d); got != track || !strings.Contains(msg, c.want) {
			t.Errorf("%+v: %q", c.d, msg)
		}
	}
	if track3, _ := AddCaption(track, CaptionDraft{Start: "4", End: "1:02:03", Text: "later"}); !strings.Contains(track3, "01:02:03.000") {
		t.Errorf("hours: %q", track3)
	}
	for in, want := range map[string]float64{"12.5": 12.5, "0:05.5": 5.5, "1:02": 62, "1,5": 1.5, "1:00:00": 3600} {
		if got, ok := ParseCaptionTime(in); !ok || got != want {
			t.Errorf("ParseCaptionTime(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "-1", "a:b", "1:2:3:4"} {
		if _, ok := ParseCaptionTime(bad); ok {
			t.Errorf("ParseCaptionTime(%q) accepted", bad)
		}
	}
}
