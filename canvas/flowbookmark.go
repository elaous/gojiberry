package canvas

import "sort"

// Bookmarks are named places in the text: Format.Bookmark on a stretch of
// text (a word, a selection, or one character) names it, so the place
// follows the text through edits. A link to "#name" jumps to one;
// Bookmarks lists them for a DOM component (components.Bookmarks).

// Bookmark is a named stretch of text.
type Bookmark struct {
	Name string
	// Start and End are the stream range it covers.
	Start, End int
}

// charBookmarks lists the bookmark name of every character of items.
func charBookmarks(items []Item) []string {
	var out []string
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			for range v.Text {
				out = append(out, v.Format.Bookmark)
			}
		case Inline:
			out = append(out, "")
		}
	}
	return out
}

// Bookmarks returns the bookmarks in text order.
func (f *TextFlow) Bookmarks() []Bookmark {
	var out []Bookmark
	idx := map[string]int{}
	for i, name := range charBookmarks(f.items()) {
		if name == "" {
			continue
		}
		if k, ok := idx[name]; ok {
			out[k].End = i + 1
			continue
		}
		idx[name] = len(out)
		out = append(out, Bookmark{Name: name, Start: i, End: i + 1})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// GotoBookmark moves the caret to the start of the named bookmark (see
// GotoOffset) and reports whether there is one.
func (f *TextFlow) GotoBookmark(name string) bool {
	for _, b := range f.Bookmarks() {
		if b.Name == name {
			f.GotoOffset(b.Start)
			return true
		}
	}
	return false
}

// BookmarkHost is implemented by targets that have bookmarks (a TextFlow).
type BookmarkHost interface {
	Bookmarks() []Bookmark
	GotoBookmark(name string) bool
}

var _ BookmarkHost = (*TextFlow)(nil)

// RemoveBookmark takes the named bookmark off its text.
func (f *TextFlow) RemoveBookmark(name string) bool {
	found := false
	retagAll(f, func(fm *Format) {
		if fm.Bookmark == name {
			fm.Bookmark, found = "", true
		}
	})
	return found
}

// BookmarkMarkColor is the colour of the mark drawn in the text where a bookmark starts (edit mode only).
const BookmarkMarkColor = "#b8860b"

// drawBookmarks marks where each bookmark starts, so a place that only had a name can be seen: a ribbon in the margin
// edge of the line. It is drawn while editing and never exported.
func (f *TextFlow) drawBookmarks() {
	if f.mode != ModeEdit || f.Painter == nil {
		return
	}
	for _, b := range f.Bookmarks() {
		x, y, h, ok := f.CaretRect(b.Start)
		if !ok {
			continue
		}
		f.Painter.SetFillStyle(BookmarkMarkColor)
		f.Painter.FillRect(x-2, y, 2, h)   // the bar at the start of the place
		f.Painter.FillRect(x-2, y-1, 7, 3) // and its top, like the ribbon of a book
	}
}
