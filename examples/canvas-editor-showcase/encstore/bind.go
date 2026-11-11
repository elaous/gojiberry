package encstore

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"

	"github.com/elaous/gojiberry/docstore/encrypt"
)

const (
	sealContext = "gojiberry/showcase/enc-config/seal/v1"
	linkContext = "gojiberry/showcase/enc-config/link/v1"
	maxLinks    = 32
)

type Seal struct {
	Signer string  `json:"signer"` // hex Ed25519 public key
	Owners []Owner `json:"owners"` // the list the key saw
	Sig    string  `json:"sig"`    // hex
}

type Link struct {
	From   string  `json:"from"`            // digest of the list before
	To     []Owner `json:"to"`              // the list after
	Nonce  string  `json:"nonce,omitempty"` // hex, signed with the lists; empty on links made before it existed
	Signer string  `json:"signer"`
	Sig    string  `json:"sig"`
}

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

func linkMessageN(from, to, nonce string) []byte {
	if nonce == "" {
		return linkMessage(from, to)
	}
	return append(linkMessage(from, to), []byte("\x00"+nonce)...)
}

func sameLink(a, b Link) bool {
	return a.From == b.From && a.Signer == b.Signer && a.Sig == b.Sig && a.Nonce == b.Nonce && ownersDigest(a.To) == ownersDigest(b.To)
}

func linkRecorded(cfg Config, l Link) bool {
	return slices.ContainsFunc(cfg.Links, func(r Link) bool { return sameLink(r, l) })
}

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
	// a nonce keeps a repeat of the same lists distinct
	if jl != nil && jl.valid() && ownersDigest(jl.To) == dst && !linkRecorded(Config{Links: links}, *jl) && (jl.Nonce != "" || !reaches(jl.From)) {
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

func journalLink(old, to []Owner, signers []*encrypt.Signer) *Link {
	if len(old) == 0 {
		return nil
	}
	from, dst := ownersDigest(old), ownersDigest(to)
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return nil
	}
	nonce := hex.EncodeToString(nb)
	for _, sg := range signers {
		pub := hex.EncodeToString(sg.Public())
		if hasSigner(old, pub) {
			return &Link{From: from, To: slices.Clone(to), Nonce: nonce, Signer: pub, Sig: hex.EncodeToString(sg.Sign(linkMessageN(from, dst, nonce)))}
		}
	}
	return nil
}

func pendingSeals(to []Owner, signers []*encrypt.Signer) []Seal {
	var out []Seal
	for _, sg := range signers {
		out = append(out, makeSeal(sg, to))
	}
	return out
}

func ownSeal(cfg Config, o Owner) *Seal {
	for i := range cfg.Seals {
		sl := &cfg.Seals[i]
		if sl.Signer == o.Signer && sl.valid() && hasOwnerKey(sl.Owners, o.Public) {
			return sl
		}
	}
	return nil
}

func walk(cfg Config, start []Owner, target string) ([][]Owner, bool) {
	visited := map[string]bool{}
	var dfs func(cur []Owner, path [][]Owner) ([][]Owner, bool)
	dfs = func(cur []Owner, path [][]Owner) ([][]Owner, bool) {
		d := ownersDigest(cur)
		if d == target {
			return path, true
		}
		if visited[d] {
			return nil, false
		}
		visited[d] = true
		for i := range cfg.Links {
			l := &cfg.Links[i]
			if l.From != d || !hasSigner(cur, l.Signer) || !l.valid() {
				continue
			}
			if got, ok := dfs(l.To, append(path[:len(path):len(path)], l.To)); ok {
				return got, true
			}
		}
		return nil, false
	}
	if got, ok := dfs(start, [][]Owner{start}); ok {
		return got, true
	}
	return [][]Owner{start}, false
}

func (l Link) valid() bool {
	pub, err1 := hex.DecodeString(l.Signer)
	sig, err2 := hex.DecodeString(l.Sig)
	return err1 == nil && err2 == nil && encrypt.VerifySigned(ed25519.PublicKey(pub), linkMessageN(l.From, ownersDigest(l.To), l.Nonce), sig)
}

func reach(cfg Config, o Owner, target string) ([][]Owner, bool) {
	seal := ownSeal(cfg, o)
	if seal == nil {
		return nil, false
	}
	return walk(cfg, seal.Owners, target)
}

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
				return nil, ErrConfigChanged
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
