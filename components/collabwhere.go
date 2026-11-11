package components

import "strings"

const wherePrefixRunes = 30

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
