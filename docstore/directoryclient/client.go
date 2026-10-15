// Package directoryclient is the client of the docstore's key directory (docstore.Server
// WithDirectory): publish my keys, revoke them, look a person up, enroll an owner key and watch
// the transparency log for my own address. It runs natively and as js/wasm in a browser (CI vets
// it with GOOS=js GOARCH=wasm). Besides net/http, age and Ed25519 it depends on the docstore
// package (the wire types) and docstore/encrypt (the message formats and the audit), which pull
// in their own dependencies: it is not a standalone minimal client. It does the client's half of
// every check, so that what it reports rests on what the server proved and not on what it said:
//
//   - Publish answers the proof-of-possession challenge with the key's own identity (an age
//     identity, an SSH one, a passkey's PRF identity), attests an ssh-ed25519 key's binding to the
//     address, and, when the address is enrolled, signs the operation with the owner key.
//   - Lookup re-checks every owner signature and attestation itself, and marks a row OwnerSigned
//     only if the signature verifies AND its key is one the client had pinned for the address
//     before (Pins; or the audited chain of its own address, State). The signature alone proves
//     nothing: the row carries the key it was signed with, so the server supplies both. The first
//     owner key seen for an address is remembered (trust on first use) but marks the row
//     OwnerUnpinned, not OwnerSigned; a different key later is OwnerChanged; a row that had owner
//     evidence and lost it, or an unsigned row of an address whose owner key is pinned, is
//     OwnerDowngraded.
//   - WatchLog fetches the log since the last position seen, checks the chain and the server's
//     signatures against pinned keys, checks the head against the one remembered (a log cut back
//     or rewritten is an error), audits the entries of my address against the owner-key rules,
//     and reports every entry for my address that I did not make: "someone published a key for
//     your address".
//
// The client holds no session: the caller gives it the signed-in session's token (Token), or an
// http.Client with the cookie. What the directory guarantees, and what a server holding its key
// can still do, is in docs/encryption-and-passkeys.md, step 4b.
package directoryclient

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// Client talks to a docstore server's /directory routes.
type Client struct {
	// BaseURL is the server ("https://host", or "" / "/api" for the same origin in a browser).
	// http is accepted only for a loopback host (localhost, 127.0.0.1, ::1): over plain http every
	// answer, the pseudonym and the log included, could be rewritten on the way.
	BaseURL string
	// HTTP is the client to use (http.DefaultClient when nil).
	HTTP *http.Client
	// Token returns the signed-in session's token, sent as a bearer token; nil sends none (the
	// HTTP client's cookie jar may carry the session).
	Token func() string
	// LogKeys are the directory's log public keys by key ID, pinned out of band. WatchLog refuses
	// to run without them unless TrustServerKeys is set.
	LogKeys map[string]ed25519.PublicKey
	// TrustServerKeys lets WatchLog take the keys the server states for itself. That checks the
	// chain and that it is consistent, not that the server is the one you meant: pin LogKeys.
	TrustServerKeys bool
	// Witnesses are the public keys of the witnesses this client trusts to cosign the log, by
	// name, pinned out of band; with WitnessQuorum they let WatchLog tell a split view (the
	// server showing different people different logs) from the one log everybody is shown.
	Witnesses map[string]ed25519.PublicKey
	// WitnessQuorum is how many of Witnesses must have cosigned the newest cosigned entry for
	// WatchLog to accept the log (0: witnesses are not asked). WatchLog then also refuses a log
	// that ends before the cosigned entry or has another entry there (ErrSplitView).
	WitnessQuorum int
	// Pins holds the owner keys somebody confirmed for an address (the host, the user out of band,
	// or this client by enrolling a key itself): Lookup marks a row signed by one OwnerPinned. It
	// never learns a key from a lookup (trust on first use is kept in State.Seen and marks
	// OwnerFirstSeen only). nil: nothing is pinned.
	Pins OwnerPins
	// State, if set, is told the log numbers of the operations this client makes, so WatchLog
	// does not report them as somebody else's.
	State *WatchState
	// RequireOwner makes Lookup report ErrUnenrolled (with the rows, for display) when the address
	// has no owner key: such an address has nothing to compare an entry's number or a key's origin
	// with, so a host that needs more than the server's word must not encrypt to it without the
	// person confirming the key's fingerprint another way.
	RequireOwner bool
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Error is a refusal by the server. Is maps it to the docstore sentinel errors, so
// errors.Is(err, docstore.ErrDirectoryOwnerRequired) and the like work.
type Error struct {
	Status int
	Body   string
}

// maxErrorBody is how much of a refusal's body an Error keeps: a server (or whatever answers
// for it) cannot make the client hold or print more.
const maxErrorBody = 4 << 10

func (e *Error) Error() string { return fmt.Sprintf("directory: server said %d: %s", e.Status, e.Body) }

// Is implements errors.Is for the docstore directory errors.
func (e *Error) Is(target error) bool {
	b := strings.ToLower(e.Body)
	switch target {
	case docstore.ErrDirectoryOwnerRequired:
		return e.Status == http.StatusPreconditionRequired && strings.Contains(b, "owner")
	case docstore.ErrDirectoryEnrollmentRequired:
		return e.Status == http.StatusPreconditionRequired && strings.Contains(b, "enroll an owner key")
	case docstore.ErrDirectoryProofRequired:
		return e.Status == http.StatusPreconditionRequired && strings.Contains(b, "proof")
	case docstore.ErrDirectoryBadOwnerSig:
		return e.Status == http.StatusForbidden && strings.Contains(b, "owner signature")
	case docstore.ErrDirectoryBadProof:
		return e.Status == http.StatusForbidden && strings.Contains(b, "proof of possession")
	case docstore.ErrDirectoryRevoked:
		return e.Status == http.StatusConflict && strings.Contains(b, "revoked")
	case docstore.ErrDirectoryEnrolled:
		return e.Status == http.StatusConflict && strings.Contains(b, "already has an owner key")
	case docstore.ErrDirectoryNotEnrolled:
		return e.Status == http.StatusConflict && strings.Contains(b, "no owner key")
	case docstore.ErrDirectoryResetWait:
		return e.Status == http.StatusConflict && strings.Contains(b, "reset")
	case docstore.ErrDirectoryNotFound:
		return e.Status == http.StatusNotFound
	case docstore.ErrDirectoryBusy:
		return e.Status == http.StatusServiceUnavailable
	case docstore.ErrDirectoryRefused:
		return e.Status == http.StatusForbidden
	}
	return false
}

// ErrInsecureBaseURL is returned for a BaseURL that is plain http to a host that is not the
// local machine.
var ErrInsecureBaseURL = errors.New("directoryclient: BaseURL must be https (http is accepted for localhost only)")

// checkBaseURL refuses a BaseURL that would send the session token and read the log over
// unprotected http. "" and a path ("/api": the page's own origin in a browser) are left to the
// page's origin.
func checkBaseURL(base string) error {
	if base == "" || strings.HasPrefix(base, "/") {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("directoryclient: BaseURL %q is not an https URL", base)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		h := strings.ToLower(u.Hostname())
		if h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "127.0.0.1" || h == "::1" {
			return nil
		}
	}
	return ErrInsecureBaseURL
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if err := checkBaseURL(c.BaseURL); err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != nil {
		if t := c.Token(); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		if len(data) > maxErrorBody {
			data = data[:maxErrorBody]
		}
		return &Error{Status: res.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("directory: unreadable answer: %w", err)
		}
	}
	return nil
}

// Head fetches the signed head of the log and checks its signature against the pinned keys.
func (c *Client) Head(ctx context.Context) (docstore.DirectoryHead, error) {
	var h docstore.DirectoryHead
	if err := c.do(ctx, http.MethodGet, "/directory/log/head", nil, &h); err != nil {
		return h, err
	}
	if c.LogKeys != nil {
		if err := encrypt.VerifyHead(h, c.LogKeys); err != nil {
			return h, err
		}
	}
	return h, nil
}

// position is the log position an owner signature names: the head now.
func (c *Client) position(ctx context.Context) (string, error) {
	h, err := c.Head(ctx)
	if err != nil {
		return "", err
	}
	return encrypt.FormatLogPosition(h.Seq, h.Hash), nil
}

// note records an operation of this client: the number is the server's, the rest is what the
// client itself did (see MadeEntry).
func (c *Client) note(seq int64, op, fingerprint, ownerSig string) {
	if c.State != nil && seq > 0 {
		c.State.NoteMade(MadeEntry{Seq: seq, Op: op, Fingerprint: fingerprint, OwnerSig: ownerSig})
	}
}

// OwnerKey is an owner signing key, held by the person (never by the server): make one with
// GenerateOwnerKey, keep it (a browser stores it, a CLI keeps a file), and give it to Publish,
// Revoke and the enrollment calls.
type OwnerKey = ed25519.PrivateKey

// OwnerSigner is what signs for an owner key: an OwnerKey, or anything else that is a crypto.Signer
// with an ed25519.PublicKey, such as a browser key that cannot be copied out. The client never needs
// the key's bytes, only its signatures.
type OwnerSigner = crypto.Signer

// ownerSet says an OwnerSigner was given: not nil, and not an empty OwnerKey held in an interface.
func ownerSet(s OwnerSigner) bool {
	if s == nil {
		return false
	}
	if k, ok := s.(ed25519.PrivateKey); ok {
		return len(k) > 0
	}
	return true
}

// publicOf is the public half of s.
func publicOf(s OwnerSigner) (ed25519.PublicKey, error) {
	pub, ok := s.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("directoryclient: the owner key is not an Ed25519 key")
	}
	return pub, nil
}

// GenerateOwnerKey makes an owner signing key.
func GenerateOwnerKey() (OwnerKey, error) {
	_, priv, err := encrypt.GenerateOwnerKey()
	return priv, err
}

// ExportOwnerKey is the form an owner key is stored in (base64 of the 32-byte seed).
func ExportOwnerKey(k OwnerKey) string { return base64.StdEncoding.EncodeToString(k.Seed()) }

// ImportOwnerKey reads ExportOwnerKey's output.
func ImportOwnerKey(s string) (OwnerKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("directoryclient: not an owner key")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// PublishRequest is what Publish needs.
type PublishRequest struct {
	// Email is the signed-in account's address (the owner signature and the attestation name it).
	Email string
	// Key is the public key to publish ("age1..." or an ssh public key line); Label what to call
	// it. With SSHPrivateKey set, Key may be empty: the public half of that key is published.
	Key, Label string
	// Identity opens the proof-of-possession challenge: the age identity of Key (an age identity,
	// encrypt.ParseSSHIdentity's, encrypt.PRFIdentity's for a passkey). Nil sends no proof; a
	// server that requires one then answers 428.
	Identity age.Identity
	// SSHPrivateKey is an unencrypted ssh-ed25519 private key (PEM): the key attests its own
	// binding to the address. An age or passkey key cannot; the owner signature attests those.
	SSHPrivateKey []byte
	// Owner signs the operation when the address has an owner key (nil: the server refuses with
	// 428 if it does).
	Owner OwnerSigner
}

// Publish publishes a key for the signed-in account's address and returns the server's row.
func (c *Client) Publish(ctx context.Context, r PublishRequest) (docstore.DirectoryKey, error) {
	req := docstore.DirectoryPublish{Key: strings.TrimSpace(r.Key), Label: r.Label}
	if len(r.SSHPrivateKey) > 0 {
		pub, att, err := encrypt.AttestSSH(r.SSHPrivateKey, r.Email, c.now())
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		if req.Key == "" {
			req.Key = pub
		} else if strings.Fields(req.Key)[0] != strings.Fields(pub)[0] || len(strings.Fields(req.Key)) < 2 || strings.Fields(req.Key)[1] != strings.Fields(pub)[1] {
			return docstore.DirectoryKey{}, errors.New("directoryclient: the ssh private key is not the key being published")
		}
		req.Attestation = &att
	}
	if req.Key == "" {
		return docstore.DirectoryKey{}, errors.New("directoryclient: no key to publish")
	}
	key, err := encrypt.ParsePublicKey(req.Key)
	if err != nil {
		return docstore.DirectoryKey{}, err
	}
	if r.Identity != nil {
		var ch docstore.DirectoryChallenge
		if err := c.do(ctx, http.MethodPost, "/directory/challenge", map[string]string{"key": req.Key}, &ch); err != nil {
			return docstore.DirectoryKey{}, err
		}
		proof, err := encrypt.OpenChallenge(ch, r.Identity)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		req.Proof = &proof
	}
	ownerSig := ""
	if ownerSet(r.Owner) {
		pos, err := c.position(ctx)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		s, err := encrypt.SignOwnerPublishWith(r.Owner, r.Email, key.Fingerprint, r.Label, c.now(), pos)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		req.Owner = &s
		ownerSig = s.Sig
	}
	var out docstore.DirectoryKey
	if err := c.do(ctx, http.MethodPost, "/directory/publish", req, &out); err != nil {
		return out, err
	}
	c.note(out.Seq, encrypt.OpPublish, key.Fingerprint, ownerSig)
	return out, nil
}

// Revoke removes a key (by fingerprint) from the signed-in account's address. owner signs it when
// the address has an owner key.
func (c *Client) Revoke(ctx context.Context, email, fingerprint string, owner OwnerSigner) (docstore.DirectoryKey, error) {
	var in *docstore.DirectoryRevoke
	ownerSig := ""
	if ownerSet(owner) {
		pos, err := c.position(ctx)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		s, err := encrypt.SignOwnerOpWith(owner, encrypt.OpRevoke, email, fingerprint, c.now(), pos)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		in = &docstore.DirectoryRevoke{Owner: &s}
		ownerSig = s.Sig
	}
	var out docstore.DirectoryKey
	var body any
	if in != nil {
		body = in
	}
	if err := c.do(ctx, http.MethodDelete, "/directory/keys/"+url.PathEscape(docstore.DirectoryFingerprintSegment(fingerprint)), body, &out); err != nil {
		return out, err
	}
	c.note(out.Seq, encrypt.OpRevoke, fingerprint, ownerSig)
	return out, nil
}

// Key is a lookup row with the client's own checks.
type Key struct {
	docstore.DirectoryKey
	// OwnerSigned is true only if the row's owner signature verifies here AND the key it was made
	// with is one the client holds for the address from before this lookup: a key it pinned
	// (Client.Pins: from an earlier lookup, Enroll, or the host's own record) or, for its own
	// address, one in the chain WatchLog audited (State.OwnerKeys). docstore's own OwnerSigned
	// flag is the server's word; this one is not. A signature under a key the row itself carries
	// proves nothing about the address's owner (the server supplies both), so such a row is
	// OwnerUnpinned instead.
	OwnerSigned bool
	// OwnerPinned: the owner key in the row is one that was confirmed for the address (OwnerSigned
	// says as much): pinned by the host or the user (Client.Pins), enrolled by this client itself,
	// or in the chain WatchLog audited for the client's own address. OwnerUnpinned: the signature
	// verifies under the key in the row, but nothing the client held before ties that key to the
	// address's owner: no confirmed pin existed (the key is now remembered in State, so the next
	// lookup can tell a change; this lookup's answer is the server's word). OwnerFirstSeen: the
	// same key an earlier lookup showed first (trust on first use): consistent over time, but still
	// nobody confirmed it, so it is not OwnerSigned and not OwnerPinned: a server that showed the
	// wrong key the first time shows the same wrong key every time. Confirm the key out of band
	// (Pins.Set) or pin it from the owner's own audit. Components show OwnerUnpinned and
	// OwnerFirstSeen rows as unconfirmed.
	OwnerPinned, OwnerUnpinned, OwnerFirstSeen bool
	// OwnerChanged: another key than the pinned one (or, with none pinned, than the first one seen,
	// earlier or in the same answer) signed the row: the address has a new owner
	// key (a rotation or a reset: check WatchLog's report) or somebody enrolled one. The row is not
	// OwnerSigned.
	OwnerChanged bool
	// OwnerDowngraded: the row lost the owner evidence it carried when last seen, or the address
	// has a pinned owner key and the row carries none: a server (or a record) that stopped
	// showing what it showed. A key published before the owner enrolled shows it too, until it is
	// published again with the owner's signature.
	OwnerDowngraded bool
	// SeqChanged: the row is not owner-signed and the log number of its entry is not the one an earlier
	// lookup showed for the same key. Nothing signs that number on an address without an owner key, so a
	// server can name another entry (another address's, for the same public key); it is also what a
	// republication after a revocation looks like. Enroll an owner key to close it; until then, treat the
	// row as the server's word.
	SeqChanged bool
	// AttestationVerified: the key's own attestation verifies here.
	AttestationVerified bool
}

// Lookup finds the keys published for an address and checks their owner signatures and
// attestations itself (see Key). It remembers, per address (State.Seen), which rows carried owner
// evidence, so that a row that loses it is flagged.
func (c *Client) Lookup(ctx context.Context, email string) ([]Key, error) {
	var res struct {
		Keys []docstore.DirectoryKey `json:"keys"`
	}
	if err := c.do(ctx, http.MethodGet, "/directory/lookup?email="+url.QueryEscape(email), nil, &res); err != nil {
		return nil, err
	}
	addr := strings.ToLower(strings.TrimSpace(email))
	var pinned string
	havePin := false
	if c.Pins != nil {
		pinned, havePin = c.Pins.Get(email)
	}
	audited := c.auditedOwnerKeys(addr) // the chain WatchLog derived for the client's own address
	var seen SeenAddress
	if c.State != nil && c.State.Seen != nil {
		seen = c.State.Seen[addr]
	}
	accepted := map[string]bool{}
	for _, pub := range seen.Accepted {
		accepted[pub] = true
	}
	wasSigned := map[string]bool{}
	for _, fp := range seen.Signed {
		wasSigned[fp] = true
	}
	out := make([]Key, 0, len(res.Keys))
	newSeqs := map[string]int64{}
	var nowSigned []string
	anyEvidence := false
	// ref is the owner key the rows are compared with when none is confirmed: the first one seen
	// (earlier, from State, or by a row of this answer). refRemembered says it comes from an earlier
	// lookup.
	ref, refRemembered := seen.FirstOwner, seen.FirstOwner != ""
	for _, k := range res.Keys {
		row := Key{DirectoryKey: k}
		if k.Attestation != nil && k.Verified {
			row.AttestationVerified = encrypt.VerifyAttestation(k.Key, email, *k.Attestation)
		}
		row.Attested = row.AttestationVerified
		evidence := k.OwnerSigned && k.Verified && !k.Revoked && encrypt.VerifyOwnerEvidence(email, k)
		if evidence {
			anyEvidence = true
			nowSigned = append(nowSigned, k.Fingerprint)
			switch {
			case audited[k.Owner.Pub] || (havePin && pinned == k.Owner.Pub):
				// Confirmed: the audited chain of the client's own address, or a key the host or
				// user pinned (or this client enrolled itself).
				row.OwnerSigned, row.OwnerPinned = true, true
			case havePin:
				row.OwnerChanged = true
			case ref == "":
				ref = k.Owner.Pub // first sight: remembered below, nothing confirms it
				row.OwnerUnpinned = true
			case ref != k.Owner.Pub && accepted[k.Owner.Pub]:
				row.OwnerFirstSeen = true // a key the user accepted after a rotation: remembered, not confirmed
			case ref != k.Owner.Pub:
				row.OwnerChanged = true // another key than the first one seen (earlier, or in this answer)
			case refRemembered:
				row.OwnerFirstSeen = true // the same key as an earlier lookup showed: still unconfirmed
			default:
				row.OwnerUnpinned = true
			}
		} else if !k.Revoked && (wasSigned[k.Fingerprint] || havePin || len(audited) > 0 || seen.FirstOwner != "") {
			// No evidence now: it had some before, or the address's owner key is known.
			row.OwnerDowngraded = true
		}
		row.DirectoryKey.OwnerSigned = row.OwnerSigned
		if !row.OwnerSigned && !k.Revoked && k.Seq > 0 {
			if first, ok := seen.Seqs[k.Fingerprint]; ok {
				row.SeqChanged = first != k.Seq
			} else if _, noted := newSeqs[k.Fingerprint]; !noted {
				newSeqs[k.Fingerprint] = k.Seq
			}
		}
		out = append(out, row)
	}
	if c.State != nil {
		if c.State.Seen == nil {
			c.State.Seen = map[string]SeenAddress{}
		}
		merged := seen
		merged.Enrolled = merged.Enrolled || anyEvidence
		// Trust on first use: the first owner key seen for an address is remembered here (in the
		// state the host keeps), not in Pins, which holds only keys somebody confirmed.
		if merged.FirstOwner == "" {
			merged.FirstOwner = ref
		}
		for _, fp := range nowSigned {
			if !wasSigned[fp] {
				merged.Signed = append(merged.Signed, fp)
			}
		}
		if len(newSeqs) > 0 { // the first number shown for a key stays the reference
			merged.Seqs = maps.Clone(merged.Seqs)
			if merged.Seqs == nil {
				merged.Seqs = map[string]int64{}
			}
			for fp, n := range newSeqs {
				merged.Seqs[fp] = n
			}
		}
		c.State.Seen[addr] = merged
	}
	if c.RequireOwner {
		for _, k := range out {
			if !k.Revoked && !k.OwnerEnrolled {
				return out, ErrUnenrolled
			}
		}
	}
	return out, nil
}

// ErrUnenrolled is returned by Lookup, with the rows, when Client.RequireOwner is set and the address
// has not enrolled an owner key.
var ErrUnenrolled = errors.New("directoryclient: the address has no owner key, so nothing but the server's word ties these keys to it")

// auditedOwnerKeys is the set of owner keys of the client's own address that WatchLog derived
// from the log (State.OwnerKeys), when email is that address: keys the client holds from the
// audited chain rather than from the server's answer of the moment.
func (c *Client) auditedOwnerKeys(email string) map[string]bool {
	if c.State == nil || c.State.Email == "" || c.State.Email != email {
		return nil
	}
	out := map[string]bool{}
	for _, k := range c.State.OwnerKeys {
		out[k.Pub] = true
	}
	return out
}

// Enrollment asks whether an address has an owner key.
func (c *Client) Enrollment(ctx context.Context, email string) (docstore.DirectoryEnrollment, error) {
	var e docstore.DirectoryEnrollment
	err := c.do(ctx, http.MethodGet, "/directory/enroll?email="+url.QueryEscape(email), nil, &e)
	return e, err
}

// enrollRequest builds the request and returns the signature the log entry will carry (the new
// key's enrollment, the authorizer's rotation, or the new key's acceptance for a reset).
func (c *Client) enrollRequest(ctx context.Context, email, mode string, owner, authorizer OwnerSigner) (docstore.DirectoryEnroll, error) {
	var ch docstore.DirectoryEnrollChallenge
	if err := c.do(ctx, http.MethodPost, "/directory/enroll/challenge", struct{}{}, &ch); err != nil {
		return docstore.DirectoryEnroll{}, err
	}
	pub, err := publicOf(owner)
	if err != nil {
		return docstore.DirectoryEnroll{}, err
	}
	fp := encrypt.OwnerFingerprint(pub)
	at := time.Unix(ch.Time, 0)
	req := docstore.DirectoryEnroll{Mode: mode, OwnerPub: encrypt.FormatOwnerPub(pub), Token: ch.Token, Time: ch.Time, Prev: ch.Prev}
	sign := func(by OwnerSigner, op string) (string, error) {
		s, err := encrypt.SignOwnerOpWith(by, op, email, fp, at, ch.Prev)
		return s.Sig, err
	}
	switch mode {
	case encrypt.OpEnroll:
		if req.Sig, err = sign(owner, encrypt.OpEnroll); err != nil {
			return docstore.DirectoryEnroll{}, err
		}
	case encrypt.OpRotate:
		if req.Sig, err = sign(authorizer, encrypt.OpRotate); err != nil {
			return docstore.DirectoryEnroll{}, err
		}
		if req.Proof, err = sign(owner, "accept"); err != nil {
			return docstore.DirectoryEnroll{}, err
		}
	case encrypt.OpReset:
		if req.Proof, err = sign(owner, "accept"); err != nil {
			return docstore.DirectoryEnroll{}, err
		}
	}
	return req, nil
}

func (c *Client) enroll(ctx context.Context, email, mode string, owner, authorizer OwnerSigner) (docstore.DirectoryEnrollment, error) {
	req, err := c.enrollRequest(ctx, email, mode, owner, authorizer)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	var e docstore.DirectoryEnrollment
	if err := c.do(ctx, http.MethodPost, "/directory/enroll", req, &e); err != nil {
		return e, err
	}
	sig := req.Sig
	if mode == encrypt.OpReset {
		sig = req.Proof
	}
	pub, err := publicOf(owner)
	if err != nil {
		return e, err
	}
	c.note(e.Seq, mode, encrypt.OwnerFingerprint(pub), sig)
	// Pin the key this client made and sent, never the one in the server's answer (which is the
	// server's word and could name another).
	if c.Pins != nil {
		c.Pins.Set(email, encrypt.FormatOwnerPub(pub))
	}
	return e, nil
}

// Enroll registers owner as the owner key of the signed-in account's address. The server needs
// the provider session and the new key's own signature over its challenge. This is trust on first
// use: whoever is signed in as the address first can enroll first, and the log shows it to
// everybody who watches the address.
func (c *Client) Enroll(ctx context.Context, email string, owner OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpEnroll, owner, nil)
}

// Rotate replaces the owner key: the new key is signed in by the current one.
func (c *Client) Rotate(ctx context.Context, email string, current, next OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpRotate, next, current)
}

// RequestReset asks to replace a lost owner key through the provider session. It is logged at
// once and takes effect only after the waiting period (24 hours), during which the owner key can
// still cancel it (CancelReset).
func (c *Client) RequestReset(ctx context.Context) (docstore.DirectoryEnrollment, error) {
	var e docstore.DirectoryEnrollment
	err := c.do(ctx, http.MethodPost, "/directory/enroll/reset", struct{}{}, &e)
	if err == nil {
		c.note(e.Seq, encrypt.OpResetRequest, "", "")
	}
	return e, err
}

// CompleteReset installs next as the owner key once the waiting period of RequestReset is over.
func (c *Client) CompleteReset(ctx context.Context, email string, next OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpReset, next, nil)
}

// CancelReset ends a pending reset, signed by the current owner key.
func (c *Client) CancelReset(ctx context.Context, email string, owner OwnerSigner) (docstore.DirectoryEnrollment, error) {
	pos, err := c.position(ctx)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	s, err := encrypt.SignOwnerOpWith(owner, encrypt.OpResetCancel, email, "", c.now(), pos)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	var e docstore.DirectoryEnrollment
	if err := c.do(ctx, http.MethodPost, "/directory/enroll/cancel", docstore.DirectoryRevoke{Owner: &s}, &e); err != nil {
		return e, err
	}
	c.note(e.Seq, encrypt.OpResetCancel, "", s.Sig)
	return e, nil
}

// LogAddress is the signed-in owner's pseudonym in the log (and under retired directory keys).
func (c *Client) LogAddress(ctx context.Context) (docstore.DirectoryLogAddress, error) {
	var a docstore.DirectoryLogAddress
	err := c.do(ctx, http.MethodGet, "/directory/log/address", nil, &a)
	return a, err
}

// OwnerPins holds the owner key confirmed for an address (pinned by the host or the user, or the
// key this client enrolled); Lookup flags a different one.
type OwnerPins interface {
	Get(email string) (pub string, ok bool)
	Set(email, pub string)
}

// MemoryPins is an OwnerPins in memory; a host that keeps pins across runs implements the
// interface over its own storage.
type MemoryPins map[string]string

// Get implements OwnerPins.
func (m MemoryPins) Get(email string) (string, bool) {
	p, ok := m[strings.ToLower(strings.TrimSpace(email))]
	return p, ok
}

// Set implements OwnerPins.
func (m MemoryPins) Set(email, pub string) { m[strings.ToLower(strings.TrimSpace(email))] = pub }

// AcceptOwner is the user's answer to "this address shows another owner key than the first one I
// saw" (Key.OwnerChanged): after the user has checked the new key with its owner by another way, it
// is remembered as accepted, and rows signed by it stop being flagged as changed. The first key
// seen stays accepted too (an owner who rotated still has rows of the old key). It changes only
// what this client remembers: a key somebody pinned (Pins) or the audited chain of the client's own
// address is not touched. It is an error when the client keeps no State or the address was never
// looked up.
func (c *Client) AcceptOwner(email, ownerPub string) error {
	addr := strings.ToLower(strings.TrimSpace(email))
	if c.State == nil {
		return errors.New("directoryclient: AcceptOwner needs a State to remember the key in")
	}
	seen, ok := c.State.Seen[addr]
	if !ok {
		return fmt.Errorf("directoryclient: %s was never looked up", email)
	}
	if ownerPub == "" {
		return errors.New("directoryclient: AcceptOwner needs the owner key to accept")
	}
	if !slices.Contains(seen.Accepted, ownerPub) {
		seen.Accepted = append(seen.Accepted, ownerPub)
	}
	c.State.Seen[addr] = seen
	return nil
}
