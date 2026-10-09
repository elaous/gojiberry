package components

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/i18n"
)

// The errors auth/passkey/client names are told apart and said in words, not filed under "something went wrong".
func TestExplainErrorKnowsThePasskeyCeremonyErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // a phrase of the English message
	}{
		{"timed out", client.ErrTimedOut, "open for a minute"},
		{"not verified", fmt.Errorf("register: %w", client.ErrNotVerified), "could not verify that it is you"},
		{"cancelled", client.ErrCancelled, "closed or refused"},
		{"already registered", fmt.Errorf("add: %w", client.ErrAlreadyRegistered), "already has a passkey"},
		{"insecure page", client.ErrInsecureContext, "secure page"},
		{"unsupported authenticator", client.ErrUnsupportedAuthenticator, "cannot do what the page asked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := ExplainError(c.err)
			if info.Kind != ErrKindPasskey || !strings.Contains(info.Message, c.want) {
				t.Fatalf("%q kind %q, want a passkey message with %q", info.Message, info.Kind, c.want)
			}
			if info.Details == "" {
				t.Error("the technical text must stay as details")
			}
		})
	}
	// An unrelated error is still the generic one.
	if info := ExplainError(errors.New("boom")); info.Kind != ErrKindUnknown {
		t.Errorf("kind %q", info.Kind)
	}
}

func TestEveryPasskeyErrorMessageIsInEveryPack(t *testing.T) {
	cat := i18n.Default()
	for _, id := range []string{"err.passkey.timedOut", "err.passkey.notVerified", "err.passkey.cancelled", "err.passkey.already", "err.passkey.insecure", "err.passkey.unsupported"} {
		for _, l := range []string{"de", "fr", "es"} {
			if got := cat.Text(l, id, "MISSING"); got == "MISSING" {
				t.Errorf("%s has no %q", l, id)
			}
		}
	}
}
