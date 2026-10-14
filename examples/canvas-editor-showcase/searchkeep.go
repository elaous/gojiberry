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

// The sealed search index kept between visits.
//
// "Search all documents" builds a local index of the documents' text. To not read every document again at
// each visit, the index is kept in the browser's IndexedDB, only sealed (docsearch.Index.Seal, AES-256-GCM)
// under a key derived from a secret that the page can make and the storage cannot give away:
//
//   - the encrypted store: the secret comes from the unlocked owner key (encstore.Store.SearchSecret),
//     which exists only while a key is held. Nothing readable by the server or by the storage can open the
//     index, and while the store is locked the index is neither read nor written. The index of an encrypted
//     store is never kept unsealed or under any other key.
//   - a store that is not encrypted (its documents are already readable where they are kept): the secret
//     comes from a per-device Ed25519 key that the browser keeps non-extractable (searchvault_js.go), so the
//     index is not left in the clear next to a copy of its own key. A browser without such a key keeps
//     nothing and rebuilds at each visit.
//
// An index that cannot be opened (damaged, changed, sealed under another key: another owner list, another
// device key) is dropped and rebuilt from the documents; docsearch.Open refuses it.

// sealedVault keeps named byte strings on the device.
type sealedVault interface {
	// Get returns nil, nil when nothing is kept under name.
	Get(name string) ([]byte, error)
	Put(name string, data []byte) error
	Delete(name string) error
}

// keptName is where the index of a place is kept.
func keptName(target string) string { return "index/" + target }

// searchKey is the key that seals the index of target, from a secret the person holds.
func searchKey(secret []byte, target string) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("no secret to seal the search index with")
	}
	return docsearch.DeriveKey(secret, "showcase "+target)
}

// keyTag identifies a key without revealing it, to notice that the key changed (another unlock).
func keyTag(key []byte) string {
	h := sha256.Sum256(append([]byte("gojiberry/showcase/search-tag\x00"), key...))
	return hex.EncodeToString(h[:8])
}

// deltaName is where the seq-th change after the base of target is kept.
func deltaName(target string, seq int) string { return keptName(target) + "/d/" + strconv.Itoa(seq) }

const (
	// maxDeltas is how many changes are kept after the base before the index is sealed whole again.
	maxDeltas = 32
)

// kept is the sealed index of a place as it is in the vault: the base and how many changes follow it.
type kept struct {
	base       []byte // the base as stored (a delta is bound to it)
	seq        int    // changes stored after it
	deltaBytes int    // their size
}

// openKept reads the index kept for target (the base, then the changes after it in order) and opens it with
// key. opened is false when there was none, or the base could not be opened: it is then removed (so a wrong
// index is not tried again) and an empty index is returned, to be built from the documents. A change that
// cannot be applied (lost, altered) ends the chain there and the later ones are removed: the refresh reads
// again what they would have said.
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

// dropDeltas removes the changes of target from the one numbered from on.
func dropDeltas(v sealedVault, target string, from int) {
	for seq := from; ; seq++ {
		d, err := v.Get(deltaName(target, seq))
		if err != nil || d == nil {
			return
		}
		_ = v.Delete(deltaName(target, seq))
	}
}

// keepIndex seals ix whole under key and keeps it for target as the new base, dropping the changes of the old one.
func keepIndex(v sealedVault, target string, key []byte, ix *docsearch.Index, old kept) (kept, error) {
	if v == nil || key == nil {
		return kept{}, nil
	}
	sealed, err := ix.Seal(key)
	if err != nil {
		return old, err
	}
	// the changes of the old base go first: a base must never be followed by changes bound to another
	dropDeltas(v, target, 1)
	if err := v.Put(keptName(target), sealed); err != nil {
		return kept{}, err
	}
	return kept{base: sealed}, nil
}

// keepChange keeps what one refresh changed as the next change after the base; when there is no base yet, the
// changes have grown to half the base or there are many of them, the whole index is sealed as a new base instead.
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

// indexCache is the index of the page, with what it belongs to: the place it indexes and the key it is
// sealed under. It is loaded from the sealed copy (the base and its changes) at the first search of a visit,
// and each refresh that changed something keeps only that change.
type indexCache struct {
	ix     docsearch.Index
	target string
	tag    string // keyTag of the key the index was loaded under ("" when there is none)
	set    bool
	stored bool // what is kept is the index as it is now
	k      kept
}

// refresh brings the index up to date with view, the documents of target, loading the sealed copy first
// when this is the first call for this place and key. key is nil when the index may not be kept (a locked
// encrypted store, a browser without a device key): the index is then only in memory, and an index that
// was built under another key (or none) is dropped. loaded says the sealed copy was used.
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
