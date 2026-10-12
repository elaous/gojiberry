package speechcoach

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Contributing recordings to research is optional, explicit and per sitting. Nothing leaves the device
// unless the person has been shown the exact package, confirmed that package, and the operator has
// configured an endpoint (there is none by default, and never a third party's). The package carries
// no name, account, address, device or time: each recording gets a new random ID, dates are fixed, and
// the request has no cookie, sign-in token or referrer. One random token per contribution lets the
// operator delete it if the person asks (Revoke sends that request; once sent, only the operator's
// honouring it can take the data back, and the interface says so before the person confirms).

// MaxContributionBytes limits one package.
const MaxContributionBytes = 64 << 20

// Errors of the contribution flow.
var (
	ErrNoEndpoint   = errors.New("speechcoach: no research endpoint is configured")
	ErrNoConsent    = errors.New("speechcoach: the person has not confirmed this package")
	ErrRevoked      = errors.New("speechcoach: consent was withdrawn")
	ErrNothingToAdd = errors.New("speechcoach: there is nothing to contribute")
	ErrTooLarge     = errors.New("speechcoach: the package is too large")
)

// CheckEndpoint validates an operator-configured endpoint: https, or http to the local machine for
// development. No credentials in the URL, no fragment. It returns the cleaned URL.
func CheckEndpoint(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrNoEndpoint
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("speechcoach: the research address is not valid")
	}
	local := false
	if h := u.Hostname(); h == "localhost" || net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback() {
		local = true
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return "", errors.New("speechcoach: the research address must be https")
	}
	u.RawQuery = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// PlanItem is one recording in a package, as it will be sent.
type PlanItem struct {
	ID         string  `json:"id"` // random, new for this package
	Prompt     string  `json:"prompt"`
	Transcript string  `json:"transcript"`
	Lang       string  `json:"lang"`
	Seconds    float64 `json:"seconds"`
	Bytes      int     `json:"bytes"`
}

// Plan is exactly what a contribution would send.
type Plan struct {
	Endpoint string     `json:"endpoint"`
	Token    string     `json:"token"` // sent in X-Contribution-Token; names no one
	Items    []PlanItem `json:"items"`
	Fields   []string   `json:"fields"` // every kind of field in the package
	Headers  []string   `json:"headers"`
	Bytes    int        `json:"bytes"`
	Digest   string     `json:"digest"` // SHA-256 of the body, hex: what Confirm must be given
	body     []byte
}

// Body is the exact request body (a zip in the export format).
func (p *Plan) Body() []byte { return p.body }

// Prepare builds the package for the entries chosen. Audio and the person's transcripts are the content;
// everything else that could identify is removed or replaced.
func Prepare(endpoint string, entries []Entry) (*Plan, error) {
	ep, err := CheckEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNothingToAdd
	}
	tok, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	p := &Plan{Endpoint: ep, Token: tok,
		Fields:  []string{"audio (16 kHz mono WAV)", "transcript", "prompt phrase", "language", "duration", "quality hints", "checksum of the audio", "random recording ID"},
		Headers: []string{"Content-Type: application/zip", "X-Contribution-Token (random, for deletion)"}}
	var d Dataset
	for _, e := range entries {
		id, err := randomHex(12)
		if err != nil {
			return nil, err
		}
		c := e
		c.ID, c.Created = id, time.Unix(0, 0).UTC()
		d.Add(c)
		p.Items = append(p.Items, PlanItem{ID: id, Prompt: e.Prompt, Transcript: e.Transcript, Lang: e.Lang, Seconds: e.Seconds, Bytes: len(e.WAV)})
	}
	if len(d.entries) != len(entries) { // two takes of one phrase collapse: send what was shown
		return nil, errors.New("speechcoach: two recordings of the same phrase were chosen")
	}
	var buf bytes.Buffer
	if _, err := d.Export(&buf, nil); err != nil {
		return nil, err
	}
	if buf.Len() > MaxContributionBytes {
		return nil, ErrTooLarge
	}
	p.body = buf.Bytes()
	p.Bytes = buf.Len()
	sum := sha256.Sum256(p.body)
	p.Digest = hex.EncodeToString(sum[:])
	return p, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Doer sends a request: *http.Client, or a wrapper that adds what the platform needs (in the browser,
// the header that omits credentials).
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Contribution is the consent of one sitting. It is in memory only: closing the page ends it, and the
// next sitting starts without consent.
type Contribution struct {
	confirmed string // digest of the plan the person confirmed
	endpoint  string
	revoked   bool
	sent      []string // tokens of the packages sent
}

// Confirm records that the person saw this plan and agrees to send exactly it. A later plan, or a
// plan to another address, needs its own confirmation.
func (c *Contribution) Confirm(p *Plan) {
	c.confirmed, c.endpoint, c.revoked = p.Digest, p.Endpoint, false
}

// Confirmed says the plan is the one confirmed in this sitting and consent was not withdrawn.
func (c *Contribution) Confirmed(p *Plan) bool {
	return !c.revoked && c.confirmed != "" && p != nil && c.confirmed == p.Digest && c.endpoint == p.Endpoint
}

// Sent is the number of packages sent in this sitting.
func (c *Contribution) Sent() int { return len(c.sent) }

// Send posts the plan, only when it is the confirmed one. The request carries the body, its type and the
// token, and nothing else; a redirect is not followed to another address.
func (c *Contribution) Send(ctx context.Context, d Doer, p *Plan) error {
	if c.revoked {
		return ErrRevoked
	}
	if !c.Confirmed(p) {
		return ErrNoConsent
	}
	if sum := sha256.Sum256(p.body); hex.EncodeToString(sum[:]) != p.Digest {
		return ErrNoConsent // the body is not what was shown
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(p.body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("X-Contribution-Token", p.Token)
	req.Header.Del("Cookie")
	res, err := d.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("the research server answered %s", http.StatusText(res.StatusCode))
	}
	c.sent = append(c.sent, p.Token)
	return nil
}

// Revoke withdraws consent: nothing more is sent, and a deletion request (DELETE <endpoint>/<token>) goes
// out for each package sent in this sitting. It returns how many the server confirmed deleted (a 2xx or
// 404, the latter meaning it no longer holds it) and the first error. Withdrawing works even when a
// request fails: consent is withdrawn either way.
func (c *Contribution) Revoke(ctx context.Context, d Doer) (deleted int, err error) {
	c.revoked, c.confirmed = true, ""
	var rest []string
	for _, tok := range c.sent {
		req, e := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint+"/"+tok, nil)
		if e == nil && d != nil {
			var res *http.Response
			if res, e = d.Do(req); e == nil {
				res.Body.Close()
				if res.StatusCode/100 == 2 || res.StatusCode == http.StatusNotFound {
					deleted++
					continue
				}
				e = fmt.Errorf("the research server answered %s", http.StatusText(res.StatusCode))
			}
		}
		if err == nil {
			err = e
		}
		rest = append(rest, tok)
	}
	c.sent = rest
	return deleted, err
}
