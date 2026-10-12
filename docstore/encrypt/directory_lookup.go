package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/observe"
	"log"
	"strings"
	"time"
)

// ErrTooManyKeys is returned by Publish when the address already has as many published keys as
// the directory keeps.
var ErrTooManyKeys = errors.New("encrypt: this address already has the most keys the directory keeps")

// publishedKey is the lookup row of a stored record, with its flags computed. Verified needs the
// server's signature, and, for a record that a retired key signed or that belongs to an address
// with an owner key, a log entry that backs it (backedEntry): a retired key that was compromised
// cannot make a record Verified by signing one, and a server that holds the directory key cannot
// put an unlogged record under an owner's address. On an owner-enrolled address a record newer
// than the enrollment must also carry the owner's signature (OwnerSigned) to be Verified. Attested
// is from that and the key's own signature.
func (d *AdapterDirectory) publishedKey(ctx context.Context, st stored, want string, includeRevoked bool, enr *enrollRecord) (PublishedKey, error) {
	rec := st.rec
	k := PublishedKey{Label: rec.Label, Text: st.text, Type: st.typ, Fingerprint: st.fp, Kid: rec.Kid, Seq: rec.Seq}
	verified := d.signedBy(st, want)
	retired := rec.Kid != d.Signer.KeyID()
	var entry LogEntry
	var haveEntry, addrOK bool
	if verified && (enr != nil || retired) {
		var signed bool
		var err error
		if entry, haveEntry, signed, err = d.entryFor(ctx, st); err != nil {
			return PublishedKey{}, err
		}
		addrOK = haveEntry && signed && d.entryIsFor(entry, want)
		if retired && !addrOK {
			verified = false
		}
	}
	if enr != nil {
		k.OwnerEnrolled, k.OwnerResetPending = true, enr.ResetSince > 0
		if verified {
			// An owner signature binds the address itself (it names it), so the entry only has to
			// be the publication's; without one, the record must predate the enrollment and be
			// backed by an entry of this address.
			if ev, ok := ownerEvidence(enr, st, entry, haveEntry); ok {
				k.OwnerSigned, k.Owner = true, &ev
			} else if rec.Revoked == 0 && (len(enr.Keys) == 0 || rec.Seq > enr.Keys[0].Seq || !addrOK) {
				verified = false // after the enrollment without the owner's signature, or with nothing behind it
			}
		}
	}
	k.Verified = verified
	if k.Verified {
		k.Held = rec.V >= 3 && rec.Held
		if rec.V >= 3 && rec.Attest != "" {
			att := docstore.DirectoryAttestation{At: rec.AttestAt, Sig: rec.Attest}
			if VerifyAttestation(st.text, want, att) {
				k.Attested, k.Attestation = true, &att
			}
		}
	}
	if rec.Revoked > 0 && includeRevoked {
		k.Revoked, k.RevokedAt = true, rec.Revoked
	}
	return k, nil
}

// ownerEvidence says the record carries the signature of an owner key that was the address's
// owner key at the time of its log entry, the same one the entry carries, and returns it.
func ownerEvidence(enr *enrollRecord, st stored, entry LogEntry, haveEntry bool) (docstore.DirectoryOwnerEvidence, bool) {
	rec := st.rec
	if !haveEntry || rec.V < 4 || rec.OwnerSig == "" || entry.OwnerSig != rec.OwnerSig || entry.OpTime != rec.OwnerTime || entry.OwnerPrev != rec.OwnerPrev || entry.OwnerV != rec.OwnerV {
		return docstore.DirectoryOwnerEvidence{}, false
	}
	op := OpPublish
	if rec.Revoked > 0 {
		op = OpRevoke
	}
	for _, k := range enr.Keys {
		if k.Seq >= rec.Seq || (k.Until != 0 && rec.Seq >= k.Until) {
			continue
		}
		pub, err := ParseOwnerPub(k.Pub)
		if err == nil && verifyOwnerVersion(pub, rec.OwnerV, op, rec.Email, st.fp, LabelHash(rec.Label), rec.OwnerTime, rec.OwnerPrev, rec.OwnerSig) {
			return docstore.DirectoryOwnerEvidence{Pub: k.Pub, Time: rec.OwnerTime, Prev: rec.OwnerPrev, Sig: rec.OwnerSig, V: rec.OwnerV}, true
		}
	}
	return docstore.DirectoryOwnerEvidence{}, false
}

// LookupOptions are the choices of LookupWith.
type LookupOptions struct {
	// IncludeRevoked also returns the keys revoked within RevokeWindow, flagged Revoked.
	IncludeRevoked bool
}

// Lookup returns the keys published for email. A record counts as Verified only when its
// signature is a server key's (current or retired), for this record and this address, from a
// provider user, backed by a log entry where that matters (see publishedKey); a record that
// fails any of that is returned unverified (the owner then confirms the fingerprint), one whose
// key does not match its ID, or that cannot be read, is skipped. Revoked keys are not returned.
// With no Signer it returns nothing; a malformed address (not exactly one "@") is
// docstore.ErrDirectoryBadAddress.
func (d *AdapterDirectory) Lookup(ctx context.Context, email string) ([]PublishedKey, error) {
	return d.LookupWith(ctx, email, LookupOptions{})
}

// LookupWith is Lookup with options. A revoked key (a tombstone with a valid signature) is left
// out, or with IncludeRevoked returned flagged; a tombstone whose signature does not verify is
// ignored (it could only have been written by someone with write access to the store, who could
// as well delete the record).
func (d *AdapterDirectory) LookupWith(ctx context.Context, email string, opt LookupOptions) ([]PublishedKey, error) {
	want := normalizeEmail(email)
	if want == "" || d.Signer == nil {
		return nil, nil
	}
	if !docstore.ValidAddress(want) {
		return nil, docstore.ErrDirectoryBadAddress
	}
	state, err := d.readAddress(ctx, want)
	if err != nil {
		return nil, err
	}
	enr, _, err := d.readEnrollment(ctx, want)
	if err != nil {
		return nil, err
	}
	now := d.now()
	var out []PublishedKey
	for _, st := range state.list {
		if st.rec.Revoked > 0 {
			if !opt.IncludeRevoked || !d.signedBy(st, want) || now.Sub(time.Unix(st.rec.Revoked, 0)) >= d.window() {
				continue
			}
		}
		k, err := d.publishedKey(ctx, st, want, opt.IncludeRevoked, enr)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// Revoke removes the key with this fingerprint ("SHA256:..." or the form fingerprintSegment
// makes of it) from user's own address: the record is replaced at once by a signed tombstone, so
// Lookup stops returning the key, and the revocation is logged. The tombstone carries the number
// of the revoke entry, and is kept for RevokeWindow, during which the key cannot be published
// again (ErrRevoked) and a lookup can ask for it. Only the address of the signed-in user is
// reachable: ErrKeyNotFound if it has no such published key (or it was revoked already),
// ErrNotPublishable for a user who could not have published. On an address with an owner key,
// owner is required (the owner's signature over the revoke operation). Like Publish, the
// tombstone is written conditionally on the record that was read, and a record that cannot be
// written leaves a void log entry.
func (d *AdapterDirectory) Revoke(ctx context.Context, user auth.User, fingerprint string, owner *docstore.DirectoryOwnerSig) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
	}
	if err := d.checkUser(user); err != nil {
		return PublishedKey{}, err
	}
	email := normalizeEmail(user.Email)
	seg := fingerprintSegment(strings.TrimSpace(fingerprint))
	defer d.lockAddress(user.Email)()
	now := d.now()
	if err := d.reconcileLocked(ctx, email, now); err != nil {
		return PublishedKey{}, err
	}
	for round := 0; round < maxCASRounds; round++ {
		state, err := d.readAddress(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		var st stored
		found := false
		for fp, cand := range state.best {
			if fingerprintSegment(fp) == seg && cand.rec.Revoked == 0 {
				st, found = cand, true
			}
		}
		if !found {
			return PublishedKey{}, ErrKeyNotFound
		}
		enr, _, err := d.readEnrollment(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		o, err := d.checkOwner(ctx, enr, OpRevoke, email, st.fp, "", owner, now)
		if err != nil {
			return PublishedKey{}, err
		}
		in := logInput{Op: OpRevoke, Email: email, Fingerprint: st.fp}
		tomb := st.rec
		tomb.OwnerSig, tomb.OwnerTime, tomb.OwnerPrev, tomb.OwnerV = "", 0, "", 0
		if o != nil {
			in.OwnerSig, in.OpTime, in.OwnerPrev = o.Sig, o.Time, o.Prev
			tomb.OwnerSig, tomb.OwnerTime, tomb.OwnerPrev = o.Sig, o.Time, o.Prev
		}
		seq, err := d.appendLog(ctx, in, now)
		if err != nil {
			return PublishedKey{}, err
		}
		id := d.directoryID(email, st.fp)
		tomb.Publisher, tomb.Provider, tomb.Revoked, tomb.Seq = user.ID, providerOf(user.ID), now.Unix(), seq
		tomb.Held, tomb.Attest, tomb.AttestAt = false, "", 0
		tomb = d.sign(tomb, id)
		data, err := json.Marshal(tomb)
		if err != nil {
			d.voidLog(ctx, email, seq, st.fp, now)
			return PublishedKey{}, err
		}
		switch err := d.saveIf(ctx, id, data, state.digests[id]); {
		case err == nil:
			d.dropOtherNames(ctx, email, st.fp, id)
			return d.publishedKey(ctx, stored{id: id, rec: tomb, fp: st.fp, typ: st.typ, text: st.text}, email, true, enr)
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, st.fp, now)
		default:
			d.voidLog(ctx, email, seq, st.fp, now)
			return PublishedKey{}, err
		}
	}
	return PublishedKey{}, docstore.ErrDirectoryBusy
}

// Sweep deletes what has expired: used challenges (kept until the challenge itself could no
// longer be answered, which is what makes a replay refused) and revoked-key tombstones past
// RevokeWindow. It returns how many it deleted. Run it now and then (RunSweep).
func (d *AdapterDirectory) Sweep(ctx context.Context) (int, error) {
	done := observe.Default().Sweep("directory")
	n, err := d.sweepRecords(ctx)
	done(n, err)
	return n, err
}

func (d *AdapterDirectory) sweepRecords(ctx context.Context) (int, error) {
	if d.Signer == nil {
		return 0, ErrDirectoryOff
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, directoryPrefix)
	if err != nil {
		return 0, err
	}
	now := d.now()
	n := 0
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		expired := false
		email := ""
		var rec directoryRecord
		tombstone := false
		if strings.HasPrefix(m.ID, popPrefix) {
			var u struct {
				Exp int64 `json:"exp"`
			}
			expired = json.Unmarshal(doc.Data, &u) != nil || now.Unix() > u.Exp+int64(popGrace/time.Second)
		} else {
			expired = json.Unmarshal(doc.Data, &rec) == nil && rec.Revoked > 0 && now.Sub(time.Unix(rec.Revoked, 0)) >= d.window()
			email = rec.Email
			tombstone = expired
		}
		if !expired {
			continue
		}
		// Delete only what was read: a republish since replaced the tombstone with a live record,
		// and an unconditional delete would remove the key somebody just published. It is made
		// under the address's lock, so in this process it cannot interleave with a publish, and
		// across replicas the store's conditional delete decides.
		err = func() error {
			if docstore.ValidAddress(email) {
				defer d.lockAddress(email)()
				if tombstone {
					// What the revoked key left under a retired name goes with the tombstone.
					return d.pruneTombstone(ctx, email, rec, m.ID, docstore.Digest(doc.Data))
				}
			}
			return d.deleteIf(ctx, m.ID, docstore.Digest(doc.Data))
		}()
		switch {
		case err == nil:
			n++
		case errors.Is(err, docstore.ErrConflict), errors.Is(err, docstore.ErrNotFound):
			// changed or gone since it was read: not ours to delete
		default:
			return n, err
		}
	}
	return n, nil
}

// RunSweep calls Sweep and then ReconcileEnrollments every interval (an hour when not positive)
// until ctx ends; errors are logged. The reconciliation also runs once the reconcile grace is
// over after the start, and again after any pass that found an entry too young to judge: a
// restart (whose start-up reconcile cannot complete an entry younger than the grace) does not
// leave a record behind the log until the next interval.
func (d *AdapterDirectory) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	retryAfter := d.grace() + d.grace()/10 + time.Millisecond
	retry := time.NewTimer(retryAfter)
	defer retry.Stop()
	reconcile := func() {
		rep, err := d.reconcileAll(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("encrypt: reconciling the owner enrollments: %v", err)
		}
		if rep.Deferred > 0 {
			retry.Reset(retryAfter)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry.C:
			reconcile()
		case <-t.C:
			if _, err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("encrypt: sweeping the key directory: %v", err)
			}
			reconcile()
		}
	}
}
