package saver

import (
	"context"

	"github.com/elaous/gojiberry/docstore"
)

// Usage is how much of a target's space is taken.
type Usage struct {
	Used               int64
	Documents, Trashed int
	Total              int64
	Left               int64
	Percent            float64
	Known              bool
}

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
