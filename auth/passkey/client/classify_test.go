package client

import (
	"errors"
	"testing"
	"time"
)

func TestRejectionsAreToldApart(t *testing.T) {
	short := 2 * time.Second
	long := PromptTimeout
	for _, tc := range []struct {
		name, msg string
		elapsed   time.Duration
		want      error
		not       []error
	}{
		{"InvalidStateError", "", short, ErrAlreadyRegistered, nil},
		{"SecurityError", "The operation is insecure.", short, ErrInsecureContext, []error{ErrCancelled}},
		{"NotSupportedError", "", short, ErrUnsupportedAuthenticator, []error{ErrCancelled}},
		{"ConstraintError", "", short, ErrUnsupportedAuthenticator, nil},
		{"NotAllowedError", "The operation either timed out or was not allowed.", long, ErrTimedOut, []error{ErrNotVerified}},
		{"NotAllowedError", "User verification failed: wrong PIN", short, ErrNotVerified, []error{ErrTimedOut}},
		{"NotAllowedError", "The operation either timed out or was not allowed.", short, ErrCancelled, []error{ErrTimedOut, ErrNotVerified}},
		{"AbortError", "aborted", short, ErrCancelled, []error{ErrTimedOut}},
	} {
		got := classify(tc.name, tc.msg, tc.elapsed)
		if !errors.Is(got, tc.want) {
			t.Errorf("%s %q after %v: %v, want %v", tc.name, tc.msg, tc.elapsed, got, tc.want)
		}
		for _, n := range tc.not {
			if errors.Is(got, n) {
				t.Errorf("%s %q after %v: %v is also %v", tc.name, tc.msg, tc.elapsed, got, n)
			}
		}
	}
	if !errors.Is(ErrTimedOut, ErrCancelled) || !errors.Is(ErrNotVerified, ErrCancelled) {
		t.Error("the kinds are not ErrCancelled")
	}
	if errors.Is(ErrInsecureContext, ErrCancelled) || errors.Is(ErrAlreadyRegistered, ErrCancelled) {
		t.Error("a refusal is not a cancellation")
	}
	if classify("TypeError", "x", short) != nil {
		t.Error("an unknown failure was classified")
	}
}

func TestServerErrorTellsAlreadyCreatedApart(t *testing.T) {
	e := serverError(409, []byte(`{"error":"x","code":"account_exists"}`))
	if !errors.Is(e, ErrAlreadyCreated) {
		t.Errorf("%v", e)
	}
	if errors.Is(serverError(409, []byte(`{"error":"the only way"}`)), ErrAlreadyCreated) || errors.Is(serverError(410, []byte(`{"error":"x","code":"account_exists"}`)), ErrAlreadyCreated) {
		t.Error("another failure was taken for it")
	}
}

func TestServerErrorKeepsTheServersSentenceForAPerson(t *testing.T) {
	err := serverError(400, []byte(`{"error":"that e-mail address is taken"}`))
	var um interface{ UserMessage() string }
	if !errors.As(err, &um) || um.UserMessage() != "that e-mail address is taken" {
		t.Fatalf("%v: no user message", err)
	}
	if err.Error() != "that e-mail address is taken" {
		t.Errorf("Error() = %q", err.Error())
	}
	if got := serverError(409, []byte(`{"error":"x","code":"account_exists"}`)); !errors.Is(got, ErrAlreadyCreated) {
		t.Errorf("account_exists = %v", got)
	}
	if got := serverError(502, []byte(`<html>bad gateway</html>`)); errors.As(got, &um) {
		t.Errorf("an answer without a sentence must not claim to be one: %v", got)
	}
}
