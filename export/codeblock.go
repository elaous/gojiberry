package export

import "strings"

// softBreakRune reconstructs a Code block's own raw text exactly as
// editor's own document.exportSnapshot found it (matching editor's
// own document.softLineBreak, U+2028 LINE SEPARATOR) -- used only by
// the formats that pass a code block's content through verbatim
// (Markdown/HTML/RTF/DOCX), each then applying its own escaping to
// the result. PlainText's own Code handling, by contrast, converts a
// soft break to a real newline uniformly across every block kind, and
// PDF's tokenizes it into a forced line-wrap break like any other
// block -- see each Exporter's own doc comment for why "verbatim"
// isn't the same answer everywhere.
const softBreakRune = ' '

// rawCodeText concatenates segs' own text with no formatting applied,
// reinserting softBreakRune for each soft-break segment -- the
// verbatim reconstruction Markdown/HTML/RTF/DOCX's own Code-block
// case wants before applying their own escaping.
func rawCodeText(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteRune(softBreakRune)
			continue
		}
		sb.WriteString(seg.Text)
	}
	return sb.String()
}
