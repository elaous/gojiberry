package docsearch

import (
	"context"
	"errors"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Extract turns a stored document's bytes (already decrypted) into the title and text to index.
type Extract func(data []byte) (title, text string, err error)

// Result says what Refresh did.
type Result struct {
	Added, Updated, Removed, Unchanged int
	// Skipped are the ids that could not be read or understood; they are not in the index.
	Skipped []string
}

// Refresh brings ix in line with the store: documents that are new or newer than what was indexed are
// loaded and extracted, documents that are gone are dropped, the rest are left alone (so a refresh
// after one save reads one document). Pass the adapter that decrypts (the encrypting adapter), so
// the index is built from plain text on this device and never from what the server holds. A
// cancelled context stops it with the work so far kept.
func Refresh(ctx context.Context, ix *Index, store docstore.Adapter, extract Extract) (Result, error) {
	var r Result
	metas, err := store.List(ctx)
	if err != nil {
		return r, err
	}
	live := make(map[string]bool, len(metas))
	for _, m := range metas {
		live[m.ID] = true
		stamp := m.UpdatedAt.UnixNano()
		old, known := ix.docs[m.ID]
		if known && old.Updated == stamp {
			r.Unchanged++
			continue
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
		d, err := store.Load(ctx, m.ID)
		if err != nil {
			if errors.Is(err, docstore.ErrNotFound) { // deleted since the listing
				delete(live, m.ID)
				continue
			}
			r.Skipped = append(r.Skipped, m.ID)
			continue
		}
		title, text, err := extract(d.Data)
		if err != nil {
			delete(ix.docs, m.ID) // never keep stale text for a document that no longer reads
			r.Skipped = append(r.Skipped, m.ID)
			continue
		}
		ix.Add(m.ID, title, text)
		ix.docs[m.ID].Updated = stamp
		if known {
			r.Updated++
		} else {
			r.Added++
		}
	}
	for id := range ix.docs {
		if !live[id] {
			delete(ix.docs, id)
			r.Removed++
		}
	}
	return r, nil
}

// FromDocfile is the Extract for documents saved in any of the docfile formats: the title and the
// text of the paragraphs and table cells.
func FromDocfile(data []byte) (title, text string, err error) {
	var last error
	for _, c := range docfile.Formats() {
		d, e := c.Unmarshal(data)
		if e != nil {
			last = e
			continue
		}
		var b strings.Builder
		for _, it := range d.Content {
			if it.Chars != nil {
				b.WriteString(it.Chars.Text)
			}
			if it.Equation != nil {
				b.WriteString(" " + it.Equation.Alt + " ") // the words of the formula, what a person searches for
			}
			if it.Table != nil {
				for _, row := range it.Table.Rows {
					for _, cell := range row.Cells {
						b.WriteString(" " + cell + " ")
					}
				}
			}
		}
		if d.Title == "" && b.Len() == 0 {
			last = errors.New("docsearch: nothing in the document")
			continue // a format that "reads" anything as an empty document is not the right one
		}
		return d.Title, b.String(), nil
	}
	return "", "", last
}
