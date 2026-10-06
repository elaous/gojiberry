package encrypt

import (
	"context"
	"strings"

	"github.com/elaous/gojiberry/docstore"
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
	// Held says whoever published the key decrypted a challenge sent to it first: it was held
	// at publish time (proof of possession). Attested says Verified, and the key's own
	// signature binds it to the address (ssh-ed25519 keys; age and passkey keys cannot attest);
	// Attestation is that signature, which VerifyAttestation checks without the server's word.
	Held        bool
	Attested    bool
	Attestation *docstore.DirectoryAttestation
	// OwnerEnrolled: the address has an owner signing key. OwnerSigned: Verified, and the
	// owner signed this publication under that key (the strongest mark: it holds against the
	// directory's server too; Owner is the evidence, VerifyOwnerEvidence checks it without the
	// server's word). OwnerResetPending: a reset of the owner key is waiting out its delay.
	OwnerEnrolled, OwnerSigned, OwnerResetPending bool
	Owner                                         *docstore.DirectoryOwnerEvidence
	// Seq is the number of the transparency log entry of the publication (0: none: a record
	// written before the log, or by someone without the log's key), Kid the ID of the server key
	// that signed the record.
	Seq int64
	Kid string
	// Revoked and RevokedAt (Unix seconds) are set only for a key found by a lookup that asked
	// for revoked keys (AdapterDirectory.LookupWith).
	Revoked   bool
	RevokedAt int64
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
