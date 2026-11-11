package encrypt

import (
	"context"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

// PublishedKey is a public key found in a key directory.
type PublishedKey struct {
	// Text is the public key as it was published.
	Text string
	// Label is what the publisher called the key.
	Label string
	// Type and Fingerprint are computed from Text.
	Type, Fingerprint string
	// Verified says the key was registered by an account owner.
	Verified bool
	// Held says whoever published the key proved possession.
	Held        bool
	Attested    bool
	Attestation *docstore.DirectoryAttestation
	// OwnerEnrolled says the address has an owner signing key.
	OwnerEnrolled, OwnerSigned, OwnerResetPending bool
	Owner                                         *docstore.DirectoryOwnerEvidence
	// Seq is the number of the transparency log entry.
	Seq int64
	Kid string
	// Revoked and RevokedAt are set once the key is revoked.
	Revoked   bool
	RevokedAt int64
}

// KeyDirectory is where the keys people published are kept.
type KeyDirectory interface {
	// Lookup returns the keys published for an address.
	Lookup(ctx context.Context, email string) ([]PublishedKey, error)
}

// normalizeEmail is the form an address is compared in.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
