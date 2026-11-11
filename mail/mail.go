package mail

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
)

// Message is one outgoing message.
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

// Sender sends a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

var ErrNotConfigured = errors.New("mail: no provider is configured (set GOJIBERRY_MAIL_PROVIDER)")

var ErrInvalid = errors.New("mail: the message is not valid")

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

func redacted(secret string) string {
	if secret == "" {
		return "(none)"
	}
	return "(set)"
}
