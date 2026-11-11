package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAlreadyRegistered = errors.New("this device already has a passkey for this account; use another device or another security key")

var ErrCancelled = errors.New("passkey: cancelled")

type kindError struct {
	msg    string
	parent error
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.parent }

var ErrTimedOut error = &kindError{"passkey: the prompt timed out", ErrCancelled}

var ErrNotVerified error = &kindError{"passkey: the authenticator could not verify you (wrong PIN or biometric, or none is set up on this device)", ErrCancelled}

var ErrInsecureContext = errors.New("passkey: not allowed on this page (passkeys need https or localhost, and the page's own domain)")

var ErrUnsupportedAuthenticator = errors.New("passkey: this authenticator cannot do what the page asked (a discoverable passkey with user verification)")

const PromptTimeout = 60 * time.Second

var promptTimeout = PromptTimeout

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

var ErrAlreadyCreated error = &codedError{msg: "passkey: the first passkey for this account was already made in another tab or on another device; reload and add another passkey from your account if you want one", code: CodeAccountExists}

const CodeAccountExists = "account_exists"

type codedError struct{ msg, code string }

func (e *codedError) Error() string       { return e.msg }
func (e *codedError) PasskeyCode() string { return e.code }
func (e *codedError) Is(t error) bool {
	c, ok := t.(*codedError)
	return ok && c.code == e.code
}

func serverError(status int, body []byte) error {
	var e struct{ Error, Code string }
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		if status == 409 && e.Code == CodeAccountExists {
			return ErrAlreadyCreated
		}
		return &serverMessage{e.Error}
	}
	return fmt.Errorf("passkey: the server answered %d", status)
}

type serverMessage struct{ text string }

func (e *serverMessage) Error() string       { return e.text }
func (e *serverMessage) UserMessage() string { return e.text }
