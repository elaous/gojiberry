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
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	// locks serialise Publish per address within this process (striped by the address's hash).
	locks [64]sync.Mutex
	// casMu serialises the transparency log's appends and the challenge claims in this process.
	casMu sync.Mutex
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
	Sig      string `json:"sig"`
}

func (rec directoryRecord) message(id string) []byte {
	if rec.V == 2 {
		b, _ := json.Marshal([]any{"gojiberry-directory-v1", id, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At})
		return b
	}
	b, _ := json.Marshal([]any{"gojiberry-directory-v3", id, rec.Kid, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At, rec.Held, rec.Attest, rec.AttestAt, rec.Seq, rec.Revoked})
	return b
}

// sign makes rec a current record: the current key's ID and signature for id.
func (d *AdapterDirectory) sign(rec directoryRecord, id string) directoryRecord {
	rec.V, rec.Kid = 3, d.Signer.KeyID()
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
	id   string
	rec  directoryRecord
	fp   string
	typ  string
	text string
	name int // index into names: 0 is the current key's
}

func (st stored) when() int64 { return max(st.rec.At, st.rec.Revoked) }

// readAddress reads every record of an address (under every key's name) whose key is the one its
// ID names, and for each fingerprint keeps the newest (the current name's wins a tie); an
// unreadable or misnamed record is skipped.
func (d *AdapterDirectory) readAddress(ctx context.Context, email string) (map[string]stored, []stored, error) {
	want := normalizeEmail(email)
	best := map[string]stored{}
	var order []string
	var all []stored
	for i, prefix := range d.names(want) {
		metas, err := docstore.ListPrefix(ctx, d.Store, prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("encrypt: listing the key directory: %w", err)
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
				return nil, nil, err
			}
			var rec directoryRecord
			if json.Unmarshal(doc.Data, &rec) != nil {
				continue
			}
			r, err := parsePublic(rec.Key)
			if err != nil || prefix+fingerprintSegment(r.Fingerprint) != m.ID {
				continue // a key that is not the one its record is named for
			}
			st := stored{id: m.ID, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text, name: i}
			all = append(all, st)
			if cur, ok := best[r.Fingerprint]; !ok {
				best[r.Fingerprint] = st
				order = append(order, r.Fingerprint)
			} else if st.when() > cur.when() {
				best[r.Fingerprint] = st
			}
		}
	}
	var list []stored
	for _, fp := range order {
		list = append(list, best[fp])
	}
	return best, list, nil
}

// signedBy says the record's signature is a server key's for this record, this address and a
// provider user (not a passkey one).
func (d *AdapterDirectory) signedBy(st stored, want string) bool {
	rec := st.rec
	if rec.V != 2 && rec.V != 3 {
		return false
	}
	sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
	return d.Signer.Verify(rec.Kid, rec.message(st.id), sig) &&
		normalizeEmail(rec.Email) == want &&
		rec.Provider != "" && rec.Provider != "passkey" && providerOf(rec.Publisher) == rec.Provider
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
// key and not yet used, answered before it expired) the record says Held. A proof that does not
// check is refused (ErrBadProof) and consumes nothing but itself; a missing one is refused
// (ErrProofRequired) only when RequireProof is set. The challenge is used up as soon as it
// checks, whatever happens next.
//
// Attestation. An opt.Attestation must be a valid signature by the key over the binding
// (AttestationMessage) for this address, dated within ten minutes of now, and the key must be an
// ssh-ed25519 one (ErrBadAttestation otherwise); the record then carries it and Lookup reports
// the key Attested.
//
// The publication is appended to the transparency log first and the record carries its number.
func (d *AdapterDirectory) PublishWith(ctx context.Context, user auth.User, key Recipient, opt PublishOptions) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
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
	best, _, err := d.readAddress(ctx, email)
	if err != nil {
		return PublishedKey{}, err
	}
	maxKeys := d.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 20
	}
	counted := 0
	var stale []stored // tombstones past their window: pruned
	for fp, st := range best {
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
	if cur, ok := best[r.Fingerprint]; (!ok || cur.rec.Revoked > 0) && counted >= maxKeys {
		return PublishedKey{}, ErrTooManyKeys
	}
	// Everything has checked: the challenge is used up now (single use), and the publication
	// is logged before the record is written.
	if proven {
		if err := d.claimChallenge(ctx, tok); err != nil {
			return PublishedKey{}, err
		}
	}
	seq, err := d.appendLog(ctx, "publish", email, r.Fingerprint, now)
	if err != nil {
		return PublishedKey{}, err
	}
	id := d.directoryID(email, r.Fingerprint)
	rec := directoryRecord{Key: r.Text, Label: cleanLabel(key.Label), Publisher: user.ID, Provider: providerOf(user.ID), Email: email, At: now.Unix(), Held: proven, Seq: seq}
	if att != nil {
		rec.Attest, rec.AttestAt = att.Sig, att.At
	}
	rec = d.sign(rec, id)
	data, err := json.Marshal(rec)
	if err != nil {
		return PublishedKey{}, err
	}
	if err := d.Store.Save(ctx, id, data); err != nil {
		return PublishedKey{}, err
	}
	d.dropOtherNames(ctx, email, r.Fingerprint, id)
	for _, st := range stale {
		if st.fp != r.Fingerprint {
			_ = d.Store.Delete(ctx, st.id) // best effort: a tombstone left behind is pruned next time
		}
	}
	return d.publishedKey(stored{id: id, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text}, email, false), nil
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

// ErrTooManyKeys is returned by Publish when the address already has as many published keys as
// the directory keeps.
var ErrTooManyKeys = errors.New("encrypt: this address already has the most keys the directory keeps")

// publishedKey is the lookup row of a stored record, with its flags computed: Verified from the
// server's signature, Attested from that and the key's own.
func (d *AdapterDirectory) publishedKey(st stored, want string, includeRevoked bool) PublishedKey {
	rec := st.rec
	k := PublishedKey{Label: rec.Label, Text: st.text, Type: st.typ, Fingerprint: st.fp, Kid: rec.Kid, Seq: rec.Seq}
	k.Verified = d.signedBy(st, want)
	if k.Verified {
		k.Held = rec.V == 3 && rec.Held
		if rec.V == 3 && rec.Attest != "" {
			att := docstore.DirectoryAttestation{At: rec.AttestAt, Sig: rec.Attest}
			if VerifyAttestation(st.text, want, att) {
				k.Attested, k.Attestation = true, &att
			}
		}
	}
	if rec.Revoked > 0 && includeRevoked {
		k.Revoked, k.RevokedAt = true, rec.Revoked
	}
	return k
}

// LookupOptions are the choices of LookupWith.
type LookupOptions struct {
	// IncludeRevoked also returns the keys revoked within RevokeWindow, flagged Revoked.
	IncludeRevoked bool
}

// Lookup returns the keys published for email. A record counts as Verified only when its
// signature is a server key's (current or retired), for this record and this address, from a
// provider user; a record that fails any of that is returned unverified (the owner then confirms
// the fingerprint), one whose key does not match its ID, or that cannot be read, is skipped.
// Revoked keys are not returned. With no Signer it returns nothing.
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
	_, list, err := d.readAddress(ctx, want)
	if err != nil {
		return nil, err
	}
	now := d.now()
	var out []PublishedKey
	for _, st := range list {
		if st.rec.Revoked > 0 {
			if !opt.IncludeRevoked || !d.signedBy(st, want) || now.Sub(time.Unix(st.rec.Revoked, 0)) >= d.window() {
				continue
			}
		}
		out = append(out, d.publishedKey(st, want, opt.IncludeRevoked))
	}
	return out, nil
}

// Revoke removes the key with this fingerprint ("SHA256:..." or the form fingerprintSegment
// makes of it) from user's own address: the record is replaced at once by a signed tombstone, so
// Lookup stops returning the key, and the revocation is logged. The tombstone is kept for
// RevokeWindow, during which the key cannot be published again (ErrRevoked) and a lookup can ask
// for it. Only the address of the signed-in user is reachable: ErrKeyNotFound if it has no such
// published key (or it was revoked already), ErrNotPublishable for a user who could not have
// published.
func (d *AdapterDirectory) Revoke(ctx context.Context, user auth.User, fingerprint string) (PublishedKey, error) {
	if d.Signer == nil {
		return PublishedKey{}, ErrDirectoryOff
	}
	if !publishable(user) {
		return PublishedKey{}, ErrNotPublishable
	}
	email := normalizeEmail(user.Email)
	seg := fingerprintSegment(strings.TrimSpace(fingerprint))
	defer d.lockAddress(user.Email)()
	best, _, err := d.readAddress(ctx, email)
	if err != nil {
		return PublishedKey{}, err
	}
	var st stored
	found := false
	for fp, cand := range best {
		if fingerprintSegment(fp) == seg && cand.rec.Revoked == 0 {
			st, found = cand, true
		}
	}
	if !found {
		return PublishedKey{}, ErrKeyNotFound
	}
	now := d.now()
	if _, err := d.appendLog(ctx, "revoke", email, st.fp, now); err != nil {
		return PublishedKey{}, err
	}
	id := d.directoryID(email, st.fp)
	tomb := st.rec
	tomb.Publisher, tomb.Provider, tomb.Revoked = user.ID, providerOf(user.ID), now.Unix()
	tomb.Held, tomb.Attest, tomb.AttestAt = false, "", 0
	tomb = d.sign(tomb, id)
	data, err := json.Marshal(tomb)
	if err != nil {
		return PublishedKey{}, err
	}
	if err := d.Store.Save(ctx, id, data); err != nil {
		return PublishedKey{}, err
	}
	d.dropOtherNames(ctx, email, st.fp, id)
	return d.publishedKey(stored{id: id, rec: tomb, fp: st.fp, typ: st.typ, text: st.text}, email, true), nil
}

// Resign moves every record that a retired key signed or named to the current key: the record
// is verified (by the retired key; one that does not verify is left alone, not endorsed), written
// again under the current key's name with the current key's signature, and the old copy deleted.
// It is what finishes a rotation (GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV): when it reports no more
// records, the retired key can be removed from the list. It returns how many it moved. Nothing
// the publisher proved is touched (the proof flag, attestation and log number are copied), and
// nothing is logged: the log records publications and revocations, which this is not. Expired
// tombstones are dropped on the way.
func (d *AdapterDirectory) Resign(ctx context.Context) (int, error) {
	if d.Signer == nil {
		return 0, ErrDirectoryOff
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, directoryPrefix)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, m := range metas {
		if strings.HasPrefix(m.ID, popPrefix) {
			continue
		}
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return moved, err
		}
		var rec directoryRecord
		if json.Unmarshal(doc.Data, &rec) != nil || (rec.V != 2 && rec.V != 3) {
			continue
		}
		r, err := parsePublic(rec.Key)
		if err != nil {
			continue
		}
		email := normalizeEmail(rec.Email)
		oldPrefix := m.ID[:strings.LastIndex(m.ID, "/")+1]
		if email == "" || oldPrefix+fingerprintSegment(r.Fingerprint) != m.ID {
			continue
		}
		current := d.addressPrefix(email) + fingerprintSegment(r.Fingerprint)
		st := stored{id: m.ID, rec: rec, fp: r.Fingerprint, typ: r.Type, text: r.Text}
		if !d.signedBy(st, email) {
			continue
		}
		if rec.Revoked > 0 && d.now().Sub(time.Unix(rec.Revoked, 0)) >= d.window() {
			_ = d.Store.Delete(ctx, m.ID)
			continue
		}
		if rec.V == 3 && rec.Kid == d.Signer.KeyID() && m.ID == current {
			continue // already current
		}
		next := d.sign(rec, current)
		data, err := json.Marshal(next)
		if err != nil {
			return moved, err
		}
		if err := d.Store.Save(ctx, current, data); err != nil {
			return moved, err
		}
		if m.ID != current {
			_ = d.Store.Delete(ctx, m.ID)
		}
		moved++
	}
	return moved, nil
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
		if strings.HasPrefix(m.ID, popPrefix) {
			var u struct {
				Exp int64 `json:"exp"`
			}
			expired = json.Unmarshal(doc.Data, &u) != nil || now.Unix() > u.Exp+int64(popGrace/time.Second)
		} else {
			var rec directoryRecord
			expired = json.Unmarshal(doc.Data, &rec) == nil && rec.Revoked > 0 && now.Sub(time.Unix(rec.Revoked, 0)) >= d.window()
		}
		if expired {
			if err := d.Store.Delete(ctx, m.ID); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return n, err
			}
			n++
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
	case errors.Is(err, ErrNotPublishable), errors.Is(err, ErrTooManyKeys), errors.Is(err, ErrDirectoryOff):
		return fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	}
	return err
}

func toDirectoryKey(k PublishedKey) docstore.DirectoryKey {
	return docstore.DirectoryKey{Key: k.Text, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified,
		Held: k.Held, Attested: k.Attested, Attestation: k.Attestation, Seq: k.Seq, Kid: k.Kid, Revoked: k.Revoked, RevokedAt: k.RevokedAt}
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
	opt := PublishOptions{Attestation: req.Attestation}
	if req.Proof != nil {
		opt.Challenge, opt.Nonce = req.Proof.Token, req.Proof.Nonce
	}
	k, err := s.d.PublishWith(ctx, user, r, opt)
	if err != nil {
		return docstore.DirectoryKey{}, refusal(err)
	}
	return toDirectoryKey(k), nil
}

func (s dirService) Revoke(ctx context.Context, user auth.User, fingerprint string) (docstore.DirectoryKey, error) {
	k, err := s.d.Revoke(ctx, user, fingerprint)
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
