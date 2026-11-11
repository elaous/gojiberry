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

// Replace across documents is planned, then applied.

// Change is what a plan would do to one document.
type Change struct {
	ID, Title string
	// Count is how many matches would be replaced.
	Count int
	// Before and After show the first match in context.
	Before, After string
}

// Plan is the previewed replacement.
type Plan struct {
	// Changes lists the documents a plan would write.
	Changes []Change
	// Skipped are documents that could not be read.
	Skipped []string

	store    docstore.Adapter
	old, new map[string][]byte
}

// PlanReplace finds matches in every document of the store.
func PlanReplace(ctx context.Context, store docstore.Adapter, o canvas.FindOptions, repl string) (*Plan, error) {
	if _, err := canvas.FindAll("", o); err != nil {
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

// Total is how many replacements the plan makes.
func (p *Plan) Total() int {
	n := 0
	for _, c := range p.Changes {
		n += c.Count
	}
	return n
}

// Bytes returns document id's stored bytes.
func (p *Plan) Bytes(id string) (old, replacement []byte, ok bool) {
	o, found := p.old[id]
	if !found {
		return nil, nil, false
	}
	return bytes.Clone(o), bytes.Clone(p.new[id]), true
}

// Applied is the outcome of Apply.
type Applied struct {
	// Done are the documents written; Conflicts are skipped ones.
	Done, Conflicts []string

	store      docstore.Adapter
	old, wrote map[string][]byte
}

// Apply writes the planned documents.
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

// Undo puts back the text each written document had.
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

// swap replaces from by to if the store still holds it.
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

// decode reads a stored document in whichever format it has.
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

// window is about 60 characters of text around a match.
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
