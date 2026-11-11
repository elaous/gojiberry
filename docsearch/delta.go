package docsearch

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
)

// An index kept between visits is updated by deltas.

var deltaMagic = []byte("GJSD1")

// Delta is the change one refresh made to an index.
type Delta struct {
	Put    map[string]*doc `json:"put,omitempty"`
	Remove []string        `json:"remove,omitempty"`
}

// Empty says the delta changes nothing.
func (d Delta) Empty() bool { return len(d.Put) == 0 && len(d.Remove) == 0 }

// DeltaFor is the change a refresh returned.
func (ix *Index) DeltaFor(res Result) Delta {
	d := Delta{Put: map[string]*doc{}}
	for _, id := range res.Changed {
		if x, ok := ix.docs[id]; ok {
			d.Put[id] = x
		}
	}
	d.Remove = slices.Clone(res.Gone)
	return d
}

// baseID ties a delta to its base index.
func baseID(base []byte) ([]byte, error) {
	n := len(magic) + 12
	if len(base) < n {
		return nil, ErrCorrupt
	}
	return base[len(magic):n], nil
}

func deltaAAD(id []byte, seq int) []byte {
	aad := append(slices.Clone(deltaMagic), id...)
	return binary.BigEndian.AppendUint64(aad, uint64(seq))
}

// SealDelta seals d as the seq-th change.
func SealDelta(key, base []byte, seq int, d Delta) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	id, err := baseID(base)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(slices.Clone(deltaMagic), nonce...)
	return aead.Seal(out, nonce, plain, deltaAAD(id, seq)), nil
}

// ApplyDelta opens the seq-th change sealed by SealDelta.
func (ix *Index) ApplyDelta(key, base []byte, seq int, sealed []byte) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	id, err := baseID(base)
	if err != nil {
		return err
	}
	n := len(deltaMagic) + aead.NonceSize()
	if len(sealed) < n+aead.Overhead() || string(sealed[:len(deltaMagic)]) != string(deltaMagic) {
		return ErrCorrupt
	}
	plain, err := aead.Open(nil, sealed[len(deltaMagic):n], sealed[n:], deltaAAD(id, seq))
	if err != nil {
		return ErrCorrupt
	}
	var d Delta
	if json.Unmarshal(plain, &d) != nil {
		return errors.Join(ErrCorrupt)
	}
	if ix.docs == nil {
		ix.docs = map[string]*doc{}
	}
	for id, x := range d.Put {
		if x != nil {
			nd := newDoc(x.Title, x.Text)
			nd.Updated = x.Updated
			ix.docs[id] = nd
		}
	}
	for _, id := range d.Remove {
		delete(ix.docs, id)
	}
	return nil
}
