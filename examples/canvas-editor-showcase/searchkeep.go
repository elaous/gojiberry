package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

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

// openKept reads the index kept for target and opens it with key. opened is false when there was none, or
// it could not be opened: it is then removed (so a wrong index is not tried again) and an empty index is
// returned, to be built from the documents.
func openKept(v sealedVault, target string, key []byte) (ix *docsearch.Index, opened bool) {
	if v == nil || key == nil {
		return &docsearch.Index{}, false
	}
	sealed, err := v.Get(keptName(target))
	if err != nil || sealed == nil {
		return &docsearch.Index{}, false
	}
	ix, err = docsearch.Open(key, sealed)
	if err != nil {
		_ = v.Delete(keptName(target))
		return &docsearch.Index{}, false
	}
	return ix, true
}

// keepIndex seals ix under key and keeps it for target.
func keepIndex(v sealedVault, target string, key []byte, ix *docsearch.Index) error {
	if v == nil || key == nil {
		return nil
	}
	sealed, err := ix.Seal(key)
	if err != nil {
		return err
	}
	return v.Put(keptName(target), sealed)
}

// indexCache is the index of the page, with what it belongs to: the place it indexes and the key it is
// sealed under. It is loaded from the sealed copy at the first search of a visit, and the sealed copy
// is written again after a refresh changed something.
type indexCache struct {
	ix     docsearch.Index
	target string
	tag    string // keyTag of the key the index was loaded under ("" when there is none)
	set    bool
	stored bool // the sealed copy kept is the index as it is now
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
		c.ix, c.target, c.tag, c.set, c.stored = docsearch.Index{}, target, tag, true, false
		if p, ok := openKept(v, target, key); ok {
			c.ix, c.stored, loaded = *p, true, true
		}
	}
	res, err = docsearch.Refresh(ctx, &c.ix, view, docsearch.FromDocfile)
	if err != nil {
		return res, loaded, err
	}
	if res.Added+res.Updated+res.Removed > 0 {
		c.stored = false
	}
	if key != nil && !c.stored && keepIndex(v, target, key, &c.ix) == nil {
		c.stored = true
	}
	return res, loaded, nil
}
