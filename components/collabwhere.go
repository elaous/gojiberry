package components

import "strings"

// A collaborator's cursor is a coloured caret, which says nothing to a screen reader. Saying where
// everybody is, all the time, would drown the person, so the position is given only when asked
// ("Where is Bo?" in the presence list: see PresenceProps.OnWhere), as a sentence built here from the
// document's text and the cursor's offsets (in runes).

// wherePrefixRunes is how much text before the cursor is quoted.
const wherePrefixRunes = 30

// WhereSentence says where name's cursor is in text: the paragraph (the lines of text, counted from 1,
// empty ones too), and the few words before the caret, or that text is selected. has is false when the
// person has no cursor in the document (a viewer who has not clicked, someone who left the page).
func WhereSentence(name, text string, anchor, caret int, has bool) string {
	if !has {
		return trf("collab.whereNone", "%s has no cursor in the document.", name)
	}
	runes := []rune(text)
	anchor, caret = max(0, min(anchor, len(runes))), max(0, min(caret, len(runes)))
	paragraphs := 1 + strings.Count(text, "\n")
	para := 1 + strings.Count(string(runes[:caret]), "\n")
	if anchor != caret {
		return trf("collab.whereSelected", "%s has text selected in paragraph %d of %d.", name, para, paragraphs)
	}
	start := caret
	for start > 0 && runes[start-1] != '\n' && caret-start < wherePrefixRunes {
		start--
	}
	before := strings.TrimSpace(strings.ReplaceAll(string(runes[start:caret]), "\uFFFC", " ")) // an object is not text
	if before == "" {
		return trf("collab.whereStart", "%s is at the start of paragraph %d of %d.", name, para, paragraphs)
	}
	if start > 0 && runes[start-1] != '\n' { // the quote begins in the middle of a word
		before = "…" + before
	}
	return trf("collab.whereAfter", "%s is in paragraph %d of %d, after \"%s\".", name, para, paragraphs, before)
}
