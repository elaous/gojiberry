package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
)

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
func (s *Sharing) RevokeSigner(ctx context.Context, key ed25519.PublicKey, o RevokeSignerOptions) (RevokeSignerReport, error) {
	var rep RevokeSignerReport
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
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		pass, err := s.RepairAll(ctx, RepairAllOptions{Resign: true, Progress: o.Progress})
		if err != nil {
			return rep, err
		}
		rep.Passes++
		rep.Resigned += pass.Resigned
		// anything the key still signed (it wrote while the pass ran), or that could not be moved?
		check, err := s.RepairAll(ctx, RepairAllOptions{Resign: true, DryRun: true})
		if err != nil {
			return rep, err
		}
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
