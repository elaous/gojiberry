package encrypt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
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
	// RevokeWindow is how long a revoked key stays remembered: during it the key cannot be
	// published again (ErrRevoked), so a replayed request cannot silently bring it back, and a
	// lookup that asks for revoked keys shows it. DefaultRevokeWindow when 0.
	RevokeWindow time.Duration
	// ResetWait is how long a provider-session reset of an address's owner key waits between its
	// request and its taking effect (DefaultResetWait, 24 hours, when 0). Do not shorten it in a
	// deployment: auditors hold the server to 24 hours (AuditOptions.ResetWait).
	ResetWait time.Duration
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

// PublishOptions is what a publisher sends besides the key: the answer to a challenge
// (Challenge and Nonce, see Challenge and OpenChallenge) and, for an ssh-ed25519 key, the key's
// own attestation of the binding to the address (see Attest).
type PublishOptions struct {
	Challenge, Nonce string
	Attestation      *docstore.DirectoryAttestation
	// Owner is the owner's signature of the publish operation, required when the address has an
	// enrolled owner key (see Enroll).
	Owner *docstore.DirectoryOwnerSig
}

// Errors of Publish and Revoke besides ErrNotPublishable, ErrTooManyKeys and ErrDirectoryOff.
// They are the docstore errors, so the routes map them to statuses.
var (
	ErrProofRequired  = docstore.ErrDirectoryProofRequired
	ErrBadProof       = docstore.ErrDirectoryBadProof
	ErrBadAttestation = docstore.ErrDirectoryBadAttestation
	ErrKeyNotFound    = docstore.ErrDirectoryNotFound
	ErrRevoked        = docstore.ErrDirectoryRevoked
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

// Publish records key as a key of user's e-mail address, with no proof of possession (it
// refuses with ErrProofRequired when RequireProof is set). See PublishWith.
func (d *AdapterDirectory) Publish(ctx context.Context, user auth.User, key Recipient) error {
	_, err := d.PublishWith(ctx, user, key, PublishOptions{})
	return err
}

// PublishWith records key as a key of user's e-mail address. It refuses (ErrNotPublishable)
// unless the user's e-mail is explicitly verified (auth.User.VerifiedEmail) and came from a
// provider session: a passkey user ("passkey:...") has none that is verified. The type and
// fingerprint are recomputed from key.Text. Publishing the same key again replaces the record,
// signed by the current key (so it is how a record moves to a new key after a rotation). At most
// MaxKeys keys are kept for one address, counting keys revoked within RevokeWindow, and a key
// revoked within that window is refused (ErrRevoked).
//
// Proof of possession. With opt.Challenge and opt.Nonce (from a challenge made for this user and
// key and not yet used, answered before it expired, issued by the current directory key) the
// record says Held. A proof that does not check is refused (ErrBadProof) and consumes nothing
// but itself; a missing one is refused (ErrProofRequired) only when RequireProof is set. The
// challenge is used up as soon as it checks, whatever happens next.
//
// Attestation. An opt.Attestation must be a valid signature by the key over the binding
// (AttestationMessage) for this address, dated within ten minutes of now, and the key must be an
// ssh-ed25519 one (ErrBadAttestation otherwise); the record then carries it and Lookup reports
// the key Attested.
//
// Owner. If the address has an enrolled owner key (Enroll), opt.Owner is required: the owner's
// signature over the publish operation (ErrOwnerRequired, ErrBadOwnerSig), recorded in the log
// entry and in the record; Lookup then marks the key OwnerSigned. A key an owner-enrolled
// address did not sign is not Verified.
//
// Atomicity. The publication is appended to the transparency log first and the record carries
// its number; the record is then written conditionally on being what was read (create it if there
// was none), and the checks (tombstones, the cap, the owner) are made again from a fresh read if
// another replica got there first. A record that cannot be written leaves a "void" entry behind,
// so the log never claims a publication that did not happen. On a store without a conditional
// write this holds only inside this process (see CheckStore).
func (d *AdapterDirectory) PublishWith(ctx context.Context, user auth.User, key Recipient, opt PublishOptions) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
	}
	if err := d.checkUser(user); err != nil {
		return PublishedKey{}, err
	}
	r, err := parsePublic(key.Text)
	if err != nil {
		return PublishedKey{}, err
	}
	now := d.now()
	email := normalizeEmail(user.Email)

	// Check what the request carries before anything is written or used up.
	var att *docstore.DirectoryAttestation
	if opt.Attestation != nil {
		if r.Type != "ssh-ed25519" {
			return PublishedKey{}, fmt.Errorf("%w: a %s key cannot attest", ErrBadAttestation, r.Type)
		}
		if skew := now.Unix() - opt.Attestation.At; skew > int64(attestSkew/time.Second) || -skew > int64(attestSkew/time.Second) {
			return PublishedKey{}, fmt.Errorf("%w: it is dated %d seconds from now", ErrBadAttestation, skew)
		}
		if !VerifyAttestation(r.Text, email, *opt.Attestation) {
			return PublishedKey{}, ErrBadAttestation
		}
		att = opt.Attestation
	}
	var tok challengeToken
	proven := opt.Challenge != "" || opt.Nonce != ""
	switch {
	case proven:
		if tok, err = d.checkProof(user, r, opt.Challenge, opt.Nonce, now); err != nil {
			return PublishedKey{}, err
		}
	case d.RequireProof:
		return PublishedKey{}, ErrProofRequired
	}

	defer d.lockAddress(user.Email)()
	maxKeys := d.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 20
	}
	id := d.directoryID(email, r.Fingerprint)
	claimed := false
	for round := 0; round < maxCASRounds; round++ {
		state, err := d.readAddress(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		enr, _, err := d.readEnrollment(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		label := cleanLabel(key.Label)
		owner, err := d.checkOwner(ctx, enr, OpPublish, email, r.Fingerprint, LabelHash(label), opt.Owner, now)
		if err != nil {
			return PublishedKey{}, err
		}
		counted := 0
		var stale []stored // tombstones past their window: pruned
		for fp, st := range state.best {
			if st.rec.Revoked > 0 {
				if !d.signedBy(st, email) { // an unsigned tombstone is not believed, and not counted
					continue
				}
				if now.Sub(time.Unix(st.rec.Revoked, 0)) < d.window() {
					if fp == r.Fingerprint {
						return PublishedKey{}, ErrRevoked
					}
					counted++
				} else {
					stale = append(stale, st)
				}
				continue
			}
			counted++
		}
		if cur, ok := state.best[r.Fingerprint]; (!ok || cur.rec.Revoked > 0) && counted >= maxKeys {
			return PublishedKey{}, ErrTooManyKeys
		}
		// Everything has checked: the challenge is used up now (single use), and the publication
		// is logged before the record is written.
		if proven && !claimed {
			if err := d.claimChallenge(ctx, tok); err != nil {
				return PublishedKey{}, err
			}
			claimed = true
		}
		in := logInput{Op: OpPublish, Email: email, Fingerprint: r.Fingerprint}
		rec := directoryRecord{Key: r.Text, Label: label, Publisher: user.ID, Provider: providerOf(user.ID), Email: email, At: now.Unix(), Held: proven}
		if owner != nil {
			in.OwnerSig, in.OpTime, in.OwnerPrev, in.OwnerV, in.LabelHash = owner.Sig, owner.Time, owner.Prev, owner.V, LabelHash(label)
			rec.OwnerSig, rec.OwnerTime, rec.OwnerPrev, rec.OwnerV = owner.Sig, owner.Time, owner.Prev, owner.V
		}
		seq, err := d.appendLog(ctx, in, now)
		if err != nil {
			return PublishedKey{}, err
		}
		rec.Seq = seq
		if att != nil {
			rec.Attest, rec.AttestAt = att.Sig, att.At
		}
		rec = d.sign(rec, id)
		data, err := json.Marshal(rec)
		if err != nil {
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
			return PublishedKey{}, err
		}
		switch err := d.saveIf(ctx, id, data, state.digests[id]); {
		case err == nil:
			d.dropOtherNames(ctx, email, r.Fingerprint, id)
			for _, st := range stale {
				if st.fp != r.Fingerprint {
					_ = d.deleteIf(ctx, st.id, st.digest) // best effort: a tombstone left behind is pruned next time
				}
			}
			st := stored{id: id, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text}
			return d.publishedKey(ctx, st, email, false, enr)
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, r.Fingerprint, now) // another replica changed it: look again
		default:
			d.voidLog(ctx, email, seq, r.Fingerprint, now)
			return PublishedKey{}, err
		}
	}
	return PublishedKey{}, docstore.ErrDirectoryBusy
}

// dropOtherNames deletes the records of fingerprint fp that a retired key's name holds (the
// record now lives under id, signed by the current key).
func (d *AdapterDirectory) dropOtherNames(ctx context.Context, email, fp, id string) {
	for _, prefix := range d.names(email) {
		if other := prefix + fingerprintSegment(fp); other != id {
			_ = d.Store.Delete(ctx, other) // not found is the usual case
		}
	}
}

// deleteIf deletes the record id only if it still has digest prev: with the store's conditional
// delete when it has one (docstore.ConditionalDeleter), otherwise as one step inside this process.
// docstore.ErrConflict if it changed, docstore.ErrNotFound if it is gone.
func (d *AdapterDirectory) deleteIf(ctx context.Context, id, prev string) error {
	if c, ok := d.Store.(docstore.ConditionalDeleter); ok {
		err := c.DeleteIf(ctx, id, prev)
		if !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return err
		}
	}
	d.casMu.Lock()
	defer d.casMu.Unlock()
	doc, err := d.Store.Load(ctx, id)
	if err != nil {
		return err
	}
	if docstore.Digest(doc.Data) != prev {
		return docstore.ErrConflict
	}
	return d.Store.Delete(ctx, id)
}

// ErrTooManyKeys is returned by Publish when the address already has as many published keys as
// the directory keeps.
var ErrTooManyKeys = errors.New("encrypt: this address already has the most keys the directory keeps")

// publishedKey is the lookup row of a stored record, with its flags computed. Verified needs the
// server's signature, and, for a record that a retired key signed or that belongs to an address
// with an owner key, a log entry that backs it (backedEntry): a retired key that was compromised
// cannot make a record Verified by signing one, and a server that holds the directory key cannot
// put an unlogged record under an owner's address. On an owner-enrolled address a record newer
// than the enrollment must also carry the owner's signature (OwnerSigned) to be Verified. Attested
// is from that and the key's own signature.
func (d *AdapterDirectory) publishedKey(ctx context.Context, st stored, want string, includeRevoked bool, enr *enrollRecord) (PublishedKey, error) {
	rec := st.rec
	k := PublishedKey{Label: rec.Label, Text: st.text, Type: st.typ, Fingerprint: st.fp, Kid: rec.Kid, Seq: rec.Seq}
	verified := d.signedBy(st, want)
	retired := rec.Kid != d.Signer.KeyID()
	var entry LogEntry
	var haveEntry, addrOK bool
	if verified && (enr != nil || retired) {
		var signed bool
		var err error
		if entry, haveEntry, signed, err = d.entryFor(ctx, st); err != nil {
			return PublishedKey{}, err
		}
		addrOK = haveEntry && signed && d.entryIsFor(entry, want)
		if retired && !addrOK {
			verified = false
		}
	}
	if enr != nil {
		k.OwnerEnrolled, k.OwnerResetPending = true, enr.ResetSince > 0
		if verified {
			// An owner signature binds the address itself (it names it), so the entry only has to
			// be the publication's; without one, the record must predate the enrollment and be
			// backed by an entry of this address.
			if ev, ok := ownerEvidence(enr, st, entry, haveEntry); ok {
				k.OwnerSigned, k.Owner = true, &ev
			} else if rec.Revoked == 0 && (len(enr.Keys) == 0 || rec.Seq > enr.Keys[0].Seq || !addrOK) {
				verified = false // after the enrollment without the owner's signature, or with nothing behind it
			}
		}
	}
	k.Verified = verified
	if k.Verified {
		k.Held = rec.V >= 3 && rec.Held
		if rec.V >= 3 && rec.Attest != "" {
			att := docstore.DirectoryAttestation{At: rec.AttestAt, Sig: rec.Attest}
			if VerifyAttestation(st.text, want, att) {
				k.Attested, k.Attestation = true, &att
			}
		}
	}
	if rec.Revoked > 0 && includeRevoked {
		k.Revoked, k.RevokedAt = true, rec.Revoked
	}
	return k, nil
}

// ownerEvidence says the record carries the signature of an owner key that was the address's
// owner key at the time of its log entry, the same one the entry carries, and returns it.
func ownerEvidence(enr *enrollRecord, st stored, entry LogEntry, haveEntry bool) (docstore.DirectoryOwnerEvidence, bool) {
	rec := st.rec
	if !haveEntry || rec.V < 4 || rec.OwnerSig == "" || entry.OwnerSig != rec.OwnerSig || entry.OpTime != rec.OwnerTime || entry.OwnerPrev != rec.OwnerPrev || entry.OwnerV != rec.OwnerV {
		return docstore.DirectoryOwnerEvidence{}, false
	}
	op := OpPublish
	if rec.Revoked > 0 {
		op = OpRevoke
	}
	for _, k := range enr.Keys {
		if k.Seq >= rec.Seq || (k.Until != 0 && rec.Seq >= k.Until) {
			continue
		}
		pub, err := ParseOwnerPub(k.Pub)
		if err == nil && verifyOwnerVersion(pub, rec.OwnerV, op, rec.Email, st.fp, LabelHash(rec.Label), rec.OwnerTime, rec.OwnerPrev, rec.OwnerSig) {
			return docstore.DirectoryOwnerEvidence{Pub: k.Pub, Time: rec.OwnerTime, Prev: rec.OwnerPrev, Sig: rec.OwnerSig, V: rec.OwnerV}, true
		}
	}
	return docstore.DirectoryOwnerEvidence{}, false
}

// LookupOptions are the choices of LookupWith.
type LookupOptions struct {
	// IncludeRevoked also returns the keys revoked within RevokeWindow, flagged Revoked.
	IncludeRevoked bool
}

// Lookup returns the keys published for email. A record counts as Verified only when its
// signature is a server key's (current or retired), for this record and this address, from a
// provider user, backed by a log entry where that matters (see publishedKey); a record that
// fails any of that is returned unverified (the owner then confirms the fingerprint), one whose
// key does not match its ID, or that cannot be read, is skipped. Revoked keys are not returned.
// With no Signer it returns nothing; a malformed address (not exactly one "@") is
// docstore.ErrDirectoryBadAddress.
func (d *AdapterDirectory) Lookup(ctx context.Context, email string) ([]PublishedKey, error) {
	return d.LookupWith(ctx, email, LookupOptions{})
}

// LookupWith is Lookup with options. A revoked key (a tombstone with a valid signature) is left
// out, or with IncludeRevoked returned flagged; a tombstone whose signature does not verify is
// ignored (it could only have been written by someone with write access to the store, who could
// as well delete the record).
func (d *AdapterDirectory) LookupWith(ctx context.Context, email string, opt LookupOptions) ([]PublishedKey, error) {
	want := normalizeEmail(email)
	if want == "" || d.Signer == nil {
		return nil, nil
	}
	if !docstore.ValidAddress(want) {
		return nil, docstore.ErrDirectoryBadAddress
	}
	state, err := d.readAddress(ctx, want)
	if err != nil {
		return nil, err
	}
	enr, _, err := d.readEnrollment(ctx, want)
	if err != nil {
		return nil, err
	}
	now := d.now()
	var out []PublishedKey
	for _, st := range state.list {
		if st.rec.Revoked > 0 {
			if !opt.IncludeRevoked || !d.signedBy(st, want) || now.Sub(time.Unix(st.rec.Revoked, 0)) >= d.window() {
				continue
			}
		}
		k, err := d.publishedKey(ctx, st, want, opt.IncludeRevoked, enr)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// Revoke removes the key with this fingerprint ("SHA256:..." or the form fingerprintSegment
// makes of it) from user's own address: the record is replaced at once by a signed tombstone, so
// Lookup stops returning the key, and the revocation is logged. The tombstone carries the number
// of the revoke entry, and is kept for RevokeWindow, during which the key cannot be published
// again (ErrRevoked) and a lookup can ask for it. Only the address of the signed-in user is
// reachable: ErrKeyNotFound if it has no such published key (or it was revoked already),
// ErrNotPublishable for a user who could not have published. On an address with an owner key,
// owner is required (the owner's signature over the revoke operation). Like Publish, the
// tombstone is written conditionally on the record that was read, and a record that cannot be
// written leaves a void log entry.
func (d *AdapterDirectory) Revoke(ctx context.Context, user auth.User, fingerprint string, owner *docstore.DirectoryOwnerSig) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
	}
	if err := d.checkUser(user); err != nil {
		return PublishedKey{}, err
	}
	email := normalizeEmail(user.Email)
	seg := fingerprintSegment(strings.TrimSpace(fingerprint))
	defer d.lockAddress(user.Email)()
	now := d.now()
	for round := 0; round < maxCASRounds; round++ {
		state, err := d.readAddress(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		var st stored
		found := false
		for fp, cand := range state.best {
			if fingerprintSegment(fp) == seg && cand.rec.Revoked == 0 {
				st, found = cand, true
			}
		}
		if !found {
			return PublishedKey{}, ErrKeyNotFound
		}
		enr, _, err := d.readEnrollment(ctx, email)
		if err != nil {
			return PublishedKey{}, err
		}
		o, err := d.checkOwner(ctx, enr, OpRevoke, email, st.fp, "", owner, now)
		if err != nil {
			return PublishedKey{}, err
		}
		in := logInput{Op: OpRevoke, Email: email, Fingerprint: st.fp}
		tomb := st.rec
		tomb.OwnerSig, tomb.OwnerTime, tomb.OwnerPrev, tomb.OwnerV = "", 0, "", 0
		if o != nil {
			in.OwnerSig, in.OpTime, in.OwnerPrev = o.Sig, o.Time, o.Prev
			tomb.OwnerSig, tomb.OwnerTime, tomb.OwnerPrev = o.Sig, o.Time, o.Prev
		}
		seq, err := d.appendLog(ctx, in, now)
		if err != nil {
			return PublishedKey{}, err
		}
		id := d.directoryID(email, st.fp)
		tomb.Publisher, tomb.Provider, tomb.Revoked, tomb.Seq = user.ID, providerOf(user.ID), now.Unix(), seq
		tomb.Held, tomb.Attest, tomb.AttestAt = false, "", 0
		tomb = d.sign(tomb, id)
		data, err := json.Marshal(tomb)
		if err != nil {
			d.voidLog(ctx, email, seq, st.fp, now)
			return PublishedKey{}, err
		}
		switch err := d.saveIf(ctx, id, data, state.digests[id]); {
		case err == nil:
			d.dropOtherNames(ctx, email, st.fp, id)
			return d.publishedKey(ctx, stored{id: id, rec: tomb, fp: st.fp, typ: st.typ, text: st.text}, email, true, enr)
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, st.fp, now)
		default:
			d.voidLog(ctx, email, seq, st.fp, now)
			return PublishedKey{}, err
		}
	}
	return PublishedKey{}, docstore.ErrDirectoryBusy
}

// Sweep deletes what has expired: used challenges (kept until the challenge itself could no
// longer be answered, which is what makes a replay refused) and revoked-key tombstones past
// RevokeWindow. It returns how many it deleted. Run it now and then (RunSweep).
func (d *AdapterDirectory) Sweep(ctx context.Context) (int, error) {
	if d.Signer == nil {
		return 0, ErrDirectoryOff
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, directoryPrefix)
	if err != nil {
		return 0, err
	}
	now := d.now()
	n := 0
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		expired := false
		email := ""
		if strings.HasPrefix(m.ID, popPrefix) {
			var u struct {
				Exp int64 `json:"exp"`
			}
			expired = json.Unmarshal(doc.Data, &u) != nil || now.Unix() > u.Exp+int64(popGrace/time.Second)
		} else {
			var rec directoryRecord
			expired = json.Unmarshal(doc.Data, &rec) == nil && rec.Revoked > 0 && now.Sub(time.Unix(rec.Revoked, 0)) >= d.window()
			email = rec.Email
		}
		if !expired {
			continue
		}
		// Delete only what was read: a republish since replaced the tombstone with a live record,
		// and an unconditional delete would remove the key somebody just published. It is made
		// under the address's lock, so in this process it cannot interleave with a publish, and
		// across replicas the store's conditional delete decides.
		err = func() error {
			if docstore.ValidAddress(email) {
				defer d.lockAddress(email)()
			}
			return d.deleteIf(ctx, m.ID, docstore.Digest(doc.Data))
		}()
		switch {
		case err == nil:
			n++
		case errors.Is(err, docstore.ErrConflict), errors.Is(err, docstore.ErrNotFound):
			// changed or gone since it was read: not ours to delete
		default:
			return n, err
		}
	}
	return n, nil
}

// RunSweep calls Sweep every interval (an hour when not positive) until ctx ends; errors are
// logged.
func (d *AdapterDirectory) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("encrypt: sweeping the key directory: %v", err)
			}
		}
	}
}

var _ KeyDirectory = (*AdapterDirectory)(nil)

// DirectoryService is the docstore.DirectoryService over d, for docstore.Server.WithDirectory:
// the routes call it after checking the session. It maps the directory's refusals to the
// errors the server answers with.
func DirectoryService(d *AdapterDirectory) docstore.DirectoryService { return dirService{d} }

type dirService struct{ d *AdapterDirectory }

// refusal wraps the directory's own refusals as docstore.ErrDirectoryRefused (403); the
// docstore errors pass through.
func refusal(err error) error {
	switch {
	case errors.Is(err, docstore.ErrDirectoryBadAddress):
		return err
	case errors.Is(err, errEnrollmentCorrupt):
		return fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	case errors.Is(err, ErrNotPublishable), errors.Is(err, ErrTooManyKeys), errors.Is(err, ErrDirectoryOff):
		return fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	}
	return err
}

func toDirectoryKey(k PublishedKey) docstore.DirectoryKey {
	return docstore.DirectoryKey{Key: k.Text, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified,
		Held: k.Held, Attested: k.Attested, Attestation: k.Attestation, Seq: k.Seq, Kid: k.Kid, Revoked: k.Revoked, RevokedAt: k.RevokedAt,
		OwnerEnrolled: k.OwnerEnrolled, OwnerSigned: k.OwnerSigned, OwnerResetPending: k.OwnerResetPending, Owner: k.Owner}
}

func (s dirService) Challenge(ctx context.Context, user auth.User, key string) (docstore.DirectoryChallenge, error) {
	c, err := s.d.Challenge(ctx, user, key)
	if errors.Is(err, errBadPublicKey) {
		return docstore.DirectoryChallenge{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryBadKey, err)
	}
	return c, refusal(err)
}

func (s dirService) Publish(ctx context.Context, user auth.User, req docstore.DirectoryPublish) (docstore.DirectoryKey, error) {
	r, err := parsePublic(req.Key)
	if err != nil {
		return docstore.DirectoryKey{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryBadKey, err)
	}
	r.Label = req.Label
	opt := PublishOptions{Attestation: req.Attestation, Owner: req.Owner}
	if req.Proof != nil {
		opt.Challenge, opt.Nonce = req.Proof.Token, req.Proof.Nonce
	}
	k, err := s.d.PublishWith(ctx, user, r, opt)
	if err != nil {
		return docstore.DirectoryKey{}, refusal(err)
	}
	return toDirectoryKey(k), nil
}

func (s dirService) Revoke(ctx context.Context, user auth.User, fingerprint string, owner *docstore.DirectoryOwnerSig) (docstore.DirectoryKey, error) {
	k, err := s.d.Revoke(ctx, user, fingerprint, owner)
	if err != nil {
		return docstore.DirectoryKey{}, refusal(err)
	}
	return toDirectoryKey(k), nil
}

func (s dirService) Lookup(ctx context.Context, email string, includeRevoked bool) ([]docstore.DirectoryKey, error) {
	keys, err := s.d.LookupWith(ctx, email, LookupOptions{IncludeRevoked: includeRevoked})
	if err != nil {
		return nil, err
	}
	out := make([]docstore.DirectoryKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, toDirectoryKey(k))
	}
	return out, nil
}

func (s dirService) Log(ctx context.Context, since int64, limit int) (docstore.DirectoryLog, error) {
	return s.d.ReadLog(ctx, since, limit)
}

// LogFor implements docstore.DirectoryLogFilterer.
func (s dirService) LogFor(ctx context.Context, addresses []string, since int64, scan, limit int) (docstore.DirectoryLog, error) {
	return s.d.LogFor(ctx, addresses, since, scan, limit)
}

// OperatorResetEnrollment implements docstore.DirectoryOperator.
func (s dirService) OperatorResetEnrollment(ctx context.Context, pseudonym string) (docstore.DirectoryEnrollment, error) {
	return s.d.OperatorResetEnrollment(ctx, pseudonym)
}

func (s dirService) Head(ctx context.Context) (docstore.DirectoryHead, error) { return s.d.Head(ctx) }

func (s dirService) LogAddress(_ context.Context, user auth.User) (docstore.DirectoryLogAddress, error) {
	if s.d.Signer == nil {
		return docstore.DirectoryLogAddress{}, refusal(ErrDirectoryOff)
	}
	if err := s.d.checkUser(user); err != nil {
		return docstore.DirectoryLogAddress{}, refusal(err)
	}
	all := s.d.LogAddressesFor(user.Email)
	return docstore.DirectoryLogAddress{Address: all[0], Previous: all[1:]}, nil
}

func (s dirService) Enrollment(ctx context.Context, email string) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.Enrollment(ctx, email)
	return e, refusal(err)
}

func (s dirService) EnrollChallenge(ctx context.Context, user auth.User) (docstore.DirectoryEnrollChallenge, error) {
	c, err := s.d.EnrollChallenge(ctx, user)
	return c, refusal(err)
}

func (s dirService) Enroll(ctx context.Context, user auth.User, req docstore.DirectoryEnroll) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.Enroll(ctx, user, req)
	return e, refusal(err)
}

func (s dirService) RequestReset(ctx context.Context, user auth.User) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.RequestReset(ctx, user)
	return e, refusal(err)
}

func (s dirService) CancelReset(ctx context.Context, user auth.User, owner *docstore.DirectoryOwnerSig) (docstore.DirectoryEnrollment, error) {
	e, err := s.d.CancelReset(ctx, user, owner)
	return e, refusal(err)
}

// DirectoryHealth implements docstore.DirectoryHealther: the census of the last Startup.
func (s dirService) DirectoryHealth() docstore.DirectoryHealth { return s.d.Health() }

// ParsePublicKey reads a public key as the directory takes it: "age1..." (an age, recovery or
// passkey key) or an "ssh-ed25519 ..."/"ssh-rsa ..." line. It is how a client gets the type and
// the fingerprint a publish names.
func ParsePublicKey(text string) (Recipient, error) { return parsePublic(text) }
