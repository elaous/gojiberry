package client

import (
	"errors"
	"strings"
	"time"
)

// The passkey ceremonies fail in ways a person should be told apart. The browser reports most of
// them as the same NotAllowedError (the prompt was closed, it timed out, the authenticator did not
// verify the person, the site was not allowed), so this package tells them apart as far as the
// browser lets it, and says honestly when it cannot.

// ErrAlreadyRegistered is returned when the authenticator already holds a passkey for the account.
var ErrAlreadyRegistered = errors.New("this device already has a passkey for this account; use another device or another security key")

// ErrCancelled is returned when the person closed the browser's passkey prompt, or the browser
// refused without saying why (NotAllowedError, the one answer that covers a closed prompt, a
// refusal and some failures to verify). ErrTimedOut and ErrNotVerified are ErrCancelled too, so
// a caller that only handles cancelling still works; one that tells them apart checks them first.
var ErrCancelled = errors.New("passkey: cancelled")

// kindError is a named failure that is also a parent (errors.Is).
type kindError struct {
	msg    string
	parent error
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.parent }

// ErrTimedOut is returned when the prompt was open for the whole time the page allowed
// (PromptTimeout) and no one answered. It is an ErrCancelled.
var ErrTimedOut error = &kindError{"passkey: the prompt timed out", ErrCancelled}

// ErrNotVerified is returned when the browser says the authenticator could not verify the person
// (a wrong PIN, a fingerprint that did not match, no screen lock set up). It is an ErrCancelled.
// Most browsers do not say so and report NotAllowedError: that is ErrCancelled.
var ErrNotVerified error = &kindError{"passkey: the authenticator could not verify you (wrong PIN or biometric, or none is set up on this device)", ErrCancelled}

// ErrInsecureContext is returned for a SecurityError: passkeys work on https pages and localhost,
// for the domain the page is served from.
var ErrInsecureContext = errors.New("passkey: not allowed on this page (passkeys need https or localhost, and the page's own domain)")

// ErrUnsupportedAuthenticator is returned when the authenticator cannot do what was asked
// (the algorithms offered, a resident key, user verification).
var ErrUnsupportedAuthenticator = errors.New("passkey: this authenticator cannot do what the page asked (a discoverable passkey with user verification)")

// PromptTimeout is how long the page lets the browser's prompt stay open (the WebAuthn timeout
// option); a NotAllowedError after about this long is ErrTimedOut.
const PromptTimeout = 60 * time.Second

// promptTimeout is PromptTimeout, a variable so a test can shorten it.
var promptTimeout = PromptTimeout

// classify turns a rejection of navigator.credentials.create or get (the DOMException's name and
// message, and how long the prompt was open) into one of this package's errors. nil: it is not a
// failure this package knows, and the caller returns the rejection as it is.
func classify(name, message string, elapsed time.Duration) error {
	switch name {
	case "InvalidStateError":
		return ErrAlreadyRegistered
	case "SecurityError":
		return ErrInsecureContext
	case "NotSupportedError", "ConstraintError":
		return ErrUnsupportedAuthenticator
	case "NotAllowedError":
		low := strings.ToLower(message)
		switch {
		case elapsed >= promptTimeout-promptTimeout/20:
			return ErrTimedOut
		case strings.Contains(low, "verification") || strings.Contains(low, "biometric") || strings.Contains(low, "pin"):
			return ErrNotVerified
		}
	}
	if name == "NotAllowedError" || name == "AbortError" {
		return cancelled(name, message)
	}
	return nil
}

func cancelled(name, message string) error {
	return &kindError{"passkey: cancelled (" + name + ": " + message + ")", ErrCancelled}
}
