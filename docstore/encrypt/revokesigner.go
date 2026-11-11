package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// RevokedSignerPrefix is the keyspace of revoked-signer manifests.
const RevokedSignerPrefix = docstore.RevokedSignerPrefix

type signerManifest struct {
	Key    string      `json:"key"` // SignerFingerprint of the removed key
	At     int64       `json:"at"`
	Frames []FrameMark `json:"frames"`
	Signer []byte      `json:"signer"` // the public key that signed
	Sig    string      `json:"sig"`
}

func (m signerManifest) message() []byte {
	h := sha256.New()
	h.Write([]byte("gojiberry-revoked-signer-manifest-v1|" + m.Key + "|"))
	_ = json.NewEncoder(h).Encode(struct {
		At     int64       `json:"at"`
		Frames []FrameMark `json:"frames"`
	}{m.At, m.Frames})
	return h.Sum(nil)
}

func (s *Sharing) loadSignerManifest(ctx context.Context, fp string) (*signerManifest, error) {
	d, err := s.Adapter.Inner.Load(ctx, RevokedSignerPrefix+fp)
	if errors.Is(err, docstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m signerManifest
	if json.Unmarshal(d.Data, &m) != nil || m.Key != fp || !bytes.Equal(m.Signer, s.Signer.Public()) {
		return nil, nil // a removed key can delete a manifest, never forge one
	}
	if sig, err := base64.StdEncoding.DecodeString(m.Sig); err != nil || !VerifySigned(m.Signer, m.message(), sig) {
		return nil, nil
	}
	return &m, nil
}

func (s *Sharing) saveSignerManifest(ctx context.Context, fp string, frames []FrameMark) (*signerManifest, error) {
	sort.Slice(frames, func(i, j int) bool {
		if frames[i].File != frames[j].File {
			return frames[i].File < frames[j].File
		}
		return frames[i].Hash < frames[j].Hash
	})
	m := signerManifest{Key: fp, At: time.Now().Unix(), Frames: frames, Signer: s.Signer.Public()}
	m.Sig = base64.StdEncoding.EncodeToString(s.Signer.Sign(m.message()))
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := s.Adapter.Inner.Save(ctx, RevokedSignerPrefix+fp, data); err != nil {
		return nil, err
	}
	return &m, nil
}

// Errors of RevokeSigner; the key stays when they occur.
var (
	// ErrRevokeNeedsSigner: the Sharing and its Adapter need a Signer.
	ErrRevokeNeedsSigner = errors.New("encrypt: RevokeSigner needs the Sharing's and the Adapter's Signer: the key that stays")
	// ErrRevokeCurrentSigner: the key to remove is the current signer.
	ErrRevokeCurrentSigner = errors.New("encrypt: RevokeSigner cannot remove the Signer it signs with")
	// ErrRevokeCannotResign: some documents could not be re-signed.
	ErrRevokeCannotResign = errors.New("encrypt: some documents could not be signed again, so the key was not removed")
	// ErrRevokeStillWriting: the store kept changing past MaxPasses.
	ErrRevokeStillWriting = errors.New("encrypt: the key kept writing frames while it was being replaced, so it was not removed")
)

// RevokeSignerOptions say how RevokeSigner goes about its work.
type RevokeSignerOptions struct {
	// Remove takes the key out of every list of owners.
	Remove func(ctx context.Context) error
	// Progress is passed on to RepairAll for each document.
	Progress func(RepairProgress)
	// MaxPasses bounds how often the store is swept.
	MaxPasses int
}

// RevokeSignerReport is what RevokeSigner did.
type RevokeSignerReport struct {
	// Passes is how many times the store was swept.
	Passes, Resigned int
	// Removed says Remove was called.
	Removed bool
	// Late are the documents with late frames of the key.
	Late []string
	// Manifest is how many frames the removed key signed.
	Manifest int
	During   []string
	// Skipped are the documents that could not be repaired.
	Skipped []RepairSkip
}

// RevokeSigner replaces an owner signing key by others.
func (s *Sharing) RevokeSigner(ctx context.Context, key ed25519.PublicKey, o RevokeSignerOptions) (rep RevokeSignerReport, _ error) {
	if s.Adapter == nil || s.Signer == nil || s.Adapter.Signer == nil || o.Remove == nil {
		return rep, ErrRevokeNeedsSigner
	}
	if bytes.Equal(s.Signer.Public(), key) || bytes.Equal(s.Adapter.Signer.Public(), key) {
		return rep, ErrRevokeCurrentSigner
	}
	maxPasses := o.MaxPasses
	if maxPasses <= 0 {
		maxPasses = 5
	}
	fp := SignerFingerprint(key)
	man, err := s.loadSignerManifest(ctx, fp)
	if err != nil {
		return rep, err
	}
	if man == nil {
		first, err := s.RepairAll(ctx, RepairAllOptions{DryRun: true, FramesOf: key})
		if err != nil {
			return rep, err
		}
		if man, err = s.saveSignerManifest(ctx, fp, first.Frames); err != nil {
			return rep, err
		}
	}
	rep.Manifest = len(man.Frames)
	known := make(map[FrameMark]bool, len(man.Frames))
	for _, f := range man.Frames {
		known[f] = true
	}
	during := map[string]bool{}
	note := func(r RepairAllReport) {
		for _, f := range r.Frames {
			if !known[f] {
				during[f.File] = true
			}
		}
	}
	defer func() {
		rep.During = rep.During[:0]
		for f := range during {
			rep.During = append(rep.During, f)
		}
		sort.Strings(rep.During)
	}()
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		pass, err := s.RepairAll(ctx, RepairAllOptions{Resign: true, Progress: o.Progress, FramesOf: key})
		if err != nil {
			return rep, err
		}
		note(pass)
		rep.Passes++
		rep.Resigned += pass.Resigned
		check, err := s.RepairAll(ctx, RepairAllOptions{Resign: true, DryRun: true, FramesOf: key})
		if err != nil {
			return rep, err
		}
		note(check)
		if len(check.Skipped) > 0 {
			rep.Skipped = check.Skipped
			return rep, fmt.Errorf("%w: %d documents", ErrRevokeCannotResign, len(check.Skipped))
		}
		if len(check.Todo) == 0 {
			break
		}
		if rep.Passes >= maxPasses {
			return rep, fmt.Errorf("%w: %d documents after %d passes", ErrRevokeStillWriting, len(check.Todo), rep.Passes)
		}
	}
	if err := o.Remove(ctx); err != nil {
		return rep, fmt.Errorf("encrypt: removing the key from the lists: %w", err)
	}
	rep.Removed = true
	after, err := s.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil {
		return rep, err
	}
	for _, sk := range after.Skipped {
		if sk.Reason == SkipForged {
			rep.Late = append(rep.Late, sk.Doc)
		}
	}
	return rep, nil
}
