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

// RevokedSignerPrefix is the keyspace of the manifests RevokeSigner writes (one record per removed key,
// "revoked-signer/<fingerprint>"), in the clear and signed by the key that stays. DefaultSkip leaves it alone.
const RevokedSignerPrefix = "revoked-signer/"

// signerManifest is what the removed key had written when the removal began: the frames it signed, by file and
// content hash. It is signed by the key that stays, so the removed key cannot replace it with its own.
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

// loadSignerManifest reads the manifest of a key if one is stored and verifies under the signer that stays.
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
		return nil, nil // not ours: made afresh (a removed key can delete a manifest, never forge one)
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

// Errors of RevokeSigner. The key is not removed in any of these cases.
var (
	// ErrRevokeNeedsSigner: the Sharing and its Adapter need a Signer (a key that stays) to sign everything again with.
	ErrRevokeNeedsSigner = errors.New("encrypt: RevokeSigner needs the Sharing's and the Adapter's Signer: the key that stays")
	// ErrRevokeCurrentSigner: the key to remove is the Signer the run signs with.
	ErrRevokeCurrentSigner = errors.New("encrypt: RevokeSigner cannot remove the Signer it signs with")
	// ErrRevokeCannotResign: some documents could not be signed again (see RevokeSignerReport.Skipped); removing the
	// key would make their frames refused as forged.
	ErrRevokeCannotResign = errors.New("encrypt: some documents could not be signed again, so the key was not removed")
	// ErrRevokeStillWriting: after MaxPasses passes the removed key was still writing frames; try again when it has stopped.
	ErrRevokeStillWriting = errors.New("encrypt: the key kept writing frames while it was being replaced, so it was not removed")
)

// RevokeSignerOptions say how RevokeSigner goes about it.
type RevokeSignerOptions struct {
	// Remove takes the key out of every list of owner signers the host holds (the Adapter's keyring, the
	// Sharing's OwnerSigners, its config) and makes the Sharing and Adapter it was called on see that. It is
	// called once, when no document is left that the key signed, and must be idempotent (a run that is
	// cut short is finished by running RevokeSigner again). Required.
	Remove func(ctx context.Context) error
	// Progress is passed on to RepairAll for each document.
	Progress func(RepairProgress)
	// MaxPasses bounds how often the store is signed again while the key keeps writing (5 if 0).
	MaxPasses int
}

// RevokeSignerReport is what RevokeSigner did.
type RevokeSignerReport struct {
	// Passes is how many times the store was signed again; Resigned the documents and items moved to the
	// signer that stays, over all passes.
	Passes, Resigned int
	// Removed says Remove was called.
	Removed bool
	// Late are the documents in which a frame of the removed key was found after the removal: written by the
	// key while the removal was happening. They are refused as forged from now on (the reader says so), and the
	// owner decides what to do with each (Repair with AcceptUnsigned for a file that is theirs, else restore it).
	Late []string
	// Manifest is how many frames the removed key had signed when the removal began (the manifest kept in the
	// store as RevokedSignerPrefix + the key's fingerprint, signed by the key that stays; a run that is finished
	// by running it again keeps the first manifest).
	Manifest int
	// During are the files in which the removed key wrote a frame after the removal began: a frame that is not in
	// the manifest (another file, or the same file with other content). RevokeSigner signed them again like the
	// rest, so they verify; look at each before trusting it. Frames the key writes after the last scan are in Late.
	During []string
	// Skipped are the documents that could not be signed again, with the reason (when ErrRevokeCannotResign).
	Skipped []RepairSkip
}

// RevokeSigner replaces an owner signing key by the one the Sharing signs with, as one operation: it signs
// every document, record and the index again with the Signer that stays (RepairAll with Resign), checks by
// a dry run that nothing is left that the old key signed (running again if it wrote meanwhile), calls
// o.Remove to take the old key out of the lists, and scans once more: a frame of the old key that appears
// by then is refused as forged and reported in Late.
//
// What it does not do: stop a holder of the old key who can write the store from writing bytes until Remove
// has run, or after it (the store cannot refuse bytes). It shortens the time in which what they write
// verifies to the duration of the run, and reports what they wrote in it. See docs/frame-revocation-design.md.
//
// It is restartable: every step is idempotent, so a run that is cut short (context, crash, a failed Remove) is
// finished by running it again. Nothing is removed unless every document could be signed again.
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
	// What the key had written when the removal began, signed by the key that stays. A manifest left by a run
	// that was cut short is kept: the first one is the one that counts.
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
		// anything the key still signed (it wrote while the pass ran), or that could not be moved?
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
	// what the key wrote after the last check shows up now as a frame no owner signer signed
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
