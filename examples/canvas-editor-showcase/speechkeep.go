package main

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/speechcoach"
)

const (
	keptSpeechIndex = "speech/index"
	keptSpeechCorr  = "speech/corrections"
	keptSpeechPurp  = "showcase speech"
)

func keptSpeechEntry(id string) string { return "speech/e/" + id }

func speechSealKey(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("no secret to seal the recordings with")
	}
	return speechcoach.SealKey(secret, keptSpeechPurp)
}

func putSealed(v sealedVault, key []byte, name string, data []byte) error {
	sealed, err := speechcoach.Seal(key, name, data)
	if err != nil {
		return err
	}
	return v.Put(name, sealed)
}

func getSealed(v sealedVault, key []byte, name string) ([]byte, error) {
	sealed, err := v.Get(name)
	if err != nil || sealed == nil {
		return nil, err
	}
	return speechcoach.Open(key, name, sealed)
}

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

func wipeSpeech(v sealedVault) {
	if l, ok := v.(interface{ Names(prefix string) []string }); ok {
		for _, n := range l.Names("speech/") {
			_ = v.Delete(n)
		}
	}
	_ = v.Delete(keptSpeechIndex)
	_ = v.Delete(keptSpeechCorr)
}

func keepCorrections(v sealedVault, key []byte, c *speechcoach.Corrections) error {
	b, err := c.Marshal()
	if err != nil {
		return err
	}
	return putSealed(v, key, keptSpeechCorr, b)
}

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
