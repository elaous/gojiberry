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

// PublishOptions is what a publisher sends besides the key.
type PublishOptions struct {
	Challenge, Nonce string
	Attestation      *docstore.DirectoryAttestation
	// Owner is the owner's signature of the publication.
	Owner *docstore.DirectoryOwnerSig
}

// Publish records key as a key of an address.
func (d *AdapterDirectory) Publish(ctx context.Context, user auth.User, key Recipient) error {
	_, err := d.PublishWith(ctx, user, key, PublishOptions{})
	return err
}

// refuses unless the e-mail is explicitly verified
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
	// a crash may leave a log enrollment without its record
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
				if !d.signedBy(st, email) {
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
		// single use: claimed after all checks, logged before the record
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
					_ = d.pruneTombstone(ctx, email, st.rec, st.id, st.digest)
				}
			}
			st := stored{id: id, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text}
			return d.publishedKey(ctx, st, email, false, enr)
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
		default:
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
			return PublishedKey{}, err
		}
	}
	return PublishedKey{}, docstore.ErrDirectoryBusy
}

func (d *AdapterDirectory) dropOtherNames(ctx context.Context, email, fp, id string) {
	for _, prefix := range d.names(email) {
		if other := prefix + fingerprintSegment(fp); other != id {
			_ = d.Store.Delete(ctx, other) // not found is the usual case
		}
	}
}

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
				continue
			}
			if err := d.deleteIf(ctx, other, docstore.Digest(doc.Data)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return err
			}
		}
	}
	return d.deleteIf(ctx, id, digest)
}

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
