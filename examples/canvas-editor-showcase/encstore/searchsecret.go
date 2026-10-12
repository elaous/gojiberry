package encstore

import (
	"crypto/sha256"
)

const searchContext = "gojiberry/showcase/search-secret/v1"

// SearchSecret is a secret for sealing something kept on this device that is derived from the
// unlocked owner key (for the sealed search index): the key's signature of a fixed statement, hashed.
// Ed25519 signs the same statement to the same bytes every time, so the same key gives the same secret
// on every visit, and nothing that is stored (the key list, the files, the server) can produce it; it
// exists only while a key is held. The statement names the owner list, so a change of the owners makes
// an earlier sealed value unopenable (a removed key must not open what was sealed after it was
// trusted). False while the store is locked, has no keys, or is in the middle of a change.
func (s *Store) SearchSecret() ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sg := s.signer()
	if sg == nil || s.cfg.Pending != nil || len(s.cfg.Owners) == 0 {
		return nil, false
	}
	sig := sg.Sign([]byte(searchContext + "\x00" + ownersDigest(s.cfg.Owners)))
	h := sha256.Sum256(append([]byte(searchContext+"\x00"), sig...))
	return h[:], true
}
