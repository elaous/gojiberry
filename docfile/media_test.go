package docfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

const vtt = "\xef\xbb\xbfWEBVTT\n\nNOTE written by hand\n\n1\n00:00:01.000 --> 00:00:03.000\n<v Ann>Good morning.</v>\n\n00:00:03.500 --> 00:00:06.000 align:start\nWelcome to <i>the show</i>.\nWe start now.\n\nSTYLE\n::cue { color: red }\n"

func TestCueTextIsTheWordsOfACaptionFile(t *testing.T) {
	want := "Good morning.\nWelcome to the show. We start now."
	if got := CueText(vtt); got != want {
		t.Errorf("CueText = %q, want %q", got, want)
	}
	if got := CueText("Just words, no timing."); got != "Just words, no timing." {
		t.Errorf("a text with no cues is the words themselves: %q", got)
	}
	if got := CueText("WEBVTT\n\n"); got != "" {
		t.Errorf("a header alone has no words: %q", got)
	}
	if got := CueText(strings.ReplaceAll(vtt, "\n", "\r\n")); got != want {
		t.Errorf("CRLF line ends: %q", got)
	}
}

func TestMediaItemKindCaptionsAndWords(t *testing.T) {
	for _, c := range []struct {
		m               MediaItem
		audio, captions bool
		kind, words     string
	}{
		{MediaItem{URL: "a.mp4"}, false, false, "video", ""},
		{MediaItem{URL: "a.mp3", Kind: "Audio"}, true, false, "audio", ""},
		{MediaItem{URL: "a.mp4", Captions: vtt}, false, true, "video", "Good morning.\nWelcome to the show. We start now."},
		{MediaItem{URL: "a.mp4", CaptionsURL: "a.vtt", Transcript: "  Said and shown.  "}, false, true, "video", "Said and shown."},
		{MediaItem{URL: "a.mp4", Captions: "WEBVTT\n\n", CaptionsURL: " "}, false, false, "video", ""},
	} {
		if c.m.IsAudio() != c.audio || c.m.HasCaptions() != c.captions || c.m.KindName() != c.kind || c.m.Words() != c.words {
			t.Errorf("%+v: audio %v captions %v kind %q words %q", c.m, c.m.IsAudio(), c.m.HasCaptions(), c.m.KindName(), c.m.Words())
		}
	}
}

func chars(items []Item) string {
	var sb strings.Builder
	for _, it := range items {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	return sb.String()
}

// A clip is written as text that stands for it: a line naming it as a link, and its words under Transcript.
func TestMediaTextNamesTheClipAndCarriesItsWords(t *testing.T) {
	m := MediaItem{URL: "https://example.com/talk.mp4", Title: "The talk", Captions: vtt}
	items := MediaText(m)
	if got, want := chars(items), "Video: The talk\nTranscript\nGood morning.\nWelcome to the show. We start now.\n"; got != want {
		t.Errorf("MediaText = %q, want %q", got, want)
	}
	var link string
	for _, it := range items {
		if it.Chars != nil && it.Chars.Format.Link != "" {
			link = it.Chars.Text + " -> " + it.Chars.Format.Link
		}
	}
	if link != "The talk -> https://example.com/talk.mp4" {
		t.Errorf("the name is a link to the clip: %q", link)
	}
	if got := chars(MediaText(MediaItem{URL: "a.mp3", Kind: MediaAudio})); got != "Audio: a.mp3\n" {
		t.Errorf("an audio clip with no title or words: %q", got)
	}
}

// A document with clips opens in the editor with each clip as a card (it has no player), and saving it again gives
// the clip back with every field; the plain formats flatten it to the text that stands for it.
func TestMediaOpensAsACardAndDocumentsKeepTheirClips(t *testing.T) {
	m := &MediaItem{URL: "a.mp4", Title: "Clip", Transcript: "Spoken words.", Captions: "WEBVTT\n", CaptionsURL: "a.vtt", Width: 320, Height: 180}
	d := Document{Content: []Item{{Chars: &Chars{Text: "Before"}}, {Media: m}, {Chars: &Chars{Text: "After"}}}}
	var card *canvas.MediaObject
	var text strings.Builder
	for _, it := range ToItems(d.Content) {
		switch it := it.(type) {
		case canvas.Chars:
			text.WriteString(it.Text)
		case canvas.Inline:
			card, _ = it.Object.(*canvas.MediaObject)
		}
	}
	if text.String() != "BeforeAfter" || card == nil || card.Title != "Clip" || card.URL != "a.mp4" || card.Transcript != "Spoken words." {
		t.Fatalf("opened as %q with the card %+v", text.String(), card)
	}
	back := FromItems(ToItems(d.Content))
	var got *MediaItem
	for _, it := range back {
		if it.Media != nil {
			got = it.Media
		}
	}
	if got == nil || *got != *m {
		t.Errorf("the clip after opening and saving: %+v, want %+v", got, m)
	}
	flat := d.MediaAsText()
	if got, want := chars(flat.Content), "Before\nVideo: Clip\nTranscript\nSpoken words.\nAfter"; got != want {
		t.Errorf("MediaAsText = %q, want %q", got, want)
	}
	for _, it := range flat.Content {
		if it.Media != nil {
			t.Error("a clip is left in the flattened document")
		}
	}
	if d.Content[1].Media == nil {
		t.Error("MediaAsText changed the document it was called on")
	}
	plain := Document{Content: []Item{{Chars: &Chars{Text: "x"}}}}
	if !reflect.DeepEqual(plain.MediaAsText(), plain) {
		t.Error("a document with no clip changes")
	}
}

// The clip survives every codec with all its fields (the fill document has one with every field set).
func TestMediaItemIsSavedByEveryCodec(t *testing.T) {
	d := Document{Content: []Item{{Media: &MediaItem{URL: "u", Kind: "audio", Title: "t", Captions: vtt, CaptionsURL: "c", Transcript: "w", Width: 640, Height: 360}}}}
	for _, c := range Formats() {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		got, err := c.Unmarshal(b)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Errorf("%s: %+v %v", c.Name(), got, err)
		}
	}
}
