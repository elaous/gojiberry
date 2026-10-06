package encrypt

import (
	"context"
	"encoding/base64"
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
// "directory/<keyed hash of the e-mail>/<fingerprint>" holding the public key, who published it
// (the user's ID and provider) and the address, signed by the server's DirectorySigner.
// Publish writes one for the signed-in user; Lookup reads them.
//
// What this guarantees. Lookup marks a key Verified only when its record carries a valid
// signature of the server's directory key for exactly that record (its ID, key, publisher,
// provider, address and time), from a provider user (not a passkey one), for the address
// asked. Anyone who can write the store but does not hold the key cannot make a record
// Verified, and cannot even compute the name of an address's records, because the name is an
// HMAC of the address under that key. The server (docstore.Server) also keeps "directory/"
// out of its document API, so its own sessions cannot write there at all; the signature is
// what holds when the store is reachable some other way. What is not guaranteed: a signature
// proves the server published the record for that account, not that the key is safe; whoever
// holds the directory key (the server) can publish anything; and the verified e-mail is only
// as good as the identity provider's claim.
//
// With no Signer the directory is off: Publish returns ErrDirectoryOff and Lookup finds nothing.
// Lookup lists only the address's own prefix (docstore.PrefixLister when the store has it).
type AdapterDirectory struct {
	// Store holds the records. Use the raw store, not an encrypting wrapper's view of it
	// (DefaultSkip leaves "directory/" readable in either case).
	Store  docstore.Adapter
	Signer DirectorySigner
	// MaxKeys is how many keys one address may have published (20 when 0).
	MaxKeys int
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// directoryRecord is the stored form. Sig signs signedBody (the other fields and the record's
// ID), base64.
type directoryRecord struct {
	V         int    `json:"v"`
	Key       string `json:"key"`
	Label     string `json:"label,omitempty"`
	Publisher string `json:"publisher"` // the user's ID, "{provider}:{subject}"
	Provider  string `json:"provider"`
	Email     string `json:"email"` // the address, as the publisher's provider verified it
	At        int64  `json:"at"`    // unix seconds
	Sig       string `json:"sig"`
}

func (rec directoryRecord) message(id string) []byte {
	b, _ := json.Marshal([]any{"gojiberry-directory-v1", id, rec.Key, rec.Label, rec.Publisher, rec.Provider, rec.Email, rec.At})
	return b
}

func (d *AdapterDirectory) emailHash(email string) string {
	return d.Signer.Pseudonym("email:" + normalizeEmail(email))
}

// fingerprintSegment makes a fingerprint ("SHA256:ab+/c") usable as the last part of an ID.
func fingerprintSegment(fp string) string {
	return strings.NewReplacer("/", "_", "+", "-", ":", "-").Replace(fp)
}

func (d *AdapterDirectory) addressPrefix(email string) string {
	return directoryPrefix + d.emailHash(email) + "/"
}

func (d *AdapterDirectory) directoryID(email, fingerprint string) string {
	return d.addressPrefix(email) + fingerprintSegment(fingerprint)
}

// parsePublic reads a public key by its shape.
func parsePublic(text string) (Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return ParseAgeRecipient(text)
	}
	return ParseSSHRecipient(text)
}

// cleanLabel keeps a label short and printable: control characters are dropped, 80 runes at most.
func cleanLabel(s string) string {
	var b []rune
	for _, c := range strings.TrimSpace(s) {
		if c < 0x20 || c == 0x7f {
			continue
		}
		if b = append(b, c); len(b) == 80 {
			break
		}
	}
	return string(b)
}

// providerOf is the provider part of a user ID ("google:123" -> "google"), "" if there is none.
func providerOf(userID string) string {
	p, _, ok := strings.Cut(userID, ":")
	if !ok {
		return ""
	}
	return p
}

// Publish records key as a key of user's e-mail address. It refuses (ErrNotPublishable) unless
// the user has an e-mail and came from a provider session: a passkey user ("passkey:...") has
// none that is verified. The type and fingerprint are recomputed from key.Text. Publishing the
// same key again replaces the record. At most MaxKeys keys are kept for one address.
func (d *AdapterDirectory) Publish(ctx context.Context, user auth.User, key Recipient) error {
	if d.Signer == nil {
		return ErrDirectoryOff
	}
	if normalizeEmail(user.Email) == "" || user.ID == "" || strings.HasPrefix(user.ID, passkeySessionPrefix) || providerOf(user.ID) == "" {
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
	id := d.directoryID(user.Email, r.Fingerprint)
	existing, err := docstore.ListPrefix(ctx, d.Store, d.addressPrefix(user.Email))
	if err != nil {
		return fmt.Errorf("encrypt: listing the key directory: %w", err)
	}
	max := d.MaxKeys
	if max <= 0 {
		max = 20
	}
	replacing := false
	for _, m := range existing {
		if m.ID == id {
			replacing = true
		}
	}
	if !replacing && len(existing) >= max {
		return ErrTooManyKeys
	}
	rec := directoryRecord{V: 2, Key: r.Text, Label: cleanLabel(key.Label), Publisher: user.ID, Provider: providerOf(user.ID), Email: normalizeEmail(user.Email), At: now().Unix()}
	rec.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(rec.message(id)))
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return d.Store.Save(ctx, id, data)
}

// ErrTooManyKeys is returned by Publish when the address already has as many published keys as
// the directory keeps.
var ErrTooManyKeys = errors.New("encrypt: this address already has the most keys the directory keeps")

// Lookup returns the keys published for email. A record counts as Verified only when its
// signature is the server's, for this record and this address, from a provider user; a record
// that fails any of that is returned unverified (the owner then confirms the fingerprint),
// one whose key does not match its ID, or that cannot be read, is skipped. With no Signer it
// returns nothing.
func (d *AdapterDirectory) Lookup(ctx context.Context, email string) ([]PublishedKey, error) {
	want := normalizeEmail(email)
	if want == "" || d.Signer == nil {
		return nil, nil
	}
	prefix := d.addressPrefix(want)
	metas, err := docstore.ListPrefix(ctx, d.Store, prefix)
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
		if err != nil || d.directoryID(want, r.Fingerprint) != m.ID {
			continue // a key that is not the one its record is named for
		}
		sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
		verified := rec.V == 2 && d.Signer.Verify(rec.message(m.ID), sig) &&
			normalizeEmail(rec.Email) == want &&
			rec.Provider != "" && rec.Provider != "passkey" && providerOf(rec.Publisher) == rec.Provider
		out = append(out, PublishedKey{Label: rec.Label, Text: r.Text, Type: r.Type, Fingerprint: r.Fingerprint, Verified: verified})
	}
	return out, nil
}

var _ KeyDirectory = (*AdapterDirectory)(nil)

// DirectoryService is the docstore.DirectoryService over d, for docstore.Server.WithDirectory:
// the routes call it after checking the session. It maps the directory's refusals to the
// errors the server answers with.
func DirectoryService(d *AdapterDirectory) docstore.DirectoryService { return dirService{d} }

type dirService struct{ d *AdapterDirectory }

func (s dirService) Publish(ctx context.Context, user auth.User, key, label string) (docstore.DirectoryKey, error) {
	r, err := parsePublic(key)
	if err != nil {
		return docstore.DirectoryKey{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryBadKey, err)
	}
	r.Label = label
	switch err := s.d.Publish(ctx, user, r); {
	case errors.Is(err, ErrNotPublishable), errors.Is(err, ErrTooManyKeys), errors.Is(err, ErrDirectoryOff):
		return docstore.DirectoryKey{}, fmt.Errorf("%w: %v", docstore.ErrDirectoryRefused, err)
	case err != nil:
		return docstore.DirectoryKey{}, err
	}
	return docstore.DirectoryKey{Key: r.Text, Label: cleanLabel(label), Type: r.Type, Fingerprint: r.Fingerprint, Verified: true}, nil
}

func (s dirService) Lookup(ctx context.Context, email string) ([]docstore.DirectoryKey, error) {
	keys, err := s.d.Lookup(ctx, email)
	if err != nil {
		return nil, err
	}
	out := make([]docstore.DirectoryKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, docstore.DirectoryKey{Key: k.Text, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified})
	}
	return out, nil
}
