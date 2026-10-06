package encrypt

import (
	"context"
	"strings"
)

// PublishedKey is a public key found in a key directory for an e-mail address.
type PublishedKey struct {
	// Text is the public key as it was published ("ssh-ed25519 AAAA..." or "age1...").
	Text string
	// Label is what the publisher called the key (signed with the record).
	Label string
	// Type and Fingerprint are computed from Text when the directory is read, not taken from
	// what the publisher wrote.
	Type, Fingerprint string
	// Verified says the key was registered by an account whose e-mail the identity provider
	// explicitly marked verified (auth.User.VerifiedEmail), while it was signed in. It proves the account and that the account published the key. It does not
	// prove the key is safe (the account's own key may be compromised), so a host that adds
	// a key still shows the fingerprint; false means the e-mail is only a label and the owner
	// must confirm the fingerprint with the person.
	Verified bool
}

// KeyDirectory is where the keys people published for their e-mail address are looked up. It is
// injected: Sharing and the People tab do not know how it is stored. AdapterDirectory is one
// over a docstore.
type KeyDirectory interface {
	// Lookup returns the keys published for email (case does not matter), verified or not. It
	// returns an empty list, not an error, when there are none.
	Lookup(ctx context.Context, email string) ([]PublishedKey, error)
}

// normalizeEmail is the form an address is compared and hashed in.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
