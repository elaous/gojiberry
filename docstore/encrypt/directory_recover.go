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

const reconcileGrace = 2 * time.Minute

func (d *AdapterDirectory) grace() time.Duration {
	if d.ReconcileGrace > 0 {
		return d.ReconcileGrace
	}
	return reconcileGrace
}

func stateEntry(op string) bool {
	switch op {
	case OpEnroll, OpRotate, OpReset, OpResetRequest, OpResetCancel, OpOperatorReset, OpVoid:
		return true
	}
	return false
}

type logIndex struct {
	scanned int64
	byAddr  map[string][]LogEntry
}

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
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

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
		return nil
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

// ReconcileReport is what one reconciliation pass did.
type ReconcileReport struct {
	// Completed is how many records it completed.
	Completed int
	// Deferred is how many addresses were left for later.
	Deferred int
	// Unmapped is how many pseudonyms have owner-state entries.
	Unmapped int
}

// ReconcileEnrollments reconciles every address.
func (d *AdapterDirectory) ReconcileEnrollments(ctx context.Context) (int, error) {
	rep, err := d.reconcileAll(ctx)
	return rep.Completed, err
}

func (d *AdapterDirectory) reconcileAll(ctx context.Context) (ReconcileReport, error) {
	var rep ReconcileReport
	if d.Signer == nil {
		return rep, ErrDirectoryOff
	}
	emails, err := d.knownEmails(ctx)
	if err != nil {
		return rep, err
	}
	if _, err := d.stateEntries(ctx, nil); err != nil {
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
	un := d.findUnmapped(covered)
	rep.Unmapped = len(un)
	d.warnUnmapped(rep.Unmapped)
	return rep, nil
}

// DefaultUnmappedCap is how many unmapped addresses are listed by default.
const DefaultUnmappedCap = 100

func (d *AdapterDirectory) findUnmapped(covered map[string]bool) []docstore.DirectoryUnmappedAddress {
	d.idxMu.Lock()
	defer d.idxMu.Unlock()
	var out []docstore.DirectoryUnmappedAddress
	for a, ents := range d.idx.byAddr {
		if covered[a] {
			continue
		}
		voided := map[int64]bool{}
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

// UnmappedAddresses lists the log pseudonyms without an address.
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

// OperatorResetEnrollment removes the owner enrollment of an address.
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
