package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"time"
)

// PublishOptions is what a publisher sends besides the key: the answer to a challenge
// (Challenge and Nonce, see Challenge and OpenChallenge) and, for an ssh-ed25519 key, the key's
// own attestation of the binding to the address (see Attest).
type PublishOptions struct {
	Challenge, Nonce string
	Attestation      *docstore.DirectoryAttestation
	// Owner is the owner's signature of the publish operation, required when the address has an
	// enrolled owner key (see Enroll).
	Owner *docstore.DirectoryOwnerSig
}

// Publish records key as a key of user's e-mail address, with no proof of possession (it
// refuses with ErrProofRequired when RequireProof is set). See PublishWith.
func (d *AdapterDirectory) Publish(ctx context.Context, user auth.User, key Recipient) error {
	_, err := d.PublishWith(ctx, user, key, PublishOptions{})
	return err
}

// PublishWith records key as a key of user's e-mail address. It refuses (ErrNotPublishable)
// unless the user's e-mail is explicitly verified (auth.User.VerifiedEmail) and came from a
// provider session: a passkey user ("passkey:...") has none that is verified. The type and
// fingerprint are recomputed from key.Text. Publishing the same key again replaces the record,
// signed by the current key (so it is how a record moves to a new key after a rotation). At most
// MaxKeys keys are kept for one address, counting keys revoked within RevokeWindow, and a key
// revoked within that window is refused (ErrRevoked).
//
// Proof of possession. With opt.Challenge and opt.Nonce (from a challenge made for this user and
// key and not yet used, answered before it expired, issued by the current directory key) the
// record says Held. A proof that does not check is refused (ErrBadProof) and consumes nothing
// but itself; a missing one is refused (ErrProofRequired) only when RequireProof is set. The
// challenge is used up as soon as it checks, whatever happens next.
//
// Attestation. An opt.Attestation must be a valid signature by the key over the binding
// (AttestationMessage) for this address, dated within ten minutes of now, and the key must be an
// ssh-ed25519 one (ErrBadAttestation otherwise); the record then carries it and Lookup reports
// the key Attested.
//
// Owner. If the address has an enrolled owner key (Enroll), opt.Owner is required: the owner's
// signature over the publish operation (ErrOwnerRequired, ErrBadOwnerSig), recorded in the log
// entry and in the record; Lookup then marks the key OwnerSigned. A key an owner-enrolled
// address did not sign is not Verified.
//
// Atomicity. The publication is appended to the transparency log first and the record carries
// its number; the record is then written conditionally on being what was read (create it if there
// was none), and the checks (tombstones, the cap, the owner) are made again from a fresh read if
// another replica got there first. A record that cannot be written leaves a "void" entry behind,
// so the log never claims a publication that did not happen. On a store without a conditional
// write this holds only inside this process (see CheckStore).
func (d *AdapterDirectory) PublishWith(ctx context.Context, user auth.User, key Recipient, opt PublishOptions) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
	}
	if err := d.checkUser(user); err != nil {
		return PublishedKey{}, err
	}
	r, err := parsePublic(key.Text)
	if err != nil {
		return PublishedKey{}, err
	}
	now := d.now()
	email := normalizeEmail(user.Email)

	// Check what the request carries before anything is written or used up.
	var att *docstore.DirectoryAttestation
	if opt.Attestation != nil {
		if r.Type != "ssh-ed25519" {
			return PublishedKey{}, fmt.Errorf("%w: a %s key cannot attest", ErrBadAttestation, r.Type)
		}
		if skew := now.Unix() - opt.Attestation.At; skew > int64(attestSkew/time.Second) || -skew > int64(attestSkew/time.Second) {
			return PublishedKey{}, fmt.Errorf("%w: it is dated %d seconds from now", ErrBadAttestation, skew)
		}
		if !VerifyAttestation(r.Text, email, *opt.Attestation) {
			return PublishedKey{}, ErrBadAttestation
		}
		att = opt.Attestation
	}
	var tok challengeToken
	proven := opt.Challenge != "" || opt.Nonce != ""
	switch {
	case proven:
		if tok, err = d.checkProof(user, r, opt.Challenge, opt.Nonce, now); err != nil {
			return PublishedKey{}, err
		}
	case d.RequireProof:
		return PublishedKey{}, ErrProofRequired
	}

	defer d.lockAddress(user.Email)()
	// A crash may have left an enrollment in the log without its record: the address would look
	// un-enrolled here (no owner signature asked) while the log says otherwise.
	if err := d.reconcileLocked(ctx, email, now); err != nil {
		return PublishedKey{}, err
	}
	maxKeys := d.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 20
	}
	id := d.directoryID(email, r.Fingerprint)
	claimed := false
	for round := 0; round < maxCASRounds; round++ {
		state, err := d.readAddress(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		enr, _, err := d.readEnrollment(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		if d.RequireEnrollment && enr == nil {
			return PublishedKey{}, ErrEnrollmentRequired
		}
		label := cleanLabel(key.Label)
		owner, err := d.checkOwner(ctx, enr, OpPublish, email, r.Fingerprint, LabelHash(label), opt.Owner, now)
		if err != nil {
			return PublishedKey{}, err
		}
		counted := 0
		var stale []stored // tombstones past their window: pruned
		for fp, st := range state.best {
			if st.rec.Revoked > 0 {
				if !d.signedBy(st, email) { // an unsigned tombstone is not believed, and not counted
					continue
				}
				if now.Sub(time.Unix(st.rec.Revoked, 0)) < d.window() {
					if fp == r.Fingerprint {
						return PublishedKey{}, ErrRevoked
					}
					counted++
				} else {
					stale = append(stale, st)
				}
				continue
			}
			counted++
		}
		if cur, ok := state.best[r.Fingerprint]; (!ok || cur.rec.Revoked > 0) && counted >= maxKeys {
			return PublishedKey{}, ErrTooManyKeys
		}
		// Everything has checked: the challenge is used up now (single use), and the publication
		// is logged before the record is written.
		if proven && !claimed {
			if err := d.claimChallenge(ctx, tok); err != nil {
				return PublishedKey{}, err
			}
			claimed = true
		}
		in := logInput{Op: OpPublish, Email: email, Fingerprint: r.Fingerprint}
		rec := directoryRecord{Key: r.Text, Label: label, Publisher: user.ID, Provider: providerOf(user.ID), Email: email, At: now.Unix(), Held: proven}
		if owner != nil {
			in.OwnerSig, in.OpTime, in.OwnerPrev, in.OwnerV, in.LabelHash = owner.Sig, owner.Time, owner.Prev, owner.V, LabelHash(label)
			rec.OwnerSig, rec.OwnerTime, rec.OwnerPrev, rec.OwnerV = owner.Sig, owner.Time, owner.Prev, owner.V
		}
		seq, err := d.appendLog(ctx, in, now)
		if err != nil {
			return PublishedKey{}, err
		}
		rec.Seq = seq
		if att != nil {
			rec.Attest, rec.AttestAt = att.Sig, att.At
		}
		rec = d.sign(rec, id)
		data, err := json.Marshal(rec)
		if err != nil {
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
			return PublishedKey{}, err
		}
		switch err := d.saveIf(ctx, id, data, state.digests[id]); {
		case err == nil:
			d.dropOtherNames(ctx, email, r.Fingerprint, id)
			for _, st := range stale {
				if st.fp != r.Fingerprint {
					_ = d.pruneTombstone(ctx, email, st.rec, st.id, st.digest) // best effort: a tombstone left behind is pruned next time
				}
			}
			st := stored{id: id, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text}
			return d.publishedKey(ctx, st, email, false, enr)
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, r.Fingerprint, now) // another replica changed it: look again
		default:
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
			return PublishedKey{}, err
		}
	}
	return PublishedKey{}, docstore.ErrDirectoryBusy
}

// dropOtherNames deletes the records of fingerprint fp that a retired key's name holds (the
// record now lives under id, signed by the current key).
func (d *AdapterDirectory) dropOtherNames(ctx context.Context, email, fp, id string) {
	for _, prefix := range d.names(email) {
		if other := prefix + fingerprintSegment(fp); other != id {
			_ = d.Store.Delete(ctx, other) // not found is the usual case
		}
	}
}

// pruneTombstone removes a tombstone past its window together with what the revoked key left
// under the other names of the address (a retired directory key's): a live copy there, older than
// the revocation, would otherwise become the newest record of the fingerprint the moment the
// tombstone is gone, and bring the revoked key back. The copies go first; a newer live copy (a
// later publication) is kept, and if any delete fails the tombstone stays for the next run.
// docstore.ErrConflict: something changed since it was read (not ours to delete).
func (d *AdapterDirectory) pruneTombstone(ctx context.Context, email string, tomb directoryRecord, id, digest string) error {
	if r, err := parsePublic(tomb.Key); err == nil {
		seg := fingerprintSegment(r.Fingerprint)
		when := max(tomb.At, tomb.Revoked)
		for _, prefix := range d.names(email) {
			other := prefix + seg
			if other == id {
				continue
			}
			doc, err := d.Store.Load(ctx, other)
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			var o directoryRecord
			if json.Unmarshal(doc.Data, &o) != nil || (o.Revoked == 0 && o.At > when) {
				continue // unreadable (never found by a lookup), or published again after the revocation
			}
			if err := d.deleteIf(ctx, other, docstore.Digest(doc.Data)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return err
			}
		}
	}
	return d.deleteIf(ctx, id, digest)
}

// deleteIf deletes the record id only if it still has digest prev: with the store's conditional
// delete when it has one (docstore.ConditionalDeleter), otherwise as one step inside this process.
// docstore.ErrConflict if it changed, docstore.ErrNotFound if it is gone.
func (d *AdapterDirectory) deleteIf(ctx context.Context, id, prev string) error {
	if c, ok := d.Store.(docstore.ConditionalDeleter); ok {
		err := c.DeleteIf(ctx, id, prev)
		if !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return err
		}
	}
	d.casMu.Lock()
	defer d.casMu.Unlock()
	doc, err := d.Store.Load(ctx, id)
	if err != nil {
		return err
	}
	if docstore.Digest(doc.Data) != prev {
		return docstore.ErrConflict
	}
	return d.Store.Delete(ctx, id)
}
