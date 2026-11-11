package encstore

import (
	"encoding/json"
	"errors"
	"fmt"
)

const keyListKind = "gojiberry-keylist/1"

const maxKeyListBytes = 256 << 10

var ErrNotAKeyList = errors.New("that is not a key list from another device")

type keyListEnvelope struct {
	Kind   string `json:"kind"`
	Config Config `json:"config"`
}

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

// ParseKeyList reads a key list made by ExportKeyList.
func ParseKeyList(text string) (Config, string, error) {
	if len(text) > maxKeyListBytes {
		return Config{}, "", fmt.Errorf("%w: too large", ErrNotAKeyList)
	}
	var env keyListEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil || env.Kind != keyListKind {
		return Config{}, "", ErrNotAKeyList
	}
	cfg := env.Config
	cfg.Pending = nil
	if len(cfg.Owners) == 0 || len(cfg.Owners) > 64 {
		return Config{}, "", fmt.Errorf("%w: no keys", ErrNotAKeyList)
	}
	if _, _, err := parseOwners(cfg.Owners); err != nil {
		return Config{}, "", fmt.Errorf("%w: %v", ErrNotAKeyList, err)
	}
	return cfg, ShortCode(cfg.Owners), nil
}

func ShortCode(owners []Owner) string {
	d := ownersDigest(owners)
	return d[0:4] + " " + d[4:8] + " " + d[8:12]
}
