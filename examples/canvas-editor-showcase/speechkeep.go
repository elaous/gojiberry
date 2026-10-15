package main

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/speechcoach"
)

// The Speech tab's recordings and corrections kept between visits (roadmap 56). A person's voice is theirs
// and sensitive, so nothing is kept unless they ask; what is kept is sealed (AES-256-GCM, speechcoach.Seal)
// under a key made from a secret that the storage cannot give away: the unlocked encrypted store's owner key,
// or, with no encrypted store, a per-device key the browser keeps non-extractable (searchvault_js.go). A locked
// store, or a browser with neither, keeps nothing. Each recording is sealed on its own, bound to its name, so
// one cannot be moved to another name; an item that cannot be opened is dropped and the rest is kept.

const (
	keptSpeechIndex = "speech/index"
	keptSpeechCorr  = "speech/corrections"
	keptSpeechPurp  = "showcase speech"
)

func keptSpeechEntry(id string) string { return "speech/e/" + id }

// speechSealKey is the key everything of the Speech tab is sealed under, from the secret the person holds.
func speechSealKey(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("no secret to seal the recordings with")
	}
	return speechcoach.SealKey(secret, keptSpeechPurp)
}

// putSealed seals data under key and name and keeps it.
func putSealed(v sealedVault, key []byte, name string, data []byte) error {
	sealed, err := speechcoach.Seal(key, name, data)
	if err != nil {
		return err
	}
	return v.Put(name, sealed)
}

// getSealed reads and opens a kept item; nil, nil when nothing is kept under the name.
func getSealed(v sealedVault, key []byte, name string) ([]byte, error) {
	sealed, err := v.Get(name)
	if err != nil || sealed == nil {
		return nil, err
	}
	return speechcoach.Open(key, name, sealed)
}

// keepSpeech brings the vault in step with the dataset: the recordings it lacks are sealed and put, the ones the
// dataset no longer has are removed, and the index of ids is written last. kept is the ids the vault is known
// to hold (updated in place), so only the difference is written.
func keepSpeech(v sealedVault, key []byte, d *speechcoach.Dataset, kept map[string]bool) error {
	now := map[string]bool{}
	var ids []string
	for _, e := range d.List() {
		now[e.ID] = true
		ids = append(ids, e.ID)
		if !kept[e.ID] {
			if err := putSealed(v, key, keptSpeechEntry(e.ID), speechcoach.EncodeEntry(e)); err != nil {
				return err
			}
			kept[e.ID] = true
		}
	}
	for id := range kept {
		if !now[id] {
			if err := v.Delete(keptSpeechEntry(id)); err != nil {
				return err
			}
			delete(kept, id)
		}
	}
	idx, _ := json.Marshal(ids)
	return putSealed(v, key, keptSpeechIndex, idx)
}

// loadSpeech reads the kept recordings into d and returns how many were opened. A recording that cannot be
// opened (changed, or sealed under another key) is removed and left out; an index that cannot be opened removes
// everything kept, since nothing can be trusted to belong to this key. kept is filled with the ids held.
func loadSpeech(v sealedVault, key []byte, d *speechcoach.Dataset, kept map[string]bool) (n int, err error) {
	raw, err := getSealed(v, key, keptSpeechIndex)
	if err != nil {
		wipeSpeech(v)
		return 0, err
	}
	if raw == nil {
		return 0, nil
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		wipeSpeech(v)
		return 0, errors.New("the kept list of recordings is damaged")
	}
	for _, id := range ids {
		b, err := getSealed(v, key, keptSpeechEntry(id))
		if err != nil || b == nil {
			_ = v.Delete(keptSpeechEntry(id))
			continue
		}
		e, err := speechcoach.DecodeEntry(b)
		if err != nil {
			_ = v.Delete(keptSpeechEntry(id))
			continue
		}
		d.Add(e)
		kept[e.ID] = true
		n++
	}
	return n, nil
}

// wipeSpeech removes every kept recording, the index and the corrections. The vault lists what it holds under
// the prefix (every vault of this page can), since the ids are inside the index, which may be the part that
// cannot be opened.
func wipeSpeech(v sealedVault) {
	if l, ok := v.(interface{ Names(prefix string) []string }); ok {
		for _, n := range l.Names("speech/") {
			_ = v.Delete(n)
		}
	}
	_ = v.Delete(keptSpeechIndex)
	_ = v.Delete(keptSpeechCorr)
}

// keepCorrections seals the correction memory.
func keepCorrections(v sealedVault, key []byte, c *speechcoach.Corrections) error {
	b, err := c.Marshal()
	if err != nil {
		return err
	}
	return putSealed(v, key, keptSpeechCorr, b)
}

// loadCorrections reads the kept corrections (an empty memory when there are none or they cannot be opened).
func loadCorrections(v sealedVault, key []byte) *speechcoach.Corrections {
	b, err := getSealed(v, key, keptSpeechCorr)
	if err != nil {
		_ = v.Delete(keptSpeechCorr)
	}
	if b == nil || err != nil {
		return &speechcoach.Corrections{}
	}
	c, err := speechcoach.UnmarshalCorrections(b)
	if err != nil {
		return &speechcoach.Corrections{}
	}
	return c
}
