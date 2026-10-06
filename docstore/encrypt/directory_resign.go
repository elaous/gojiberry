package encrypt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// ResignReport is what Resign did and found.
type ResignReport struct {
	// Records is how many directory records it looked at (challenge claims excluded).
	Records int
	// Resigned were moved to the current key (the old copy deleted).
	Resigned int
	// Current were signed by the current key under its name already.
	Current int
	// Skipped have a valid signature of a known key but no log entry behind them (Seq 0, or an
	// entry that is not theirs): they are left as they are, under the old name, and not endorsed
	// by the current key. A record forged with a compromised retired key looks like this.
	Skipped int
	// Unverified carry a signature that no known key makes valid, under a name the server knows.
	Unverified int
	// Unknown are named under a key the server does not know: nothing can find them (a wrong
	// directory key, or a retired one missing from GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV).
	Unknown int
	// Dropped are tombstones past the revoke window, deleted.
	Dropped int
	// Enrollments are owner enrollment records moved to the current key.
	Enrollments int
	// Contended were left because another replica kept changing them.
	Contended int
}

// Resign moves records that a retired key signed or named to the current key, after a rotation
// (GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV). A record is moved only if it is worth endorsing:
//
//   - its signature verifies, it is named for its own address and key, and
//   - a log entry stands behind it (backedEntry: its Seq names a validly signed entry for the same
//     address, fingerprint and operation), so a record forged with a compromised retired key
//     (which cannot be told from a real one by its signature) is not laundered into one the
//     current key signed. Such a record is left under the old name, counted as Skipped, and
//     stays as unverified as it was (a lookup marks a record that a retired key signed Verified
//     only when it has a log entry too).
//
// If the old key was compromised, do not rotate through it: remove it from the PREV list instead
// (and republish what matters), so what it signed is no longer believed at all.
//
// Each record is moved under the lock of its address and written conditionally on what was read
// (a create if there is no record under the current name, a replace of the digest read
// otherwise), with the newest copy of the key across all names deciding: if a newer copy exists
// (a tombstone Revoke wrote while this ran, in a rolling deploy) the old one is dropped, never
// written over it. The old copy is deleted only if it is still the one that was read. Nothing the
// publisher proved is touched (the proof flag, attestation, owner signature and log number are
// copied), and nothing is logged. Expired tombstones are dropped on the way. Run it before
// serving (AdapterDirectory.Startup does).
func (d *AdapterDirectory) Resign(ctx context.Context) (ResignReport, error) {
	var rep ResignReport
	if d.Signer == nil {
		return rep, ErrDirectoryOff
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, directoryPrefix)
	if err != nil {
		return rep, err
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, popPrefix) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if err := d.resignOne(ctx, m.ID, &rep); err != nil {
			return rep, err
		}
	}
	if err := d.resignEnrollments(ctx, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func (d *AdapterDirectory) resignOne(ctx context.Context, id string, rep *ResignReport) error {
	doc, err := d.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var rec directoryRecord
	if json.Unmarshal(doc.Data, &rec) != nil {
		return nil
	}
	rep.Records++
	r, err := parsePublic(rec.Key)
	email := normalizeEmail(rec.Email)
	if err != nil || email == "" || !docstore.ValidAddress(email) {
		rep.Unknown++
		return nil
	}
	oldPrefix := id[:strings.LastIndex(id, "/")+1]
	known := false
	for _, n := range d.names(email) {
		if n == oldPrefix {
			known = true
		}
	}
	if !known || oldPrefix+fingerprintSegment(r.Fingerprint) != id {
		rep.Unknown++
		return nil
	}
	current := d.addressPrefix(email) + fingerprintSegment(r.Fingerprint)
	st := stored{id: id, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text, digest: docstore.Digest(doc.Data)}
	if !d.signedBy(st, email) {
		rep.Unverified++
		return nil
	}
	if rec.Revoked > 0 && d.now().Sub(time.Unix(rec.Revoked, 0)) >= d.window() {
		switch err := d.deleteIf(ctx, id, st.digest); {
		case err == nil:
			rep.Dropped++
		case errors.Is(err, docstore.ErrNotFound), errors.Is(err, docstore.ErrConflict):
		default:
			return err
		}
		return nil
	}
	if rec.V >= 3 && rec.Kid == d.Signer.KeyID() && id == current {
		rep.Current++
		return nil
	}
	if _, backed, err := d.backedEntry(ctx, st, email); err != nil {
		return err
	} else if !backed {
		rep.Skipped++
		return nil
	}

	unlock := d.lockAddress(email)
	defer unlock()
	for round := 0; round < maxCASRounds; round++ {
		state, err := d.readAddress(ctx, email)
		if err != nil {
			return err
		}
		var ours *stored
		for i := range state.all {
			if state.all[i].id == id {
				ours = &state.all[i]
			}
		}
		if ours == nil {
			return nil // deleted meanwhile
		}
		if newest := state.best[r.Fingerprint]; newest.id != ours.id {
			// A newer (or equal, under the current name) copy exists: this one is stale. Drop it
			// only if the newer one is a record this server stands behind.
			if d.signedBy(newest, email) {
				if err := d.deleteIf(ctx, id, ours.digest); err != nil && !errors.Is(err, docstore.ErrNotFound) && !errors.Is(err, docstore.ErrConflict) {
					return err
				}
			}
			return nil
		}
		next := d.sign(ours.rec, current)
		data, err := json.Marshal(next)
		if err != nil {
			return err
		}
		switch err := d.saveIf(ctx, current, data, state.digests[current]); {
		case err == nil:
			if id != current {
				if err := d.deleteIf(ctx, id, ours.digest); err != nil && !errors.Is(err, docstore.ErrNotFound) && !errors.Is(err, docstore.ErrConflict) {
					return err
				}
			}
			rep.Resigned++
			return nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return err
		}
	}
	rep.Contended++
	return nil
}

// resignEnrollments moves owner enrollment records to the current key's name and signature.
func (d *AdapterDirectory) resignEnrollments(ctx context.Context, rep *ResignReport) error {
	metas, err := docstore.ListPrefix(ctx, d.Store, docstore.DirectoryEnrollPrefix)
	if err != nil {
		return err
	}
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var rec enrollRecord
		if json.Unmarshal(doc.Data, &rec) != nil || rec.V != 1 || !docstore.ValidAddress(rec.Email) {
			rep.Unknown++
			continue
		}
		sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
		names := d.enrollNames(rec.Email)
		known := false
		for _, n := range names {
			known = known || n == m.ID
		}
		switch {
		case !known:
			rep.Unknown++
			continue
		case !d.Signer.Verify(rec.Kid, rec.message(m.ID), sig):
			rep.Unverified++
			continue
		case m.ID == names[0] && rec.Kid == d.Signer.KeyID():
			continue
		}
		func() {
			defer d.lockAddress(rec.Email)()
			cur, digest, err := d.readEnrollment(ctx, rec.Email)
			if err != nil || cur == nil {
				return
			}
			if err := d.writeEnrollment(ctx, rec.Email, *cur, digest); err == nil {
				rep.Enrollments++
			} else if !errors.Is(err, docstore.ErrConflict) {
				log.Printf("encrypt: moving an owner enrollment to the current directory key: %v", err)
			} else {
				rep.Contended++
			}
		}()
	}
	return nil
}

// Startup is what a server runs before it serves: Resign (moving records of a retired key to the
// current one, and counting what it found) and the census /health shows. It returns the report;
// a caller logs it, in particular Unknown, which counts records named under keys this server does
// not have: a wrong GOJIBERRY_DOCSTORE_DIRECTORY_KEY, or a retired key missing from
// GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV, leaves every such record unfindable.
func (d *AdapterDirectory) Startup(ctx context.Context) (ResignReport, error) {
	rep, err := d.Resign(ctx)
	if err != nil {
		return rep, err
	}
	h := docstore.DirectoryHealth{Records: rep.Records, Resigned: rep.Resigned, Skipped: rep.Skipped + rep.Unverified, UnknownKeys: rep.Unknown, CheckedAt: d.now().Unix()}
	d.report.Store(&h)
	return rep, nil
}

// Health is the census of the last Startup (zero before one ran).
func (d *AdapterDirectory) Health() docstore.DirectoryHealth {
	if h := d.report.Load(); h != nil {
		return *h
	}
	return docstore.DirectoryHealth{}
}

// Summary is a one line account of the report, for the start-up log.
func (r ResignReport) Summary() string {
	s := fmt.Sprintf("%d directory records: %d re-signed, %d already current, %d skipped (no log entry behind them), %d unverified, %d dropped (expired)",
		r.Records, r.Resigned, r.Current, r.Skipped, r.Unverified, r.Dropped)
	if r.Unknown > 0 {
		s += fmt.Sprintf("; %d directory records are named under unknown keys (wrong GOJIBERRY_DOCSTORE_DIRECTORY_KEY, or a retired key missing from GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV)", r.Unknown)
	}
	return s
}

// CheckStore decides whether the store can carry the directory's log and challenge claims for the
// number of server processes declared (GOJIBERRY_DOCSTORE_REPLICAS). Those claims are atomic
// across processes only on a store with a conditional write (docstore.ConditionalAdapter, probed
// here, since a wrapper may have the method and not the capability); on any other they are atomic
// within one process. With more than one replica on such a store it returns an error: do not
// enable the directory there. With one replica it logs a warning and returns nil.
func (d *AdapterDirectory) CheckStore(ctx context.Context, replicas int) error {
	if docstore.SupportsConditional(ctx, d.Store) {
		return nil
	}
	if replicas > 1 {
		return fmt.Errorf("encrypt: the key directory's log and challenge claims need a store with conditional writes (docstore.ConditionalAdapter) when %d server processes share it: use a store that has one (SQLite, Postgres, MongoDB, S3), or run one process", replicas)
	}
	log.Print("docstore: WARNING: the key directory is on a store without conditional writes: its log appends and challenge claims are atomic within this process only; do not run a second server process on this store (GOJIBERRY_DOCSTORE_REPLICAS)")
	return nil
}
