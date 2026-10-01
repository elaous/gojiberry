package saver

import (
	"context"
	"errors"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/fonts"
)

type blobs struct{ a docstore.Adapter }

// Blobs adapts a docstore backend to fonts.Blobs, so a fonts.Library
// keeps its downloaded font files in the same place as the documents
// (Postgres, SQLite, S3, local files, ...). Keys start with
// fonts.KeyPrefix, which Saver.List skips.
func Blobs(a docstore.Adapter) fonts.Blobs { return blobs{a} }

func (b blobs) Put(ctx context.Context, key string, data []byte) error {
	return b.a.Save(ctx, key, data)
}

func (b blobs) Get(ctx context.Context, key string) ([]byte, bool, error) {
	d, err := b.a.Load(ctx, key)
	if errors.Is(err, docstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return d.Data, true, nil
}
