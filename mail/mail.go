// Package mail sends the few messages the system needs to send (an address verification link, a
// sign-in notice) through one of two providers behind one small interface: any SMTP server, or the
// Resend API. Which one runs is a deployment choice made with environment variables (FromEnv), so no
// code or file in the repository holds a credential.
//
// The rules both senders keep: a message goes to a few valid addresses and nowhere else (no header
// injection through a subject or an address, a cap on recipients and size), credentials are never in
// an error, a log line or String(), and a sender that cannot do its job says so instead of
// pretending (ErrNotConfigured).
package mail

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
)

// Message is one outgoing message. Text is required; HTML is optional and goes alongside it.
type Message struct {
	// To are the recipients (at most MaxRecipients), plain addresses.
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Limits.
const (
	MaxRecipients = 10
	MaxSubject    = 200
	MaxBody       = 256 << 10
)

// Sender sends a message. It returns once the provider has accepted it, which is not delivery.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrNotConfigured is returned by FromEnv when no provider is chosen, and by a nil-safe Sender that
// has nothing behind it: the caller offers no e-mail features then.
var ErrNotConfigured = errors.New("mail: no provider is configured (set GOJIBERRY_MAIL_PROVIDER)")

// ErrInvalid wraps every reason a message is refused before anything is sent.
var ErrInvalid = errors.New("mail: the message is not valid")

// check validates a message and returns the plain recipient addresses.
func check(m Message) ([]string, error) {
	bad := func(why string) ([]string, error) { return nil, fmt.Errorf("%w: %s", ErrInvalid, why) }
	if len(m.To) == 0 || len(m.To) > MaxRecipients {
		return bad(fmt.Sprintf("it needs 1 to %d recipients", MaxRecipients))
	}
	if strings.TrimSpace(m.Subject) == "" || len(m.Subject) > MaxSubject || hasLineBreak(m.Subject) {
		return bad("the subject is empty, too long or has a line break")
	}
	if strings.TrimSpace(m.Text) == "" {
		return bad("it has no text")
	}
	if len(m.Text)+len(m.HTML) > MaxBody {
		return bad("it is too large")
	}
	out := make([]string, 0, len(m.To))
	for _, a := range m.To {
		addr, err := address(a)
		if err != nil {
			return bad("a recipient is not an address")
		}
		out = append(out, addr)
	}
	return out, nil
}

// address returns the bare address of s, refusing anything with a name, a list, a comment or a line
// break: only "user@host" is accepted, so nothing a user typed becomes part of a header.
func address(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || hasLineBreak(s) || strings.ContainsAny(s, "<>,;\" ") {
		return "", errors.New("not a plain address")
	}
	a, err := netmail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", errors.New("not a plain address")
	}
	return s, nil
}

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

// redacted shows only that a secret is set.
func redacted(secret string) string {
	if secret == "" {
		return "(none)"
	}
	return "(set)"
}
