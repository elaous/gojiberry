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
// What this does not do is in the package comment. The seals alone cannot detect a config that
// was put back to an older valid state or a deleted one; the anchor (anchor.go) and Inspect do.

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

// bindingFor makes the seals and links of a change from list "old" to list "to", signed by the
// keys that are unlocked now. The old seals of keys that stay owners are kept (they reach the new
// list through the link). jl is the link the journal carries (the old list's signature over the
// new one, made when the change started by a holder of an old key): it is kept when it verifies,
// so the old keys still reach the new list when the change was finished with a new key alone.
func bindingFor(old Config, to []Owner, signers []*encrypt.Signer, jl *Link) (seals []Seal, links []Link) {
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
	dst := ownersDigest(to)
	reaches := func(from string) bool {
		return slices.ContainsFunc(links, func(l Link) bool { return l.From == from && ownersDigest(l.To) == dst })
	}
	if jl != nil && jl.valid() && ownersDigest(jl.To) == dst && !reaches(jl.From) {
		links = append(links, *jl)
	}
	if len(old.Owners) > 0 && ownersDigest(old.Owners) != dst && !reaches(ownersDigest(old.Owners)) {
		for _, sg := range signers {
			if hasSigner(old.Owners, hex.EncodeToString(sg.Public())) {
				from := ownersDigest(old.Owners)
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

// journalLink is the old list's signature over the new one, by a held key of the old list (nil when
// no such key is held: turning encryption on, or a change started without one).
func journalLink(old, to []Owner, signers []*encrypt.Signer) *Link {
	if len(old) == 0 {
		return nil
	}
	from, dst := ownersDigest(old), ownersDigest(to)
	for _, sg := range signers {
		pub := hex.EncodeToString(sg.Public())
		if hasSigner(old, pub) {
			return &Link{From: from, To: slices.Clone(to), Signer: pub, Sig: hex.EncodeToString(sg.Sign(linkMessage(from, dst)))}
		}
	}
	return nil
}

// pendingSeals signs the owner list a change is making current, for the journal.
func pendingSeals(to []Owner, signers []*encrypt.Signer) []Seal {
	var out []Seal
	for _, sg := range signers {
		out = append(out, makeSeal(sg, to))
	}
	return out
}

// ownSeal is the key's own seal in cfg: signed by its signing key, listing the key itself.
func ownSeal(cfg Config, o Owner) *Seal {
	for i := range cfg.Seals {
		sl := &cfg.Seals[i]
		if sl.Signer == o.Signer && sl.valid() && hasOwnerKey(sl.Owners, o.Public) {
			return sl
		}
	}
	return nil
}

// walk follows the links of cfg from the list start to the list whose digest is target, each link
// signed by a key of the list it leaves. It returns every list on the way (start first) and
// whether target was reached.
func walk(cfg Config, start []Owner, target string) ([][]Owner, bool) {
	cur := start
	lists := [][]Owner{cur}
	for steps := 0; ownersDigest(cur) != target; steps++ {
		if steps > len(cfg.Links) {
			return lists, false
		}
		from := ownersDigest(cur)
		var next *Link
		for i := range cfg.Links {
			if cfg.Links[i].From == from && hasSigner(cur, cfg.Links[i].Signer) && cfg.Links[i].valid() {
				next = &cfg.Links[i]
				break
			}
		}
		if next == nil {
			return lists, false
		}
		cur = next.To
		lists = append(lists, cur)
	}
	return lists, true
}

func (l Link) valid() bool {
	pub, err1 := hex.DecodeString(l.Signer)
	sig, err2 := hex.DecodeString(l.Sig)
	return err1 == nil && err2 == nil && encrypt.VerifySigned(ed25519.PublicKey(pub), linkMessage(l.From, ownersDigest(l.To)), sig)
}

// reach says whether the key's own seal leads, through links signed by owners, to the list whose
// digest is target, and returns the lists on the way (all of them vouched for by the key's own
// signature and the owners'). This ignores any unfinished change.
func reach(cfg Config, o Owner, target string) ([][]Owner, bool) {
	seal := ownSeal(cfg, o)
	if seal == nil {
		return nil, false
	}
	return walk(cfg, seal.Owners, target)
}

// checkSeal is the check at unlock: the key's own seal must lead to the current owner list. A key
// that has no seal is accepted once only when it is an SSH key (its secret was never available
// when the list was made: trust on first use, which the anchor then backs, anchor.go); a passkey
// or a recovery key always has one (every change is made with it in hand), so a list without its
// seal is ErrConfigChanged, whatever Version says (Version is written by whoever writes the
// storage). It returns the lists the seal vouches for (nil for a first-use SSH key).
//
// While a change is unfinished nothing is checked here: the store is read-only then, and Recover
// authenticates the journal (authJournal).
func (s *Store) checkSeal(o Owner) ([][]Owner, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg.Pending != nil {
		return nil, nil
	}
	if ownSeal(cfg, o) == nil {
		for _, sl := range cfg.Seals {
			if sl.Signer == o.Signer {
				return nil, ErrConfigChanged // a seal of this key that is not valid or not its own
			}
		}
		if o.Kind == KindSSH {
			return nil, nil
		}
		return nil, ErrConfigChanged
	}
	lists, ok := reach(cfg, o, ownersDigest(cfg.Owners))
	if !ok {
		return nil, ErrConfigChanged
	}
	return lists, nil
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
