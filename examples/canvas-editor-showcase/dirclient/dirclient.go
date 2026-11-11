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

type OwnerKey = directoryclient.OwnerKey

type OwnerSigner = directoryclient.OwnerSigner

// GenerateOwnerKey makes a new owner key.
func GenerateOwnerKey() (OwnerKey, error) { return directoryclient.GenerateOwnerKey() }

func ExportOwnerKey(k OwnerKey) string { return directoryclient.ExportOwnerKey(k) }

// ImportOwnerKey reads what ExportOwnerKey wrote.
func ImportOwnerKey(s string) (OwnerKey, error) {
	return directoryclient.ImportOwnerKey(strings.TrimSpace(s))
}

func OwnerFingerprint(k OwnerSigner) string {
	return encrypt.OwnerFingerprint(k.Public().(ed25519.PublicKey))
}

// WatchResult is what a run of Watch found.
type WatchResult struct {
	HeadSeq                      int64
	New                          int
	Alerts                       []Alert
	Filtered, AddressUnconfirmed bool
}

// AlertKind says what sort of thing an Alert reports.
type AlertKind string

const (
	AlertEntry        AlertKind = "entry"
	AlertReset        AlertKind = "reset"
	AlertResetRequest AlertKind = "reset-request"
	AlertViolation    AlertKind = "violation"
)

// Alert is one thing watching found.
type Alert struct {
	Kind AlertKind
	// Seq is the number of the log entry.
	Seq int64
	// Text says it in English, with the entry number.
	Text string
}

var ErrBadState = errors.New("the saved log position could not be read: the next look is a first look")

var ErrNoLogKeys = errors.New("no log keys are pinned for this directory")

var ErrLogRewritten = errors.New("the log was cut back or rewritten since the last look")

func LogKeyFingerprint(b64 string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + " " + h[4:8] + " " + h[8:12] + " " + h[12:16]
}

func NormalizeFingerprint(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', ':', '-', '\t', '\n', '\r':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

func OwnerPubFingerprint(pub string) string {
	k, err := encrypt.ParseOwnerPub(pub)
	if err != nil {
		return ""
	}
	return encrypt.OwnerFingerprint(k)
}

type Enrollment struct {
	ResetPending         bool
	ResetSince, ResetDue time.Time
	// Enrolled: the address has an owner key.
	Enrolled     bool
	Fingerprints []string
	Current      string
}

type Key struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Verified    bool   `json:"verified"`
	Held        bool   `json:"held"`
	Attested    bool   `json:"-"`
	// OwnerEnrolled: the address has an owner key.
	OwnerEnrolled, OwnerSigned bool `json:"-"`
	Owner                      OwnerState
	OwnerPub                   string `json:"-"`
	SeqChanged                 bool   `json:"-"`
}

type OwnerState string

const (
	// OwnerNone: the row carries no owner evidence.
	OwnerNone      OwnerState = ""
	OwnerPinned    OwnerState = "pinned"
	OwnerUnpinned  OwnerState = "unpinned"
	OwnerFirstSeen OwnerState = "first-seen"
	OwnerChanged   OwnerState = "changed"
	// OwnerDowngraded: the row lost the owner evidence it carried before.
	OwnerDowngraded OwnerState = "downgraded"
)

func (s OwnerState) NeedsConfirming() bool {
	return s == OwnerUnpinned || s == OwnerFirstSeen || s == OwnerChanged
}

// Directory is what the page needs of a key directory.
type Directory interface {
	// Lookup lists the keys published for an e-mail address.
	Lookup(ctx context.Context, email string) ([]Key, error)
	Publish(ctx context.Context, r PublishRequest) (Key, error)
	Me(ctx context.Context) (string, error)
	// Enrollment says whether the address has an owner key.
	Enrollment(ctx context.Context, email string) (Enrollment, error)
	Enroll(ctx context.Context, email string, owner OwnerSigner) error
	ServerLogKeys(ctx context.Context) (map[string]string, error)
	SetLogKeys(keys map[string]string) error
	Watch(ctx context.Context, email string, owner OwnerSigner) (WatchResult, error)
	State() string
	LoadState(raw string) error
	PinOwner(email, ownerPub string)
	Pins() map[string]string
	LoadPins(map[string]string)
	Rotate(ctx context.Context, email string, current, next OwnerSigner) error
	RequestReset(ctx context.Context) (Enrollment, error)
	CompleteReset(ctx context.Context, email string, next OwnerSigner) error
	CancelReset(ctx context.Context, email string, owner OwnerSigner) error
	Pin(email string, owner OwnerSigner)
}

// PublishRequest is a key to publish.
type PublishRequest struct {
	Key, Label string
	Identity   age.Identity
	Email      string
	Owner      OwnerSigner
}

// The failures a page tells the person about.
var (
	ErrNoDirectory    = errors.New("this server has no key directory")
	ErrUnenrolled     = directoryclient.ErrUnenrolled
	ErrSignInRequired = errors.New("the key directory needs a signed-in account: set a session token")
	ErrNotAllowed     = errors.New("this account may not do that: the directory needs an account with a verified e-mail from an identity provider")
	// ErrRateLimited: too many requests.
	ErrRateLimited   = errors.New("the directory is rate limited: try again in a minute")
	ErrProofRequired = errors.New("the directory wants proof that you hold this key: unlock it first")
	ErrOwnerRequired = errors.New("this address has an owner key: publishing needs an entry signed by it")
	// ErrAlreadyEnrolled: the address already has an owner key.
	ErrAlreadyEnrolled = errors.New("this address already has an owner key")
	ErrResetWait       = errors.New("the waiting period of the reset is not over")
	ErrNotEnrolled     = errors.New("this address has no owner key")
	// ErrBadProof: the proof of possession did not verify.
	ErrBadProof = errors.New("the directory did not accept the proof that you hold this key")
)

type Client struct {
	// Base is the server's address ("https://docs.example.com", no trailing path).
	Base  string
	Token func(ctx context.Context) (string, error)
	// HTTP is the HTTP client; nil is http.DefaultClient.
	HTTP         *http.Client
	Now          func() time.Time
	RequireOwner bool

	OnState func(string)

	mu      sync.Mutex
	tok     string
	dc      *directoryclient.Client
	pins    directoryclient.MemoryPins
	logKeys map[string]ed25519.PublicKey
	state   directoryclient.WatchState
}

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
