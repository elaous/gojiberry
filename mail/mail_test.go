package mail

import (
	"errors"
	"strings"
	"testing"
)

func TestMessageRules(t *testing.T) {
	ok := Message{To: []string{"ana@example.com"}, Subject: "Hello", Text: "body"}
	if to, err := check(ok); err != nil || len(to) != 1 || to[0] != "ana@example.com" {
		t.Fatalf("%v %v", to, err)
	}
	bad := map[string]Message{
		"no recipient":          {Subject: "s", Text: "t"},
		"too many":              {To: make([]string, MaxRecipients+1), Subject: "s", Text: "t"},
		"a name in an address":  {To: []string{"Ana <ana@example.com>"}, Subject: "s", Text: "t"},
		"a list in one entry":   {To: []string{"a@example.com,b@example.com"}, Subject: "s", Text: "t"},
		"a header in an entry":  {To: []string{"a@example.com\r\nBcc: x@example.com"}, Subject: "s", Text: "t"},
		"not an address":        {To: []string{"nope"}, Subject: "s", Text: "t"},
		"empty subject":         {To: ok.To, Subject: "  ", Text: "t"},
		"subject line break":    {To: ok.To, Subject: "hi\r\nBcc: x@example.com", Text: "t"},
		"long subject":          {To: ok.To, Subject: strings.Repeat("s", MaxSubject+1), Text: "t"},
		"no text":               {To: ok.To, Subject: "s", Text: " "},
		"too large":             {To: ok.To, Subject: "s", Text: strings.Repeat("x", MaxBody+1)},
		"quoted local part":     {To: []string{`"a b"@example.com`}, Subject: "s", Text: "t"},
		"a comment in an entry": {To: []string{"a@example.com (hi)"}, Subject: "s", Text: "t"},
	}
	for name, m := range bad {
		if _, err := check(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSendersNeverShowTheirSecrets(t *testing.T) {
	for _, s := range []fmtStringer{
		SMTP{Host: "smtp.example.com", Username: "u", Password: "hunter2-secret", From: "a@example.com"},
		Resend{APIKey: "re_supersecretkey", From: "a@example.com"},
	} {
		got := s.String()
		for _, secret := range []string{"hunter2-secret", "re_supersecretkey"} {
			if strings.Contains(got, secret) {
				t.Errorf("%q shows a secret", got)
			}
		}
		if !strings.Contains(got, "(set)") {
			t.Errorf("%q should say a secret is set", got)
		}
	}
}

type fmtStringer interface{ String() string }
