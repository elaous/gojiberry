package directoryclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// WatchState is what a watcher remembers between runs.
type WatchState struct {
	Seq    int64  `json:"seq"`
	Hash   string `json:"hash"`
	Cursor int64  `json:"cursor,omitempty"`
	// Filtered says the last run read only one address.
	Filtered bool `json:"filtered,omitempty"`
	// Email is the address watched.
	Email string      `json:"email,omitempty"`
	Made  []MadeEntry `json:"made,omitempty"`
	// Addresses are the log pseudonyms of the watched address.
	Addresses []string `json:"addresses,omitempty"`
	// Enrolled, OwnerKeys and ResetSince are the audited owner state.
	Enrolled   bool                         `json:"enrolled,omitempty"`
	OwnerKeys  []docstore.DirectoryOwnerKey `json:"owner_keys,omitempty"`
	ResetSince int64                        `json:"reset_since,omitempty"`
	Pending    []docstore.DirectoryLogEntry `json:"pending,omitempty"`
	// Reported is the highest entry number reported.
	Reported int64 `json:"reported,omitempty"`
	// Seen is what Lookup saw for each address.
	Seen map[string]SeenAddress `json:"seen,omitempty"`
}

// ErrBadWatchState is returned by ParseWatchState for bad state.
var ErrBadWatchState = errors.New("directoryclient: the saved watch state cannot be read")

// ParseWatchState reads state a host kept between runs.
func ParseWatchState(raw string) (WatchState, error) {
	if raw == "" {
		return WatchState{}, nil
	}
	var st WatchState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return WatchState{}, fmt.Errorf("%w: %v", ErrBadWatchState, err)
	}
	// json null decodes without error: reject it
	if strings.TrimSpace(raw) == "null" {
		return WatchState{}, fmt.Errorf("%w: null", ErrBadWatchState)
	}
	if st.Seq < 0 || st.Cursor < 0 || st.Reported < 0 {
		return WatchState{}, fmt.Errorf("%w: a negative position", ErrBadWatchState)
	}
	return st, nil
}

// SeenAddress is what a Lookup saw for an address.
type SeenAddress struct {
	Signed     []string `json:"signed,omitempty"`
	Enrolled   bool     `json:"enrolled,omitempty"`
	FirstOwner string   `json:"first_owner,omitempty"`
	// Accepted are the further owner keys the user accepted.
	Accepted []string `json:"accepted_owners,omitempty"`
	// Seqs are the log numbers of the keys seen.
	Seqs map[string]int64 `json:"seqs,omitempty"`
}

type MadeEntry struct {
	// Seq is the log number the server gave the entry.
	Seq int64 `json:"seq"`
	// Op is the operation.
	Op string `json:"op,omitempty"`
	// Fingerprint is the key's fingerprint for an owner operation.
	Fingerprint string `json:"fingerprint,omitempty"`
	// OwnerSig is the signature the client sent.
	OwnerSig string `json:"owner_sig,omitempty"`
}

// NoteMade records an operation of this client.
func (s *WatchState) NoteMade(m MadeEntry) { s.Made = append(s.Made, m) }

func madeMismatch(m MadeEntry, e docstore.DirectoryLogEntry) string {
	switch {
	case m.Op != "" && e.Op != m.Op:
		return "the entry the server numbered as this client's " + m.Op + " is a " + e.Op
	case m.Fingerprint != "" && docstore.DirectoryFingerprintSegment(e.Fingerprint) != docstore.DirectoryFingerprintSegment(m.Fingerprint):
		return "the entry the server numbered as this client's " + m.Op + " is about another key"
	}
	if m.OwnerSig != "" {
		got := e.OwnerSig
		if m.Op == encrypt.OpReset {
			got = e.ProofSig
		}
		if got != m.OwnerSig {
			return "the entry the server numbered as this client's " + m.Op + " does not carry the signature the client sent"
		}
	}
	return ""
}

type Foreign struct {
	Entry docstore.DirectoryLogEntry
	// Message says it in words.
	Message string
	// Voided: a void entry cancelled it.
	Voided bool
}

// WatchReport is what WatchLog found.
type WatchReport struct {
	// Head is the signed head the run was checked against.
	Head docstore.DirectoryHead
	// Entries are the new entries about the watched address.
	Entries []docstore.DirectoryLogEntry
	// Voided maps an entry number to its voiding entry.
	Voided map[int64]int64
	// LateVoided maps entries an earlier void reached.
	LateVoided map[int64]int64
	// Foreign are the new entries about the address by others.
	Foreign []Foreign
	// Violations are entries that break the owner rules.
	Violations []encrypt.AuditViolation
	// Notes are entries dated a little ahead of the clock.
	Notes []encrypt.AuditNote
	// Enrolled, OwnerKeys and ResetPending describe the owner state.
	Enrolled     bool
	OwnerKeys    []docstore.DirectoryOwnerKey
	ResetPending bool
	// AddressUnconfirmed says the run used the address without confirmation.
	AddressUnconfirmed bool
	// Witnessed is the newest entry a quorum of witnesses signed.
	Witnessed      *docstore.DirectoryCosigned
	WitnessChecked bool
	// Filtered says the server showed only the watched address.
	Filtered bool
}

// WatchOptions are the choices of WatchLog.
type WatchOptions struct {
	// Owner is the watcher's owner key, if any.
	Owner     OwnerSigner
	ResetWait time.Duration
}

// ErrLogCut is returned by WatchLog when the log was cut.
var ErrLogCut = encrypt.ErrLogTruncated

// ErrAddressMismatch is returned by WatchLog when the address differs.
var ErrAddressMismatch = errors.New("directoryclient: an entry this client made is not under the address pseudonym the server reported")

var ErrFilteredEnrolled = errors.New("directoryclient: the address is enrolled but the server shows a filtered log")

var zeroPrev = strings.Repeat("0", 64)

// ErrOwnEntryMissing is returned when the owner's own entry is missing.
var ErrOwnEntryMissing = errors.New("directoryclient: an entry this client made is not in the log")

const pageSize = 200

type logRead struct {
	entries  []docstore.DirectoryLogEntry
	keys     map[string]ed25519.PublicKey
	filtered bool
	next     int64
}

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

// WatchLog is the watcher's run for the signed-in owner.
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
	cursor := st.Cursor
	if cursor == 0 || (st.Filtered && cursor > st.Seq) {
		// a filtered cursor never passes the saved head
		cursor = st.Seq
	}
	fromSeen := func(filtered bool) int64 {
		if !filtered && !st.Filtered && st.Seq > 0 {
			return st.Seq - 1
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
	// an enrolled address gets the whole log: filtered is withholding
	if filtered && st.Enrolled {
		return rep, ErrFilteredEnrolled
	}
	chained := !filtered && !st.Filtered
	anchored := chained && st.Seq > 0
	from := cursor
	if anchored {
		from = st.Seq - 1
	}
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
			if !anchored {
				if entries[0].Seq != from+1 {
					return rep, fmt.Errorf("%w: the log was asked for from entry %d and starts at entry %d", ErrLogCut, from+1, entries[0].Seq)
				}
				if from == 0 && entries[0].Prev != zeroPrev {
					return rep, fmt.Errorf("%w: the first entry does not follow the start of the log", ErrLogCut)
				}
			}
			if _, err := encrypt.VerifyLog(entries, entries[0].Prev, entries[0].Seq-1, keys); err != nil {
				return rep, err
			}
		}
		inRead := lastSeq >= head.Seq
		if !inRead && (anchored || len(entries) > 0 || head.Seq > from) {
			return rep, fmt.Errorf("%w: the log reaches entry %d, the signed head is entry %d", ErrLogCut, lastSeq, head.Seq)
		}
		if head.Seq > 0 {
			var atHead *docstore.DirectoryLogEntry
			for i := range entries {
				if entries[i].Seq == head.Seq {
					atHead = &entries[i]
				}
			}
			if atHead == nil && !anchored && head.Seq <= from {
				e, ok, err := c.entryAt(ctx, head.Seq, keys)
				if err != nil {
					return rep, err
				}
				if ok {
					atHead = &e
				}
			}
			if atHead == nil || atHead.Hash != head.Hash {
				return rep, fmt.Errorf("%w: entry %d is not the one the signed head names", ErrLogCut, head.Seq)
			}
		}
		if st.Filtered && st.Seq > 0 {
			if len(entries) > 0 && entries[0].Seq == st.Seq+1 {
				if entries[0].Prev != st.Hash {
					return rep, fmt.Errorf("%w: entry %d does not follow the saved head (entry %d)", ErrLogCut, entries[0].Seq, st.Seq)
				}
			} else {
				var at *docstore.DirectoryLogEntry
				for i := range entries {
					if entries[i].Seq == st.Seq {
						at = &entries[i]
					}
				}
				if at == nil {
					e, ok, err := c.entryAt(ctx, st.Seq, keys)
					if err != nil {
						return rep, err
					}
					if !ok {
						return rep, fmt.Errorf("%w: entry %d, the saved head, is not in the log", ErrLogCut, st.Seq)
					}
					at = &e
				}
				if at.Hash != st.Hash {
					return rep, fmt.Errorf("%w: entry %d is not the saved head", ErrLogCut, st.Seq)
				}
			}
		}
		if chained && st.Seq > 0 && len(entries) > 0 {
			fresh = entries[1:] // the first is the one seen
		}
	}

	if c.WitnessQuorum > 0 {
		w, checked, err := c.checkWitnessed(ctx, head, entries, keys)
		if err != nil {
			return rep, err
		}
		rep.Witnessed, rep.WitnessChecked = &w, checked
	}

	known := map[string]bool{}
	var addrs []string
	for _, a := range append(append([]string(nil), st.Addresses...), append([]string{addr.Address}, addr.Previous...)...) {
		if a != "" && !known[a] {
			known[a] = true
			addrs = append(addrs, a)
		}
	}
	pinned := append([]string(nil), st.Addresses...)
	own := map[int64]bool{}
	for _, m := range st.Made {
		seq := m.Seq
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
		if !found {
			return rep, fmt.Errorf("%w: entry %d", ErrOwnEntryMissing, seq)
		}
		// a server-chosen number must not pick the pinned address
		if why := madeMismatch(m, e); why != "" {
			rep.Violations = append(rep.Violations, encrypt.AuditViolation{Seq: seq, Why: why})
			continue
		}
		if !known[e.Address] {
			return rep, fmt.Errorf("%w: entry %d is under %s", ErrAddressMismatch, seq, e.Address)
		}
		own[seq] = true
		have := false
		for _, p := range pinned {
			have = have || p == e.Address
		}
		if !have {
			pinned = append(pinned, e.Address)
		}
	}
	rep.AddressUnconfirmed = len(pinned) == 0

	var start *encrypt.AddressAudit
	if st.Enrolled || st.ResetSince != 0 || len(st.OwnerKeys) > 0 {
		start = &encrypt.AddressAudit{Enrolled: st.Enrolled, OwnerKeys: st.OwnerKeys, ResetSince: st.ResetSince}
	}
	mine := map[string]bool{}
	for _, a := range addrs {
		mine[a] = true
	}
	// a pending entry read again must be the very entry remembered
	pendingHash := map[int64]string{}
	for _, e := range st.Pending {
		pendingHash[e.Seq] = e.Hash
	}
	todo := append([]docstore.DirectoryLogEntry(nil), st.Pending...)
	var news []docstore.DirectoryLogEntry
	for _, e := range fresh {
		if h, ok := pendingHash[e.Seq]; ok {
			if h != e.Hash {
				return rep, fmt.Errorf("%w: entry %d is not the one seen before", ErrLogCut, e.Seq)
			}
			continue
		}
		news = append(news, e)
		if mine[e.Address] {
			todo = append(todo, e)
		}
	}
	auditEntries := news
	if len(st.Pending) > 0 {
		auditEntries = append(append([]docstore.DirectoryLogEntry(nil), st.Pending...), news...)
		sort.SliceStable(auditEntries, func(i, j int) bool { return auditEntries[i].Seq < auditEntries[j].Seq })
	}
	aopt := encrypt.AuditOptions{Start: start, ResetWait: opt.ResetWait, Now: c.now()}
	audit := encrypt.AuditAddress(email, addrs, auditEntries, aopt)
	rep.Enrolled, rep.OwnerKeys, rep.ResetPending = audit.Enrolled, audit.OwnerKeys, audit.ResetSince != 0
	for _, v := range audit.Violations {
		if v.Seq > st.Reported {
			rep.Violations = append(rep.Violations, v)
		}
	}
	for _, n := range audit.Notes {
		if n.Seq > st.Reported {
			rep.Notes = append(rep.Notes, n)
		}
	}
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
		if filtered && audit.Enrolled && enr.Enrolled && ownerSet(opt.Owner) {
			pub := encrypt.FormatOwnerPub(opt.Owner.Public().(ed25519.PublicKey))
			for _, k := range audit.OwnerKeys {
				if k.Pub == pub {
					return rep, ErrFilteredEnrolled
				}
			}
		}
	}
	rep.Voided = map[int64]int64{}
	rep.LateVoided = map[int64]int64{}
	for _, e := range audit.Entries {
		if e.Seq > st.Reported {
			rep.Entries = append(rep.Entries, e)
			if v, ok := audit.Voided[e.Seq]; ok {
				rep.Voided[e.Seq] = v
			}
		} else if v, ok := audit.Voided[e.Seq]; ok && v > st.Reported {
			rep.LateVoided[e.Seq] = v
		}
	}

	var ownerPub string
	if ownerSet(opt.Owner) {
		ownerPub = encrypt.FormatOwnerPub(opt.Owner.Public().(ed25519.PublicKey))
	}
	for _, e := range rep.Entries {
		_, voided := audit.Voided[e.Seq]
		if e.Op != encrypt.OpOperatorReset && (own[e.Seq] || (ownerPub != "" && signedByOwner(email, e, ownerPub))) {
			continue
		}
		rep.Foreign = append(rep.Foreign, Foreign{Entry: e, Message: foreignMessage(e), Voided: voided})
	}

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

	if filtered {
		st.Seq, st.Hash = head.Seq, head.Hash
		// never past the signed head: later entries are read again
		st.Cursor = min(max(read.next, cursor), head.Seq)
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
	for _, v := range audit.Voided {
		st.Reported = max(st.Reported, v)
	}
	return rep, nil
}

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
