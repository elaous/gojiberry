package directoryclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// WatchState is what a watcher remembers between runs of WatchLog (JSON-encodable, for the host
// to keep): the last log entry it saw (number and hash), the numbers of the entries its own
// operations made, and the enrollment state it derived for the address.
type WatchState struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
	// Made are the log numbers of operations this client made since the last WatchLog.
	Made []int64 `json:"made,omitempty"`
	// Enrolled, OwnerKeys and ResetSince are the audit state after the last entry seen.
	Enrolled   bool                         `json:"enrolled,omitempty"`
	OwnerKeys  []docstore.DirectoryOwnerKey `json:"owner_keys,omitempty"`
	ResetSince int64                        `json:"reset_since,omitempty"`
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
}

// WatchReport is what WatchLog found.
type WatchReport struct {
	// Head is the signed head the run was checked against.
	Head docstore.DirectoryHead
	// Entries are the new entries about the watcher's address, in order, whoever made them
	// (voided ones left out).
	Entries []docstore.DirectoryLogEntry
	// Foreign are the new entries about the address that the watcher did not make: not noted by
	// this client and, when the address is enrolled, not signed by the watcher's owner key. A
	// publication for an enrolled address that the owner key did not sign cannot come from the
	// owner: it is also a Violation.
	Foreign []Foreign
	// Violations are entries that break the owner rules of an enrolled address
	// (encrypt.AuditAddress): proof that somebody with the directory key, not the owner, wrote
	// them.
	Violations []encrypt.AuditViolation
	// Enrolled, OwnerKeys and ResetPending describe the address after the last entry.
	Enrolled     bool
	OwnerKeys    []docstore.DirectoryOwnerKey
	ResetPending bool
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

// pageSize is how many entries the route returns at most.
const pageSize = 200

// WatchLog is the watcher's run for the signed-in account's own address. It
//
//  1. asks for the address's pseudonym in the log (GET /directory/log/address) and the signed
//     head (GET /directory/log/head), checks the head's signature against the pinned keys
//     (Client.LogKeys) and against the head remembered in st: a lower number, or the same number
//     with another hash, is ErrLogCut;
//  2. reads the log from the entry last seen on, checks that entry is still the same
//     (encrypt.VerifyLogHead), the chain and every signature (encrypt.VerifyLog), and that the log
//     reaches the head it signed;
//  3. audits the entries of the address (encrypt.AuditAddress) and reports those the watcher did
//     not make.
//
// st is updated to the new last entry only when everything verified; on an error it is left as it
// was. The first run (an empty st) takes the whole log as new.
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
	// Read from the entry last seen (so it can be compared), or from the start.
	since := int64(0)
	if st.Seq > 0 {
		since = st.Seq - 1
	}
	var entries []docstore.DirectoryLogEntry
	for {
		var page docstore.DirectoryLog
		if err := c.do(ctx, http.MethodGet, "/directory/log?since="+strconv.FormatInt(since, 10), nil, &page); err != nil {
			return rep, err
		}
		if keys == nil {
			if !c.TrustServerKeys {
				return rep, errors.New("directoryclient: no pinned log keys (Client.LogKeys)")
			}
			keys = serverKeys(page)
		}
		entries = append(entries, page.Entries...)
		if len(page.Entries) < pageSize {
			break
		}
		since = page.Entries[len(page.Entries)-1].Seq
	}
	if err := encrypt.VerifyLogHead(st.Seq, st.Hash, entries); err != nil {
		return rep, err
	}
	if len(entries) > 0 {
		if _, err := encrypt.VerifyLog(entries, entries[0].Prev, entries[0].Seq-1, keys); err != nil {
			return rep, err
		}
	}
	var lastSeq int64
	var lastHash string
	if n := len(entries); n > 0 {
		lastSeq, lastHash = entries[n-1].Seq, entries[n-1].Hash
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

	// Audit the address from the state remembered, over the entries not seen before.
	fresh := entries
	if st.Seq > 0 && len(entries) > 0 {
		fresh = entries[1:] // the first is the one seen
	}
	addrs := append([]string{addr.Address}, addr.Previous...)
	var start *encrypt.AddressAudit
	if st.Enrolled || st.ResetSince != 0 || len(st.OwnerKeys) > 0 {
		start = &encrypt.AddressAudit{Enrolled: st.Enrolled, OwnerKeys: st.OwnerKeys, ResetSince: st.ResetSince}
	}
	audit := encrypt.AuditAddress(email, addrs, fresh, encrypt.AuditOptions{Start: start, ResetWait: opt.ResetWait})
	rep.Violations = audit.Violations
	rep.Enrolled, rep.OwnerKeys, rep.ResetPending = audit.Enrolled, audit.OwnerKeys, audit.ResetSince != 0
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
	rep.Entries = audit.Entries

	var ownerPub string
	if opt.Owner != nil {
		ownerPub = encrypt.FormatOwnerPub(opt.Owner.Public().(ed25519.PublicKey))
	}
	for _, e := range audit.Entries {
		if st.made(e.Seq) || (ownerPub != "" && signedByOwner(email, e, ownerPub)) {
			continue
		}
		rep.Foreign = append(rep.Foreign, Foreign{Entry: e, Message: foreignMessage(e)})
	}

	if lastSeq > 0 {
		st.Seq, st.Hash = lastSeq, lastHash
	}
	st.Made = nil
	st.Enrolled, st.OwnerKeys, st.ResetSince = audit.Enrolled, audit.OwnerKeys, audit.ResetSince
	return rep, nil
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
		return encrypt.VerifyOwnerOp(pub, e.Op, email, e.Fingerprint, e.OpTime, e.OwnerPrev, e.OwnerSig)
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
	}
	return "someone changed your address in the directory"
}
