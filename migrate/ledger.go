package migrate

import (
	"context"

	"github.com/elaous/gojiberry/saver"
)

// NewSaverLedger makes the Ledger of what a Saver target holds: the origin tags (OriginTag) of its documents. The
// tags are read once, when it is made, so a run sees what was there when it began.
//
// A document brought in an end-to-end encrypted folder keeps no readable tags (the catalog hides them), so it
// is not known and is brought again; a trashed document still counts as brought (the person removed it on
// purpose).
func NewSaverLedger(ctx context.Context, s *saver.Saver, target string) (Ledger, error) {
	entries, err := s.List(ctx, target)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, e := range entries {
		tags = append(tags, e.Tags...)
	}
	return ParseOrigins(tags), nil
}
