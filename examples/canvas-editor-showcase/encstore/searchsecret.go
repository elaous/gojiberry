package encstore

import (
	"crypto/sha256"
)

const searchContext = "gojiberry/showcase/search-secret/v1"

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
