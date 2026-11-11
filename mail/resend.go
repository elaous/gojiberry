package mail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/resend/resend-go/v4"
)

// Resend sends through the Resend API (https://resend.com).
type Resend struct {
	APIKey, From string
	// BaseURL overrides the API address (tests); empty is Resend's own.
	BaseURL string
	HTTP    *http.Client
}

// String never shows the key.
func (r Resend) String() string {
	return fmt.Sprintf("resend(from=%s, key=%s)", r.From, redacted(r.APIKey))
}

// Send implements Sender.
func (r Resend) Send(ctx context.Context, m Message) error {
	to, err := check(m)
	if err != nil {
		return err
	}
	if r.APIKey == "" {
		return errors.New("mail: the Resend API key is not set")
	}
	from, err := address(r.From)
	if err != nil {
		return errors.New("mail: the sender address is not a plain address")
	}
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	c := resend.NewCustomClient(hc, r.APIKey)
	if r.BaseURL != "" {
		u, err := url.Parse(r.BaseURL)
		if err != nil {
			return errors.New("mail: the Resend base URL is not a URL")
		}
		c.BaseURL = u
	}
	_, err = c.Emails.SendWithContext(ctx, &resend.SendEmailRequest{From: from, To: to, Subject: m.Subject, Text: m.Text, Html: m.HTML})
	if err != nil {
		// the library's error can carry the request: give only the reason
		return fmt.Errorf("mail: Resend did not accept the message: %s", briefError(err))
	}
	return nil
}

func briefError(err error) string {
	s := err.Error()
	for i, r := range s {
		if r == '\n' || r == '\r' {
			s = s[:i]
			break
		}
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
