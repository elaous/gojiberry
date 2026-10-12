// Package dirclient is the showcase's view of a docstore server's key directory
// (docstore.Server.WithDirectory). The routes, the proof of possession and the checks of what the
// server answers are directoryclient's; this package is the small interface the page uses and the
// failures it tells the person about, so the page does not depend on the client's shape. It has no
// browser code (net/http runs over fetch in the page), so it is tested natively against the demo
// server.
package dirclient

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/directoryclient"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// OwnerKey is the Ed25519 key that signs the entries of one address in the directory. Whoever holds
// it can publish and revoke for the address, so it is kept and backed up like a password.
type OwnerKey = directoryclient.OwnerKey

// OwnerSigner signs for an owner key: an OwnerKey, or a key the page cannot copy out (a crypto.Signer
// over an Ed25519 public key).
type OwnerSigner = directoryclient.OwnerSigner

// GenerateOwnerKey makes a new owner key.
func GenerateOwnerKey() (OwnerKey, error) { return directoryclient.GenerateOwnerKey() }

// ExportOwnerKey is the key as text to keep (a backup); ImportOwnerKey reads it back.
func ExportOwnerKey(k OwnerKey) string { return directoryclient.ExportOwnerKey(k) }

// ImportOwnerKey reads what ExportOwnerKey wrote.
func ImportOwnerKey(s string) (OwnerKey, error) {
	return directoryclient.ImportOwnerKey(strings.TrimSpace(s))
}

// OwnerFingerprint is the short name of an owner key, what the person compares.
func OwnerFingerprint(k OwnerSigner) string {
	return encrypt.OwnerFingerprint(k.Public().(ed25519.PublicKey))
}

// WatchResult is what a run of Watch found.
type WatchResult struct {
	// HeadSeq is the number of the log entry the run was checked up to; New how many entries about
	// the address it read.
	HeadSeq int64
	New     int
	// Alerts are what the person did not do: entries made by somebody else, entries that break the
	// owner rules, and entries dated ahead of this device's clock. Each has a kind, so the page can
	// word the ones that matter most (a reset of the owner key) in its own way, and a text in English.
	Alerts []Alert
	// Filtered: the server showed only the entries about the address (enroll an owner key to read
	// the whole log). AddressUnconfirmed: no entry of this device confirms the log name of the
	// address yet; publish or enroll, then watch again.
	Filtered, AddressUnconfirmed bool
}

// AlertKind says what sort of thing an Alert reports.
type AlertKind string

const (
	// AlertEntry is an entry about the address that the person did not make (a published or revoked key,
	// an enrollment, a rotation).
	AlertEntry AlertKind = "entry"
	// AlertReset is a completed reset of the owner key: the key before it no longer signs.
	AlertReset AlertKind = "reset"
	// AlertResetRequest is a request to reset the owner key, which takes effect after the waiting
	// period unless the owner cancels it.
	AlertResetRequest AlertKind = "reset-request"
	// AlertViolation is an entry that breaks the owner rules of an enrolled address.
	AlertViolation AlertKind = "violation"
)

// Alert is one thing watching found.
type Alert struct {
	Kind AlertKind
	// Seq is the number of the log entry.
	Seq int64
	// Text says it in English, with the entry number.
	Text string
}

// ErrBadState: the saved watch state could not be read and was dropped.
var ErrBadState = errors.New("the saved log position could not be read: the next look is a first look")

// ErrNoLogKeys: no log keys are pinned, so the log cannot be checked.
var ErrNoLogKeys = errors.New("no log keys are pinned for this directory")

// ErrLogRewritten: the log is shorter than what was seen, or has a different entry where one was
// seen: the server cut it back or rewrote it.
var ErrLogRewritten = errors.New("the log was cut back or rewritten since the last look")

// LogKeyFingerprint is how a log key (base64, as ServerLogKeys states it) is shown to be compared:
// the first 16 hex characters of its SHA-256 in groups of four, "" if it is not an Ed25519 key.
func LogKeyFingerprint(b64 string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + " " + h[4:8] + " " + h[8:12] + " " + h[12:16]
}

// NormalizeFingerprint makes a fingerprint typed or pasted by a person comparable: lower case, with
// spaces, colons and dashes removed.
func NormalizeFingerprint(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', ':', '-', '\t', '\n', '\r':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

// OwnerPubFingerprint is the fingerprint of an owner public key as shown in Key.OwnerPub, "" if it
// cannot be read.
func OwnerPubFingerprint(pub string) string {
	k, err := encrypt.ParseOwnerPub(pub)
	if err != nil {
		return ""
	}
	return encrypt.OwnerFingerprint(k)
}

// Enrollment is what the server says of an address's owner keys.
type Enrollment struct {
	// ResetPending: somebody asked to replace the owner key through the provider session and the
	// waiting period is running; ResetSince is when it was asked and ResetDue when it can be
	// completed (the zero time when none). The owner key can still cancel it until then.
	ResetPending         bool
	ResetSince, ResetDue time.Time
	// Enrolled: the address has an owner key.
	Enrolled bool
	// Fingerprints are the fingerprints of its owner keys as the server lists them (the ones it
	// kept, oldest first); Current is the one that signs now, "" when none.
	Fingerprints []string
	Current      string
}

// Key is a key the directory lists for an address, as the People tab's picker shows it.
type Key struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	// Verified is what the server answered: the key was published by an account whose e-mail the
	// identity provider verified. It is never inferred from anything the page typed.
	Verified bool `json:"verified"`
	// Held: whoever published the key opened a challenge sent to it (proof of possession).
	Held bool `json:"held"`
	// Attested: the key's own signature binds it to the address, checked here.
	Attested bool `json:"-"`
	// OwnerEnrolled: the address has an owner key. OwnerSigned: this client checked that the
	// owner key signed this row AND that key is one it holds for the address from before (never
	// the server's word alone). Owner says, for a row that carries owner evidence, what is known of
	// the key that signed it; OwnerPub is that key.
	OwnerEnrolled, OwnerSigned bool `json:"-"`
	Owner                      OwnerState
	OwnerPub                   string `json:"-"`
	// SeqChanged: the row is not owner-signed and its log number is not the one an earlier look showed.
	SeqChanged bool `json:"-"`
}

// OwnerState says what this client knows of the owner key that signed a row.
type OwnerState string

const (
	// OwnerNone: the row carries no owner evidence.
	OwnerNone OwnerState = ""
	// OwnerPinned: the key is one somebody confirmed for the address (OwnerSigned).
	OwnerPinned OwnerState = "pinned"
	// OwnerUnpinned: the signature verifies under the key in the row, but nothing held before ties
	// that key to the address's owner: the server's word.
	OwnerUnpinned OwnerState = "unpinned"
	// OwnerFirstSeen: the same key an earlier lookup showed first: consistent, not confirmed.
	OwnerFirstSeen OwnerState = "first-seen"
	// OwnerChanged: another key than the pinned (or first seen) one signed the row.
	OwnerChanged OwnerState = "changed"
	// OwnerDowngraded: the row lost the owner evidence it carried before.
	OwnerDowngraded OwnerState = "downgraded"
)

// NeedsConfirming says the person should compare OwnerPub with its owner and confirm it.
func (s OwnerState) NeedsConfirming() bool {
	return s == OwnerUnpinned || s == OwnerFirstSeen || s == OwnerChanged
}

// Directory is what the page needs of a key directory.
type Directory interface {
	// Lookup lists the keys published for an e-mail address.
	Lookup(ctx context.Context, email string) ([]Key, error)
	// Publish publishes a public key for the signed-in account's own address.
	Publish(ctx context.Context, r PublishRequest) (Key, error)
	// Me is the e-mail address the server knows the signed-in account by (GET /auth/me): the
	// address an owner key is enrolled for.
	Me(ctx context.Context) (string, error)
	// Enrollment says whether the address has an owner key.
	Enrollment(ctx context.Context, email string) (Enrollment, error)
	// Enroll registers owner as the owner key of the signed-in account's address.
	Enroll(ctx context.Context, email string, owner OwnerSigner) error
	// ServerLogKeys is the log public keys the server states for itself (key ID to base64 key):
	// what a first use pins. They prove only that the server is consistent with itself.
	ServerLogKeys(ctx context.Context) (map[string]string, error)
	// SetLogKeys sets the log keys that signed heads and entries are checked against (pinned out
	// of band, or on first use).
	SetLogKeys(keys map[string]string) error
	// Watch reads the log since the last run, checks it against the pinned keys and the saved
	// position, and reports what happened to the address that the person did not do.
	Watch(ctx context.Context, email string, owner OwnerSigner) (WatchResult, error)
	// State is the position and memory kept between runs as text to store; LoadState restores
	// it. A state that cannot be read is dropped (ErrBadState) and the next run is a first look.
	State() string
	LoadState(raw string) error
	// PinOwner remembers ownerPub (as shown in Key.OwnerPub) as the owner key of email, after the
	// person compared its fingerprint with its owner another way. Pins returns all pins, and
	// LoadPins restores them (for the page to keep).
	PinOwner(email, ownerPub string)
	Pins() map[string]string
	LoadPins(map[string]string)
	// Rotate replaces the owner key: next is signed in by the current one.
	Rotate(ctx context.Context, email string, current, next OwnerSigner) error
	// RequestReset asks to replace a lost owner key through the signed-in session; it takes effect
	// after a waiting period (24 hours) during which the owner key can cancel it.
	RequestReset(ctx context.Context) (Enrollment, error)
	// CompleteReset installs next as the owner key once the waiting period is over.
	CompleteReset(ctx context.Context, email string, next OwnerSigner) error
	// CancelReset ends a pending reset, signed by the current owner key.
	CancelReset(ctx context.Context, email string, owner OwnerSigner) error
	// Pin tells the client that owner is the key somebody confirmed for the address (it enrolled
	// it, or the person restored it from a backup), so rows signed by it can be told from rows
	// signed by another.
	Pin(email string, owner OwnerSigner)
}

// PublishRequest is a key to publish.
type PublishRequest struct {
	// Key is the public key text, Label what to call it.
	Key, Label string
	// Identity opens the server's proof-of-possession challenge for Key: the unlocked private
	// side. Nil sends no proof, and a server that requires one answers ErrProofRequired.
	Identity age.Identity
	// Email and Owner sign the entry with the address's owner key. An address that has one
	// refuses an entry without it (ErrOwnerRequired). Owner nil: no signature.
	Email string
	Owner OwnerSigner
}

// The failures a page tells the person about.
var (
	// ErrNoDirectory: the server has no key directory (the routes are absent, or it is not a
	// docstore server).
	ErrNoDirectory = errors.New("this server has no key directory")
	// ErrUnenrolled is returned by Lookup, together with the keys, when RequireOwner is set and the address
	// has no owner key: only the server's word ties them to it.
	ErrUnenrolled = directoryclient.ErrUnenrolled
	// ErrSignInRequired: the server wants a signed-in provider account (a session token).
	ErrSignInRequired = errors.New("the key directory needs a signed-in account: set a session token")
	// ErrNotAllowed: the account may not do this (a passkey account cannot look up or publish; an
	// account whose e-mail is not verified cannot publish).
	ErrNotAllowed = errors.New("this account may not do that: the directory needs an account with a verified e-mail from an identity provider")
	// ErrRateLimited: too many requests.
	ErrRateLimited = errors.New("the directory is rate limited: try again in a minute")
	// ErrProofRequired: the server wants proof that the publisher holds the key, and none could be
	// given (the key is locked, or it is not one that can open a challenge).
	ErrProofRequired = errors.New("the directory wants proof that you hold this key: unlock it first")
	// ErrOwnerRequired: the address has an owner key and the server wants an entry signed by it.
	ErrOwnerRequired = errors.New("this address has an owner key: publishing needs an entry signed by it")
	// ErrAlreadyEnrolled: the address already has an owner key. If it is yours, restore it from its
	// backup; if not, somebody enrolled before you did (the log shows who and when).
	ErrAlreadyEnrolled = errors.New("this address already has an owner key")
	// ErrResetWait: the reset cannot be completed yet; the waiting period is still running.
	ErrResetWait = errors.New("the waiting period of the reset is not over")
	// ErrNotEnrolled: the address has no owner key to rotate, reset or cancel.
	ErrNotEnrolled = errors.New("this address has no owner key")
	// ErrBadProof: the proof of possession did not verify.
	ErrBadProof = errors.New("the directory did not accept the proof that you hold this key")
)

// Client is a Directory over a docstore server's routes, through directoryclient.
type Client struct {
	// Base is the server's address ("https://docs.example.com", no trailing path).
	Base string
	// Token returns the bearer token of the signed-in account, "" for none. It is asked for each
	// request.
	Token func(ctx context.Context) (string, error)
	// HTTP is the HTTP client; nil is http.DefaultClient.
	HTTP *http.Client
	// Now is the clock the owner signatures are dated by (time.Now when nil); a test moves it with the
	// server's.
	Now func() time.Time
	// RequireOwner makes Lookup report ErrUnenrolled, with the rows, for an address that has no owner
	// key (set before the first call).
	RequireOwner bool

	// OnState is called with the new State text after anything that changes it, so the page can
	// keep it.
	OnState func(string)

	mu      sync.Mutex
	tok     string
	dc      *directoryclient.Client
	pins    directoryclient.MemoryPins
	logKeys map[string]ed25519.PublicKey
	state   directoryclient.WatchState
}

// client is the directoryclient.Client for this call, its token set from Token. One is kept, so
// what it remembers between lookups lasts as long as this Client.
func (c *Client) client(ctx context.Context) (*directoryclient.Client, error) {
	var tok string
	if c.Token != nil {
		t, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		tok = t
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tok = tok
	if c.dc == nil {
		c.dc = &directoryclient.Client{BaseURL: c.Base, HTTP: c.HTTP, RequireOwner: c.RequireOwner, Now: c.Now, Pins: c.pinsLocked(), State: &c.state, LogKeys: c.logKeys, Token: func() string {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.tok
		}}
	}
	return c.dc, nil
}

func (c *Client) pinsLocked() directoryclient.MemoryPins {
	if c.pins == nil {
		c.pins = directoryclient.MemoryPins{}
	}
	return c.pins
}

// saved passes the state to OnState.
func (c *Client) saved() {
	if c.OnState != nil {
		c.OnState(c.State())
	}
}

// State implements Directory.
func (c *Client) State() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(c.state)
	return string(b)
}

// LoadState implements Directory.
func (c *Client) LoadState(raw string) error {
	st, err := directoryclient.ParseWatchState(raw)
	c.mu.Lock()
	c.state = st // the zero state when raw could not be read
	c.mu.Unlock()
	if err != nil {
		return ErrBadState
	}
	return nil
}

// SetLogKeys implements Directory.
func (c *Client) SetLogKeys(keys map[string]string) error {
	parsed := map[string]ed25519.PublicKey{}
	for kid, k := range keys {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return fmt.Errorf("the log key %q is not an Ed25519 public key", kid)
		}
		parsed[kid] = b
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logKeys = parsed
	if len(parsed) == 0 {
		c.logKeys = nil
	}
	if c.dc != nil {
		c.dc.LogKeys = c.logKeys
	}
	return nil
}

// ServerLogKeys implements Directory: the keys the log route states (GET /directory/log).
func (c *Client) ServerLogKeys(ctx context.Context) (map[string]string, error) {
	tok := ""
	if c.Token != nil {
		t, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		tok = t
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+"/directory/log?since=0", nil)
	if err != nil {
		return nil, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, ErrNoDirectory
	case http.StatusUnauthorized:
		return nil, ErrSignInRequired
	case http.StatusForbidden:
		return nil, ErrNotAllowed
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("the log route answered %d", res.StatusCode)
	}
	var page docstore.DirectoryLog
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&page); err != nil {
		return nil, err
	}
	return page.Keys, nil
}

// Watch implements Directory.
func (c *Client) Watch(ctx context.Context, email string, owner OwnerSigner) (WatchResult, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return WatchResult{}, err
	}
	c.mu.Lock()
	have := len(c.logKeys) > 0
	c.mu.Unlock()
	if !have {
		return WatchResult{}, ErrNoLogKeys
	}
	defer c.saved()
	rep, err := dc.WatchLog(ctx, email, &c.state, directoryclient.WatchOptions{Owner: owner})
	if err != nil {
		if errors.Is(err, directoryclient.ErrLogCut) {
			return WatchResult{}, errors.Join(ErrLogRewritten, err)
		}
		return WatchResult{}, failure(err)
	}
	out := WatchResult{HeadSeq: rep.Head.Seq, New: len(rep.Entries), Filtered: rep.Filtered, AddressUnconfirmed: rep.AddressUnconfirmed}
	for _, f := range rep.Foreign {
		kind := AlertEntry
		switch f.Entry.Op {
		case encrypt.OpReset:
			kind = AlertReset
		case encrypt.OpResetRequest:
			kind = AlertResetRequest
		}
		out.Alerts = append(out.Alerts, Alert{Kind: kind, Seq: f.Entry.Seq, Text: fmt.Sprintf("%s (log entry %d)", f.Message, f.Entry.Seq)})
	}
	for _, v := range rep.Violations {
		out.Alerts = append(out.Alerts, Alert{Kind: AlertViolation, Seq: v.Seq, Text: fmt.Sprintf("log entry %d breaks the owner rules: %s", v.Seq, v.Why)})
	}
	return out, nil
}

// PinOwner implements Directory.
func (c *Client) PinOwner(email, ownerPub string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinsLocked().Set(email, ownerPub)
}

// Pins implements Directory.
func (c *Client) Pins() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.pins))
	for k, v := range c.pins {
		out[k] = v
	}
	return out
}

// LoadPins implements Directory.
func (c *Client) LoadPins(m map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pins = directoryclient.MemoryPins{}
	for k, v := range m {
		c.pins.Set(k, v)
	}
}

// Pin implements Directory.
func (c *Client) Pin(email string, owner OwnerSigner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinsLocked().Set(email, encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)))
}

// Me implements Directory: GET /auth/me.
func (c *Client) Me(ctx context.Context) (string, error) {
	var tok string
	if c.Token != nil {
		t, err := c.Token(ctx)
		if err != nil {
			return "", err
		}
		tok = t
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+"/auth/me", nil)
	if err != nil {
		return "", err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "", ErrNoDirectory
	case http.StatusUnauthorized:
		return "", ErrSignInRequired
	default:
		return "", errors.New("the server could not say who is signed in")
	}
	var u struct{ Email string }
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&u); err != nil {
		return "", err
	}
	if strings.TrimSpace(u.Email) == "" {
		return "", ErrNotAllowed
	}
	return strings.ToLower(strings.TrimSpace(u.Email)), nil
}

// Enrollment implements Directory.
func (c *Client) Enrollment(ctx context.Context, email string) (Enrollment, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	e, err := dc.Enrollment(ctx, email)
	if err != nil {
		return Enrollment{}, failure(err)
	}
	out := enrollmentOf(e)
	for _, k := range e.Keys {
		if pub, err := encrypt.ParseOwnerPub(k.Pub); err == nil {
			fp := encrypt.OwnerFingerprint(pub)
			out.Fingerprints = append(out.Fingerprints, fp)
			if k.Until == 0 { // still valid: the one that signs now
				out.Current = fp
			}
		}
	}
	if out.Current == "" && len(out.Fingerprints) > 0 {
		out.Current = out.Fingerprints[len(out.Fingerprints)-1]
	}
	return out, nil
}

func enrollmentOf(e docstore.DirectoryEnrollment) Enrollment {
	out := Enrollment{Enrolled: e.Enrolled, ResetPending: e.ResetSince != 0}
	if e.ResetSince != 0 {
		out.ResetSince = time.Unix(e.ResetSince, 0)
	}
	if e.ResetDue != 0 {
		out.ResetDue = time.Unix(e.ResetDue, 0)
	}
	return out
}

// Rotate implements Directory.
func (c *Client) Rotate(ctx context.Context, email string, current, next OwnerSigner) error {
	dc, err := c.client(ctx)
	if err != nil {
		return err
	}
	defer c.saved()
	if _, err := dc.Rotate(ctx, email, current, next); err != nil {
		return failure(err)
	}
	return nil
}

// RequestReset implements Directory.
func (c *Client) RequestReset(ctx context.Context) (Enrollment, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	defer c.saved()
	e, err := dc.RequestReset(ctx)
	if err != nil {
		return Enrollment{}, failure(err)
	}
	return enrollmentOf(e), nil
}

// CompleteReset implements Directory.
func (c *Client) CompleteReset(ctx context.Context, email string, next OwnerSigner) error {
	dc, err := c.client(ctx)
	if err != nil {
		return err
	}
	defer c.saved()
	if _, err := dc.CompleteReset(ctx, email, next); err != nil {
		return failure(err)
	}
	return nil
}

// CancelReset implements Directory.
func (c *Client) CancelReset(ctx context.Context, email string, owner OwnerSigner) error {
	dc, err := c.client(ctx)
	if err != nil {
		return err
	}
	defer c.saved()
	if _, err := dc.CancelReset(ctx, email, owner); err != nil {
		return failure(err)
	}
	return nil
}

// Enroll implements Directory.
func (c *Client) Enroll(ctx context.Context, email string, owner OwnerSigner) error {
	dc, err := c.client(ctx)
	if err != nil {
		return err
	}
	defer c.saved()
	if _, err := dc.Enroll(ctx, email, owner); err != nil {
		return failure(err)
	}
	return nil
}

// failure puts the server's refusals in this package's sentinels (the page's words for them).
func failure(err error) error {
	var de *directoryclient.Error
	if !errors.As(err, &de) {
		return err
	}
	switch {
	case errors.Is(err, docstore.ErrDirectoryOwnerRequired):
		return ErrOwnerRequired
	case errors.Is(err, docstore.ErrDirectoryProofRequired):
		return ErrProofRequired
	case errors.Is(err, docstore.ErrDirectoryBadProof):
		return ErrBadProof
	case errors.Is(err, docstore.ErrDirectoryEnrolled):
		return ErrAlreadyEnrolled
	case errors.Is(err, docstore.ErrDirectoryResetWait):
		return ErrResetWait
	case errors.Is(err, docstore.ErrDirectoryNotEnrolled):
		return ErrNotEnrolled
	}
	switch de.Status {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return ErrNoDirectory
	case http.StatusUnauthorized:
		return ErrSignInRequired
	case http.StatusForbidden:
		return errors.Join(ErrNotAllowed, err)
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return err
}

// Lookup implements Directory.
func (c *Client) Lookup(ctx context.Context, email string) ([]Key, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := dc.Lookup(ctx, email)
	defer c.saved()
	unenrolled := errors.Is(err, directoryclient.ErrUnenrolled) // RequireOwner: the rows come with the error
	if err != nil && !unenrolled {
		return nil, failure(err)
	}
	out := make([]Key, 0, len(rows))
	for _, r := range rows {
		k := Key{Key: r.Key, Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Verified: r.Verified, Held: r.Held,
			Attested: r.AttestationVerified, OwnerEnrolled: r.OwnerEnrolled, OwnerSigned: r.OwnerSigned, SeqChanged: r.SeqChanged}
		if r.Owner != nil {
			k.OwnerPub = r.Owner.Pub
			switch {
			case r.OwnerChanged:
				k.Owner = OwnerChanged
			case r.OwnerPinned:
				k.Owner = OwnerPinned
			case r.OwnerFirstSeen:
				k.Owner = OwnerFirstSeen
			case r.OwnerUnpinned:
				k.Owner = OwnerUnpinned
			}
		} else if r.OwnerDowngraded {
			k.Owner = OwnerDowngraded
		}
		out = append(out, k)
	}
	if unenrolled {
		return out, ErrUnenrolled
	}
	return out, nil
}

// Publish implements Directory.
func (c *Client) Publish(ctx context.Context, r PublishRequest) (Key, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return Key{}, err
	}
	defer c.saved()
	k, err := dc.Publish(ctx, directoryclient.PublishRequest{Email: r.Email, Key: r.Key, Label: r.Label, Identity: r.Identity, Owner: r.Owner})
	if err != nil {
		return Key{}, failure(err)
	}
	return Key{Key: k.Key, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified, Held: k.Held}, nil
}
