package encrypt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
)

// ConfidentialCheck is the hook a saver calls before a change.
func ConfidentialCheck(sharing *Sharing, owners ...Recipient) func(ctx context.Context, target, id string) error {
	return func(ctx context.Context, _, id string) error {
		var shared []Recipient
		if sharing != nil {
			list, err := sharing.List(ctx, id)
			if err != nil {
				return err
			}
			for _, s := range list {
				shared = append(shared, Recipient{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint, Text: s.Key, Hint: s.Hint})
			}
		}
		return CheckRecoverableOwner(owners, shared)
	}
}

// ProtectionE2EE is the end-to-end protection level.
const ProtectionE2EE = docfile.ProtectionE2EE

// ErrProtection is returned for an unknown protection level.
var ErrProtection = errors.New("encrypt: the protection level is \"server\" or \"e2ee\"")

func normalizeProtection(p string) (string, error) {
	switch p {
	case "", "server":
		return "", nil
	case ProtectionE2EE:
		return ProtectionE2EE, nil
	}
	return "", ErrProtection
}

// ConfidentialKeyring chooses recipients by protection level.
type ConfidentialKeyring struct {
	// People is the owners' keyring.
	People StoreKeyring
	// AtRest is the server-level keyring.
	AtRest Keyring
	// ServerRecipients are keys the server holds.
	ServerRecipients []age.Recipient
}

// IsConfidential reports whether a record is confidential.
func (k ConfidentialKeyring) IsConfidential(ctx context.Context, docID string) (bool, error) {
	rec, _, err := loadRecord(ctx, k.People.Store, docID, k.People.OwnerSigners, k.People.SeqFloor)
	return rec.Protection == ProtectionE2EE, err
}

// Recipients implements Keyring.
func (k ConfidentialKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	rs, _, err := k.Bind(ctx, id)
	return rs, err
}

// Binding implements Binder from the signed record.
func (k ConfidentialKeyring) Binding(ctx context.Context, id string) (Binding, error) {
	doc := DocID(id)
	if doc == "" {
		return Binding{}, nil
	}
	rec, digest, err := loadRecord(ctx, k.People.Store, doc, k.People.OwnerSigners, k.People.SeqFloor)
	if err != nil {
		return Binding{}, err
	}
	return rec.binding(digest != ""), nil
}

// Bind implements Binder: recipients and the binding.
func (k ConfidentialKeyring) Bind(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	doc := DocID(id)
	if doc == "" {
		rs, err := k.AtRest.Recipients(ctx, id)
		return rs, Binding{}, err
	}
	rec, digest, err := loadRecord(ctx, k.People.Store, doc, k.People.OwnerSigners, k.People.SeqFloor)
	if err != nil {
		return nil, Binding{}, err
	}
	b := rec.binding(digest != "")
	if rec.Protection != ProtectionE2EE {
		rs, err := k.AtRest.Recipients(ctx, id)
		return rs, b, err
	}
	all := append([]age.Recipient(nil), k.People.Owners...)
	for _, e := range rec.Recipients {
		r, err := parseShared(e)
		if err != nil {
			return nil, Binding{}, fmt.Errorf("encrypt: a recipient of %s cannot be read: %w", doc, err)
		}
		all = append(all, r.Key)
	}
	server, err := k.serverIDs()
	if err != nil {
		return nil, Binding{}, err
	}
	out := all[:0:0]
	for _, r := range all {
		if len(server) > 0 {
			rid, err := recipientID(r)
			if err != nil {
				return nil, Binding{}, err // fail closed: it cannot be told from a server key
			}
			if rid.isServer(server) {
				continue
			}
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, Binding{}, ErrNoRecipients
	}
	return out, b, nil
}

func (k ConfidentialKeyring) serverIDs() (map[string][]string, error) {
	var m map[string][]string
	for _, s := range k.ServerRecipients {
		id, err := recipientID(s)
		if err != nil {
			return nil, err
		}
		if m == nil {
			m = map[string][]string{}
		}
		m[id.id] = append(m[id.id], id.fp)
	}
	return m, nil
}

var ErrUncomparableRecipient = errors.New("encrypt: a recipient's key cannot be compared with the server's keys")

type recipID struct{ id, fp string }

// fails closed: a tag-only match counts as a server key
func (r recipID) isServer(server map[string][]string) bool {
	for _, fp := range server[r.id] {
		if r.fp == "" || fp == "" || fp == r.fp {
			return true
		}
	}
	return false
}

func recipientID(r age.Recipient) (recipID, error) {
	if s, ok := r.(fmt.Stringer); ok {
		return recipID{id: "text:" + s.String()}, nil
	}
	var fileKey [16]byte // any key: only the stanza's public header is read
	stanzas, err := r.Wrap(fileKey[:])
	if err == nil {
		for _, st := range stanzas {
			if (st.Type == "ssh-ed25519" || st.Type == "ssh-rsa") && len(st.Args) > 0 {
				id := recipID{id: "ssh:" + st.Type + ":" + st.Args[0]}
				if kf, ok := r.(keyFingerprinter); ok {
					id.fp = kf.keyFingerprint()
				}
				return id, nil
			}
		}
	}
	return recipID{}, fmt.Errorf("%w: %T", ErrUncomparableRecipient, r)
}

// Identities implements Keyring.
func (k ConfidentialKeyring) Identities(ctx context.Context, id string) ([]age.Identity, error) {
	out := append([]age.Identity(nil), k.People.Keys...)
	more, err := k.AtRest.Identities(ctx, id)
	return append(out, more...), err
}

// RekeyError is returned by SetProtection when a re-key fails.
type RekeyError struct {
	Doc        string
	Stale      []string
	Unreadable []string
	Err        error
}

func (e *RekeyError) Error() string {
	msg := fmt.Sprintf("encrypt: re-keying %s did not finish, still wrapped to the previous recipients: %s: %v", e.Doc, strings.Join(e.Stale, ", "), e.Err)
	if len(e.Unreadable) > 0 {
		msg += fmt.Sprintf(" (cannot be opened, so none was re-keyed: %s)", strings.Join(e.Unreadable, ", "))
	}
	return msg
}

func (e *RekeyError) Unwrap() error { return e.Err }

// UnreadableFiles are stored files that could not be read.
func (e *RekeyError) UnreadableFiles() []string { return e.Unreadable }

// SetProtection records a document's protection level.
func (s *Sharing) SetProtection(ctx context.Context, docID, protection string) (int, error) {
	want, err := normalizeProtection(protection)
	if err != nil {
		return 0, err
	}
	if !docfile.ValidID(docID) {
		return 0, errors.New("encrypt: not a document ID")
	}
	cur, digest, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	if err != nil {
		return 0, err
	}
	if cur.Protection == want && !cur.Pending {
		if digest == "" && s.Adapter != nil {
			// no record: server level counts only if no file says one existed
			if err := s.Adapter.checkDocumentByID(ctx, docID, Binding{}); err != nil {
				return 0, err
			}
		}
		return 0, nil // settled: read only
	}
	rec, err := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		if rec.Protection == want {
			return false, nil
		}
		rec.Protection, rec.Pending = want, true
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if rec.Protection == want && !rec.Pending {
		return 0, nil
	}
	if err := s.setIndex(ctx, docID, want, rec.Seq); err != nil {
		return 0, err
	}
	n, stale, unreadable, err := s.finishFiles(ctx, docID, repairOpts{})
	if err != nil && len(stale) > 0 {
		err = &RekeyError{Doc: docID, Stale: stale, Unreadable: unreadable, Err: err}
	}
	return n, err
}

// RekeyHook returns the function to set on the saver.
func RekeyHook(sharing *Sharing) func(ctx context.Context, target, id, protection string) error {
	return func(ctx context.Context, _, id, protection string) error {
		_, err := sharing.SetProtection(ctx, id, protection)
		return err
	}
}
