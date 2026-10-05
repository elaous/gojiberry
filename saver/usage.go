package saver

import (
	"context"

	"github.com/elaous/gojiberry/docstore"
)

// Usage is how much of a target's space is taken.
type Usage struct {
	// Used is the size of everything in the store: documents, their versions and
	// the catalog, and downloaded fonts. Documents counts the documents (not in
	// the trash) and Trashed those in it.
	Used               int64
	Documents, Trashed int
	// Total is the space allocated, 0 when it is not known (then Left and Percent
	// are 0 too, and Known is false).
	Total int64
	Left  int64
	// Percent is Used as a share of Total, 0 to 100 (more when over).
	Percent float64
	Known   bool
}

// Usage measures what the target's store holds against its capacity: the
// target's Capacity, else what the adapter reports (docstore.QuotaReporter).
func (s *Saver) Usage(ctx context.Context, target string) (Usage, error) {
	t, err := s.Target(target)
	if err != nil {
		return Usage{}, err
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return Usage{}, err
	}
	var u Usage
	for _, m := range metas {
		u.Used += int64(m.Size)
	}
	live, trashed, err := s.Lists(ctx, target)
	if err != nil {
		return Usage{}, err
	}
	u.Documents, u.Trashed = len(live), len(trashed)
	u.Total = t.Capacity
	if u.Total == 0 {
		if r, ok := t.Adapter.(docstore.QuotaReporter); ok {
			if q, err := r.Usage(ctx); err == nil {
				u.Total = q.Total
			}
		}
	}
	if u.Total > 0 {
		u.Known = true
		u.Left = max(u.Total-u.Used, 0)
		u.Percent = float64(u.Used) / float64(u.Total) * 100
	}
	return u, nil
}
