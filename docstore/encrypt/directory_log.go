package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// The transparency log: an append-only record of every publication, revocation and owner
// enrollment, so that a client or an auditor can notice a rewritten history.
//
// Entries are records "directory-log/e/<12-digit number>" holding {seq, prev, at, op, address,
// fingerprint, kid, ref, owner_*, hash, sig}: prev is the hash of the entry before, hash is the
// SHA-256 of the entry's other fields, and sig is the server key's signature of the hash, so
// removing, changing or reordering an entry breaks the chain from there on, and no entry can be
// added without the server's key. "directory-log/head" holds the number and hash of the last
// entry, as a hint for the next append (an entry that exists past it is found and the head
// catches up).
//
// What the log is for, and what it is not (see docs/encryption-and-passkeys.md, step 4b): an
// auditor who keeps the last entry it saw (number and hash) and checks the chain from there
// (VerifyLog, VerifyLogHead) detects that earlier history was changed or cut, and a person who
// watches the entries for their own address (the keyed pseudonym LogAddressFor gives the owner)
// sees every key published or revoked for it, including one the server published on its own,
// unless the address has an owner key: then every entry for it must carry the owner's signature
// (AuditAddress), which the server alone cannot make. Entry times (At) are written by the server
// but must not go backwards along the chain beyond a small skew (VerifyLog), so the waits the
// rules depend on (the 24 hours of a reset) are measured by chain order and cannot be shortened by
// back-dating. It cannot detect a rewrite from a point the auditor never saw; a server that signs
// a whole new chain (it holds the key) shows a different history to different people unless
// auditors compare notes (witnesses narrow that: see directory_witness.go); and a record written without an entry (by
// someone with write access to the store) is not in it. What a lookup demands of the log: a
// record's Seq names its entry, and a lookup requires that entry (same address, key and
// operation, signed by a directory key) only where it matters: for a record signed by a retired
// key, and for an address that has an owner key (publishedKey). For an ordinary address a record
// with a valid signature of the current key is Verified without an entry (a record the publish
// route wrote has one, and a watcher of the address sees it, but the lookup does not check).

// zeroHash is the Prev of the first entry.
var zeroHash = strings.Repeat("0", 64)

const (
	logEntryPrefix = docstore.DirectoryLogPrefix + "e/"
	logHeadID      = docstore.DirectoryLogPrefix + "head"
)

// Operations of a log entry.
const (
	OpPublish      = "publish"
	OpRevoke       = "revoke"
	OpEnroll       = "enroll"
	OpRotate       = "rotate"
	OpResetRequest = "reset-request"
	OpReset        = "reset"
	OpResetCancel  = "reset-cancel"
	OpVoid         = "void"
	// OpOperatorReset removes the owner enrollment of an address by the operator's hand
	// (AdapterDirectory.OperatorResetEnrollment). It is signed by the directory key like every
	// entry, not by the owner, so it cannot be hidden: AuditAddress accepts it as legitimate and
	// lists it (AddressAudit.OperatorResets), and a watcher is told ("Foreign").
	OpOperatorReset = "operator-reset"
)

// DefaultAtSkew is how far back an entry's time may be from the latest time earlier in the chain:
// VerifyLog and AuditAddress refuse a chain whose times go back by more (the server writes them,
// so back-dating a reset request, say, to shorten its wait would otherwise be free).
const DefaultAtSkew = 5 * time.Minute

// VoidWindow is how long after the entry it cancels a "void" entry may be written: a void is the
// compensation of a failed write, made in the same round (seconds). AuditAddress accepts no later
// one, for any operation: it is a violation and cancels nothing. (A crash that leaves an owner-state
// entry without its record is not mended by a void but by completing the record from the log:
// see directory_recover.go.)
const VoidWindow = 2 * time.Minute

// voidable says an entry of this kind may be cancelled by a void: a publication, revocation or
// owner-state change whose record may fail to be written. A void of a void or of an operator
// reset is not one.
func voidable(op string) bool {
	switch op {
	case OpPublish, OpRevoke, OpEnroll, OpRotate, OpResetRequest, OpReset, OpResetCancel:
		return true
	}
	return false
}

// LogEntry is one entry of the log (docstore.DirectoryLogEntry).
type LogEntry = docstore.DirectoryLogEntry

func logEntryID(seq int64) string { return fmt.Sprintf("%s%012d", logEntryPrefix, seq) }

// LogAddressFor is the Address of the entries about an e-mail address: a keyed hash (HMAC under
// the directory's key material, domain-separated from the record names) of the lower-cased,
// trimmed address. Only the directory can compute it, so a guessed address cannot be tested
// against the log; the signed-in owner is told theirs (GET /directory/log/address) to find the
// entries about their own address. It is the current key's; LogAddressesFor adds the retired
// keys' (entries made before a rotation).
func (d *AdapterDirectory) LogAddressFor(email string) string {
	return d.Signer.Pseudonym(logAddressDomain + normalizeEmail(email))
}

// LogAddressesFor is LogAddressFor under every key the signer knows, the current one first, then
// the pseudonyms recorded for the address under keys since removed from the retired list (aliases,
// directory_alias.go).
func (d *AdapterDirectory) LogAddressesFor(email string) []string {
	all := d.Signer.Pseudonyms(logAddressDomain + normalizeEmail(email))
	d.aliasMu.RLock()
	defer d.aliasMu.RUnlock()
	for _, a := range d.aliases[all[0]] {
		if !slices.Contains(all, a) {
			all = append(all, a)
		}
	}
	return all
}

const logAddressDomain = "gojiberry-directory-log-address|"

// SignLogEntry fills in an entry's Kid, Hash and Sig with the signer's current key: what the
// directory does when it appends one, for tools that build or mirror a log (and for tests of an
// auditor, which need a log with an entry the directory would not have written).
func SignLogEntry(s DirectorySigner, e LogEntry) LogEntry {
	e.Kid = s.KeyID()
	e.Hash = logHash(e)
	e.Sig = base64.StdEncoding.EncodeToString(s.Sign(logSigMessage(e.Hash)))
	return e
}

// logHash is the hash of an entry's fields other than Hash and Sig.
func logHash(e LogEntry) string {
	fields := []any{"gojiberry-directory-log-v2", e.Seq, e.Prev, e.At, e.Op, e.Address, e.Fingerprint, e.Kid,
		e.Ref, e.OwnerPub, e.OwnerSig, e.ProofSig, e.OpTime, e.OwnerPrev}
	if e.OwnerV != 0 || e.LabelHash != "" { // entries made before the label was signed keep their hash
		fields[0] = "gojiberry-directory-log-v3"
		fields = append(fields, e.OwnerV, e.LabelHash)
	}
	b, _ := json.Marshal(fields)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func logSigMessage(hash string) []byte { return []byte("gojiberry-directory-log-sig-v1|" + hash) }

type logHead struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

func (d *AdapterDirectory) readLogHead(ctx context.Context) (logHead, string, error) {
	doc, err := d.Store.Load(ctx, logHeadID)
	if errors.Is(err, docstore.ErrNotFound) {
		return logHead{Hash: zeroHash}, "", nil
	}
	if err != nil {
		return logHead{}, "", err
	}
	var h logHead
	if json.Unmarshal(doc.Data, &h) != nil || h.Seq < 0 || h.Hash == "" {
		return logHead{Hash: zeroHash}, docstore.Digest(doc.Data), nil // unreadable: found again by walking
	}
	return h, docstore.Digest(doc.Data), nil
}

// logInput is what an entry says besides its place in the chain.
type logInput struct {
	Op, Email, Fingerprint string
	Ref                    int64
	OwnerPub, OwnerSig     string
	ProofSig, OwnerPrev    string
	OpTime                 int64
	OwnerV                 int
	LabelHash              string
	// Address, when set, is the entry's address pseudonym itself (for an operator reset of an
	// address whose e-mail is not known, only its pseudonym); Email is then ignored.
	Address string
}

// maxLogRounds bounds the rounds an append loses to other appenders before it gives up
// (ErrDirectoryBusy, answered 503). logBackoff is the wait before the next round: it grows with
// the rounds lost and carries jitter, so appenders that collided do not collide again in step.
const maxLogRounds = 16

var logBackoffBase = 2 * time.Millisecond

func logBackoff(lost int) time.Duration {
	base := logBackoffBase << min(lost, 6)
	return base/2 + time.Duration(mrand.Int64N(int64(base/2)+1))
}

// appendLog adds an entry and returns its number. Several processes on one conditional store
// append in turn: each creates the entry after the last (create-if-absent), and a loser waits a
// little (backoff, jitter) and starts over; after maxLogRounds lost rounds it gives up with
// docstore.ErrDirectoryBusy. Without a conditional store only this process's appends are
// serialised, and two replicas can fork the chain, which VerifyLog then reports (and
// AdapterDirectory.CheckStore refuses to run replicas on such a store).
func (d *AdapterDirectory) appendLog(ctx context.Context, in logInput, at time.Time) (int64, error) {
	lost := 0 // rounds lost to another appender; walking forward to a lagging head is not one
	for lost < maxLogRounds {
		head, headDigest, err := d.readLogHead(ctx)
		if err != nil {
			return 0, err
		}
		next := head.Seq + 1
		if doc, err := d.Store.Load(ctx, logEntryID(next)); err == nil {
			// The head lags (a crash between the entry and the head): catch it up and go on.
			var e LogEntry
			if json.Unmarshal(doc.Data, &e) != nil || e.Seq != next || e.Hash == "" {
				return 0, fmt.Errorf("encrypt: the directory log entry %d is unreadable", next)
			}
			d.advanceHead(ctx, e, headDigest)
			continue
		} else if !errors.Is(err, docstore.ErrNotFound) {
			return 0, err
		}
		// An entry is never dated before the one it follows (a replica whose clock is behind
		// would otherwise write a chain that VerifyLog refuses).
		when := at.Unix()
		if head.Seq > 0 {
			pe, found, err := d.readEntry(ctx, head.Seq)
			if err != nil {
				return 0, err
			}
			if found && pe.At > when {
				when = pe.At
			}
		}
		addr := in.Address
		if addr == "" {
			addr = d.LogAddressFor(in.Email)
		}
		e := LogEntry{Seq: next, Prev: head.Hash, At: when, Op: in.Op, Address: addr, Fingerprint: in.Fingerprint,
			Kid: d.Signer.KeyID(), Ref: in.Ref, OwnerPub: in.OwnerPub, OwnerSig: in.OwnerSig, ProofSig: in.ProofSig, OpTime: in.OpTime, OwnerPrev: in.OwnerPrev,
			OwnerV: in.OwnerV, LabelHash: in.LabelHash}
		e.Hash = logHash(e)
		e.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(logSigMessage(e.Hash)))
		data, _ := json.Marshal(e)
		switch err := d.createOnly(ctx, logEntryID(next), data); {
		case err == nil:
			d.advanceHead(ctx, e, headDigest)
			return next, nil
		case errors.Is(err, docstore.ErrConflict):
			lost++
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(logBackoff(lost)):
			}
		default:
			return 0, err
		}
	}
	return 0, docstore.ErrDirectoryBusy
}

// voidLog appends the compensating entry of an entry whose record could not be written: the log
// then never claims a publication (or revocation, enrollment) that did not happen. It is best
// effort: if even that fails, the error is logged, and the entry stands with no record behind it,
// which an owner who watches the log sees as an operation they did not make.
func (d *AdapterDirectory) voidLog(ctx context.Context, email string, ref int64, fingerprint string, at time.Time) {
	// The caller's context may be what failed; the compensation must not be lost to it.
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := d.appendLog(vctx, logInput{Op: OpVoid, Email: email, Fingerprint: fingerprint, Ref: ref}, at); err != nil {
		log.Printf("encrypt: the directory log entry %d has no record behind it and could not be voided: %v", ref, err)
	}
}

// advanceHead moves the head to e; best effort (a lost race or a failure leaves it behind, which
// the next append repairs).
func (d *AdapterDirectory) advanceHead(ctx context.Context, e LogEntry, prevDigest string) {
	data, _ := json.Marshal(logHead{Seq: e.Seq, Hash: e.Hash})
	_ = d.saveIf(ctx, logHeadID, data, prevDigest)
}

// latest walks from the head hint to the real last entry and returns its number and hash.
func (d *AdapterDirectory) latest(ctx context.Context) (logHead, error) {
	head, _, err := d.readLogHead(ctx)
	if err != nil {
		return logHead{}, err
	}
	for {
		doc, err := d.Store.Load(ctx, logEntryID(head.Seq+1))
		if errors.Is(err, docstore.ErrNotFound) {
			return head, nil
		}
		if err != nil {
			return logHead{}, err
		}
		var e LogEntry
		if json.Unmarshal(doc.Data, &e) != nil || e.Seq != head.Seq+1 || e.Hash == "" {
			return logHead{}, fmt.Errorf("encrypt: the directory log entry %d is unreadable", head.Seq+1)
		}
		head = logHead{Seq: e.Seq, Hash: e.Hash}
	}
}

func logHeadMessage(seq int64, hash string, at int64) []byte {
	return []byte(fmt.Sprintf("gojiberry-directory-log-head-v1|%d|%s|%d", seq, hash, at))
}

// Head is the signed head of the log: the number and hash of the last entry, the time, and the
// server key's signature of them. A client that keeps the head it saw checks a later one with
// CheckHead, and the entries since with VerifyLogHead, so it notices a log that was cut back or
// rewritten.
func (d *AdapterDirectory) Head(ctx context.Context) (docstore.DirectoryHead, error) {
	if d.Signer == nil {
		return docstore.DirectoryHead{}, ErrDirectoryOff
	}
	h, err := d.latest(ctx)
	if err != nil {
		return docstore.DirectoryHead{}, err
	}
	at := d.now().Unix()
	return docstore.DirectoryHead{Seq: h.Seq, Hash: h.Hash, At: at, Kid: d.Signer.KeyID(),
		Sig: base64.StdEncoding.EncodeToString(d.Signer.Sign(logHeadMessage(h.Seq, h.Hash, at)))}, nil
}

// VerifyHead checks the signature of a head against the pinned keys (by key ID).
func VerifyHead(h docstore.DirectoryHead, keys map[string]ed25519.PublicKey) error {
	pub, ok := keys[h.Kid]
	sig, err := base64.StdEncoding.DecodeString(h.Sig)
	if !ok || err != nil || !ed25519.Verify(pub, logHeadMessage(h.Seq, h.Hash, h.At), sig) {
		return fmt.Errorf("%w: the head's signature does not verify with key %s", ErrLogBroken, h.Kid)
	}
	return nil
}

// ErrLogTruncated is returned by CheckHead and VerifyLogHead when the log is shorter than what
// the client saw, or has a different entry where the client saw one: it was cut back or
// rewritten.
var ErrLogTruncated = errors.New("encrypt: the directory log is shorter than, or differs from, what was seen before")

// CheckHead compares a head (signed: VerifyHead it first) with the one the client saw: a head
// with a lower number is a truncation, one with the same number and another hash a rewrite (both
// ErrLogTruncated). prevSeq 0 means nothing was seen.
func CheckHead(prevSeq int64, prevHash string, h docstore.DirectoryHead) error {
	switch {
	case prevSeq == 0:
		return nil
	case h.Seq < prevSeq:
		return fmt.Errorf("%w: the head is entry %d, entry %d was seen", ErrLogTruncated, h.Seq, prevSeq)
	case h.Seq == prevSeq && h.Hash != prevHash:
		return fmt.Errorf("%w: entry %d has another hash than the one seen", ErrLogTruncated, h.Seq)
	}
	return nil
}

// VerifyLogHead is the client's check that the log still contains what it saw. entries is the
// log read from the entry the client saw last (GET /directory/log?since=<prevSeenSeq-1>): its
// first entry must be number prevSeenSeq with hash prevSeenHash, and the entries must chain from
// there (their number, Prev and Hash; VerifyLog checks the signatures). An empty answer, a first
// entry with another number or hash, or a broken chain is ErrLogTruncated or ErrLogBroken: the
// server cut the log back or rewrote it. With prevSeenSeq 0 (nothing seen) it accepts anything
// that chains from the start.
func VerifyLogHead(prevSeenSeq int64, prevSeenHash string, entries []LogEntry) error {
	if prevSeenSeq <= 0 {
		return nil
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w: no entry %d", ErrLogTruncated, prevSeenSeq)
	}
	first := entries[0]
	if first.Seq != prevSeenSeq || first.Hash != prevSeenHash || logHash(first) != first.Hash {
		return fmt.Errorf("%w: entry %d is not the one seen", ErrLogTruncated, prevSeenSeq)
	}
	prev := first
	for _, e := range entries[1:] {
		if e.Seq != prev.Seq+1 || e.Prev != prev.Hash || logHash(e) != e.Hash {
			return fmt.Errorf("%w: entry %d does not follow entry %d", ErrLogBroken, e.Seq, prev.Seq)
		}
		prev = e
	}
	return nil
}

// ReadLog returns up to limit entries after number since (all of them from 0; the library caps
// one call at 1000, the HTTP route at 200: ask again from the last number for more), and the
// public keys that verify them when the signer can show them (PublicKeyer; as the server states
// them, to be pinned elsewhere). It stops at the first number with no entry.
func (d *AdapterDirectory) ReadLog(ctx context.Context, since int64, limit int) (docstore.DirectoryLog, error) {
	if d.Signer == nil {
		return docstore.DirectoryLog{}, ErrDirectoryOff
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	out := docstore.DirectoryLog{}
	for seq := since + 1; len(out.Entries) < limit; seq++ {
		doc, err := d.Store.Load(ctx, logEntryID(seq))
		if errors.Is(err, docstore.ErrNotFound) {
			break
		}
		if err != nil {
			return docstore.DirectoryLog{}, err
		}
		var e LogEntry
		if json.Unmarshal(doc.Data, &e) != nil {
			return docstore.DirectoryLog{}, fmt.Errorf("encrypt: the directory log entry %d is unreadable", seq)
		}
		out.Entries = append(out.Entries, e)
	}
	if pk, ok := d.Signer.(PublicKeyer); ok {
		out.Keys = map[string]string{}
		for kid, k := range pk.PublicKeys() {
			out.Keys[kid] = base64.StdEncoding.EncodeToString(k)
		}
	}
	return out, nil
}

// readEntry loads one log entry (found false when there is none).
func (d *AdapterDirectory) readEntry(ctx context.Context, seq int64) (e LogEntry, found bool, err error) {
	if seq <= 0 {
		return LogEntry{}, false, nil
	}
	doc, err := d.Store.Load(ctx, logEntryID(seq))
	if errors.Is(err, docstore.ErrNotFound) {
		return LogEntry{}, false, nil
	}
	if err != nil {
		return LogEntry{}, false, err
	}
	if json.Unmarshal(doc.Data, &e) != nil || e.Seq != seq {
		return LogEntry{}, false, nil
	}
	return e, true, nil
}

// ErrLogBroken is returned by VerifyLog; the error says where.
var ErrLogBroken = errors.New("encrypt: the directory log does not verify")

// VerifyLog checks entries, a run of the log that follows the entry whose hash is prevHash
// ("" for the start of the log): each entry's number is the one after the one before, its Prev is
// that one's hash, its Hash is the hash of its fields, its signature is by the key it names
// among keys (the pinned public keys by key ID), and its time (At) is not more than
// DefaultAtSkew before the latest time earlier in the run (the server writes the times: a chain
// whose times go back is one that was back-dated, which would shorten the waits the audit
// measures). It returns the hash of the last entry, to be kept and given as prevHash next time.
// An auditor calls it on what ReadLog (GET /directory/log) returned since the last number it kept.
func VerifyLog(entries []LogEntry, prevHash string, since int64, keys map[string]ed25519.PublicKey) (lastHash string, err error) {
	return VerifyLogWith(entries, prevHash, since, keys, LogChecks{})
}

// LogChecks tunes VerifyLogWith.
type LogChecks struct {
	// AtSkew is how far back an entry's time may be from the latest time before it (DefaultAtSkew
	// when 0).
	AtSkew time.Duration
	// PrevAt is the time of the entry the run follows, when the caller knows it (0: unknown, the
	// first entry sets the baseline).
	PrevAt int64
}

// VerifyLogWith is VerifyLog with options.
func VerifyLogWith(entries []LogEntry, prevHash string, since int64, keys map[string]ed25519.PublicKey, chk LogChecks) (lastHash string, err error) {
	if prevHash == "" {
		prevHash = zeroHash
	}
	skew := chk.AtSkew
	if skew <= 0 {
		skew = DefaultAtSkew
	}
	fail := func(seq int64, why string) (string, error) {
		return "", fmt.Errorf("%w: entry %d: %s", ErrLogBroken, seq, why)
	}
	seq := since
	maxAt := chk.PrevAt
	for _, e := range entries {
		seq++
		switch {
		case e.Seq != seq:
			return fail(e.Seq, fmt.Sprintf("expected number %d", seq))
		case e.Prev != prevHash:
			return fail(e.Seq, "it does not follow the entry before it")
		case e.Hash != logHash(e):
			return fail(e.Seq, "its hash is not the hash of its fields")
		}
		pub, ok := keys[e.Kid]
		sig, decErr := base64.StdEncoding.DecodeString(e.Sig)
		if !ok || decErr != nil || !ed25519.Verify(pub, logSigMessage(e.Hash), sig) {
			return fail(e.Seq, "its signature does not verify with key "+e.Kid)
		}
		if maxAt > 0 && e.At < maxAt-int64(skew/time.Second) {
			return fail(e.Seq, fmt.Sprintf("it is dated %d seconds before an earlier entry: the times of the chain must not go back", maxAt-e.At))
		}
		maxAt = max(maxAt, e.At)
		prevHash = e.Hash
	}
	return prevHash, nil
}

// VerifyEntry checks one entry on its own: its hash is the hash of its fields and its signature
// is by the key it names among keys. It does not check the chain (the entries around it): the
// entries of a filtered log read (the caller may see only their own address's entries) are
// checked with it.
func VerifyEntry(e LogEntry, keys map[string]ed25519.PublicKey) error {
	pub, ok := keys[e.Kid]
	sig, decErr := base64.StdEncoding.DecodeString(e.Sig)
	switch {
	case e.Hash != logHash(e):
		return fmt.Errorf("%w: entry %d: its hash is not the hash of its fields", ErrLogBroken, e.Seq)
	case !ok || decErr != nil || !ed25519.Verify(pub, logSigMessage(e.Hash), sig):
		return fmt.Errorf("%w: entry %d: its signature does not verify with key %s", ErrLogBroken, e.Seq, e.Kid)
	}
	return nil
}

// LogFor is ReadLog for the entries of some addresses (their pseudonyms): it scans at most scan
// entries after since, returns up to limit of those that are about one of addresses, and says
// where it stopped (Next) and whether there is more (More). docstore.DirectoryLogFilterer.
func (d *AdapterDirectory) LogFor(ctx context.Context, addresses []string, since int64, scan, limit int) (docstore.DirectoryLog, error) {
	if d.Signer == nil {
		return docstore.DirectoryLog{}, ErrDirectoryOff
	}
	mine := map[string]bool{}
	for _, a := range addresses {
		mine[a] = true
	}
	out := docstore.DirectoryLog{Entries: []docstore.DirectoryLogEntry{}, Next: since}
	for n := 0; n < scan && len(out.Entries) < limit; n++ {
		seq := out.Next + 1
		doc, err := d.Store.Load(ctx, logEntryID(seq))
		if errors.Is(err, docstore.ErrNotFound) {
			break
		}
		if err != nil {
			return docstore.DirectoryLog{}, err
		}
		var e LogEntry
		if json.Unmarshal(doc.Data, &e) != nil {
			return docstore.DirectoryLog{}, fmt.Errorf("encrypt: the directory log entry %d is unreadable", seq)
		}
		out.Next = seq
		if mine[e.Address] {
			out.Entries = append(out.Entries, e)
		}
	}
	// More: is there an entry after where the scan stopped?
	if _, err := d.Store.Load(ctx, logEntryID(out.Next+1)); err == nil {
		out.More = true
	}
	if pk, ok := d.Signer.(PublicKeyer); ok {
		out.Keys = map[string]string{}
		for kid, k := range pk.PublicKeys() {
			out.Keys[kid] = base64.StdEncoding.EncodeToString(k)
		}
	}
	return out, nil
}
