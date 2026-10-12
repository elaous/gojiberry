package docfile

import (
	"strings"

	"github.com/elaous/gojiberry/canvas"
)

// MediaItem is an audio or video clip embedded in the document: where it comes from, a name, and the text that
// stands for it for a reader who cannot hear or see it (roadmap 68). The editor has no player of its own, so
// the model carries the media by reference and keeps the text beside it; a host that embeds media fills it, and
// the checker (package a11ycheck) and the exports require and carry the text.
type MediaItem struct {
	// URL is where the clip is fetched from; Kind is MediaAudio or MediaVideo ("" is read as video).
	URL  string `json:"url" pb:"1"`
	Kind string `json:"kind,omitempty" pb:"2"`
	// Title names the clip for a reader who cannot see it ("Interview with the mayor").
	Title string `json:"title,omitempty" pb:"3"`
	// Captions is a caption track as WebVTT text, and CaptionsURL where one is fetched from. A video needs
	// one of the two.
	Captions    string `json:"captions,omitempty" pb:"4"`
	CaptionsURL string `json:"captionsUrl,omitempty" pb:"5"`
	// Transcript is everything said and described in the clip, as text. Audio needs it (or captions, whose
	// words it is made of).
	Transcript string `json:"transcript,omitempty" pb:"6"`
	// Width and Height are the displayed size of a video.
	Width  float64 `json:"width,omitempty" pb:"7"`
	Height float64 `json:"height,omitempty" pb:"8"`
}

// The kinds of media.
const (
	MediaAudio = "audio"
	MediaVideo = "video"
)

// IsAudio says the clip is sound only; anything else is read as video.
func (m MediaItem) IsAudio() bool { return strings.EqualFold(strings.TrimSpace(m.Kind), MediaAudio) }

// KindName is "audio" or "video".
func (m MediaItem) KindName() string {
	if m.IsAudio() {
		return MediaAudio
	}
	return MediaVideo
}

// HasCaptions says the clip has a caption track: text in it, or an address to fetch one from.
func (m MediaItem) HasCaptions() bool {
	return strings.TrimSpace(CueText(m.Captions)) != "" || strings.TrimSpace(m.CaptionsURL) != ""
}

// Words is the text that stands for the clip: its transcript, else the words of its captions (the cues
// without their timing). "" when there is none.
func (m MediaItem) Words() string {
	if t := strings.TrimSpace(m.Transcript); t != "" {
		return t
	}
	return CueText(m.Captions)
}

// CueText is the words of a WebVTT caption file: the cues' text, one cue per line, without the header, the
// notes, the cue numbers and timings, or the markup inside a cue (<v Ann>, <i>). A text with no cue timing is
// read as the words themselves. It never fails: what it cannot read it leaves out.
func CueText(vtt string) string {
	vtt = strings.ReplaceAll(strings.ReplaceAll(vtt, "\r\n", "\n"), "\r", "\n")
	vtt = strings.TrimPrefix(vtt, "\xef\xbb\xbf") // a byte order mark
	if !strings.Contains(vtt, "-->") {
		return strings.TrimSpace(stripCueTags(strings.TrimPrefix(strings.TrimSpace(vtt), "WEBVTT")))
	}
	var out []string
	for _, block := range strings.Split(vtt, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		start := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				start = i + 1
				break
			}
		}
		if start < 0 { // the header, a NOTE or a STYLE block
			continue
		}
		var cue []string
		for _, l := range lines[start:] {
			if l = strings.TrimSpace(stripCueTags(l)); l != "" {
				cue = append(cue, l)
			}
		}
		if len(cue) > 0 {
			out = append(out, strings.Join(cue, " "))
		}
	}
	return strings.Join(out, "\n")
}

// stripCueTags removes the <...> markup of a cue.
func stripCueTags(s string) string {
	var sb strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
		case !in:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// MediaText is the document items that stand for a clip as text, for what cannot play it (the editor, the
// plain formats): a line naming it, as a link to it, and its transcript as paragraphs under "Transcript". A
// clip with no words writes the line alone.
func MediaText(m MediaItem) []Item {
	name := strings.TrimSpace(m.Title)
	if name == "" {
		name = m.URL
	}
	label := "Video: "
	if m.IsAudio() {
		label = "Audio: "
	}
	items := []Item{{Chars: &Chars{Text: label}}}
	if name != "" {
		f := Format{}
		if strings.TrimSpace(m.URL) != "" {
			f.Link = m.URL
		}
		items = append(items, Item{Chars: &Chars{Text: name, Format: f}})
	}
	items = append(items, Item{Chars: &Chars{Text: "\n"}})
	if words := m.Words(); words != "" {
		items = append(items, Item{Chars: &Chars{Text: "Transcript", Format: Format{FontWeight: 700}}}, Item{Chars: &Chars{Text: "\n"}})
		for _, line := range strings.Split(words, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				items = append(items, Item{Chars: &Chars{Text: line + "\n"}})
			}
		}
	}
	return items
}

// MediaAsText is d with each embedded clip replaced by the text that stands for it (MediaText).
func (d Document) MediaAsText() Document {
	has := false
	for _, it := range d.Content {
		if it.Media != nil {
			has = true
			break
		}
	}
	if !has {
		return d
	}
	out := d
	out.Content = make([]Item, 0, len(d.Content))
	for _, it := range d.Content {
		if it.Media != nil {
			if n := len(out.Content); n > 0 {
				// the clip's line starts a paragraph of its own
				if last := out.Content[n-1].Chars; last != nil && last.Text != "" && !strings.HasSuffix(last.Text, "\n") {
					out.Content = append(out.Content, Item{Chars: &Chars{Text: "\n"}})
				}
			}
			out.Content = append(out.Content, MediaText(*it.Media)...)
			continue
		}
		out.Content = append(out.Content, it)
	}
	return out
}

// lastChars is the last canvas item if it is text.
func lastChars(items []canvas.Item) (canvas.Chars, bool) {
	if len(items) == 0 {
		return canvas.Chars{}, false
	}
	c, ok := items[len(items)-1].(canvas.Chars)
	return c, ok
}
