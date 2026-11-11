package migrate

import (
	"context"

	"github.com/elaous/gojiberry/saver"
)

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
