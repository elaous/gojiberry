package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// Recovery of the owner enrollment.
//
// Enroll, rotate, a reset request and a reset cancellation append their log entry first and
// write the enrollment record second. A crash between the two leaves an entry that took effect in
// the log's own rules (it is validly signed, nothing voids it) with no record behind it: the audit
// then says the address is enrolled, or has a pending reset, and the directory says otherwise,
// which a watcher reads as a violation for good. Two things end that.
//
//   - Reconciliation (reconcileLocked): before an owner operation on an address, and at start-up
//     for every address that has a record (ReconcileEnrollments), the directory replays the
//     address's owner-state entries with AuditAddress and, when the record is behind the log
//     (the last entry that took effect is newer than the record's Seq), completes it: the record
//     is written from the log. The log is the proof and the record only its state, so completing
//     is what the entry already said. An entry younger than reconcileGrace may still be in flight
//     on another replica: the operation is refused for the moment (ErrDirectoryBusy, 503).
//   - The operator's reset (OperatorResetEnrollment), for what reconciliation cannot mend (an
//     address with no record whose email nobody asks about, a key and its reset both lost): it
//     removes the enrollment and logs an operator-reset entry, which the audit accepts as
//     legitimate and lists, and which every watcher of the address is told about. It is signed by
//     the directory key, not by the owner: the server can do it to any address at any time. What it
//     buys is that the act is visible, not that it is impossible.

// reconcileGrace is how long an owner-state entry may be without its record before it is taken to
// be dangling (a write in flight on another replica takes milliseconds).
const reconcileGrace = 2 * time.Minute

// stateEntry says an entry is of the owner-state kind the reconciliation replays (or a void, which
// may cancel one).
func stateEntry(op string) bool {
	switch op {
	case OpEnroll, OpRotate, OpReset, OpResetRequest, OpResetCancel, OpOperatorReset, OpVoid:
		return true
	}
	return false
}

// logIndex is the owner-state entries of the log by address, built by scanning forward from where
// the last scan stopped (the log is append-only), so a reconciliation costs the entries added
// since, not the whole log.
type logIndex struct {
	scanned int64
	byAddr  map[string][]LogEntry
}

// stateEntries returns the owner-state entries of the log about any of addrs, in order.
func (d *AdapterDirectory) stateEntries(ctx context.Context, addrs []string) ([]LogEntry, error) {
	d.idxMu.Lock()
	defer d.idxMu.Unlock()
	if d.idx.byAddr == nil {
		d.idx.byAddr = map[string][]LogEntry{}
	}
	for {
		e, found, err := d.readEntry(ctx, d.idx.scanned+1)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		d.idx.scanned = e.Seq
		if stateEntry(e.Op) {
			d.idx.byAddr[e.Address] = append(d.idx.byAddr[e.Address], e)
		}
	}
	var out []LogEntry
	for _, a := range addrs {
		out = append(out, d.idx.byAddr[a]...)
	}
	// Entries under several pseudonyms (a rotated directory key) interleave: by number.
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// reconcileLocked completes the enrollment record of email when the log is ahead of it (see the
// comment above). The caller holds the address lock. ErrDirectoryBusy: an entry newer than the
// record is too young to call dangling.
func (d *AdapterDirectory) reconcileLocked(ctx context.Context, email string, now time.Time) error {
	addrs := d.LogAddressesFor(email)
	ents, err := d.stateEntries(ctx, addrs)
	if err != nil {
		return err
	}
	if len(ents) == 0 {
		return nil
	}
	audit := AuditAddress(email, addrs, ents, AuditOptions{})
	enr, digest, err := d.readEnrollment(ctx, email)
	if err != nil {
		return err
	}
	var recSeq int64
	if enr != nil {
		recSeq = enr.Seq
	}
	if audit.LastSeq <= recSeq {
		return nil
	}
	var last LogEntry
	for _, e := range ents {
		if e.Seq == audit.LastSeq {
			last = e
		}
	}
	if !audit.Enrolled && enr == nil {
		return nil // nothing to hold: an operator reset (or a void) left it un-enrolled
	}
	if now.Unix()-last.At < int64(reconcileGrace/time.Second) {
		return docstore.ErrDirectoryBusy
	}
	switch {
	case !audit.Enrolled:
		if err := d.deleteEnrollment(ctx, email); err != nil {
			return err
		}
	default:
		next := enrollRecord{Keys: append([]docstore.DirectoryOwnerKey(nil), audit.OwnerKeys...), ResetSince: audit.ResetSince, Seq: audit.LastSeq}
		if err := d.writeEnrollment(ctx, email, next, digest); err != nil {
			return err
		}
	}
	log.Printf("encrypt: the owner enrollment of an address was behind the log (entry %d had no record behind it, a crash between the two): completed from the log", audit.LastSeq)
	return nil
}

// deleteEnrollment removes the enrollment record of email under every name.
func (d *AdapterDirectory) deleteEnrollment(ctx context.Context, email string) error {
	for _, id := range d.enrollNames(email) {
		doc, err := d.Store.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := d.deleteIf(ctx, id, docstore.Digest(doc.Data)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
			return err
		}
	}
	return nil
}

// ReconcileEnrollments reconciles every address that has an enrollment record (Startup runs it).
// It returns how many records it completed. An address whose entry is too young to judge is left
// for the next run, an unreadable record is skipped.
func (d *AdapterDirectory) ReconcileEnrollments(ctx context.Context) (int, error) {
	if d.Signer == nil {
		return 0, ErrDirectoryOff
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, docstore.DirectoryEnrollPrefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		var rec enrollRecord
		if json.Unmarshal(doc.Data, &rec) != nil || !docstore.ValidAddress(rec.Email) {
			continue
		}
		func() {
			defer d.lockAddress(rec.Email)()
			before := rec.Seq
			if err := d.reconcileLocked(ctx, rec.Email, d.now()); err != nil {
				if !errors.Is(err, docstore.ErrDirectoryBusy) {
					log.Printf("encrypt: reconciling an owner enrollment: %v", err)
				}
				return
			}
			if cur, _, err := d.readEnrollment(ctx, rec.Email); err != nil || cur == nil || cur.Seq != before {
				n++
			}
		}()
	}
	return n, nil
}

// OperatorResetEnrollment removes the owner enrollment of the address whose log pseudonym is
// given (the operator reads it from the log; the owner can ask for theirs) and logs an
// operator-reset entry. The address is then as if it had never enrolled (the next enrollment is
// trust on first use again), and everybody who watches it is told. It returns the enrollment
// afterwards (not enrolled). docstore.ErrDirectoryNotFound if the log has nothing about that
// pseudonym.
func (d *AdapterDirectory) OperatorResetEnrollment(ctx context.Context, pseudonym string) (docstore.DirectoryEnrollment, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollment{}, ErrDirectoryOff
	}
	known := false
	if ents, err := d.stateEntries(ctx, []string{pseudonym}); err != nil {
		return docstore.DirectoryEnrollment{}, err
	} else {
		known = len(ents) > 0
	}
	// The record, if there is one: found through the e-mail it carries.
	email := ""
	metas, err := docstore.ListPrefix(ctx, d.Store, docstore.DirectoryEnrollPrefix)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if err != nil {
			continue
		}
		var rec enrollRecord
		if json.Unmarshal(doc.Data, &rec) != nil || !docstore.ValidAddress(rec.Email) {
			continue
		}
		for _, a := range d.LogAddressesFor(rec.Email) {
			if a == pseudonym {
				email = rec.Email
			}
		}
	}
	if !known && email == "" {
		return docstore.DirectoryEnrollment{}, docstore.ErrDirectoryNotFound
	}
	if email != "" {
		defer d.lockAddress(email)()
	}
	var ref int64
	if email != "" {
		enr, _, err := d.readEnrollment(ctx, email)
		if err != nil && !errors.Is(err, errEnrollmentCorrupt) {
			return docstore.DirectoryEnrollment{}, err
		}
		if enr != nil {
			ref = enr.Seq
		}
	}
	// Logged first, like every owner-state change: the record goes after.
	seq, err := d.appendLog(ctx, logInput{Op: OpOperatorReset, Address: pseudonym, Ref: ref}, d.now())
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	if email != "" {
		if err := d.deleteEnrollment(ctx, email); err != nil {
			return docstore.DirectoryEnrollment{}, fmt.Errorf("encrypt: the operator reset is logged (entry %d) but the enrollment record could not be removed: %w", seq, err)
		}
	}
	log.Printf("encrypt: OPERATOR RESET of an owner enrollment (log entry %d, address %s)", seq, pseudonym)
	return docstore.DirectoryEnrollment{}, nil
}
