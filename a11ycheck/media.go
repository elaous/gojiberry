package a11ycheck

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// MediaFindings checks one embedded clip for the text that stands for it (roadmap 68): a video needs
// captions, audio needs a transcript, and a clip needs a name. Check calls it for every clip in a document
// (offset and item say where it is); a host that inserts media calls it with offset and item 0 on the clip
// about to be inserted, and refuses or asks while it returns an error, so a clip is never added with nothing
// for a deaf reader.
//
// It judges that text exists, not that it is good: captions that say nothing pass.
func MediaFindings(m docfile.MediaItem, offset, item int) []Finding {
	var out []Finding
	name := strings.TrimSpace(m.Title)
	if name == "" {
		name = m.URL
	}
	words := m.Words()
	add := func(rule string, sev Severity, msg, fix string) {
		out = append(out, Finding{Rule: rule, Severity: sev, Offset: offset, Item: item, Text: excerpt(name),
			Message: msg, Fix: fix, Action: ActionWriteMediaText})
	}
	switch {
	case m.IsAudio():
		if words == "" {
			add(RuleMediaTranscript, Error, "An audio clip has no transcript, so a deaf reader gets none of what is said in it.",
				"Write a transcript of the clip, or add captions to it.")
		}
	default:
		if !m.HasCaptions() {
			add(RuleMediaCaptions, Error, "A video has no captions, so a deaf reader gets none of what is said in it.",
				"Add a caption track (WebVTT text), or the address of one.")
		}
		if words == "" {
			add(RuleMediaTranscript, Warning, "A video has no transcript, so what is said and shown cannot be read, searched or translated.",
				"Write a transcript of the clip, with a description of what is shown.")
		}
	}
	if strings.TrimSpace(m.Title) == "" {
		add(RuleMediaTitle, Warning, "A "+m.KindName()+" clip has no title, so a reader who cannot play it knows it only by its address.",
			"Give the clip a title that says what it is.")
	}
	return out
}
