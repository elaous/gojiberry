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
	// BaseURL is the server, such as https://host.
	BaseURL string
	// HTTP is the client to use (http.DefaultClient when nil).
	HTTP *http.Client
	// Token returns the signed-in session's token.
	Token func() string
	// LogKeys are the directory's log public keys.
	LogKeys map[string]ed25519.PublicKey
	// TrustServerKeys lets WatchLog take the keys from the server.
	TrustServerKeys bool
	// Witnesses are the public keys of the witnesses.
	Witnesses map[string]ed25519.PublicKey
	// WitnessQuorum is how many witnesses must sign a head.
	WitnessQuorum int
	// Pins holds the owner keys someone confirmed.
	Pins OwnerPins
	// State, if set, is told the log numbers seen.
	State *WatchState
	// RequireOwner makes Lookup refuse unenrolled addresses.
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

// Error is a refusal by the server.
type Error struct {
	Status int
	Body   string
}

// a server cannot make the client hold or print more
const maxErrorBody = 4 << 10

func (e *Error) Error() string { return fmt.Sprintf("directory: server said %d: %s", e.Status, e.Body) }

// Is implements errors.Is for the directory's errors.
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

// ErrInsecureBaseURL is returned for a non-HTTPS BaseURL.
var ErrInsecureBaseURL = errors.New("directoryclient: BaseURL must be https (http is accepted for localhost only)")

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

func (c *Client) position(ctx context.Context) (string, error) {
	h, err := c.Head(ctx)
	if err != nil {
		return "", err
	}
	return encrypt.FormatLogPosition(h.Seq, h.Hash), nil
}

func (c *Client) note(seq int64, op, fingerprint, ownerSig string) {
	if c.State != nil && seq > 0 {
		c.State.NoteMade(MadeEntry{Seq: seq, Op: op, Fingerprint: fingerprint, OwnerSig: ownerSig})
	}
}

// OwnerKey is an owner signing key held by the client.
type OwnerKey = ed25519.PrivateKey

// OwnerSigner is what signs for an owner key.
type OwnerSigner = crypto.Signer

func ownerSet(s OwnerSigner) bool {
	if s == nil {
		return false
	}
	if k, ok := s.(ed25519.PrivateKey); ok {
		return len(k) > 0
	}
	return true
}

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

// ExportOwnerKey is the form an owner key is stored in.
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
	// Email is the signed-in account's address.
	Email string
	// Key is the public key to publish.
	Key, Label string
	// Identity opens the proof-of-possession challenge.
	Identity age.Identity
	// SSHPrivateKey is an unencrypted ssh-ed25519 private key.
	SSHPrivateKey []byte
	// nil: the server refuses with 428 if an owner key exists
	Owner OwnerSigner
}

// Publish publishes a key for the signed-in account.
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

// Revoke removes a key, by fingerprint, from the directory.
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
	// OwnerSigned is true only if the row's owner signature verifies.
	OwnerSigned bool
	// OwnerPinned: the row's owner key is the pinned one.
	OwnerPinned, OwnerUnpinned, OwnerFirstSeen bool
	// OwnerChanged: another key than the pinned one signed.
	OwnerChanged bool
	// OwnerDowngraded: the row lost its owner signature.
	OwnerDowngraded bool
	// SeqChanged: the row is not owner-signed.
	SeqChanged bool
	// AttestationVerified: the key's own attestation verifies.
	AttestationVerified bool
}

// Lookup finds the keys published for an address.
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
	// pin the key this client made, never the server's answer
	if c.Pins != nil {
		pinned, havePin = c.Pins.Get(email)
	}
	audited := c.auditedOwnerKeys(addr)
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
				row.OwnerSigned, row.OwnerPinned = true, true
			case havePin:
				row.OwnerChanged = true
			case ref == "":
				ref = k.Owner.Pub
				row.OwnerUnpinned = true
			case ref != k.Owner.Pub && accepted[k.Owner.Pub]:
				row.OwnerFirstSeen = true
			case ref != k.Owner.Pub:
				row.OwnerChanged = true
			case refRemembered:
				row.OwnerFirstSeen = true
			default:
				row.OwnerUnpinned = true
			}
		} else if !k.Revoked && (wasSigned[k.Fingerprint] || havePin || len(audited) > 0 || seen.FirstOwner != "") {
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
		// trust on first use: remembered in State, not in Pins
		if merged.FirstOwner == "" {
			merged.FirstOwner = ref
		}
		for _, fp := range nowSigned {
			if !wasSigned[fp] {
				merged.Signed = append(merged.Signed, fp)
			}
		}
		if len(newSeqs) > 0 {
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

// ErrUnenrolled is returned by Lookup for an unenrolled address.
var ErrUnenrolled = errors.New("directoryclient: the address has no owner key, so nothing but the server's word ties these keys to it")

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
	if c.Pins != nil {
		c.Pins.Set(email, encrypt.FormatOwnerPub(pub))
	}
	return e, nil
}

// Enroll registers owner as the address's owner key.
func (c *Client) Enroll(ctx context.Context, email string, owner OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpEnroll, owner, nil)
}

// Rotate replaces the owner key.
func (c *Client) Rotate(ctx context.Context, email string, current, next OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpRotate, next, current)
}

// RequestReset asks to replace a lost owner key.
func (c *Client) RequestReset(ctx context.Context) (docstore.DirectoryEnrollment, error) {
	var e docstore.DirectoryEnrollment
	err := c.do(ctx, http.MethodPost, "/directory/enroll/reset", struct{}{}, &e)
	if err == nil {
		c.note(e.Seq, encrypt.OpResetRequest, "", "")
	}
	return e, err
}

// CompleteReset installs next as the owner key.
func (c *Client) CompleteReset(ctx context.Context, email string, next OwnerSigner) (docstore.DirectoryEnrollment, error) {
	return c.enroll(ctx, email, encrypt.OpReset, next, nil)
}

// CancelReset ends a pending reset.
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

// LogAddress is the signed-in owner's pseudonym.
func (c *Client) LogAddress(ctx context.Context) (docstore.DirectoryLogAddress, error) {
	var a docstore.DirectoryLogAddress
	err := c.do(ctx, http.MethodGet, "/directory/log/address", nil, &a)
	return a, err
}

// OwnerPins holds the owner key confirmed for an address.
type OwnerPins interface {
	Get(email string) (pub string, ok bool)
	Set(email, pub string)
}

// MemoryPins is an OwnerPins in memory.
type MemoryPins map[string]string

// Get implements OwnerPins.
func (m MemoryPins) Get(email string) (string, bool) {
	p, ok := m[strings.ToLower(strings.TrimSpace(email))]
	return p, ok
}

// Set implements OwnerPins.
func (m MemoryPins) Set(email, pub string) { m[strings.ToLower(strings.TrimSpace(email))] = pub }

// AcceptOwner is the user's answer to a new owner key.
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
