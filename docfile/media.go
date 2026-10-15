package docfile

import (
	"fmt"
	"strings"
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
func MediaText(m MediaItem) []Item { return mediaText(m, mediaWords["en"]) }

func mediaText(m MediaItem, l mediaLabels) []Item {
	name := strings.TrimSpace(m.Title)
	if name == "" {
		name = m.URL
	}
	label := l.video
	if m.IsAudio() {
		label = l.audio
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
		items = append(items, Item{Chars: &Chars{Text: l.transcript, Format: Format{FontWeight: 700}}}, Item{Chars: &Chars{Text: "\n"}})
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
			out.Content = append(out.Content, MediaTextIn(*it.Media, d.Language())...)
			continue
		}
		out.Content = append(out.Content, it)
	}
	return out
}

// A Cue is one caption of a WebVTT track: the seconds it starts and ends, and its text (the lines of the cue
// joined by a newline, markup left in).
type Cue struct {
	Start, End float64
	Text       string
}

// A CueProblem is something wrong with a caption track that a person editing it should hear about.
type CueProblem struct {
	// Cue is the 1-based number of the cue, 0 for the track as a whole.
	Cue int
	// Kind is one of "no-header", "no-text", "backwards" (the cue ends before it starts), "overlap" (it starts
	// before the cue before it ends), "no-cues".
	Kind string
}

// ParseCues reads the cues of a WebVTT text, with the problems it finds. A timestamp is [hh:]mm:ss.mmm; a block with
// a timing line it cannot read is left out and reported as no-text. It never fails.
func ParseCues(vtt string) ([]Cue, []CueProblem) {
	vtt = strings.ReplaceAll(strings.ReplaceAll(vtt, "\r\n", "\n"), "\r", "\n")
	vtt = strings.TrimPrefix(vtt, "\xef\xbb\xbf")
	var problems []CueProblem
	if !strings.HasPrefix(strings.TrimSpace(vtt), "WEBVTT") {
		problems = append(problems, CueProblem{Kind: "no-header"})
	}
	var cues []Cue
	for _, block := range strings.Split(vtt, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		at := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				at = i
				break
			}
		}
		if at < 0 {
			continue // the header, a note or a style block
		}
		from, to, ok := strings.Cut(lines[at], "-->")
		if !ok {
			continue
		}
		start, ok1 := cueTime(from)
		end, ok2 := cueTime(strings.Fields(strings.TrimSpace(to) + " ")[0])
		text := strings.TrimSpace(strings.Join(lines[at+1:], "\n"))
		n := len(cues) + 1
		if !ok1 || !ok2 {
			problems = append(problems, CueProblem{Cue: n, Kind: "no-text"})
			continue
		}
		cues = append(cues, Cue{Start: start, End: end, Text: text})
		switch {
		case end <= start:
			problems = append(problems, CueProblem{Cue: n, Kind: "backwards"})
		case n > 1 && start < cues[n-2].End:
			problems = append(problems, CueProblem{Cue: n, Kind: "overlap"})
		}
		if text == "" {
			problems = append(problems, CueProblem{Cue: n, Kind: "no-text"})
		}
	}
	if len(cues) == 0 {
		problems = append(problems, CueProblem{Kind: "no-cues"})
	}
	return cues, problems
}

// cueTime reads a WebVTT timestamp ([hh:]mm:ss.mmm) as seconds.
func cueTime(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0.0
	for i, p := range parts {
		last := i == len(parts)-1
		whole, frac, hasFrac := strings.Cut(p, ".")
		if hasFrac && !last {
			return 0, false
		}
		n := 0
		if whole == "" {
			return 0, false
		}
		for _, r := range whole {
			if r < '0' || r > '9' {
				return 0, false
			}
			n = n*10 + int(r-'0')
		}
		v := float64(n)
		if hasFrac {
			f := 0.0
			scale := 0.1
			for _, r := range frac {
				if r < '0' || r > '9' {
					return 0, false
				}
				f += float64(r-'0') * scale
				scale /= 10
			}
			v += f
		}
		total = total*60 + v
	}
	return total, true
}

// FormatCueTime writes seconds as a WebVTT timestamp, hh:mm:ss.mmm.
func FormatCueTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(sec*1000 + 0.5)
	h, ms := ms/3600000, ms%3600000
	m, ms := ms/60000, ms%60000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, ms/1000, ms%1000)
}

// BuildCues writes cues as a WebVTT text.
func BuildCues(cues []Cue) string {
	var sb strings.Builder
	sb.WriteString("WEBVTT\n")
	for _, c := range cues {
		fmt.Fprintf(&sb, "\n%s --> %s\n%s\n", FormatCueTime(c.Start), FormatCueTime(c.End), strings.TrimSpace(c.Text))
	}
	return sb.String()
}

// mediaLabels are the words a clip is written with in the plain formats ("Video: name", "Transcript").
type mediaLabels struct{ video, audio, transcript string }

var mediaWords = map[string]mediaLabels{
	"en": {"Video: ", "Audio: ", "Transcript"},
	"de": {"Video: ", "Audio: ", "Transkript"},
	"fr": {"Vidéo : ", "Audio : ", "Transcription"},
	"es": {"Vídeo: ", "Audio: ", "Transcripción"},
	"pt": {"Vídeo: ", "Áudio: ", "Transcrição"},
	"he": {"וידאו: ", "שמע: ", "תמלול"},
	"ar": {"فيديو: ", "صوت: ", "نص مكتوب"},
}

// MediaTextIn is MediaText with the words "Video", "Audio" and "Transcript" in a language (de, fr, es, pt, he, ar;
// anything else is English).
func MediaTextIn(m MediaItem, lang string) []Item {
	l, ok := mediaWords[strings.ToLower(strings.SplitN(lang, "-", 2)[0])]
	if !ok {
		l = mediaWords["en"]
	}
	return mediaText(m, l)
}
