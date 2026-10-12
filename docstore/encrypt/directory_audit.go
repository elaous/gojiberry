package encrypt

import (
	"crypto/ed25519"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"math"
	"time"
)

// AuditViolation is one entry of the log that breaks the rules of an enrolled address.
type AuditViolation struct {
	Seq int64
	Why string
}

// AuditNote is an entry that is odd but within the tolerance of the rules: not a violation.
type AuditNote struct {
	Seq int64
	// Ahead is how far the entry is dated after the auditor's clock.
	Ahead time.Duration
	Why   string
}

// AddressAudit is what AuditAddress found about one address in a run of the log.
type AddressAudit struct {
	// Entries are the entries about the address, in order: every one of them, including the ones
	// a void entry cancelled (see Voided), so that nothing a server wrote about the address is
	// hidden from its owner. The void entries themselves are not listed (Voided names them).
	Entries []LogEntry
	// Voided maps the number of each entry that a valid void entry cancelled to the void's
	// number: such an entry did not take effect (its record could not be written). It stays in
	// Entries, and is still checked against the rules: a void does not excuse a violation.
	Voided map[int64]int64
	// OperatorResets are the entries by which the operator removed the address's owner
	// enrollment (OpOperatorReset), a legitimate but notable event.
	OperatorResets []LogEntry
	// Enrolled says the address has an owner key after the last entry; OwnerKeys is the chain
	// (the last is current; Seq is the enrolling entry and Until the replacing one) and
	// ResetSince the time (Unix seconds) of a reset request still pending, 0 if none.
	Enrolled   bool
	OwnerKeys  []docstore.DirectoryOwnerKey
	ResetSince int64
	// LastSeq is the number of the last entry that changed the owner state (enrolled, rotated,
	// reset, requested or cancelled a reset, or an operator reset), 0 if none: what the
	// enrollment record's Seq must be.
	LastSeq int64
	// Notes are entries dated ahead of the auditor's clock by more than NoteSkew but within
	// FutureSkew: a replica with a fast clock, which is skew and not an attack. Each carries the
	// measured difference. Notes are advice, never a violation.
	Notes []AuditNote
	// Violations are the entries that break the rules: an operation on an enrolled address
	// without a valid signature of the owner key then current, an enrollment on an enrolled
	// address, a signature dated far from the entry, an entry dated before an earlier one or in
	// the future, a reset that took effect before its waiting period was over or was never
	// requested, a rotation or reset with no acceptance by the new key, a void that does not
	// cancel an earlier entry of the same address of a kind that can fail. A server alone cannot
	// make an entry that passes; one that does not is proof that somebody with the directory key
	// (not the owner) wrote it.
	Violations []AuditViolation
}

// AuditOptions are the rules AuditAddress applies.
type AuditOptions struct {
	// ResetWait is the shortest time a reset may take between its request and its taking effect
	// (DefaultResetWait when 0): an auditor holds the server to the 24 hours it promises, not to
	// whatever the server was set to.
	ResetWait time.Duration
	// Start is the state before the entries, when they continue a run already audited (the
	// AddressAudit of the earlier run); nil for the start of the log.
	Start *AddressAudit
	// AtSkew is how far back an entry's time may be from the latest time earlier in the run
	// (DefaultAtSkew when 0), the same rule as VerifyLog's.
	AtSkew time.Duration
	// Now, when set, is the auditor's clock: an entry of the address dated more than FutureSkew
	// after it was written in the future, which a server does to make a wait look over (the
	// wait is measured by the entries' times, which the server writes). The zero time turns the
	// check off.
	Now time.Time
	// FutureSkew is the tolerance of that check (ownerSkew, 10 minutes, when 0).
	FutureSkew time.Duration
	// NoteSkew is how far ahead of Now an entry may be dated before it is noted (a minute
	// when 0). It only matters when Now is set.
	NoteSkew time.Duration
}

// AuditAddress checks the entries of the log that are about one address (addresses are the
// address's pseudonyms, LogAddressFor under the directory's keys, as GET /directory/log/address
// gives the owner; email the address itself, which the owner signatures name) against the rules
// of an owner-enrolled address: it replays the enrollment state and flags every entry that the
// owner key then current did not sign. entries is a run of the log (all addresses; the entries
// of other addresses are skipped, but their times and numbers are used), already checked for
// chain and signatures with VerifyLog.
//
// Times. The entries' times are written by the server, so the audit does not trust them alone:
// an entry dated more than AtSkew before an earlier entry of the run is a violation, so is one
// dated after the auditor's clock (Now), and the 24-hour wait of a reset is measured between the
// times of its request and its taking effect, in chain order: a request cannot be back-dated
// (the entries before it bound it from below) nor the reset forward-dated for long (the
// auditor's clock bounds it from above). An auditor that was not looking during the wait cannot
// tell a wait that was kept from one that was not.
//
// Voids. A "void" entry cancels the entry it names (Ref) only if that entry is earlier, about
// the same address, of a kind that can fail (publish, revoke or an owner-state operation), not
// already cancelled, and the void is dated within a short window of it (VoidWindow: it is the
// compensation of a failed write, made in the same round). Any other void is a violation and
// cancels nothing. A voided entry stays in Entries and in Voided, is checked like any other
// (a voided entry that breaks the rules is a violation anyway) and changes no state.
func AuditAddress(email string, addresses []string, entries []LogEntry, opt AuditOptions) AddressAudit {
	mine := map[string]bool{}
	for _, a := range addresses {
		mine[a] = true
	}
	wait := opt.ResetWait
	if wait <= 0 {
		wait = DefaultResetWait
	}
	atSkew := opt.AtSkew
	if atSkew <= 0 {
		atSkew = DefaultAtSkew
	}
	future := opt.FutureSkew
	if future <= 0 {
		future = ownerSkew
	}
	note := opt.NoteSkew
	if note <= 0 {
		note = time.Minute
	}
	var out AddressAudit
	if opt.Start != nil {
		out = *opt.Start
		out.Notes = nil
		out.Entries = nil
		out.Violations = nil
		out.Voided = nil
		out.OperatorResets = nil
		out.OwnerKeys = append([]docstore.DirectoryOwnerKey(nil), opt.Start.OwnerKeys...)
	}
	out.Voided = map[int64]int64{}
	bySeq := map[int64]LogEntry{}
	for _, e := range entries {
		bySeq[e.Seq] = e
	}
	// Which voids are valid, decided before the entries are replayed (a void comes after the
	// entry it cancels, whose effect it must prevent).
	voidBad := map[int64]string{}
	for _, e := range entries {
		if e.Op != OpVoid || !mine[e.Address] {
			continue
		}
		ref, found := bySeq[e.Ref]
		switch {
		case !found:
			voidBad[e.Seq] = "a void of an entry the audit does not hold"
		case e.Ref >= e.Seq:
			voidBad[e.Seq] = "a void of an entry that is not before it"
		case ref.Address != e.Address:
			voidBad[e.Seq] = "a void of an entry about another address"
		case !voidable(ref.Op):
			voidBad[e.Seq] = "a void of an entry of a kind that cannot be cancelled (" + ref.Op + ")"
		case out.Voided[e.Ref] != 0:
			voidBad[e.Seq] = "a void of an entry that was cancelled already"
		case e.At-ref.At > int64(VoidWindow/time.Second):
			voidBad[e.Seq] = "a void written long after the entry it cancels"
		default:
			out.Voided[e.Ref] = e.Seq
		}
	}
	bad := func(e LogEntry, why string) { out.Violations = append(out.Violations, AuditViolation{e.Seq, why}) }
	cur := func() (ed25519.PublicKey, bool) {
		if !out.Enrolled || len(out.OwnerKeys) == 0 {
			return nil, false
		}
		pub, err := ParseOwnerPub(out.OwnerKeys[len(out.OwnerKeys)-1].Pub)
		return pub, err == nil
	}
	// timed checks what every owner signature names: the time near the entry's, a position that
	// is earlier in the log (and, when we hold that entry, its hash).
	timed := func(e LogEntry) bool {
		if d := e.At - e.OpTime; d > int64(auditSkew/time.Second) || -d > int64(auditSkew/time.Second) {
			bad(e, "the owner signature is dated far from the entry")
			return false
		}
		seq, hash, err := ParseLogPosition(e.OwnerPrev)
		if err != nil || seq >= e.Seq {
			bad(e, "the owner signature names a log position that is not before the entry")
			return false
		}
		if p, ok := bySeq[seq]; ok && p.Hash != hash {
			bad(e, "the owner signature names a log position that is not the log's")
			return false
		}
		return true
	}
	var maxAt int64
	if opt.Start != nil {
		maxAt = 0 // the run's own first entry is the baseline
	}
	for _, e := range entries {
		backdated := maxAt > 0 && e.At < maxAt-int64(atSkew/time.Second)
		maxAt = max(maxAt, e.At)
		if !mine[e.Address] {
			continue
		}
		if e.Op == OpVoid {
			if why := voidBad[e.Seq]; why != "" {
				bad(e, why)
			}
			if backdated {
				bad(e, "the entry is dated before an earlier entry of the log: the times of the chain went back")
			}
			continue
		}
		out.Entries = append(out.Entries, e)
		if backdated {
			bad(e, "the entry is dated before an earlier entry of the log: the times of the chain went back")
		}
		if !opt.Now.IsZero() {
			// Compared in seconds: a Duration of a huge gap would overflow and wrap to a small or
			// negative number, which would hide a date far in the future.
			gap := e.At - opt.Now.Unix()
			switch {
			case gap > int64(future/time.Second):
				bad(e, fmt.Sprintf("the entry is dated in the future (%s ahead of the auditor's clock)", aheadText(gap)))
			case gap > int64(note/time.Second):
				out.Notes = append(out.Notes, AuditNote{Seq: e.Seq, Ahead: time.Duration(gap) * time.Second,
					Why: "the entry is dated ahead of the auditor's clock: a fast clock on a replica, or the auditor's own"})
			}
		}
		// A voided entry is checked like any other but changes nothing.
		_, isVoided := out.Voided[e.Seq]
		effect := func(f func()) {
			if !isVoided {
				f()
				out.LastSeq = e.Seq
			}
		}
		switch e.Op {
		case OpPublish, OpRevoke:
			if !out.Enrolled {
				continue
			}
			pub, ok := cur()
			switch {
			case !ok || e.OwnerSig == "":
				bad(e, "no owner signature on an enrolled address")
			case !timed(e):
			case !verifyOwnerVersion(pub, e.OwnerV, e.Op, email, e.Fingerprint, e.LabelHash, e.OpTime, e.OwnerPrev, e.OwnerSig):
				bad(e, "the owner signature does not verify under the current owner key")
			}
			// A version 1 signature does not cover the label: accepted here (entries made before
			// the label was signed exist), though the directory refuses a new one.
		case OpOperatorReset:
			out.OperatorResets = append(out.OperatorResets, e)
			effect(func() {
				out.Enrolled, out.OwnerKeys, out.ResetSince = false, nil, 0
			})
		case OpEnroll:
			if out.Enrolled {
				bad(e, "an enrollment on an address that already has an owner key")
				continue
			}
			pub, err := ParseOwnerPub(e.OwnerPub)
			if err != nil {
				bad(e, "the enrollment names no owner key")
				continue
			}
			if !timed(e) {
				continue
			}
			if !verifyOwner(pub, OpEnroll, email, OwnerFingerprint(pub), e.OpTime, e.OwnerPrev, e.OwnerSig) {
				bad(e, "the new key did not sign its enrollment")
				continue
			}
			effect(func() {
				out.Enrolled = true
				out.OwnerKeys = []docstore.DirectoryOwnerKey{{Pub: e.OwnerPub, Seq: e.Seq}}
				out.ResetSince = 0
			})
		case OpRotate, OpReset:
			newPub, err := ParseOwnerPub(e.OwnerPub)
			if err != nil || !out.Enrolled {
				bad(e, "a rotation or reset of an address with no owner key")
				continue
			}
			if !timed(e) {
				continue
			}
			ok := verifyOwner(newPub, "accept", email, OwnerFingerprint(newPub), e.OpTime, e.OwnerPrev, e.ProofSig)
			if !ok {
				bad(e, "the new key did not accept")
				continue
			}
			if e.Op == OpRotate {
				pub, have := cur()
				if !have || !verifyOwner(pub, OpRotate, email, OwnerFingerprint(newPub), e.OpTime, e.OwnerPrev, e.OwnerSig) {
					bad(e, "the current owner key did not sign the rotation")
					continue
				}
			} else if out.ResetSince == 0 || e.At < out.ResetSince+int64(wait/time.Second) {
				bad(e, "a reset that took effect before its waiting period was over, or was never requested")
				continue
			}
			effect(func() {
				out.OwnerKeys[len(out.OwnerKeys)-1].Until = e.Seq
				out.OwnerKeys = append(out.OwnerKeys, docstore.DirectoryOwnerKey{Pub: e.OwnerPub, Seq: e.Seq})
				out.ResetSince = 0
			})
		case OpResetRequest:
			if !out.Enrolled {
				bad(e, "a reset requested for an address with no owner key")
				continue
			}
			effect(func() {
				if out.ResetSince == 0 {
					out.ResetSince = e.At
				}
			})
		case OpResetCancel:
			pub, ok := cur()
			switch {
			case !out.Enrolled || out.ResetSince == 0:
				bad(e, "a reset cancelled that was not pending")
			case !ok || !timed(e):
			case !verifyOwner(pub, OpResetCancel, email, "", e.OpTime, e.OwnerPrev, e.OwnerSig):
				bad(e, "the cancellation is not signed by the current owner key")
			default:
				effect(func() { out.ResetSince = 0 })
			}
		}
	}
	return out
}

// aheadText writes a gap in seconds, which may be far beyond what a time.Duration holds.
func aheadText(secs int64) string {
	if secs > int64(math.MaxInt64/int64(time.Second)) {
		return "more than 290 years"
	}
	return (time.Duration(secs) * time.Second).String()
}
