// Package directoryclient is the client of the docstore's key directory (docstore.Server
// WithDirectory): publish my keys, revoke them, look a person up, enroll an owner key and watch
// the transparency log for my own address. It runs natively and as js/wasm in a browser (it is
// net/http, age and Ed25519 only), and does the client's half of every check, so that what it
// reports rests on what the server proved and not on what it said:
//
//   - Publish answers the proof-of-possession challenge with the key's own identity (an age
//     identity, an SSH one, a passkey's PRF identity), attests an ssh-ed25519 key's binding to the
//     address, and, when the address is enrolled, signs the operation with the owner key.
//   - Lookup re-checks every owner signature and attestation itself (a row is OwnerSigned only if
//     VerifyOwnerEvidence says so) and pins the owner key it sees first for an address (trust on
//     first use), flagging a different one.
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
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// Client talks to a docstore server's /directory routes.
type Client struct {
	// BaseURL is the server ("https://host", or "" / "/api" for the same origin in a browser).
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
	// Pins remembers the owner key first seen for an address (nil: Lookup does not pin).
	Pins OwnerPins
	// State, if set, is told the log numbers of the operations this client makes, so WatchLog
	// does not report them as somebody else's.
	State *WatchState
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

func (e *Error) Error() string { return fmt.Sprintf("directory: server said %d: %s", e.Status, e.Body) }

// Is implements errors.Is for the docstore directory errors.
func (e *Error) Is(target error) bool {
	b := strings.ToLower(e.Body)
	switch target {
	case docstore.ErrDirectoryOwnerRequired:
		return e.Status == http.StatusPreconditionRequired && strings.Contains(b, "owner")
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

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
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

func (c *Client) note(seq int64) {
	if c.State != nil && seq > 0 {
		c.State.NoteMade(seq)
	}
}

// OwnerKey is an owner signing key, held by the person (never by the server): make one with
// GenerateOwnerKey, keep it (a browser stores it, a CLI keeps a file), and give it to Publish,
// Revoke and the enrollment calls.
type OwnerKey = ed25519.PrivateKey

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
	Owner OwnerKey
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
	if r.Owner != nil {
		pos, err := c.position(ctx)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		s := encrypt.SignOwnerOp(r.Owner, encrypt.OpPublish, r.Email, key.Fingerprint, c.now(), pos)
		req.Owner = &s
	}
	var out docstore.DirectoryKey
	if err := c.do(ctx, http.MethodPost, "/directory/publish", req, &out); err != nil {
		return out, err
	}
	c.note(out.Seq)
	return out, nil
}

// Revoke removes a key (by fingerprint) from the signed-in account's address. owner signs it when
// the address has an owner key.
func (c *Client) Revoke(ctx context.Context, email, fingerprint string, owner OwnerKey) (docstore.DirectoryKey, error) {
	var in *docstore.DirectoryRevoke
	if owner != nil {
		pos, err := c.position(ctx)
		if err != nil {
			return docstore.DirectoryKey{}, err
		}
		s := encrypt.SignOwnerOp(owner, encrypt.OpRevoke, email, fingerprint, c.now(), pos)
		in = &docstore.DirectoryRevoke{Owner: &s}
	}
	var out docstore.DirectoryKey
	var body any
	if in != nil {
		body = in
	}
	if err := c.do(ctx, http.MethodDelete, "/directory/keys/"+url.PathEscape(docstore.DirectoryFingerprintSegment(fingerprint)), body, &out); err != nil {
		return out, err
	}
	c.note(out.Seq)
	return out, nil
}

// Key is a lookup row with the client's own checks.
type Key struct {
	docstore.DirectoryKey
	// OwnerSigned is true only if the server says so and the owner signature in the row verifies
	// here (docstore's OwnerSigned flag is the server's word; this one is not).
	OwnerSigned bool
	// OwnerPinned: the owner key in the row is the one pinned for the address, or was pinned just
	// now (trust on first use). OwnerChanged: another key is pinned for it: the address has a new
	// owner key (a rotation or a reset: check WatchLog's report) or somebody enrolled one.
	OwnerPinned, OwnerChanged bool
	// AttestationVerified: the key's own attestation verifies here.
	AttestationVerified bool
}

// Lookup finds the keys published for an address and checks their owner signatures and
// attestations itself.
func (c *Client) Lookup(ctx context.Context, email string) ([]Key, error) {
	var res struct {
		Keys []docstore.DirectoryKey `json:"keys"`
	}
	if err := c.do(ctx, http.MethodGet, "/directory/lookup?email="+url.QueryEscape(email), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Key, 0, len(res.Keys))
	for _, k := range res.Keys {
		row := Key{DirectoryKey: k}
		if k.Attestation != nil && k.Verified {
			row.AttestationVerified = encrypt.VerifyAttestation(k.Key, email, *k.Attestation)
		}
		row.DirectoryKey.Attested = row.AttestationVerified
		if k.OwnerSigned && k.Verified && !k.Revoked && encrypt.VerifyOwnerEvidence(email, k) {
			row.OwnerSigned = true
			if c.Pins != nil {
				pinned, ok := c.Pins.Get(email)
				switch {
				case !ok:
					c.Pins.Set(email, k.Owner.Pub)
					row.OwnerPinned = true
				case pinned == k.Owner.Pub:
					row.OwnerPinned = true
				default:
					row.OwnerChanged = true
				}
			}
		}
		row.DirectoryKey.OwnerSigned = row.OwnerSigned
		out = append(out, row)
	}
	return out, nil
}

// Enrollment asks whether an address has an owner key.
func (c *Client) Enrollment(ctx context.Context, email string) (docstore.DirectoryEnrollment, error) {
	var e docstore.DirectoryEnrollment
	err := c.do(ctx, http.MethodGet, "/directory/enroll?email="+url.QueryEscape(email), nil, &e)
	return e, err
}

func (c *Client) enrollRequest(ctx context.Context, email, mode string, owner, authorizer OwnerKey) (docstore.DirectoryEnroll, error) {
	var ch docstore.DirectoryEnrollChallenge
	if err := c.do(ctx, http.MethodPost, "/directory/enroll/challenge", struct{}{}, &ch); err != nil {
		return docstore.DirectoryEnroll{}, err
	}
	pub := owner.Public().(ed25519.PublicKey)
	fp := encrypt.OwnerFingerprint(pub)
	at := time.Unix(ch.Time, 0)
	req := docstore.DirectoryEnroll{Mode: mode, OwnerPub: encrypt.FormatOwnerPub(pub), Token: ch.Token, Time: ch.Time, Prev: ch.Prev}
	switch mode {
	case encrypt.OpEnroll:
		req.Sig = encrypt.SignOwnerOp(owner, encrypt.OpEnroll, email, fp, at, ch.Prev).Sig
	case encrypt.OpRotate:
		req.Sig = encrypt.SignOwnerOp(authorizer, encrypt.OpRotate, email, fp, at, ch.Prev).Sig
		req.Proof = encrypt.SignOwnerOp(owner, "accept", email, fp, at, ch.Prev).Sig
	case encrypt.OpReset:
		req.Proof = encrypt.SignOwnerOp(owner, "accept", email, fp, at, ch.Prev).Sig
	}
	return req, nil
}

func (c *Client) enroll(ctx context.Context, email, mode string, owner, authorizer OwnerKey) (docstore.DirectoryEnrollment, error) {
	req, err := c.enrollRequest(ctx, email, mode, owner, authorizer)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	var e docstore.DirectoryEnrollment
	if err := c.do(ctx, http.MethodPost, "/directory/enroll", req, &e); err != nil {
		return e, err
	}
	c.note(e.Seq)
	if c.Pins != nil && len(e.Keys) > 0 {
		c.Pins.Set(email, e.Keys[len(e.Keys)-1].Pub)
	}
	return e, nil
}

// Enroll registers owner as the owner key of the signed-in account's address. The server needs
// the provider session and the new key's own signature over its challenge. This is trust on first
// use: whoever is signed in as the address first can enroll first, and the log shows it to
// everybody who watches the address.
func (c *Client) Enroll(ctx context.Context, email string, owner OwnerKey) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpEnroll, owner, nil)
}

// Rotate replaces the owner key: the new key is signed in by the current one.
func (c *Client) Rotate(ctx context.Context, email string, current, next OwnerKey) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpRotate, next, current)
}

// RequestReset asks to replace a lost owner key through the provider session. It is logged at
// once and takes effect only after the waiting period (24 hours), during which the owner key can
// still cancel it (CancelReset).
func (c *Client) RequestReset(ctx context.Context) (docstore.DirectoryEnrollment, error) {
	var e docstore.DirectoryEnrollment
	err := c.do(ctx, http.MethodPost, "/directory/enroll/reset", struct{}{}, &e)
	if err == nil {
		c.note(e.Seq)
	}
	return e, err
}

// CompleteReset installs next as the owner key once the waiting period of RequestReset is over.
func (c *Client) CompleteReset(ctx context.Context, email string, next OwnerKey) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpReset, next, nil)
}

// CancelReset ends a pending reset, signed by the current owner key.
func (c *Client) CancelReset(ctx context.Context, email string, owner OwnerKey) (docstore.DirectoryEnrollment, error) {
	pos, err := c.position(ctx)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	s := encrypt.SignOwnerOp(owner, encrypt.OpResetCancel, email, "", c.now(), pos)
	var e docstore.DirectoryEnrollment
	if err := c.do(ctx, http.MethodPost, "/directory/enroll/cancel", docstore.DirectoryRevoke{Owner: &s}, &e); err != nil {
		return e, err
	}
	c.note(e.Seq)
	return e, nil
}

// LogAddress is the signed-in owner's pseudonym in the log (and under retired directory keys).
func (c *Client) LogAddress(ctx context.Context) (docstore.DirectoryLogAddress, error) {
	var a docstore.DirectoryLogAddress
	err := c.do(ctx, http.MethodGet, "/directory/log/address", nil, &a)
	return a, err
}

// OwnerPins remembers which owner key an address had when it was first seen (trust on first
// use); Lookup flags a different one.
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
