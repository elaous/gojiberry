package encrypt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// ErrNotPublishable is returned by AdapterDirectory.Publish for a user whose e-mail cannot be
// vouched for: no e-mail, or a session that did not come from an identity provider (a passkey
// session carries no e-mail: the address typed at registration is a label nothing verified).
var ErrNotPublishable = errors.New("encrypt: only a signed-in account with an explicitly verified e-mail can publish a key")

const directoryPrefix = "directory/"

// popPrefix names the used challenges ("directory/pop/<id>"): a record that exists means the
// challenge was answered once. It cannot collide with an address's prefix (a 64-character hex
// hash). Sweep deletes them after they expire.
const popPrefix = directoryPrefix + "pop/"

// passkeySessionPrefix starts the ID of a user a passkey sign-in made (auth/passkey).
const passkeySessionPrefix = "passkey:"

// DefaultRevokeWindow is how long a revoked key is remembered (RevokeWindow when 0): it cannot be
// published again, and shows as revoked, for this long.
const DefaultRevokeWindow = 24 * time.Hour

// AdapterDirectory is a KeyDirectory over a docstore.Adapter. A key is a record
// "directory/<keyed hash of the e-mail>/<fingerprint>" holding the public key, who published it
// (the user's ID and provider) and the address, signed by the server's DirectorySigner.
// Publish writes one for the signed-in user; Revoke replaces it with a signed tombstone; Lookup
// reads them.
//
// What this guarantees (the full table, and what it does not guarantee, is in
// docs/encryption-and-passkeys.md, step 4b). Lookup marks a key Verified only when its record
// carries a valid signature of one of the server's directory keys for exactly that record (its
// ID, key ID, key, publisher, provider, address, time, proof and attestation), from a provider
// user (not a passkey one), for the address asked. Anyone who can write the store but does not
// hold the key cannot make a record Verified, and cannot even compute the name of an address's
// records, because the name is an HMAC of the address under that key. The server
// (docstore.Server) also keeps "directory/" out of its document API, so its own sessions cannot
// write there at all; the signature is what holds when the store is reachable some other way.
// With proof of possession a record also says Held (the publisher decrypted a challenge sent to
// the key), and an ssh-ed25519 key's own attestation makes it Attested. What is not guaranteed:
// a signature proves the server published the record for that account, not that the key is
// safe; whoever holds the directory key (the server) can publish a key *it* holds under any
// address (it cannot make a key it does not hold Held or Attested); and the verified e-mail is
// only as good as the identity provider's claim.
//
// With no Signer the directory is off: Publish returns ErrDirectoryOff and Lookup finds nothing.
// Lookup lists only the address's own prefix (docstore.PrefixLister when the store has it).
type AdapterDirectory struct {
	// Store holds the records, the transparency log and the used challenges. Use the raw store,
	// not an encrypting wrapper's view of it (DefaultSkip leaves "directory/" and
	// "directory-log/" readable in either case). Several replicas on one store need a store that
	// is a docstore.ConditionalAdapter, or the key cap and the log can be raced (see Publish).
	Store  docstore.Adapter
	Signer DirectorySigner
	// MaxKeys is how many keys one address may have published, counting keys revoked within
	// RevokeWindow (20 when 0).
	MaxKeys int
	// RequireProof makes Publish refuse a key whose publisher did not answer a challenge
	// (ErrProofRequired). Off by default: a proof is checked when it is sent, and recorded
	// (Held), but not demanded.
	RequireProof bool
	// RequireEnrollment makes Publish refuse (ErrEnrollmentRequired) a key for an address that has
	// not enrolled an owner key. Enrolling itself is still allowed on the provider session, as ever;
	// from then on every publication carries the owner's signature, which is what closes the gap an
	// unenrolled address leaves (a server that holds the directory key can number entries for it as
	// it likes). Off by default: it makes a first publication a two-step affair.
	RequireEnrollment bool
	// RevokeWindow is how long a revoked key stays remembered: during it the key cannot be
	// published again (ErrRevoked), so a replayed request cannot silently bring it back, and a
	// lookup that asks for revoked keys shows it. DefaultRevokeWindow when 0.
	RevokeWindow time.Duration
	// ResetWait is how long a provider-session reset of an address's owner key waits between its
	// request and its taking effect (DefaultResetWait, 24 hours, when 0). Do not shorten it in a
	// deployment: auditors hold the server to 24 hours (AuditOptions.ResetWait).
	ResetWait time.Duration
	// ReconcileGrace is how old an owner-state log entry without its enrollment record must be
	// before it is taken to be left behind by a crash and the record is completed from the log
	// (2 minutes when 0; see directory_recover.go). Younger, the operation is refused with
	// ErrDirectoryBusy: the write may be in flight on another replica.
	ReconcileGrace time.Duration
	// UnmappedCap is how many unmapped addresses UnmappedAddresses lists (DefaultUnmappedCap when
	// not positive); the count is always whole.
	UnmappedCap int
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	// report is the census of the last Startup, for DirectoryHealth.
	report atomic.Pointer[docstore.DirectoryHealth]
	// locks serialise Publish per address within this process (striped by the address's hash).
	locks [64]sync.Mutex
	// casMu serialises the transparency log's appends and the challenge claims in this process.
	casMu sync.Mutex
	// idx is the owner-state entries of the log by address (reconciliation, directory_recover.go).
	idxMu sync.Mutex
	idx   logIndex
	// unmappedWarned is the count the last warning named (unmappedMu), so a sweep that finds the
	// same addresses again does not log again.
	unmappedMu     sync.Mutex
	unmappedWarned int
	// aliases are the log pseudonyms of an address under directory keys since removed from the
	// retired list, by the address's current pseudonym (directory_alias.go).
	aliasMu sync.RWMutex
	aliases map[string][]string
	// id is this process's registration name (Writers).
	idMu sync.Mutex
	id   string
}

func (d *AdapterDirectory) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *AdapterDirectory) window() time.Duration {
	if d.RevokeWindow > 0 {
		return d.RevokeWindow
	}
	return DefaultRevokeWindow
}

// lockAddress takes the lock for an address and returns its unlock. The MaxKeys cap is a
// list-then-save, so two concurrent publishes for one address could both pass it; this makes
// them take turns in one process. It does not help across replicas: with several server
// processes on one store, put a gateway in front that routes (or serialises) publishes per
// address, or use a store with a conditional write.
func (d *AdapterDirectory) lockAddress(email string) func() {
	h := fnv.New32a()
	h.Write([]byte(normalizeEmail(email)))
	mu := &d.locks[h.Sum32()%uint32(len(d.locks))]
	mu.Lock()
	return mu.Unlock
}

// directoryRecord is the stored form. Sig signs message(id) (the other fields and the record's
// ID), base64. A record with Revoked set is a tombstone: the key was revoked at that time.
type directoryRecord struct {
	V         int    `json:"v"`
	Kid       string `json:"kid,omitempty"` // the server key that signed it (V 3)
	Key       string `json:"key"`
	Label     string `json:"label,omitempty"`
	Publisher string `json:"publisher"` // the user's ID, "{provider}:{subject}"
	Provider  string `json:"provider"`
	Email     string `json:"email"` // the address, as the publisher's provider verified it
	At        int64  `json:"at"`    // unix seconds
	// Held: a challenge sent to the key was answered. Attest and AttestAt: the key's own
	// signature over the binding (base64) and its time. Seq: the log entry of the publication.
	// Revoked: unix seconds the key was revoked (a tombstone).
	Held     bool   `json:"held,omitempty"`
	Attest   string `json:"attest,omitempty"`
	AttestAt int64  `json:"attest_at,omitempty"`
	Seq      int64  `json:"seq,omitempty"`
	Revoked  int64  `json:"revoked,omitempty"`
	// Owner*: the owner's signature of this publication (or revocation), its time and the log
	// position it names (V 4); the same values are in the log entry Seq.
	OwnerSig  string `json:"owner_sig,omitempty"`
	OwnerTime int64  `json:"owner_time,omitempty"`
	OwnerPrev string `json:"owner_prev,omitempty"`
	// OwnerV is the version of the owner's message (2: it covers the label) (V 5).
	OwnerV int    `json:"owner_v,omitempty"`
	Sig    string `json:"sig"`
}

func (rec directoryRecord) message(id string) []byte {
	if rec.V == 2 {
		b, _ := json.Marshal([]any{"gojiberry-directory-v1", id, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At})
		return b
	}
	if rec.V == 3 {
		b, _ := json.Marshal([]any{"gojiberry-directory-v3", id, rec.Kid, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At, rec.Held, rec.Attest, rec.AttestAt, rec.Seq, rec.Revoked})
		return b
	}
	if rec.V == 4 {
		b, _ := json.Marshal([]any{"gojiberry-directory-v4", id, rec.Kid, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At, rec.Held, rec.Attest, rec.AttestAt, rec.Seq, rec.Revoked, rec.OwnerSig, rec.OwnerTime, rec.OwnerPrev})
		return b
	}
	b, _ := json.Marshal([]any{"gojiberry-directory-v5", id, rec.Kid, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At, rec.Held, rec.Attest, rec.AttestAt, rec.Seq, rec.Revoked, rec.OwnerSig, rec.OwnerTime, rec.OwnerPrev, rec.OwnerV})
	return b
}

// sign makes rec a current record: the current key's ID and signature for id.
func (d *AdapterDirectory) sign(rec directoryRecord, id string) directoryRecord {
	rec.V, rec.Kid = 5, d.Signer.KeyID()
	rec.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(rec.message(id)))
	return rec
}

// names are the prefixes under which an address's records may be: under the current key's
// hash, then under each retired key's.
func (d *AdapterDirectory) names(email string) []string {
	hs := d.Signer.Pseudonyms("email:" + normalizeEmail(email))
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = directoryPrefix + h + "/"
	}
	return out
}

func (d *AdapterDirectory) emailHash(email string) string {
	return d.Signer.Pseudonym("email:" + normalizeEmail(email))
}

// fingerprintSegment makes a fingerprint ("SHA256:ab+/c") usable as the last part of an ID.
func fingerprintSegment(fp string) string { return docstore.DirectoryFingerprintSegment(fp) }

func (d *AdapterDirectory) addressPrefix(email string) string {
	return directoryPrefix + d.emailHash(email) + "/"
}

func (d *AdapterDirectory) directoryID(email, fingerprint string) string {
	return d.addressPrefix(email) + fingerprintSegment(fingerprint)
}

// parsePublic reads a public key by its shape.
func parsePublic(text string) (Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return ParseAgeRecipient(text)
	}
	return ParseSSHRecipient(text)
}

// cleanLabel keeps a label short and printable: control characters and invisible formatting
// ones (bidi overrides and isolates U+202A-202E and U+2066-2069, zero-width and mark characters
// U+200B-200F, U+FEFF and every other Cf, the line and paragraph separators Zl and Zp) are
// dropped, so a label cannot reorder or hide the text around it; 80 runes at most.
func cleanLabel(s string) string {
	var b []rune
	for _, c := range strings.TrimSpace(s) {
		if c < 0x20 || c == 0x7f || unicode.IsControl(c) || unicode.Is(unicode.Cf, c) || unicode.Is(unicode.Zl, c) || unicode.Is(unicode.Zp, c) {
			continue
		}
		if b = append(b, c); len(b) == 80 {
			break
		}
	}
	return string(b)
}

// providerOf is the provider part of a user ID ("google:123" -> "google"), "" if there is none.
func providerOf(userID string) string {
	p, _, ok := strings.Cut(userID, ":")
	if !ok {
		return ""
	}
	return p
}

// Errors of Publish and Revoke besides ErrNotPublishable, ErrTooManyKeys and ErrDirectoryOff.
// They are the docstore errors, so the routes map them to statuses.
var (
	ErrProofRequired = docstore.ErrDirectoryProofRequired
	// ErrEnrollmentRequired: RequireEnrollment is set and the address has no owner key.
	ErrEnrollmentRequired = docstore.ErrDirectoryEnrollmentRequired
	ErrBadProof           = docstore.ErrDirectoryBadProof
	ErrBadAttestation     = docstore.ErrDirectoryBadAttestation
	ErrKeyNotFound        = docstore.ErrDirectoryNotFound
	ErrRevoked            = docstore.ErrDirectoryRevoked
)

// publishable says user may publish and revoke: a provider session with a verified e-mail.
func publishable(user auth.User) bool {
	return user.VerifiedEmail && normalizeEmail(user.Email) != "" && user.ID != "" && !strings.HasPrefix(user.ID, passkeySessionPrefix) && providerOf(user.ID) != ""
}

// stored is a record read from the store under one of an address's names.
type stored struct {
	id     string
	rec    directoryRecord
	fp     string
	typ    string
	text   string
	name   int    // index into names: 0 is the current key's
	digest string // docstore.Digest of the stored bytes, for the conditional write that replaces them
}

func (st stored) when() int64 { return max(st.rec.At, st.rec.Revoked) }

// addrState is everything the store holds about one address: for each fingerprint the newest
// record (best, in the order they were found: list), every copy under every name (all) and the
// digest of each stored record by ID (digests; what a conditional write must be given).
type addrState struct {
	best    map[string]stored
	list    []stored
	all     []stored
	digests map[string]string
}

// readAddress reads every record of an address (under every key's name) whose key is the one its
// ID names, and for each fingerprint keeps the newest (the current name's wins a tie); an
// unreadable or misnamed record is skipped.
func (d *AdapterDirectory) readAddress(ctx context.Context, email string) (addrState, error) {
	want := normalizeEmail(email)
	out := addrState{best: map[string]stored{}, digests: map[string]string{}}
	var order []string
	for i, prefix := range d.names(want) {
		metas, err := docstore.ListPrefix(ctx, d.Store, prefix)
		if err != nil {
			return addrState{}, fmt.Errorf("encrypt: listing the key directory: %w", err)
		}
		for _, m := range metas {
			if !strings.HasPrefix(m.ID, prefix) {
				continue
			}
			doc, err := d.Store.Load(ctx, m.ID)
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			if err != nil {
				return addrState{}, err
			}
			var rec directoryRecord
			if json.Unmarshal(doc.Data, &rec) != nil {
				continue
			}
			r, err := parsePublic(rec.Key)
			if err != nil || prefix+fingerprintSegment(r.Fingerprint) != m.ID {
				continue // a key that is not the one its record is named for
			}
			st := stored{id: m.ID, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text, name: i, digest: docstore.Digest(doc.Data)}
			out.all = append(out.all, st)
			out.digests[m.ID] = st.digest
			if cur, ok := out.best[r.Fingerprint]; !ok {
				out.best[r.Fingerprint] = st
				order = append(order, r.Fingerprint)
			} else if st.when() > cur.when() {
				out.best[r.Fingerprint] = st
			}
		}
	}
	for _, fp := range order {
		out.list = append(out.list, out.best[fp])
	}
	return out, nil
}

// signedBy says the record's signature is a server key's for this record, this address and a
// provider user (not a passkey one).
func (d *AdapterDirectory) signedBy(st stored, want string) bool {
	rec := st.rec
	if rec.V != 2 && rec.V != 3 && rec.V != 4 && rec.V != 5 {
		return false
	}
	sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
	return d.Signer.Verify(rec.Kid, rec.message(st.id), sig) &&
		normalizeEmail(rec.Email) == want &&
		rec.Provider != "" && rec.Provider != "passkey" && providerOf(rec.Publisher) == rec.Provider
}

// entryFor finds the log entry a record stands on: the one its Seq names, which must be a validly
// signed and hashed entry for this fingerprint and for the operation the record is (publish, or
// revoke for a tombstone). ok is false for a record with no such entry (Seq 0, a number that is
// not that entry): it was not made by the publish route. signed says the entry's signature is
// one of this directory's keys' (an entry made under a key since removed from the retired list
// cannot be checked here, though a client with the key pinned can).
func (d *AdapterDirectory) entryFor(ctx context.Context, st stored) (e LogEntry, ok, signed bool, err error) {
	rec := st.rec
	if rec.Seq <= 0 {
		return LogEntry{}, false, false, nil
	}
	e, found, err := d.readEntry(ctx, rec.Seq)
	if err != nil || !found {
		return LogEntry{}, false, false, err
	}
	op := OpPublish
	if rec.Revoked > 0 {
		op = OpRevoke
	}
	if e.Op != op || e.Fingerprint != st.fp || e.Hash != logHash(e) {
		return LogEntry{}, false, false, nil
	}
	sig, _ := base64.StdEncoding.DecodeString(e.Sig)
	return e, true, d.Signer.Verify(e.Kid, logSigMessage(e.Hash), sig), nil
}

// entryIsFor says the entry is about this address: its pseudonym is the address's under one of
// the keys the directory knows. After a retired key is removed from the list, entries made under
// it can no longer be matched (their records must be republished to be backed again).
func (d *AdapterDirectory) entryIsFor(e LogEntry, want string) bool {
	for _, a := range d.LogAddressesFor(want) {
		if e.Address == a {
			return true
		}
	}
	return false
}

// backedEntry is entryFor for an entry of this address.
func (d *AdapterDirectory) backedEntry(ctx context.Context, st stored, want string) (LogEntry, bool, error) {
	e, ok, signed, err := d.entryFor(ctx, st)
	if err != nil || !ok || !signed || !d.entryIsFor(e, want) {
		return LogEntry{}, false, err
	}
	return e, true, nil
}
