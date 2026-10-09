package docstore

import (
	"context"
	"errors"
	"testing"
)

func TestCheckHeadReaderMemoryLocalFSSQLite(t *testing.T) {
	CheckHeadReader(t, func() Adapter { return NewMemoryAdapter() })
	CheckHeadReader(t, func() Adapter {
		lf, err := NewLocalFSAdapter(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return lf
	})
	if sq := sqliteForTest(t); sq != nil {
		CheckHeadReader(t, func() Adapter { return sq })
	}
	CheckHeadReader(t, func() Adapter { ea, _ := newExpiring(NewMemoryAdapter()); return ea })
}

// loadOnly is an adapter with no HeadReader.
type loadOnly struct{ Adapter }

func TestExpiringLoadHeadFollowsLoad(t *testing.T) {
	ctx := context.Background()
	for name, inner := range map[string]func() Adapter{
		"head reader":    func() Adapter { return NewMemoryAdapter() },
		"no head reader": func() Adapter { return loadOnly{NewMemoryAdapter()} },
	} {
		t.Run(name, func(t *testing.T) {
			ea, clk := newExpiring(inner())
			if err := ea.Save(ctx, "d", []byte("0123456789")); err != nil {
				t.Fatal(err)
			}
			if got, err := ea.LoadHead(ctx, "d", 4); err != nil || string(got) != "0123" {
				t.Fatalf("live: %q %v", got, err)
			}
			if _, err := ea.LoadHead(ctx, "expiry:d", 4); !errors.Is(err, ErrNotFound) {
				t.Errorf("a deadline record is read: %v", err)
			}
			clk.Add(2 * day)
			// Past its deadline the document is gone, for the head as for Load, though the inner
			// store still holds the bytes.
			if _, err := ea.Load(ctx, "d"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Load of an expired document: %v", err)
			}
			if got, err := ea.LoadHead(ctx, "d", 4); !errors.Is(err, ErrNotFound) {
				t.Errorf("LoadHead of an expired document = %q, %v", got, err)
			}
		})
	}
}
