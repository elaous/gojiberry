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
// (AuditAddress), which the server alone cannot make. It cannot detect a rewrite from a point
// the auditor never saw; a server that signs a whole new chain (it holds the key) shows a
// different history to different people unless auditors compare notes (the log has no
// witnesses); and a record written without an entry (by someone with write access to the store)
// is not in it, which is why records carry the number of their entry (Seq) and a lookup trusts
// only a record that has one (backed by an entry of the same address, key and operation).

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
)

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

// LogAddressesFor is LogAddressFor under every key the signer knows, the current one first.
func (d *AdapterDirectory) LogAddressesFor(email string) []string {
	return d.Signer.Pseudonyms(logAddressDomain + normalizeEmail(email))
}

const logAddressDomain = "gojiberry-directory-log-address|"

// logHash is the hash of an entry's fields other than Hash and Sig.
func logHash(e LogEntry) string {
	b, _ := json.Marshal([]any{"gojiberry-directory-log-v2", e.Seq, e.Prev, e.At, e.Op, e.Address, e.Fingerprint, e.Kid,
		e.Ref, e.OwnerPub, e.OwnerSig, e.ProofSig, e.OpTime, e.OwnerPrev})
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
		e := LogEntry{Seq: next, Prev: head.Hash, At: at.Unix(), Op: in.Op, Address: d.LogAddressFor(in.Email), Fingerprint: in.Fingerprint,
			Kid: d.Signer.KeyID(), Ref: in.Ref, OwnerPub: in.OwnerPub, OwnerSig: in.OwnerSig, ProofSig: in.ProofSig, OpTime: in.OpTime, OwnerPrev: in.OwnerPrev}
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
// that one's hash, its Hash is the hash of its fields, and its signature is by the key it names
// among keys (the pinned public keys by key ID). It returns the hash of the last entry, to be
// kept and given as prevHash next time. An auditor calls it on what ReadLog (GET /directory/log)
// returned since the last number it kept.
func VerifyLog(entries []LogEntry, prevHash string, since int64, keys map[string]ed25519.PublicKey) (lastHash string, err error) {
	if prevHash == "" {
		prevHash = zeroHash
	}
	fail := func(seq int64, why string) (string, error) {
		return "", fmt.Errorf("%w: entry %d: %s", ErrLogBroken, seq, why)
	}
	seq := since
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
		prevHash = e.Hash
	}
	return prevHash, nil
}
