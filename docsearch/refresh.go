package docsearch

import (
	"context"
	"errors"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Extract turns a stored document's bytes into searchable text.
type Extract func(data []byte) (title, text string, err error)

// Result says what Refresh did.
type Result struct {
	Added, Updated, Removed, Unchanged int
	// Changed are the ids of documents that were re-indexed.
	Changed, Gone []string
	// Skipped are the ids that could not be read.
	Skipped []string
}

// Refresh brings the index in line with the store.
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
			// never keep stale text for a document that no longer reads
			r.Skipped = append(r.Skipped, m.ID)
			continue
		}
		title, text, err := extract(d.Data)
		if err != nil {
			if _, had := ix.docs[m.ID]; had {
				r.Gone = append(r.Gone, m.ID)
			}
			delete(ix.docs, m.ID)
			r.Skipped = append(r.Skipped, m.ID)
			continue
		}
		ix.Add(m.ID, title, text)
		ix.docs[m.ID].Updated = stamp
		r.Changed = append(r.Changed, m.ID)
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
			r.Gone = append(r.Gone, id)
		}
	}
	return r, nil
}

// FromDocfile is the Extract for documents saved in docfile form.
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
			if it.Form != nil {
				b.WriteString(" " + it.Form.Words() + " ")
			}
			if it.Equation != nil {
				b.WriteString(" " + it.Equation.Alt + " ")
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
			continue
		}
		return d.Title, b.String(), nil
	}
	return "", "", last
}
