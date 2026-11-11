package encrypt

import (
	"context"
	"crypto/ed25519"
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

// ErrNotPublishable is returned by Publish for users who may not publish.
var ErrNotPublishable = errors.New("encrypt: only a signed-in account with an explicitly verified e-mail can publish a key")

const directoryPrefix = "directory/"

// popPrefix names the used challenges.
const popPrefix = directoryPrefix + "pop/"

// passkeySessionPrefix starts the ID of a passkey session user.
const passkeySessionPrefix = "passkey:"

// DefaultRevokeWindow is how long a revoked key stays visible.
const DefaultRevokeWindow = 24 * time.Hour

// AdapterDirectory is a KeyDirectory over a docstore adapter.
type AdapterDirectory struct {
	// Store holds the records, the transparency log and the state.
	Store  docstore.Adapter
	Signer DirectorySigner
	// MaxKeys is how many keys one address may have.
	MaxKeys int
	// RequireProof makes Publish refuse a key without a proof.
	RequireProof bool
	// RequireEnrollment makes Publish refuse an address that is not enrolled.
	RequireEnrollment bool
	// RevokeWindow is how long a revoked key stays listed.
	RevokeWindow time.Duration
	// ResetWait is how long a provider-session reset waits.
	ResetWait time.Duration
	// ReconcileGrace is how old an owner-state log entry may be.
	ReconcileGrace time.Duration
	// UnmappedCap caps how many unmapped addresses are listed.
	UnmappedCap int
	// Witnesses are the public keys of the trusted witnesses.
	Witnesses map[string]ed25519.PublicKey
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	// report is the census of the last Startup.
	report atomic.Pointer[docstore.DirectoryHealth]
	// locks serialise Publish per address within this process.
	locks [64]sync.Mutex
	// casMu serialises the transparency log's appends.
	casMu sync.Mutex
	// idx is the owner-state entries of the log.
	idxMu sync.Mutex
	idx   logIndex
	// unmappedWarned is the count the last warning reported.
	unmappedMu     sync.Mutex
	unmappedWarned int
	// aliases are the log pseudonyms of an address.
	aliasMu sync.RWMutex
	aliases map[string][]string
	// id is this process's registration name.
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

// lockAddress takes an address's lock; the caller releases it.
func (d *AdapterDirectory) lockAddress(email string) func() {
	h := fnv.New32a()
	h.Write([]byte(normalizeEmail(email)))
	mu := &d.locks[h.Sum32()%uint32(len(d.locks))]
	mu.Lock()
	return mu.Unlock
}

// directoryRecord is the stored form of a key record.
type directoryRecord struct {
	V         int    `json:"v"`
	Kid       string `json:"kid,omitempty"` // the server key that signed it (V 3)
	Key       string `json:"key"`
	Label     string `json:"label,omitempty"`
	Publisher string `json:"publisher"` // the user's ID, "{provider}:{subject}"
	Provider  string `json:"provider"`
	Email     string `json:"email"` // the address, as the publisher's provider verified it
	At        int64  `json:"at"`    // unix seconds
	// Held: a challenge sent to the key was answered.
	Held     bool   `json:"held,omitempty"`
	Attest   string `json:"attest,omitempty"`
	AttestAt int64  `json:"attest_at,omitempty"`
	Seq      int64  `json:"seq,omitempty"`
	Revoked  int64  `json:"revoked,omitempty"`
	// Owner*: the owner's signature of this public key.
	OwnerSig  string `json:"owner_sig,omitempty"`
	OwnerTime int64  `json:"owner_time,omitempty"`
	OwnerPrev string `json:"owner_prev,omitempty"`
	// OwnerV is the version of the owner's message.
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

// sign makes rec a current record, signed by the server.
func (d *AdapterDirectory) sign(rec directoryRecord, id string) directoryRecord {
	rec.V, rec.Kid = 5, d.Signer.KeyID()
	rec.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(rec.message(id)))
	return rec
}

// names are the prefixes under which an address's records live.
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

// fingerprintSegment makes a URL-safe fingerprint segment.
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

// cleanLabel keeps a label short and printable.
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

// providerOf is the provider part of a user ID.
func providerOf(userID string) string {
	p, _, ok := strings.Cut(userID, ":")
	if !ok {
		return ""
	}
	return p
}

// Errors of Publish and Revoke besides ErrNotPublishable.
var (
	ErrProofRequired = docstore.ErrDirectoryProofRequired
	// ErrEnrollmentRequired: the address must enroll first.
	ErrEnrollmentRequired = docstore.ErrDirectoryEnrollmentRequired
	ErrBadProof           = docstore.ErrDirectoryBadProof
	ErrBadAttestation     = docstore.ErrDirectoryBadAttestation
	ErrKeyNotFound        = docstore.ErrDirectoryNotFound
	ErrRevoked            = docstore.ErrDirectoryRevoked
)

// publishable says whether a user may publish and revoke.
func publishable(user auth.User) bool {
	return user.VerifiedEmail && normalizeEmail(user.Email) != "" && user.ID != "" && !strings.HasPrefix(user.ID, passkeySessionPrefix) && providerOf(user.ID) != ""
}

// stored is a record read from the store.
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

// addrState is what the store holds about one address.
type addrState struct {
	best    map[string]stored
	list    []stored
	all     []stored
	digests map[string]string
}

// readAddress reads every record of an address.
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
				continue
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

// signedBy says the record's signature is a server signature.
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

// entryFor finds the log entry a record stands on.
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

// entryIsFor says the entry is about this address.
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
