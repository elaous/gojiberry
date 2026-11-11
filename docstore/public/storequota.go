package public

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// QuotaPrefix is where a StoreQuota keeps its counters.
const QuotaPrefix = docstore.PublicPrefix + "quota/"

const storeQuotaRetries = 8

// StoreQuota is a Quota kept in the store.
type StoreQuota struct {
	Store  docstore.Adapter
	Limit  int64
	Window time.Duration
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time
}

// NewStoreQuota makes a quota of limit bytes per window.
func NewStoreQuota(store docstore.Adapter, limit int64, window time.Duration) (*StoreQuota, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, errors.New("public: a StoreQuota needs a store with conditional writes")
	}
	return &StoreQuota{Store: store, Limit: limit, Window: window}, nil
}

var (
	_ Quota         = (*StoreQuota)(nil)
	_ QuotaRefunder = (*StoreQuota)(nil)
	_ WindowQuota   = (*StoreQuota)(nil)
)

func (q *StoreQuota) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *StoreQuota) window() time.Duration {
	if q.Window > 0 {
		return q.Window
	}
	return 24 * time.Hour
}

func (q *StoreQuota) number(t time.Time) int64 { return t.UnixNano() / int64(q.window()) }

func (q *StoreQuota) id(win int64, key string) string {
	sum := sha256.Sum256([]byte(key))
	return QuotaPrefix + strconv.FormatInt(win, 10) + "/" + hex.EncodeToString(sum[:])
}

type quotaRecord struct {
	Used int64 `json:"used"`
}

func (q *StoreQuota) charge(ctx context.Context, key string, delta, win int64) (bool, error) {
	cas, ok := q.Store.(docstore.ConditionalAdapter)
	if !ok {
		return false, errors.New("public: a StoreQuota needs a store with conditional writes")
	}
	id := q.id(win, key)
	for range storeQuotaRetries {
		var rec quotaRecord
		prev := ""
		switch doc, err := q.Store.Load(ctx, id); {
		case err == nil:
			prev = docstore.Digest(doc.Data)
			if json.Unmarshal(doc.Data, &rec) != nil {
				rec = quotaRecord{}
			}
		case errors.Is(err, docstore.ErrNotFound):
		default:
			return false, err
		}
		next := rec.Used + delta
		if delta > 0 && (delta > q.Limit || next > q.Limit) {
			return false, nil
		}
		if next < 0 {
			next = 0
		}
		if next == rec.Used && prev != "" {
			return true, nil
		}
		data, _ := json.Marshal(quotaRecord{Used: next})
		switch err := cas.SaveIf(ctx, id, data, prev); {
		case err == nil:
			return true, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return false, err
		}
	}
	return false, errors.New("public: the quota counter of a client is too busy to charge")
}

// Take implements Quota.
func (q *StoreQuota) Take(ctx context.Context, key string, n int64) (bool, time.Duration, error) {
	ok, _, retry, err := q.TakeIn(ctx, key, n)
	return ok, retry, err
}

// TakeIn implements WindowQuota: the window id is the window.
func (q *StoreQuota) TakeIn(ctx context.Context, key string, n int64) (bool, int64, time.Duration, error) {
	now := q.now()
	win := q.number(now)
	ok, err := q.charge(ctx, key, n, win)
	if err != nil || !ok {
		end := time.Unix(0, (win+1)*int64(q.window()))
		return false, win, max(end.Sub(now), time.Second), err
	}
	return true, win, 0, nil
}

// Refund implements QuotaRefunder: it credits the key's count.
func (q *StoreQuota) Refund(ctx context.Context, key string, n int64) {
	_, _ = q.charge(ctx, key, -n, q.number(q.now()))
}

// RefundIn implements WindowQuota: it credits the window's count.
func (q *StoreQuota) RefundIn(ctx context.Context, key string, n int64, window int64) {
	if window == q.number(q.now()) {
		_, _ = q.charge(ctx, key, -n, window)
	}
}

// Sweep deletes the counters of the windows that ended.
func (q *StoreQuota) Sweep(ctx context.Context) (int, error) {
	metas, err := docstore.ListPrefix(ctx, q.Store, QuotaPrefix)
	if err != nil {
		return 0, err
	}
	cur := q.number(q.now())
	var errs []error
	n := 0
	for _, m := range metas {
		win, hash, ok := strings.Cut(strings.TrimPrefix(m.ID, QuotaPrefix), "/")
		w, perr := strconv.ParseInt(win, 10, 64)
		if !ok || perr != nil || len(hash) != 64 || strconv.FormatInt(w, 10) != win || w >= cur {
			continue
		}
		switch err := q.Store.Delete(ctx, m.ID); {
		case err == nil || errors.Is(err, docstore.ErrNotFound):
			n++
		default:
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
}
