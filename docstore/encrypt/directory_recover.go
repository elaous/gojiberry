package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
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
//   - Reconciliation (reconcileLocked): before every operation on an address that writes (a
//     publication, a revocation and the owner operations), and from ReconcileEnrollments (start-up,
//     and RunSweep from then on), the directory replays the address's owner-state entries with
//     AuditAddress and, when the record is behind the log (the last entry that took effect is
//     newer than the record's Seq), completes it: the record is written from the log. The log is
//     the proof and the record only its state, so completing is what the entry already said. An
//     entry younger than the grace (ReconcileGrace) may still be in flight on another replica: the
//     operation is refused for the moment (ErrDirectoryBusy, 503), and a start-up that finds one
//     leaves it to the next run, which RunSweep makes once the grace is over (a restart inside the
//     grace does not leave the mismatch standing).
//     The log names an address only by its pseudonym, and the owner signatures name the e-mail, so
//     ReconcileEnrollments finds the addresses to replay from every e-mail the store holds a record
//     for: an enrollment record, or a published key (so the first enrollment of an address, which
//     has no enrollment record yet, is found when the address has a key). An address with entries
//     and neither is found when somebody operates on it, or reset by the operator (it is counted
//     listed, with a warning once per change: UnmappedAddresses, ReconcileReport.Unmapped).
//   - The operator's reset (OperatorResetEnrollment), for what reconciliation cannot mend (an
//     address with no record whose email nobody asks about, a key and its reset both lost): it
//     removes the enrollment and logs an operator-reset entry, which the audit accepts as
//     legitimate and lists, and which every watcher of the address is told about. It is signed by
//     the directory key, not by the owner: the server can do it to any address at any time. What it
//     buys is that the act is visible, not that it is impossible.

// reconcileGrace is how long an owner-state entry may be without its record before it is taken to
// be dangling (a write in flight on another replica takes milliseconds); the default of
// AdapterDirectory.ReconcileGrace.
const reconcileGrace = 2 * time.Minute

func (d *AdapterDirectory) grace() time.Duration {
	if d.ReconcileGrace > 0 {
		return d.ReconcileGrace
	}
	return reconcileGrace
}

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
	if now.Sub(time.Unix(last.At, 0)) < d.grace() {
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

// ReconcileReport is what one reconciliation pass found.
type ReconcileReport struct {
	// Completed is how many records it completed from the log (or removed, for an enrollment the
	// log shows ended).
	Completed int
	// Deferred is how many addresses had an entry too young to judge (see ReconcileGrace): they are
	// left for the next pass.
	Deferred int
	// Unmapped is how many pseudonyms have owner-state entries in the log and no e-mail the store
	// knows (no enrollment record and no published key): the pass cannot replay them. They are
	// found when somebody operates on the address, or reset by the operator. The adapter lists
	// them (UnmappedAddresses, GET /admin/directory/unmapped) and warns once per change.
	Unmapped int
}

// ReconcileEnrollments reconciles every address the store holds a record for (an enrollment, or a
// published key: the first enrollment of an address has no enrollment record yet) against the
// log (Startup runs it, and RunSweep again). It returns how many records it completed. An address
// whose entry is too young to judge is left for the next run, an unreadable record is skipped.
func (d *AdapterDirectory) ReconcileEnrollments(ctx context.Context) (int, error) {
	rep, err := d.reconcileAll(ctx)
	return rep.Completed, err
}

// reconcileAll is ReconcileEnrollments with the whole report.
func (d *AdapterDirectory) reconcileAll(ctx context.Context) (ReconcileReport, error) {
	var rep ReconcileReport
	if d.Signer == nil {
		return rep, ErrDirectoryOff
	}
	emails, err := d.knownEmails(ctx)
	if err != nil {
		return rep, err
	}
	if _, err := d.stateEntries(ctx, nil); err != nil { // bring the index up to the end of the log
		return rep, err
	}
	covered := map[string]bool{}
	for _, email := range emails {
		for _, a := range d.LogAddressesFor(email) {
			covered[a] = true
		}
		func() {
			defer d.lockAddress(email)()
			before, _, _ := d.readEnrollment(ctx, email)
			var beforeSeq int64 = -1
			if before != nil {
				beforeSeq = before.Seq
			}
			if err := d.reconcileLocked(ctx, email, d.now()); err != nil {
				if errors.Is(err, docstore.ErrDirectoryBusy) {
					rep.Deferred++
				} else {
					log.Printf("encrypt: reconciling an owner enrollment: %v", err)
				}
				return
			}
			if cur, _, err := d.readEnrollment(ctx, email); err != nil || (cur == nil) != (before == nil) || (cur != nil && cur.Seq != beforeSeq) {
				rep.Completed++
			}
		}()
	}
	// Pseudonyms with owner-state entries that none of those e-mails explains.
	un := d.findUnmapped(covered)
	rep.Unmapped = len(un)
	d.warnUnmapped(rep.Unmapped)
	return rep, nil
}

// DefaultUnmappedCap is how many unmapped addresses UnmappedAddresses lists.
const DefaultUnmappedCap = 100

// findUnmapped returns the pseudonyms of the index with owner-state entries that covered does not
// name, by first entry number. Entries that an operator reset ended or that a void cancelled have
// no effect, as AuditAddress reads them: an address left with none (last word an operator reset,
// or only voids and the entries they cancel) is settled and not listed.
func (d *AdapterDirectory) findUnmapped(covered map[string]bool) []docstore.DirectoryUnmappedAddress {
	d.idxMu.Lock()
	defer d.idxMu.Unlock()
	var out []docstore.DirectoryUnmappedAddress
	for a, ents := range d.idx.byAddr {
		if covered[a] {
			continue
		}
		voided := map[int64]bool{} // entry numbers that a void of this address cancels
		for _, e := range ents {
			if e.Op == OpVoid {
				voided[e.Ref] = true
			}
		}
		var u docstore.DirectoryUnmappedAddress
		for _, e := range ents {
			switch {
			case e.Op == OpOperatorReset:
				u = docstore.DirectoryUnmappedAddress{} // everything before it is ended
				continue
			case e.Op == OpVoid, voided[e.Seq]:
				continue
			}
			if u.FirstSeq == 0 {
				u.Address, u.FirstSeq, u.FirstAt = a, e.Seq, e.At
			}
			u.LastSeq, u.LastAt = e.Seq, e.At
		}
		if u.FirstSeq != 0 {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FirstSeq < out[j].FirstSeq })
	return out
}

// UnmappedAddresses lists the log pseudonyms with owner-state entries that no e-mail the store
// knows explains (see ReconcileReport.Unmapped), computed now: the count, the first
// UnmappedCap of them (pseudonyms only, never an e-mail), and the time. An address leaves the
// list when its owner operates on it (Enroll, RequestReset, CancelReset mend it) or the operator
// resets it (OperatorResetEnrollment).
func (d *AdapterDirectory) UnmappedAddresses(ctx context.Context) (docstore.DirectoryUnmapped, error) {
	if d.Signer == nil {
		return docstore.DirectoryUnmapped{}, ErrDirectoryOff
	}
	emails, err := d.knownEmails(ctx)
	if err != nil {
		return docstore.DirectoryUnmapped{}, err
	}
	if _, err := d.stateEntries(ctx, nil); err != nil {
		return docstore.DirectoryUnmapped{}, err
	}
	covered := map[string]bool{}
	for _, email := range emails {
		for _, a := range d.LogAddressesFor(email) {
			covered[a] = true
		}
	}
	all := d.findUnmapped(covered)
	now := d.now()
	out := docstore.DirectoryUnmapped{Count: len(all), Addresses: []docstore.DirectoryUnmappedAddress{}, ComputedAt: now.Unix()}
	limit := d.UnmappedCap
	if limit <= 0 {
		limit = DefaultUnmappedCap
	}
	if len(all) > limit {
		all, out.Truncated = all[:limit], true
	}
	for _, u := range all {
		u.AgeSeconds = max(now.Unix()-u.FirstAt, 0)
		out.Addresses = append(out.Addresses, u)
	}
	return out, nil
}

// warnUnmapped logs the unmapped count when it differs from the one last logged (start-up counts
// as a change from none), so a sweep that finds the same addresses every hour is silent.
func (d *AdapterDirectory) warnUnmapped(n int) {
	d.unmappedMu.Lock()
	changed := n != d.unmappedWarned
	d.unmappedWarned = n
	d.unmappedMu.Unlock()
	switch {
	case !changed:
	case n > 0:
		log.Printf("encrypt: WARNING: %d address pseudonyms have owner-state entries in the log and no record or key the directory can name them by; list them with GET /admin/directory/unmapped (header X-Gojiberry-Admin-Token); the address owner mends one by operating on it (enroll, request or cancel a reset), or the operator resets it with POST /admin/directory/reset-enrollment/<address> (docs/encryption-and-passkeys.md, unmapped addresses)", n)
	default:
		log.Printf("encrypt: no unmapped address pseudonyms remain in the directory log")
	}
}

// knownEmails lists the e-mail addresses the store holds a record for: enrollment records and
// published keys (tombstones too). Records that cannot be read are skipped.
func (d *AdapterDirectory) knownEmails(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(email string) {
		email = normalizeEmail(email)
		if email != "" && docstore.ValidAddress(email) && !seen[email] {
			seen[email] = true
			out = append(out, email)
		}
	}
	for _, prefix := range []string{docstore.DirectoryEnrollPrefix, directoryPrefix} {
		metas, err := docstore.ListPrefix(ctx, d.Store, prefix)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			if prefix == directoryPrefix && (strings.HasPrefix(m.ID, popPrefix) || strings.HasPrefix(m.ID, docstore.DirectoryEnrollPrefix)) {
				continue
			}
			doc, err := d.Store.Load(ctx, m.ID)
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var rec struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(doc.Data, &rec) == nil {
				add(rec.Email)
			}
		}
	}
	return out, nil
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
