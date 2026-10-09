package components

import (
	"errors"

	"github.com/elaous/gojiberry/auth/passkey/client"
)

// The passkey ceremonies fail in ways a person can act on, and auth/passkey/client names them. They are
// registered here, with the Passkey component that shows them, so every host gets them without a call: a
// closed prompt, a timeout, a wrong PIN, a device that already holds a passkey for the account, a page that
// is not https, an authenticator that cannot do what was asked. Without these ExplainError would file them
// all under "something went wrong".
func init() {
	RegisterErrorGroups(
		// ErrTimedOut and ErrNotVerified are ErrCancelled, so they come first.
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.timedOut", Default: "The passkey prompt was open for a minute and nobody answered it. Try again and answer the prompt on your device.",
			Match: func(err error) bool { return errors.Is(err, client.ErrTimedOut) }},
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.notVerified", Default: "Your device could not verify that it is you: the PIN or fingerprint did not match, or no screen lock is set up on this device. Set one up, or try again.",
			Match: func(err error) bool { return errors.Is(err, client.ErrNotVerified) }},
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.cancelled", Default: "The passkey prompt was closed or refused, so nothing was done. Try again when you are ready.",
			Match: func(err error) bool { return errors.Is(err, client.ErrCancelled) }},
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.already", Default: "This device already has a passkey for this account. Use another device or another security key.",
			Match: func(err error) bool { return errors.Is(err, client.ErrAlreadyRegistered) }},
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.insecure", Default: "Passkeys only work on a secure page (https, or localhost) and for the website's own address. Open the page from its https address.",
			Match: func(err error) bool { return errors.Is(err, client.ErrInsecureContext) }},
		ErrorGroup{Kind: ErrKindPasskey, ID: "err.passkey.unsupported", Default: "This device or security key cannot do what the page asked (a passkey that can be found by the site and checks that it is you). Use another device or another security key.",
			Match: func(err error) bool { return errors.Is(err, client.ErrUnsupportedAuthenticator) }},
	)
}
