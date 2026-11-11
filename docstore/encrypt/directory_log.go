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
	// OpOperatorReset removes an address's owner enrollment.
	OpOperatorReset = "operator-reset"
)

// DefaultAtSkew is how far back an entry's time may be.
const DefaultAtSkew = 5 * time.Minute

const VoidWindow = 2 * time.Minute

func voidable(op string) bool {
	switch op {
	case OpPublish, OpRevoke, OpEnroll, OpRotate, OpResetRequest, OpReset, OpResetCancel:
		return true
	}
	return false
}

// LogEntry is one entry of the log.
type LogEntry = docstore.DirectoryLogEntry

func logEntryID(seq int64) string { return fmt.Sprintf("%s%012d", logEntryPrefix, seq) }

// LogAddressFor is the Address of the entries about an address.
func (d *AdapterDirectory) LogAddressFor(email string) string {
	return d.Signer.Pseudonym(logAddressDomain + normalizeEmail(email))
}

// LogAddressesFor is LogAddressFor for every key of an address.
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

// SignLogEntry fills in an entry's Kid, Hash and Sig.
func SignLogEntry(s DirectorySigner, e LogEntry) LogEntry {
	e.Kid = s.KeyID()
	e.Hash = logHash(e)
	e.Sig = base64.StdEncoding.EncodeToString(s.Sign(logSigMessage(e.Hash)))
	return e
}

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
		return logHead{Hash: zeroHash}, docstore.Digest(doc.Data), nil
	}
	return h, docstore.Digest(doc.Data), nil
}

type logInput struct {
	Op, Email, Fingerprint string
	Ref                    int64
	OwnerPub, OwnerSig     string
	ProofSig, OwnerPrev    string
	OpTime                 int64
	OwnerV                 int
	LabelHash              string
	// Address, when set, is the entry's pseudonymous address.
	Address string
}

const maxLogRounds = 16

var logBackoffBase = 2 * time.Millisecond

func logBackoff(lost int) time.Duration {
	base := logBackoffBase << min(lost, 6)
	return base/2 + time.Duration(mrand.Int64N(int64(base/2)+1))
}

func (d *AdapterDirectory) appendLog(ctx context.Context, in logInput, at time.Time) (int64, error) {
	lost := 0 // rounds lost to another appender; catching up a lagging head is not
	for lost < maxLogRounds {
		head, headDigest, err := d.readLogHead(ctx)
		if err != nil {
			return 0, err
		}
		next := head.Seq + 1
		if doc, err := d.Store.Load(ctx, logEntryID(next)); err == nil {
			var e LogEntry
			if json.Unmarshal(doc.Data, &e) != nil || e.Seq != next || e.Hash == "" {
				return 0, fmt.Errorf("encrypt: the directory log entry %d is unreadable", next)
			}
			d.advanceHead(ctx, e, headDigest)
			continue
		} else if !errors.Is(err, docstore.ErrNotFound) {
			return 0, err
		}
		// never dated before the previous entry: replica clocks differ
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

func (d *AdapterDirectory) voidLog(ctx context.Context, email string, ref int64, fingerprint string, at time.Time) {
	// detached: the caller's context may be what failed
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := d.appendLog(vctx, logInput{Op: OpVoid, Email: email, Fingerprint: fingerprint, Ref: ref}, at); err != nil {
		log.Printf("encrypt: the directory log entry %d has no record behind it and could not be voided: %v", ref, err)
	}
}

func (d *AdapterDirectory) advanceHead(ctx context.Context, e LogEntry, prevDigest string) {
	data, _ := json.Marshal(logHead{Seq: e.Seq, Hash: e.Hash})
	_ = d.saveIf(ctx, logHeadID, data, prevDigest)
}

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

// Head is the signed head of the log.
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

// VerifyHead checks a head's signature.
func VerifyHead(h docstore.DirectoryHead, keys map[string]ed25519.PublicKey) error {
	pub, ok := keys[h.Kid]
	sig, err := base64.StdEncoding.DecodeString(h.Sig)
	if !ok || err != nil || !ed25519.Verify(pub, logHeadMessage(h.Seq, h.Hash, h.At), sig) {
		return fmt.Errorf("%w: the head's signature does not verify with key %s", ErrLogBroken, h.Kid)
	}
	return nil
}

// ErrLogTruncated is returned when the log shrank.
var ErrLogTruncated = errors.New("encrypt: the directory log is shorter than, or differs from, what was seen before")

// CheckHead compares a head with the log.
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

// VerifyLogHead checks that the log stands on a signed head.
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

// ReadLog returns up to limit entries after number since.
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

// ErrLogBroken is returned by VerifyLog when the log is damaged.
var ErrLogBroken = errors.New("encrypt: the directory log does not verify")

// VerifyLog checks a run of log entries.
func VerifyLog(entries []LogEntry, prevHash string, since int64, keys map[string]ed25519.PublicKey) (lastHash string, err error) {
	return VerifyLogWith(entries, prevHash, since, keys, LogChecks{})
}

// LogChecks tunes VerifyLogWith.
type LogChecks struct {
	// AtSkew is how far back an entry's time may be.
	AtSkew time.Duration
	// PrevAt is the time of the entry the run follows.
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

// VerifyEntry checks one entry on its own.
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

// LogFor is ReadLog for the entries of some addresses.
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
