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
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// The transparency log: an append-only record of every publication and revocation, so that a
// client or an auditor can notice a rewritten history.
//
// Entries are records "directory-log/e/<12-digit number>" holding {seq, prev, at, op, address,
// fingerprint, kid, hash, sig}: prev is the hash of the entry before, hash is the SHA-256 of the
// entry's other fields, and sig is the server key's signature of the hash, so removing, changing
// or reordering an entry breaks the chain from there on, and no entry can be added without the
// server's key. "directory-log/head" holds the number and hash of the last entry, as a hint for
// the next append (an entry that exists past it is found and the head catches up).
//
// What the log is for, and what it is not (see docs/encryption-and-passkeys.md, step 4b): an
// auditor who keeps the last entry it saw (number and hash) and checks the chain from there
// (VerifyLog) detects that earlier history was changed or cut, and a person who watches the
// entries for their own address (hash it with LogAddress) sees every key published or revoked for
// it, including one the server published on its own. It cannot detect a rewrite from a point the
// auditor never saw; a server that signs a whole new chain (it holds the key) shows a different
// history to different people unless auditors compare notes (the log has no witnesses); and a
// record written without an entry (by someone with write access to the store) is not in it, which
// is why records carry the number of their entry (Seq) and a Seq of 0 is worth a mark.

// zeroHash is the Prev of the first entry.
var zeroHash = strings.Repeat("0", 64)

const (
	logEntryPrefix = docstore.DirectoryLogPrefix + "e/"
	logHeadID      = docstore.DirectoryLogPrefix + "head"
)

// LogEntry is one entry of the log (docstore.DirectoryLogEntry).
type LogEntry = docstore.DirectoryLogEntry

func logEntryID(seq int64) string { return fmt.Sprintf("%s%012d", logEntryPrefix, seq) }

// LogAddress is the Address of the entries about an e-mail address: the hex SHA-256 of
// "gojiberry-directory-log|<address>" (lower-cased, trimmed). It is not keyed, so anyone who can
// read the log and guesses an address can see whether it published a key: the log is as readable
// as a lookup, which any signed-in provider user can make.
func LogAddress(email string) string {
	sum := sha256.Sum256([]byte("gojiberry-directory-log|" + normalizeEmail(email)))
	return hex.EncodeToString(sum[:])
}

// logHash is the hash of an entry's fields other than Hash and Sig.
func logHash(e LogEntry) string {
	b, _ := json.Marshal([]any{"gojiberry-directory-log-v1", e.Seq, e.Prev, e.At, e.Op, e.Address, e.Fingerprint, e.Kid})
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

// appendLog adds an entry and returns its number. Several processes on one conditional store
// append in turn: each creates the entry after the last (create-if-absent) and starts over if
// somebody else got there first. Without a conditional store only this process's appends are
// serialised, and two replicas can fork the chain, which VerifyLog then reports.
func (d *AdapterDirectory) appendLog(ctx context.Context, op, email, fingerprint string, at time.Time) (int64, error) {
	lost := 0 // rounds lost to another appender; walking forward to a lagging head is not one
	for lost < 64 {
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
		e := LogEntry{Seq: next, Prev: head.Hash, At: at.Unix(), Op: op, Address: LogAddress(email), Fingerprint: fingerprint, Kid: d.Signer.KeyID()}
		e.Hash = logHash(e)
		e.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(logSigMessage(e.Hash)))
		data, _ := json.Marshal(e)
		switch err := d.createOnly(ctx, logEntryID(next), data); {
		case err == nil:
			d.advanceHead(ctx, e, headDigest)
			return next, nil
		case errors.Is(err, docstore.ErrConflict):
			lost++
		default:
			return 0, err
		}
	}
	return 0, errors.New("encrypt: the directory log is too busy")
}

// advanceHead moves the head to e; best effort (a lost race or a failure leaves it behind, which
// the next append repairs).
func (d *AdapterDirectory) advanceHead(ctx context.Context, e LogEntry, prevDigest string) {
	data, _ := json.Marshal(logHead{Seq: e.Seq, Hash: e.Hash})
	_ = d.saveIf(ctx, logHeadID, data, prevDigest)
}

// ReadLog returns up to limit entries after number since (all of them from 0), and the public
// keys that verify them when the signer can show them (PublicKeyer; as the server states them,
// to be pinned elsewhere). It stops at the first number with no entry.
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
