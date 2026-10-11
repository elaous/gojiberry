package docsearch

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Replace across documents is two steps so that nothing changes before the person has seen it:
// PlanReplace reads every document (through the decrypting adapter, on this device) and works out the
// new bytes of each one that has a match, with a before and after view; Apply writes exactly those
// bytes; Undo puts the old bytes back. The text is replaced in the flowing text of a document, as the
// editor's own Replace all does, so a match inside a table cell is not touched.

// Change is what a plan would do to one document.
type Change struct {
	ID, Title string
	// Count is how many matches would be replaced.
	Count int
	// Before and After show the first match in its surroundings, as it is and as it would be.
	Before, After string
}

// Plan is the previewed replacement.
type Plan struct {
	// Changes lists the documents that would change, by id.
	Changes []Change
	// Skipped are documents that could not be read or understood; they are left alone.
	Skipped []string

	store    docstore.Adapter
	old, new map[string][]byte
}

// PlanReplace finds o in every document of store and prepares the replacement by repl (with $1 and
// ${name} for a regular expression), writing nothing.
func PlanReplace(ctx context.Context, store docstore.Adapter, o canvas.FindOptions, repl string) (*Plan, error) {
	if _, err := canvas.FindAll("", o); err != nil { // a bad pattern or an empty query, before reading anything
		return nil, err
	}
	metas, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	p := &Plan{store: store, old: map[string][]byte{}, new: map[string][]byte{}}
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, err := store.Load(ctx, m.ID)
		if err != nil {
			if !errors.Is(err, docstore.ErrNotFound) {
				p.Skipped = append(p.Skipped, m.ID)
			}
			continue
		}
		doc, codec, err := decode(d.Data)
		if err != nil {
			p.Skipped = append(p.Skipped, m.ID)
			continue
		}
		flow := &canvas.TextFlow{}
		doc.Apply(flow)
		matches, err := flow.Find(o)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
		}
		first := matches[0]
		before := window(flow.EditText(), first.From, first.To)
		n, err := flow.ReplaceMatches(matches, o, repl)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		after := window(flow.EditText(), first.From, first.From)
		out := docfile.FromFlow(doc.Title, flow)
		out.Updated, out.Fonts, out.Meta, out.History = doc.Updated, doc.Fonts, doc.Meta, doc.History
		b, err := codec.Marshal(out)
		if err != nil {
			p.Skipped = append(p.Skipped, m.ID)
			continue
		}
		p.old[m.ID], p.new[m.ID] = d.Data, b
		p.Changes = append(p.Changes, Change{ID: m.ID, Title: doc.Title, Count: n, Before: before, After: after})
	}
	return p, nil
}

// Total is how many replacements the plan makes in all.
func (p *Plan) Total() int {
	n := 0
	for _, c := range p.Changes {
		n += c.Count
	}
	return n
}

// Applied is the outcome of Apply, and what Undo needs.
type Applied struct {
	// Done are the documents written; Conflicts are those left alone because they changed since
	// the plan was made (somebody saved, or this person typed): plan again to include them.
	Done, Conflicts []string

	store      docstore.Adapter
	old, wrote map[string][]byte
}

// Apply writes the planned documents. A document that is no longer what the plan read is skipped.
// Only the ids in only are written when it is given (the person unticked some in the preview).
func (p *Plan) Apply(ctx context.Context, only ...string) (*Applied, error) {
	a := &Applied{store: p.store, old: map[string][]byte{}, wrote: map[string][]byte{}}
	for _, c := range p.Changes {
		if len(only) > 0 && !contains(only, c.ID) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return a, err
		}
		if err := swap(ctx, p.store, c.ID, p.old[c.ID], p.new[c.ID]); err != nil {
			if errors.Is(err, errChanged) || errors.Is(err, docstore.ErrConflict) || errors.Is(err, docstore.ErrNotFound) {
				a.Conflicts = append(a.Conflicts, c.ID)
				continue
			}
			return a, err
		}
		a.Done = append(a.Done, c.ID)
		a.old[c.ID], a.wrote[c.ID] = p.old[c.ID], p.new[c.ID]
	}
	return a, nil
}

// Undo puts back the text each written document had. A document that was saved again since is left
// alone and returned in kept, so a later edit is never thrown away.
func (a *Applied) Undo(ctx context.Context) (restored, kept []string, err error) {
	for _, id := range a.Done {
		if err := ctx.Err(); err != nil {
			return restored, kept, err
		}
		if e := swap(ctx, a.store, id, a.wrote[id], a.old[id]); e != nil {
			if errors.Is(e, errChanged) || errors.Is(e, docstore.ErrConflict) || errors.Is(e, docstore.ErrNotFound) {
				kept = append(kept, id)
				continue
			}
			return restored, kept, e
		}
		restored = append(restored, id)
	}
	return restored, kept, nil
}

var errChanged = errors.New("docsearch: the document changed")

// swap replaces from by to if the store still holds from. A store with SaveIf does it as one step;
// otherwise the document is read again just before it is written (a small window remains).
func swap(ctx context.Context, store docstore.Adapter, id string, from, to []byte) error {
	if c, ok := store.(docstore.ConditionalAdapter); ok {
		if err := c.SaveIf(ctx, id, to, docstore.Digest(from)); !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return err
		}
	}
	cur, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur.Data, from) {
		return errChanged
	}
	return store.Save(ctx, id, to)
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// decode reads a stored document in whichever docfile format it is in, and says which.
func decode(data []byte) (docfile.Document, docfile.Codec, error) {
	var last error
	for _, c := range docfile.Formats() {
		d, err := c.Unmarshal(data)
		if err != nil {
			last = err
			continue
		}
		if d.Title == "" && len(d.Content) == 0 {
			last = errors.New("docsearch: nothing in the document")
			continue
		}
		return d, c, nil
	}
	return docfile.Document{}, nil, last
}

// window is about 60 characters of text around [from, to), on one line.
func window(text string, from, to int) string {
	rs := []rune(text)
	lo, hi := max(0, from-25), min(len(rs), to+25)
	s := strings.Join(strings.Fields(string(rs[lo:hi])), " ")
	if lo > 0 {
		s = "…" + s
	}
	if hi < len(rs) {
		s += "…"
	}
	return s
}
