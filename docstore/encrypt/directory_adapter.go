package encrypt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// ErrNotPublishable is returned by AdapterDirectory.Publish for a user whose e-mail cannot be
// vouched for: no e-mail, or a session that did not come from an identity provider (a passkey
// session carries no e-mail: the address typed at registration is a label nothing verified).
var ErrNotPublishable = errors.New("encrypt: only a signed-in account with a verified e-mail can publish a key")

const directoryPrefix = "directory/"

// passkeySessionPrefix starts the ID of a user a passkey sign-in made (auth/passkey).
const passkeySessionPrefix = "passkey:"

// AdapterDirectory is a KeyDirectory over a docstore.Adapter. A key is a record
// "directory/<hash of the e-mail>/<fingerprint>" holding the public key, who published it and
// the address they published it for. Publish writes one for the signed-in user; Lookup reads
// them.
//
// What this does and does not guarantee. Publish checks the user it is given. Lookup trusts what
// the store holds, so it is only as good as who can write "directory/" records: the server in
// front of Store must let a record under an e-mail's hash be written only by a session of that
// address (not built; today whoever can write to Store can write a record that claims to be
// verified). The e-mail hash is unsalted: it keeps addresses out of the list of IDs, not out
// of a guess. Lookup lists the whole store to find the prefix: fine for a small store, to be
// replaced by a prefix lister for a big one.
type AdapterDirectory struct {
	Store docstore.Adapter
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

type directoryRecord struct {
	V         int       `json:"v"`
	Key       string    `json:"key"`
	Publisher string    `json:"publisher"` // the user's ID
	Email     string    `json:"email"`     // the address, as the publisher's provider verified it
	Verified  bool      `json:"verified"`  // set by Publish after its checks
	At        time.Time `json:"at"`
}

func emailHash(email string) string {
	sum := sha256.Sum256([]byte(normalizeEmail(email)))
	return hex.EncodeToString(sum[:])
}

// fingerprintSegment makes a fingerprint ("SHA256:ab+/c") usable as the last part of an ID.
func fingerprintSegment(fp string) string {
	return strings.NewReplacer("/", "_", "+", "-", ":", "-").Replace(fp)
}

func directoryID(email, fingerprint string) string {
	return directoryPrefix + emailHash(email) + "/" + fingerprintSegment(fingerprint)
}

// parsePublic reads a public key by its shape.
func parsePublic(text string) (Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return ParseAgeRecipient(text)
	}
	return ParseSSHRecipient(text)
}

// Publish records key as a key of user's e-mail address. It refuses (ErrNotPublishable) unless
// the user has an e-mail and came from a provider session: a passkey user ("passkey:...") has
// none that is verified. The type and fingerprint are recomputed from key.Text. Publishing the
// same key again replaces the record.
func (d *AdapterDirectory) Publish(ctx context.Context, user auth.User, key Recipient) error {
	if normalizeEmail(user.Email) == "" || user.ID == "" || strings.HasPrefix(user.ID, passkeySessionPrefix) {
		return ErrNotPublishable
	}
	r, err := parsePublic(key.Text)
	if err != nil {
		return err
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	data, err := json.Marshal(directoryRecord{V: 1, Key: r.Text, Publisher: user.ID, Email: normalizeEmail(user.Email), Verified: true, At: now().UTC()})
	if err != nil {
		return err
	}
	return d.Store.Save(ctx, directoryID(user.Email, r.Fingerprint), data)
}

// Lookup returns the keys published for email. A record counts as Verified only when it was
// written by Publish for this address by a provider user; a record that does not say so, names
// another address, has a key that does not match its ID, or comes from a passkey user is
// returned unverified (the owner then confirms the fingerprint), and an unreadable one is
// skipped.
func (d *AdapterDirectory) Lookup(ctx context.Context, email string) ([]PublishedKey, error) {
	want := normalizeEmail(email)
	if want == "" {
		return nil, nil
	}
	prefix := directoryPrefix + emailHash(want) + "/"
	metas, err := d.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("encrypt: listing the key directory: %w", err)
	}
	var out []PublishedKey
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, prefix) {
			continue
		}
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var rec directoryRecord
		if json.Unmarshal(doc.Data, &rec) != nil {
			continue
		}
		r, err := parsePublic(rec.Key)
		if err != nil || directoryID(want, r.Fingerprint) != m.ID {
			continue // a key that is not the one its record is named for
		}
		verified := rec.Verified && normalizeEmail(rec.Email) == want && rec.Publisher != "" && !strings.HasPrefix(rec.Publisher, passkeySessionPrefix)
		out = append(out, PublishedKey{Text: r.Text, Type: r.Type, Fingerprint: r.Fingerprint, Verified: verified})
	}
	return out, nil
}

var _ KeyDirectory = (*AdapterDirectory)(nil)
