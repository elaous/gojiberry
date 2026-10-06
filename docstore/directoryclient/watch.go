package directoryclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// WatchState is what a watcher remembers between runs of WatchLog (JSON-encodable, for the host
// to keep): where it stopped in the log, the numbers of the entries its own operations made, the
// pseudonym of its address that it confirmed from its own entries, and the enrollment state it
// derived for the address.
type WatchState struct {
	// Seq and Hash are the last log entry the watcher saw (or, while the server shows it only its
	// own address's entries, the last signed head); Cursor is how far it has read (the number
	// of the last entry it processed; Seq when it reads the whole log).
	Seq    int64  `json:"seq"`
	Hash   string `json:"hash"`
	Cursor int64  `json:"cursor,omitempty"`
	// Filtered says the last run read only the entries about its own address (the server shows
	// the whole log only to the owner of an enrolled address and to the operator).
	Filtered bool `json:"filtered,omitempty"`
	// Email is the address watched.
	Email string `json:"email,omitempty"`
	// Made are the log numbers of operations this client made since the last WatchLog.
	Made []int64 `json:"made,omitempty"`
	// Addresses are the log pseudonyms of the watcher's address that WatchLog confirmed: the
	// Address of an entry this client made itself (NoteMade) is the first, and from then on the
	// pseudonym the server reports must agree with them (an entry of the watcher's own that is
	// not under a pseudonym the server reported, or one pinned here, is an error:
	// ErrAddressMismatch). The pseudonym is a keyed hash under the directory's secret, so the
	// client cannot compute it: this pinning is what stops the server from pointing the watcher
	// at an address with nothing in it. Until the first entry of its own is confirmed, the
	// watcher runs on the server's word (WatchReport.AddressUnconfirmed).
	Addresses []string `json:"addresses,omitempty"`
	// Enrolled, OwnerKeys and ResetSince are the audit state after the last entry that is settled
	// (older than any void could still come for), and Pending are the entries of the address after
	// it: they are audited again with the next run, so that a void that follows an entry across
	// two runs still cancels it.
	Enrolled   bool                         `json:"enrolled,omitempty"`
	OwnerKeys  []docstore.DirectoryOwnerKey `json:"owner_keys,omitempty"`
	ResetSince int64                        `json:"reset_since,omitempty"`
	Pending    []docstore.DirectoryLogEntry `json:"pending,omitempty"`
	// Reported is the highest entry number WatchLog has already reported (as foreign or a
	// violation), so the entries it audits again are not reported twice.
	Reported int64 `json:"reported,omitempty"`
	// Seen is what Lookup saw for each address looked up: whether each key's row carried an owner
	// signature, so that a row that loses it later is noticed (Key.OwnerDowngraded).
	Seen map[string]SeenAddress `json:"seen,omitempty"`
}

// SeenAddress is what a Lookup saw for an address: the fingerprints whose rows carried an owner
// signature that verified, and whether the address showed an owner key at all.
type SeenAddress struct {
	Signed   []string `json:"signed,omitempty"`
	Enrolled bool     `json:"enrolled,omitempty"`
}

// NoteMade records that the entry with this number was made by this client.
func (s *WatchState) NoteMade(seq int64) { s.Made = append(s.Made, seq) }

func (s *WatchState) made(seq int64) bool {
	for _, m := range s.Made {
		if m == seq {
			return true
		}
	}
	return false
}

// Foreign is an entry of the log about the watcher's address that the watcher did not make.
type Foreign struct {
	Entry docstore.DirectoryLogEntry
	// Message says it in words: "someone published a key for your address".
	Message string
	// Voided: a void entry cancelled it (it did not take effect), but it was written.
	Voided bool
}

// WatchReport is what WatchLog found.
type WatchReport struct {
	// Head is the signed head the run was checked against.
	Head docstore.DirectoryHead
	// Entries are the new entries about the watcher's address, in order, whoever made them,
	// including the ones a void cancelled (see Voided): nothing the server wrote about the
	// address is left out.
	Entries []docstore.DirectoryLogEntry
	// Voided maps the number of each of the Entries that was cancelled to the void entry's number.
	Voided map[int64]int64
	// Foreign are the new entries about the address that the watcher did not make: not noted by
	// this client and, when the address is enrolled, not signed by the watcher's owner key. A
	// publication for an enrolled address that the owner key did not sign cannot come from the
	// owner: it is also a Violation. An operator reset of the enrollment is always listed.
	Foreign []Foreign
	// Violations are entries that break the owner rules of an enrolled address
	// (encrypt.AuditAddress): proof that somebody with the directory key, not the owner, wrote
	// them.
	Violations []encrypt.AuditViolation
	// Enrolled, OwnerKeys and ResetPending describe the address after the last entry.
	Enrolled     bool
	OwnerKeys    []docstore.DirectoryOwnerKey
	ResetPending bool
	// AddressUnconfirmed says the run used the pseudonym the server reported and no entry of the
	// watcher's own confirms it yet (State.Made was empty and none was pinned): the server could
	// have pointed the watcher at an address with nothing in it. Make an entry (publish a key,
	// enroll) and run again.
	AddressUnconfirmed bool
	// Filtered says the server showed only the entries about the address, so the chain between
	// them was not checked (each entry's hash and signature were, and the head's); a gap, an
	// entry the server left out, cannot be told from here. Enroll an owner key to read the whole
	// log.
	Filtered bool
}

// WatchOptions are the choices of WatchLog.
type WatchOptions struct {
	// Owner is the watcher's owner key, if the address is enrolled: entries it signed are the
	// watcher's own (made on another device, say).
	Owner OwnerKey
	// ResetWait is the shortest wait the audit accepts between a reset request and its taking
	// effect (24 hours when 0).
	ResetWait time.Duration
}

// ErrLogCut is returned by WatchLog when the log is shorter than what the watcher saw, or has a
// different entry where it saw one: the server cut it back or rewrote it.
var ErrLogCut = encrypt.ErrLogTruncated

// ErrAddressMismatch is returned by WatchLog when an entry this client made is not under a
// pseudonym the server reported for the address (or one pinned from an earlier entry): the server
// is pointing the watcher at the wrong address, so that it would see nothing.
var ErrAddressMismatch = errors.New("directoryclient: an entry this client made is not under the address pseudonym the server reported")

// ErrOwnEntryMissing is returned by WatchLog when an entry this client made is not in the log.
var ErrOwnEntryMissing = errors.New("directoryclient: an entry this client made is not in the log")

// pageSize is how many entries the route returns at most.
const pageSize = 200

// logRead is what reading the log returned.
type logRead struct {
	entries  []docstore.DirectoryLogEntry
	keys     map[string]ed25519.PublicKey
	filtered bool
	next     int64
}

// readLog reads the log after since, page by page, to its end.
func (c *Client) readLog(ctx context.Context, since int64, keys map[string]ed25519.PublicKey) (logRead, error) {
	var out logRead
	out.keys = keys
	for {
		var page docstore.DirectoryLog
		if err := c.do(ctx, http.MethodGet, "/directory/log?since="+strconv.FormatInt(since, 10), nil, &page); err != nil {
			return out, err
		}
		if out.keys == nil {
			if !c.TrustServerKeys {
				return out, errors.New("directoryclient: no pinned log keys (Client.LogKeys)")
			}
			out.keys = serverKeys(page)
		}
		out.filtered = page.Filtered
		out.entries = append(out.entries, page.Entries...)
		if page.Filtered {
			// Only the entries of the address come back; the scan says where it stopped.
			out.next = max(page.Next, since)
			if !page.More || page.Next <= since {
				return out, nil
			}
			since = page.Next
			continue
		}
		if len(page.Entries) < pageSize {
			if n := len(out.entries); n > 0 {
				out.next = out.entries[n-1].Seq
			}
			return out, nil
		}
		since = page.Entries[len(page.Entries)-1].Seq
	}
}

// entryAt fetches and checks one entry of the log by number (a client whose own entry is older
// than what it reads now).
func (c *Client) entryAt(ctx context.Context, seq int64, keys map[string]ed25519.PublicKey) (docstore.DirectoryLogEntry, bool, error) {
	var page docstore.DirectoryLog
	if err := c.do(ctx, http.MethodGet, "/directory/log?since="+strconv.FormatInt(seq-1, 10), nil, &page); err != nil {
		return docstore.DirectoryLogEntry{}, false, err
	}
	if keys == nil {
		if !c.TrustServerKeys {
			return docstore.DirectoryLogEntry{}, false, errors.New("directoryclient: no pinned log keys (Client.LogKeys)")
		}
		keys = serverKeys(page)
	}
	for _, e := range page.Entries {
		if e.Seq == seq {
			if err := encrypt.VerifyEntry(e, keys); err != nil {
				return e, false, err
			}
			return e, true, nil
		}
	}
	return docstore.DirectoryLogEntry{}, false, nil
}

// WatchLog is the watcher's run for the signed-in account's own address. It
//
//  1. asks for the address's pseudonym in the log (GET /directory/log/address) and the signed
//     head (GET /directory/log/head), checks the head's signature against the pinned keys
//     (Client.LogKeys) and against the head remembered in st: a lower number, or the same number
//     with another hash, is ErrLogCut;
//  2. reads the log from the entry last seen on, checks that entry is still the same
//     (encrypt.VerifyLogHead), the chain, the times and every signature (encrypt.VerifyLog), and
//     that the log reaches the head it signed. (A caller who is not the owner of an enrolled
//     address gets only the entries about their own address: then each entry's hash and signature
//     are checked and the chain is not, see WatchReport.Filtered);
//  3. checks that every entry this client made (State.Made) is in the log under the pseudonym the
//     server reported, and pins that pseudonym (State.Addresses): a server that reports another
//     one to blind the watcher is caught by the client's own entries (ErrAddressMismatch);
//  4. audits the entries of the address (encrypt.AuditAddress) and reports those the watcher did
//     not make, the voided ones included (marked).
//
// st is updated only when everything verified; on an error it is left as it was. The first run
// (an empty st) takes the whole log as new.
func (c *Client) WatchLog(ctx context.Context, email string, st *WatchState, opt WatchOptions) (WatchReport, error) {
	var rep WatchReport
	keys := c.LogKeys
	addr, err := c.LogAddress(ctx)
	if err != nil {
		return rep, err
	}
	head, err := c.Head(ctx)
	if err != nil {
		return rep, err
	}
	rep.Head = head
	if err := encrypt.CheckHead(st.Seq, st.Hash, head); err != nil {
		return rep, err
	}
	// Read from the entry last seen (so it can be compared), or from the start. The server says
	// whether it shows the whole log; if that is not what the last run saw, read again from where
	// the other mode continues.
	cursor := st.Cursor
	if cursor == 0 {
		cursor = st.Seq
	}
	fromSeen := func(filtered bool) int64 {
		if !filtered && !st.Filtered && st.Seq > 0 {
			return st.Seq - 1 // the entry seen comes back, to be compared
		}
		return cursor
	}
	read, err := c.readLog(ctx, fromSeen(st.Filtered), keys)
	if err != nil {
		return rep, err
	}
	if read.filtered != st.Filtered {
		if read, err = c.readLog(ctx, fromSeen(read.filtered), keys); err != nil {
			return rep, err
		}
	}
	keys, entries, filtered := read.keys, read.entries, read.filtered
	rep.Filtered = filtered
	chained := !filtered && !st.Filtered
	var lastSeq int64
	var lastHash string
	if n := len(entries); n > 0 {
		lastSeq, lastHash = entries[n-1].Seq, entries[n-1].Hash
	}
	fresh := entries
	if filtered {
		for _, e := range entries {
			if err := encrypt.VerifyEntry(e, keys); err != nil {
				return rep, err
			}
		}
	} else {
		if chained {
			if err := encrypt.VerifyLogHead(st.Seq, st.Hash, entries); err != nil {
				return rep, err
			}
		}
		if len(entries) > 0 {
			if _, err := encrypt.VerifyLog(entries, entries[0].Prev, entries[0].Seq-1, keys); err != nil {
				return rep, err
			}
		}
		// The log may have grown since the head was signed, but it must contain the head's entry.
		if lastSeq < head.Seq {
			return rep, fmt.Errorf("%w: the log reaches entry %d, the signed head is entry %d", ErrLogCut, lastSeq, head.Seq)
		}
		if head.Seq > 0 {
			var atHead *docstore.DirectoryLogEntry
			for i := range entries {
				if entries[i].Seq == head.Seq {
					atHead = &entries[i]
				}
			}
			if atHead == nil || atHead.Hash != head.Hash {
				return rep, fmt.Errorf("%w: entry %d is not the one the signed head names", ErrLogCut, head.Seq)
			}
		}
		if chained && st.Seq > 0 && len(entries) > 0 {
			fresh = entries[1:] // the first is the one seen
		}
	}

	// The pseudonym: the server's word, confirmed by the client's own entries.
	known := map[string]bool{}
	var addrs []string
	for _, a := range append(append([]string(nil), st.Addresses...), append([]string{addr.Address}, addr.Previous...)...) {
		if a != "" && !known[a] {
			known[a] = true
			addrs = append(addrs, a)
		}
	}
	pinned := append([]string(nil), st.Addresses...)
	for _, seq := range st.Made {
		var e docstore.DirectoryLogEntry
		found := false
		for _, x := range entries {
			if x.Seq == seq {
				e, found = x, true
			}
		}
		if !found {
			if e, found, err = c.entryAt(ctx, seq, keys); err != nil {
				return rep, err
			}
		}
		switch {
		case !found:
			return rep, fmt.Errorf("%w: entry %d", ErrOwnEntryMissing, seq)
		case !known[e.Address]:
			return rep, fmt.Errorf("%w: entry %d is under %s", ErrAddressMismatch, seq, e.Address)
		}
		have := false
		for _, p := range pinned {
			have = have || p == e.Address
		}
		if !have {
			pinned = append(pinned, e.Address)
		}
	}
	rep.AddressUnconfirmed = len(pinned) == 0

	// Audit the address from the settled state remembered, over the entries still pending and the
	// new ones.
	var start *encrypt.AddressAudit
	if st.Enrolled || st.ResetSince != 0 || len(st.OwnerKeys) > 0 {
		start = &encrypt.AddressAudit{Enrolled: st.Enrolled, OwnerKeys: st.OwnerKeys, ResetSince: st.ResetSince}
	}
	mine := map[string]bool{}
	for _, a := range addrs {
		mine[a] = true
	}
	todo := append([]docstore.DirectoryLogEntry(nil), st.Pending...)
	for _, e := range fresh {
		if mine[e.Address] {
			todo = append(todo, e)
		}
	}
	// The entries before them give the audit the times and numbers it measures by.
	auditEntries := fresh
	if len(st.Pending) > 0 {
		auditEntries = append(append([]docstore.DirectoryLogEntry(nil), st.Pending...), fresh...)
	}
	aopt := encrypt.AuditOptions{Start: start, ResetWait: opt.ResetWait, Now: c.now()}
	audit := encrypt.AuditAddress(email, addrs, auditEntries, aopt)
	rep.Enrolled, rep.OwnerKeys, rep.ResetPending = audit.Enrolled, audit.OwnerKeys, audit.ResetSince != 0
	for _, v := range audit.Violations {
		if v.Seq > st.Reported {
			rep.Violations = append(rep.Violations, v)
		}
	}
	// What the server says about the enrollment must be what the log shows: a record that was
	// deleted (an un-enrolling), or one that names another key than the log's, is the server's doing.
	if enr, err := c.Enrollment(ctx, email); err != nil {
		return rep, err
	} else {
		switch cur := lastOwnerKey(audit.OwnerKeys); {
		case audit.Enrolled && !enr.Enrolled:
			rep.Violations = append(rep.Violations, encrypt.AuditViolation{Why: "the log shows an owner key for the address but the directory says it has none"})
		case !audit.Enrolled && enr.Enrolled:
			rep.Violations = append(rep.Violations, encrypt.AuditViolation{Why: "the directory says the address has an owner key but the log shows none"})
		case audit.Enrolled && lastOwnerKey(enr.Keys) != cur:
			rep.Violations = append(rep.Violations, encrypt.AuditViolation{Why: "the directory names another current owner key than the log does"})
		}
	}
	rep.Voided = map[int64]int64{}
	for _, e := range audit.Entries {
		if e.Seq > st.Reported {
			rep.Entries = append(rep.Entries, e)
			if v, ok := audit.Voided[e.Seq]; ok {
				rep.Voided[e.Seq] = v
			}
		}
	}

	var ownerPub string
	if opt.Owner != nil {
		ownerPub = encrypt.FormatOwnerPub(opt.Owner.Public().(ed25519.PublicKey))
	}
	for _, e := range rep.Entries {
		_, voided := audit.Voided[e.Seq]
		if e.Op != encrypt.OpOperatorReset && (st.made(e.Seq) || (ownerPub != "" && signedByOwner(email, e, ownerPub))) {
			continue
		}
		rep.Foreign = append(rep.Foreign, Foreign{Entry: e, Message: foreignMessage(e), Voided: voided})
	}

	// Settle what no void can still reach (older, by the entries' own times, than the latest
	// entry seen less the skew and the void window); the rest is audited again next run.
	lastAt := head.At
	if n := len(entries); n > 0 && !filtered {
		lastAt = max(lastAt, entries[n-1].At)
	}
	boundary := lastAt - int64((encrypt.DefaultAtSkew+encrypt.VoidWindow)/time.Second)
	settled := 0
	for settled < len(todo) && todo[settled].At <= boundary {
		settled++
	}
	var settleAudit encrypt.AddressAudit
	if settled > 0 {
		settleAudit = encrypt.AuditAddress(email, addrs, auditEntriesFor(auditEntries, todo[:settled]), aopt)
	} else if start != nil {
		settleAudit = *start
	}

	// Everything verified: remember it.
	if filtered {
		st.Seq, st.Hash = head.Seq, head.Hash
		st.Cursor = max(read.next, cursor)
	} else if lastSeq > 0 {
		st.Seq, st.Hash, st.Cursor = lastSeq, lastHash, lastSeq
	}
	st.Filtered = filtered
	st.Email = strings.ToLower(strings.TrimSpace(email))
	st.Made = nil
	st.Addresses = pinned
	st.Enrolled, st.OwnerKeys, st.ResetSince = settleAudit.Enrolled, settleAudit.OwnerKeys, settleAudit.ResetSince
	st.Pending = append([]docstore.DirectoryLogEntry(nil), todo[settled:]...)
	for _, e := range audit.Entries {
		st.Reported = max(st.Reported, e.Seq)
	}
	for _, v := range audit.Violations {
		st.Reported = max(st.Reported, v.Seq)
	}
	return rep, nil
}

// auditEntriesFor is the part of entries that the settled ones span: everything up to the last
// settled entry's number (the entries of other addresses give the audit their times and numbers).
func auditEntriesFor(entries, settled []docstore.DirectoryLogEntry) []docstore.DirectoryLogEntry {
	if len(settled) == 0 {
		return nil
	}
	last := settled[len(settled)-1].Seq
	var out []docstore.DirectoryLogEntry
	for _, e := range entries {
		if e.Seq <= last {
			out = append(out, e)
		}
	}
	return out
}

func lastOwnerKey(keys []docstore.DirectoryOwnerKey) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[len(keys)-1].Pub
}

func serverKeys(l docstore.DirectoryLog) map[string]ed25519.PublicKey {
	out := map[string]ed25519.PublicKey{}
	for kid, k := range l.Keys {
		if b, err := base64.StdEncoding.DecodeString(k); err == nil && len(b) == ed25519.PublicKeySize {
			out[kid] = ed25519.PublicKey(b)
		}
	}
	return out
}

// signedByOwner says the entry carries a signature of the watcher's own owner key (or of a key
// in the chain the audit derived that has the same public key).
func signedByOwner(email string, e docstore.DirectoryLogEntry, ownerPub string) bool {
	pub, err := encrypt.ParseOwnerPub(ownerPub)
	if err != nil {
		return false
	}
	switch e.Op {
	case encrypt.OpPublish, encrypt.OpRevoke, encrypt.OpResetCancel:
		return encrypt.VerifyOwnerOpV(pub, e.OwnerV, e.Op, email, e.Fingerprint, e.LabelHash, e.OpTime, e.OwnerPrev, e.OwnerSig)
	case encrypt.OpEnroll:
		return e.OwnerPub == ownerPub
	case encrypt.OpRotate, encrypt.OpReset:
		if e.OwnerPub == ownerPub {
			return true // the new key is the watcher's own
		}
		// A rotation the watcher's key authorised (the key it replaces with another one).
		next, err := encrypt.ParseOwnerPub(e.OwnerPub)
		return err == nil && e.Op == encrypt.OpRotate && encrypt.VerifyOwnerOp(pub, e.Op, email, encrypt.OwnerFingerprint(next), e.OpTime, e.OwnerPrev, e.OwnerSig)
	}
	return false
}

func foreignMessage(e docstore.DirectoryLogEntry) string {
	switch e.Op {
	case encrypt.OpPublish:
		return "someone published a key for your address"
	case encrypt.OpRevoke:
		return "someone revoked a key of your address"
	case encrypt.OpEnroll:
		return "someone enrolled an owner key for your address"
	case encrypt.OpRotate:
		return "someone replaced the owner key of your address"
	case encrypt.OpResetRequest:
		return "someone asked to reset the owner key of your address (it takes effect after the waiting period)"
	case encrypt.OpReset:
		return "the owner key of your address was reset"
	case encrypt.OpResetCancel:
		return "someone cancelled a reset of the owner key of your address"
	case encrypt.OpOperatorReset:
		return "the operator of the directory removed the owner key of your address (an operator reset: it can be done to any address, and it is why this is shown)"
	}
	return "someone changed your address in the directory"
}
