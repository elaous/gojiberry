package saver

import "context"

func (s *Saver) readCatalogForTest(ctx context.Context, target string) (*catalog, error) {
	t, err := s.Target(target)
	if err != nil {
		return nil, err
	}
	return s.readCatalog(ctx, t)
}
