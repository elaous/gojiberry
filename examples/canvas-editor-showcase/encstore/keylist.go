package encstore

import (
	"encoding/json"
	"errors"
	"fmt"
)

// A store kept on a server is used from several devices. Each device keeps its own copy of the key list (the
// owner keys' public halves and the signatures that chain them), because that list is what it checks the
// store's files against. A new device that holds a key can rebuild the list from the files (Restore), but
// only as a store of that one key; ExportKeyList and ParseKeyList move the whole list from a device that
// has it. It holds public information only, and a device that is given a list still checks it against the
// store at unlock (the seals, and the fingerprint the person is asked to compare), so a list handed over by
// someone else does not make a store trust their keys.

const keyListKind = "gojiberry-keylist/1"

// maxKeyListBytes bounds what ParseKeyList reads.
const maxKeyListBytes = 256 << 10

// ErrNotAKeyList is returned by ParseKeyList for text that is not a key list.
var ErrNotAKeyList = errors.New("that is not a key list from another device")

type keyListEnvelope struct {
	Kind   string `json:"kind"`
	Config Config `json:"config"`
}

// ExportKeyList returns the store's key list as text to carry to another device, and the code
// (ShortCode) the person compares on both devices. A change that is not finished, or a store with
// encryption off, has nothing to hand over.
func (s *Store) ExportKeyList() (text, code string, err error) {
	cfg := s.Config()
	if len(cfg.Owners) == 0 {
		return "", "", errors.New("encryption is off: there is no key list")
	}
	if cfg.Pending != nil {
		return "", "", ErrInterrupted
	}
	b, err := json.Marshal(keyListEnvelope{Kind: keyListKind, Config: cfg})
	if err != nil {
		return "", "", err
	}
	return string(b), ShortCode(cfg.Owners), nil
}

// ParseKeyList reads a key list made by ExportKeyList. It checks the shape (a known kind, a bounded size,
// owner keys that parse, nothing unfinished) and returns the Config and the code to compare.
func ParseKeyList(text string) (Config, string, error) {
	if len(text) > maxKeyListBytes {
		return Config{}, "", fmt.Errorf("%w: too large", ErrNotAKeyList)
	}
	var env keyListEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil || env.Kind != keyListKind {
		return Config{}, "", ErrNotAKeyList
	}
	cfg := env.Config
	cfg.Pending = nil // a half-made change on another device is not this device's to finish
	if len(cfg.Owners) == 0 || len(cfg.Owners) > 64 {
		return Config{}, "", fmt.Errorf("%w: no keys", ErrNotAKeyList)
	}
	if _, _, err := parseOwners(cfg.Owners); err != nil {
		return Config{}, "", fmt.Errorf("%w: %v", ErrNotAKeyList, err)
	}
	return cfg, ShortCode(cfg.Owners), nil
}

// ShortCode is a short code of an owner list (the first 12 characters of its digest, in groups of four)
// that a person can read out and compare on two devices.
func ShortCode(owners []Owner) string {
	d := ownersDigest(owners)
	return d[0:4] + " " + d[4:8] + " " + d[8:12]
}
