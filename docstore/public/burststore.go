package public

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// BurstKeyPrefix is where the shared burst watch keeps records.
const BurstKeyPrefix = docstore.PublicPrefix + "burst/"

type burstStore struct {
	store docstore.ConditionalAdapter
}

type burstRecord struct {
	Docs    map[string]int64 `json:"docs"` // short document hash -> unix time of the last report
	Flagged int64            `json:"flagged,omitempty"`
}

func burstDoc(doc string) string {
	h := sha256.Sum256([]byte(doc))
	return hex.EncodeToString(h[:6])
}

func (b *burstStore) note(ctx context.Context, netHash, doc string, now time.Time, limit int) (over, first bool, err error) {
	if limit <= 0 {
		return false, false, nil
	}
	id := BurstKeyPrefix + netHash
	for range 8 {
		var rec burstRecord
		prev := ""
		d, err := b.store.Load(ctx, id)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
		case err != nil:
			return false, false, err
		default:
			prev = docstore.Digest(d.Data)
			if json.Unmarshal(d.Data, &rec) != nil {
				rec = burstRecord{}
			}
		}
		if rec.Docs == nil {
			rec.Docs = map[string]int64{}
		}
		cut := now.Add(-burstWindow).Unix()
		for k, t := range rec.Docs {
			if t <= cut {
				delete(rec.Docs, k)
			}
		}
		rec.Docs[burstDoc(doc)] = now.Unix()
		for len(rec.Docs) > limit+1 { // past the limit the exact count does not matter: drop the oldest
			oldest, ot := "", int64(0)
			for k, t := range rec.Docs {
				if oldest == "" || t < ot {
					oldest, ot = k, t
				}
			}
			delete(rec.Docs, oldest)
		}
		over = len(rec.Docs) > limit
		first = false
		if over && (rec.Flagged == 0 || now.Unix()-rec.Flagged >= int64(burstWindow/time.Second)) {
			rec.Flagged, first = now.Unix(), true
		}
		data, _ := json.Marshal(rec)
		switch err := b.store.SaveIf(ctx, id, data, prev); {
		case err == nil:
			return over, first, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return false, false, err
		}
	}
	return false, false, docstore.ErrConflict
}

func (b *burstStore) bursts(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	metas, err := docstore.ListPrefix(ctx, b.store, BurstKeyPrefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		d, err := b.store.Load(ctx, m.ID)
		if err != nil {
			continue
		}
		var rec burstRecord
		if json.Unmarshal(d.Data, &rec) != nil {
			continue
		}
		live := 0
		for _, t := range rec.Docs {
			if now.Unix()-t < int64(burstWindow/time.Second) {
				live++
			}
		}
		if live > limit {
			n++
		}
	}
	return n, nil
}

func (b *burstStore) sweep(ctx context.Context, now time.Time) (int, error) {
	metas, err := docstore.ListPrefix(ctx, b.store, BurstKeyPrefix)
	if err != nil {
		return 0, err
	}
	del, ok := b.store.(docstore.ConditionalDeleter)
	removed := 0
	for _, m := range metas {
		d, err := b.store.Load(ctx, m.ID)
		if err != nil {
			continue
		}
		var rec burstRecord
		stale := json.Unmarshal(d.Data, &rec) != nil
		if !stale {
			stale = now.Unix()-rec.Flagged >= int64(burstWindow/time.Second)
			for _, t := range rec.Docs {
				if now.Unix()-t < int64(burstWindow/time.Second) {
					stale = false
				}
			}
		}
		if !stale {
			continue
		}
		if ok {
			err = del.DeleteIf(ctx, m.ID, docstore.Digest(d.Data))
		} else {
			err = b.store.Delete(ctx, m.ID)
		}
		if err == nil {
			removed++
		}
	}
	return removed, nil
}
