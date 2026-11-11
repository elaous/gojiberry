package encrypt

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/elaous/gojiberry/docstore"
)

const witnessDomain = "gojiberry-directory-log-witness-v1|"

// WitnessMessage is what a witness signs.
func WitnessMessage(kid string, seq int64, hash string) []byte {
	return []byte(fmt.Sprintf("%s%s|%d|%s", witnessDomain, kid, seq, hash))
}

// CosignEntry makes a witness's cosignature of an entry.
func CosignEntry(sign func(msg []byte) []byte, witness, kid string, seq int64, hash string) docstore.DirectoryCosignature {
	return docstore.DirectoryCosignature{Witness: witness, Kid: kid, Seq: seq, Hash: hash,
		Sig: base64.StdEncoding.EncodeToString(sign(WitnessMessage(kid, seq, hash)))}
}

// ErrNotWitnessed is returned when too few known witnesses signed.
var ErrNotWitnessed = errors.New("encrypt: not enough witnesses cosigned the log entry")

// VerifyCosignature checks one witness's signature.
func VerifyCosignature(c docstore.DirectoryCosignature, keys map[string]ed25519.PublicKey) error {
	pub, ok := keys[c.Witness]
	sig, err := base64.StdEncoding.DecodeString(c.Sig)
	if !ok || err != nil || !ed25519.Verify(pub, WitnessMessage(c.Kid, c.Seq, c.Hash), sig) {
		return fmt.Errorf("%w: witness %q", docstore.ErrDirectoryBadCosignature, c.Witness)
	}
	return nil
}

// VerifyCosigned counts the distinct pinned witnesses that signed.
func VerifyCosigned(c docstore.DirectoryCosigned, keys map[string]ed25519.PublicKey, quorum int) error {
	seen := map[string]bool{}
	for _, w := range c.Witnesses {
		if seen[w.Witness] {
			continue
		}
		if VerifyCosignature(docstore.DirectoryCosignature{Witness: w.Witness, Kid: c.Kid, Seq: c.Seq, Hash: c.Hash, Sig: w.Sig}, keys) == nil {
			seen[w.Witness] = true
		}
	}
	if len(seen) < quorum || quorum < 1 {
		return fmt.Errorf("%w: entry %d has %d of the %d required", ErrNotWitnessed, c.Seq, len(seen), quorum)
	}
	return nil
}

func witnessID(witness string) string {
	return docstore.DirectoryLogPrefix + "witness/" + hex.EncodeToString([]byte(witness))
}

// AddCosignature implements docstore.DirectoryWitnessing.
func (d *AdapterDirectory) AddCosignature(ctx context.Context, c docstore.DirectoryCosignature) error {
	if d.Signer == nil {
		return ErrDirectoryOff
	}
	if err := VerifyCosignature(c, d.Witnesses); err != nil {
		return err
	}
	e, found, err := d.readEntry(ctx, c.Seq)
	if err != nil {
		return err
	}
	if !found || e.Hash != c.Hash || e.Kid != c.Kid {
		return fmt.Errorf("%w: the log has no such entry %d", docstore.ErrDirectoryBadCosignature, c.Seq)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	id := witnessID(c.Witness)
	d.casMu.Lock()
	defer d.casMu.Unlock()
	for range 8 {
		doc, err := d.Store.Load(ctx, id)
		prev := ""
		switch {
		case errors.Is(err, docstore.ErrNotFound):
		case err != nil:
			return err
		default:
			var old docstore.DirectoryCosignature
			if json.Unmarshal(doc.Data, &old) == nil && old.Seq > c.Seq {
				return nil
			}
			prev = docstore.Digest(doc.Data)
		}
		if ca, ok := d.Store.(docstore.ConditionalAdapter); ok {
			if err := ca.SaveIf(ctx, id, data, prev); err != nil {
				if errors.Is(err, docstore.ErrConflict) {
					continue
				}
				return err
			}
			return nil
		}
		return d.Store.Save(ctx, id, data)
	}
	return docstore.ErrDirectoryBusy
}

// Cosigned implements docstore.DirectoryWitnessing.
func (d *AdapterDirectory) Cosigned(ctx context.Context) ([]docstore.DirectoryCosigned, error) {
	if d.Signer == nil {
		return nil, ErrDirectoryOff
	}
	type key struct {
		kid, hash string
		seq       int64
	}
	groups := map[key]*docstore.DirectoryCosigned{}
	for name := range d.Witnesses {
		doc, err := d.Store.Load(ctx, witnessID(name))
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var c docstore.DirectoryCosignature
		if json.Unmarshal(doc.Data, &c) != nil || c.Witness != name || VerifyCosignature(c, d.Witnesses) != nil {
			continue
		}
		k := key{c.Kid, c.Hash, c.Seq}
		g := groups[k]
		if g == nil {
			g = &docstore.DirectoryCosigned{Kid: c.Kid, Seq: c.Seq, Hash: c.Hash}
			groups[k] = g
		}
		g.Witnesses = append(g.Witnesses, docstore.DirectoryWitnessSig{Witness: c.Witness, Sig: c.Sig})
	}
	out := make([]docstore.DirectoryCosigned, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Witnesses, func(i, j int) bool { return g.Witnesses[i].Witness < g.Witnesses[j].Witness })
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq > out[j].Seq
		}
		return len(out[i].Witnesses) > len(out[j].Witnesses)
	})
	return out, nil
}

var _ docstore.DirectoryWitnessing = (*AdapterDirectory)(nil)
