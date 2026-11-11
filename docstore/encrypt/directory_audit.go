package encrypt

import (
	"crypto/ed25519"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"math"
	"time"
)

// AuditViolation is one log entry that breaks a rule.
type AuditViolation struct {
	Seq int64
	Why string
}

type AuditNote struct {
	Seq   int64
	Ahead time.Duration
	Why   string
}

// AddressAudit is what AuditAddress found about one address.
type AddressAudit struct {
	// Entries are the entries about the address.
	Entries        []LogEntry
	Voided         map[int64]int64
	OperatorResets []LogEntry
	Enrolled       bool
	OwnerKeys      []docstore.DirectoryOwnerKey
	ResetSince     int64
	LastSeq        int64
	// Notes are entries dated ahead of the auditor's clock.
	Notes []AuditNote
	// Violations are the entries that break the rules.
	Violations []AuditViolation
}

// AuditOptions are the rules AuditAddress applies.
type AuditOptions struct {
	// ResetWait is the shortest time a reset may take.
	ResetWait time.Duration
	Start     *AddressAudit
	// AtSkew is how far back an entry's time may be.
	AtSkew time.Duration
	// Now, when set, is the auditor's clock.
	Now time.Time
	// FutureSkew is the tolerance of the future-time check.
	FutureSkew time.Duration
	NoteSkew   time.Duration
}

// AuditAddress checks the log entries about one address.
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
			gap := e.At - opt.Now.Unix()
			switch {
			case gap > int64(future/time.Second):
				bad(e, fmt.Sprintf("the entry is dated in the future (%s ahead of the auditor's clock)", aheadText(gap)))
			case gap > int64(note/time.Second):
				out.Notes = append(out.Notes, AuditNote{Seq: e.Seq, Ahead: time.Duration(gap) * time.Second,
					Why: "the entry is dated ahead of the auditor's clock: a fast clock on a replica, or the auditor's own"})
			}
		}
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

func aheadText(secs int64) string {
	if secs > int64(math.MaxInt64/int64(time.Second)) {
		return "more than 290 years"
	}
	return (time.Duration(secs) * time.Second).String()
}
