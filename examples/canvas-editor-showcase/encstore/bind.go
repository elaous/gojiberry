package encstore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"

	"github.com/elaous/gojiberry/docstore/encrypt"
)

// Binding the key list to the keys.
//
// The owner list is kept next to the store, so whoever can write the storage can replace it
// with one that adds their own key: every file saved from then on would be encrypted to them
// too. A key that unlocks the store should notice. So the list is signed by the keys that
// hold it:
//
//   - A Seal is the owner list as one key saw it, signed with that key's own signing key (the
//     one derived from the passkey's PRF output, the recovery key, or the SSH key itself, so it
//     cannot be made without the secret). At unlock the key checks its seal.
//   - Another key may have changed the list since (an owner added from a different key), so
//     each change is also recorded as a Link: the digest of the list before, the list after,
//     signed by a key that was an owner before. A seal is accepted when a chain of links, each
//     signed by an owner of the list it leaves, leads from it to the current list. A forged
//     link needs an owner's secret.
//
// What this does not do is in the package comment: it cannot detect a config that was put
// back to an older valid state, a config presented without any seal when none was ever made
// (trust on first use), or a deleted config.

const (
	sealContext = "gojiberry/showcase/enc-config/seal/v1"
	linkContext = "gojiberry/showcase/enc-config/link/v1"
	maxLinks    = 32
)

// Seal is an owner list signed by one of its keys' signing key.
type Seal struct {
	Signer string  `json:"signer"` // hex Ed25519 public key
	Owners []Owner `json:"owners"` // the list the key saw
	Sig    string  `json:"sig"`    // hex
}

// Link is a change of the owner list, signed by a key that was an owner before it.
type Link struct {
	From   string  `json:"from"` // digest of the list before
	To     []Owner `json:"to"`   // the list after
	Signer string  `json:"signer"`
	Sig    string  `json:"sig"`
}

// ownersDigest is a hex SHA-256 of the owner list, whatever its order.
func ownersDigest(list []Owner) string {
	l := slices.Clone(list)
	sort.Slice(l, func(i, j int) bool { return l[i].Public < l[j].Public })
	h := sha256.New()
	for _, o := range l {
		b, _ := json.Marshal(o)
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sealMessage(digest string) []byte { return []byte(sealContext + "\x00" + digest) }

func linkMessage(from, to string) []byte { return []byte(linkContext + "\x00" + from + "\x00" + to) }

func makeSeal(sg *encrypt.Signer, owners []Owner) Seal {
	return Seal{Signer: hex.EncodeToString(sg.Public()), Owners: slices.Clone(owners), Sig: hex.EncodeToString(sg.Sign(sealMessage(ownersDigest(owners))))}
}

func (sl Seal) valid() bool {
	pub, err := hex.DecodeString(sl.Signer)
	if err != nil {
		return false
	}
	sig, err := hex.DecodeString(sl.Sig)
	if err != nil {
		return false
	}
	return encrypt.VerifySigned(ed25519.PublicKey(pub), sealMessage(ownersDigest(sl.Owners)), sig)
}

func hasSigner(list []Owner, signer string) bool {
	return slices.ContainsFunc(list, func(o Owner) bool { return o.Signer == signer })
}

func hasOwnerKey(list []Owner, public string) bool {
	return slices.ContainsFunc(list, func(o Owner) bool { return o.Public == public })
}

// bindingFor makes the seals and links of a change from list "from" to list "to", signed by the
// keys that are unlocked now. The old seals of keys that stay owners are kept (they reach the new
// list through the link).
func bindingFor(old Config, to []Owner, signers []*encrypt.Signer) (seals []Seal, links []Link) {
	toSigner := map[string]bool{}
	for _, o := range to {
		toSigner[o.Signer] = true
	}
	have := map[string]bool{}
	for _, sg := range signers {
		pub := hex.EncodeToString(sg.Public())
		if toSigner[pub] && !have[pub] {
			have[pub] = true
			seals = append(seals, makeSeal(sg, to))
		}
	}
	for _, sl := range old.Seals {
		if toSigner[sl.Signer] && !have[sl.Signer] && sl.valid() {
			seals = append(seals, sl)
		}
	}
	links = slices.Clone(old.Links)
	if len(old.Owners) > 0 && ownersDigest(old.Owners) != ownersDigest(to) {
		for _, sg := range signers {
			if hasSigner(old.Owners, hex.EncodeToString(sg.Public())) {
				from, dst := ownersDigest(old.Owners), ownersDigest(to)
				links = append(links, Link{From: from, To: slices.Clone(to), Signer: hex.EncodeToString(sg.Public()),
					Sig: hex.EncodeToString(sg.Sign(linkMessage(from, dst)))})
				break
			}
		}
	}
	if len(links) > maxLinks {
		links = links[len(links)-maxLinks:]
	}
	return seals, links
}

// pendingSeals signs the owner list a change is making current, for the journal.
func pendingSeals(to []Owner, signers []*encrypt.Signer) []Seal {
	var out []Seal
	for _, sg := range signers {
		out = append(out, makeSeal(sg, to))
	}
	return out
}

// checkSeal is the check at unlock: the key's seal must lead to the current owner list. A key
// that has no seal is accepted once when it is an SSH key (its secret was never available when
// the list was made) or the list is of the first generation (Version 1: made before seals
// existed, or by a change that could not seal every key; trust on first use); a seal that does
// not lead here, or a missing one for a key that always has one in a Version 2 list, is
// ErrConfigChanged.
func (s *Store) checkSeal(o Owner) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg.Pending != nil {
		return nil // an unfinished change is checked when it is finished (Recover)
	}
	var seal *Seal
	for i := range cfg.Seals {
		if cfg.Seals[i].Signer == o.Signer {
			seal = &cfg.Seals[i]
		}
	}
	if seal == nil {
		if o.Kind == KindSSH || cfg.Version < 2 {
			return nil
		}
		return ErrConfigChanged
	}
	if !seal.valid() || !hasOwnerKey(seal.Owners, o.Public) {
		return ErrConfigChanged
	}
	cur := seal.Owners
	target := ownersDigest(cfg.Owners)
	for steps := 0; ownersDigest(cur) != target; steps++ {
		if steps > len(cfg.Links) {
			return ErrConfigChanged
		}
		from := ownersDigest(cur)
		var next *Link
		for i := range cfg.Links {
			if cfg.Links[i].From == from && hasSigner(cur, cfg.Links[i].Signer) {
				pub, err1 := hex.DecodeString(cfg.Links[i].Signer)
				sig, err2 := hex.DecodeString(cfg.Links[i].Sig)
				if err1 == nil && err2 == nil && encrypt.VerifySigned(ed25519.PublicKey(pub), linkMessage(from, ownersDigest(cfg.Links[i].To)), sig) {
					next = &cfg.Links[i]
					break
				}
			}
		}
		if next == nil {
			return ErrConfigChanged
		}
		cur = next.To
	}
	return nil
}

// sealOnUnlock makes the unlocked key's seal current: a first seal (trust on first use) or the
// seal of the list as it stands now. It is best effort: a failure to keep it only means the check
// runs the chain again next time.
func (s *Store) sealOnUnlock(o Owner, sg *encrypt.Signer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Pending != nil || len(s.cfg.Owners) == 0 {
		return
	}
	now := ownersDigest(s.cfg.Owners)
	for _, sl := range s.cfg.Seals {
		if sl.Signer == o.Signer && ownersDigest(sl.Owners) == now && sl.valid() {
			return
		}
	}
	cfg := cloneConfig(s.cfg)
	seals := cfg.Seals[:0:0]
	for _, sl := range cfg.Seals {
		if sl.Signer != o.Signer {
			seals = append(seals, sl)
		}
	}
	cfg.Seals = append(seals, makeSeal(sg, cfg.Owners))
	cfg.Version = versionFor(cfg)
	if s.Persist != nil && s.Persist(cloneConfig(cfg)) != nil {
		return
	}
	s.cfg = cfg
}

// versionFor is 2 when every owner key that is not an SSH key has a seal of its own (so a
// missing seal means the list was tampered with), else the list stays of the first generation,
// where a key without a seal is accepted once.
func versionFor(cfg Config) int {
	for _, o := range cfg.Owners {
		if o.Kind == KindSSH {
			continue
		}
		if !slices.ContainsFunc(cfg.Seals, func(sl Seal) bool { return sl.Signer == o.Signer && sl.valid() }) {
			return max(cfg.Version, 1)
		}
	}
	return 2
}
