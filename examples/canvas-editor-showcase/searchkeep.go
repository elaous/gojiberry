package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/elaous/gojiberry/docsearch"
	"github.com/elaous/gojiberry/docstore"
)

type sealedVault interface {
	// Get returns nil, nil when nothing is kept under name.
	Get(name string) ([]byte, error)
	Put(name string, data []byte) error
	Delete(name string) error
}

func keptName(target string) string { return "index/" + target }

func searchKey(secret []byte, target string) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("no secret to seal the search index with")
	}
	return docsearch.DeriveKey(secret, "showcase "+target)
}

func keyTag(key []byte) string {
	h := sha256.Sum256(append([]byte("gojiberry/showcase/search-tag\x00"), key...))
	return hex.EncodeToString(h[:8])
}

func deltaName(target string, seq int) string { return keptName(target) + "/d/" + strconv.Itoa(seq) }

const (
	maxDeltas = 32
)

type kept struct {
	base       []byte // the base as stored (a delta is bound to it)
	seq        int    // changes stored after it
	deltaBytes int    // their size
}

func openKept(v sealedVault, target string, key []byte) (ix *docsearch.Index, k kept, opened bool) {
	if v == nil || key == nil {
		return &docsearch.Index{}, kept{}, false
	}
	sealed, err := v.Get(keptName(target))
	if err != nil || sealed == nil {
		return &docsearch.Index{}, kept{}, false
	}
	ix, err = docsearch.Open(key, sealed)
	if err != nil {
		_ = v.Delete(keptName(target))
		dropDeltas(v, target, 1)
		return &docsearch.Index{}, kept{}, false
	}
	k = kept{base: sealed}
	for {
		d, err := v.Get(deltaName(target, k.seq+1))
		if err != nil || d == nil {
			break
		}
		if ix.ApplyDelta(key, sealed, k.seq+1, d) != nil {
			dropDeltas(v, target, k.seq+1)
			break
		}
		k.seq++
		k.deltaBytes += len(d)
	}
	return ix, k, true
}

func dropDeltas(v sealedVault, target string, from int) {
	for seq := from; ; seq++ {
		d, err := v.Get(deltaName(target, seq))
		if err != nil || d == nil {
			return
		}
		_ = v.Delete(deltaName(target, seq))
	}
}

func keepIndex(v sealedVault, target string, key []byte, ix *docsearch.Index, old kept) (kept, error) {
	if v == nil || key == nil {
		return kept{}, nil
	}
	sealed, err := ix.Seal(key)
	if err != nil {
		return old, err
	}
	dropDeltas(v, target, 1)
	if err := v.Put(keptName(target), sealed); err != nil {
		return kept{}, err
	}
	return kept{base: sealed}, nil
}

func keepChange(v sealedVault, target string, key []byte, ix *docsearch.Index, k kept, res docsearch.Result) (kept, error) {
	if v == nil || key == nil {
		return kept{}, nil
	}
	if k.base == nil || k.seq >= maxDeltas || k.deltaBytes > len(k.base)/2 {
		return keepIndex(v, target, key, ix, k)
	}
	sealed, err := docsearch.SealDelta(key, k.base, k.seq+1, ix.DeltaFor(res))
	if err != nil {
		return k, err
	}
	if err := v.Put(deltaName(target, k.seq+1), sealed); err != nil {
		return k, err
	}
	k.seq++
	k.deltaBytes += len(sealed)
	return k, nil
}

type indexCache struct {
	ix     docsearch.Index
	target string
	tag    string
	set    bool
	stored bool // what is kept is the index as it is now
	k      kept
}

func (c *indexCache) refresh(ctx context.Context, v sealedVault, target string, key []byte, view docstore.Adapter) (res docsearch.Result, loaded bool, err error) {
	tag := ""
	if key != nil {
		tag = keyTag(key)
	}
	if !c.set || c.target != target || c.tag != tag {
		c.ix, c.target, c.tag, c.set, c.stored, c.k = docsearch.Index{}, target, tag, true, false, kept{}
		if p, k, ok := openKept(v, target, key); ok {
			c.ix, c.k, c.stored, loaded = *p, k, true, true
		}
	}
	res, err = docsearch.Refresh(ctx, &c.ix, view, docsearch.FromDocfile)
	if err != nil {
		return res, loaded, err
	}
	if res.Added+res.Updated+res.Removed > 0 {
		c.stored = false
	}
	if key != nil && !c.stored {
		if nk, kerr := keepChange(v, target, key, &c.ix, c.k, res); kerr == nil {
			c.k, c.stored = nk, true
		}
	}
	return res, loaded, nil
}
