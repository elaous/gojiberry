package encrypt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
)

// ConfidentialCheck is the hook a saver asks before a document is made end to end protected
// (saver.Saver.ConfidentialCheck): it refuses with ErrNeedSecondRecipient unless one of the
// owner's own keys is not a passkey (CheckRecoverableOwner), so that losing the passkey does
// not lose the document. owners are the owner's keys, the ones every document is encrypted to
// (StoreKeyring.Owners); the keys the document is shared with are read from sharing's signed
// list and, as CheckRecoverableOwner says, do not count: a recovery key has to be one the
// owner controls. A nil sharing means no one else's keys are listed; a list that does not
// verify is an error (fail closed).
//
// This gate is about recovery, not about the past: the keys of the files are chosen by
// ConfidentialKeyring and moved by Sharing.SetProtection (RekeyHook), which the saver calls
// through Saver.Rekey; without that hook a document's previous generation and version
// snapshots keep their old wrapping until Sharing.Repair. The saver does not import this
// package: pass the result as the hook.
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

// ProtectionE2EE is the protection level of an end-to-end document, the value of the signed
// record's protection field (and of saver.ProtectionE2EE). The empty string, or "server", is
// the default: the document is encrypted at rest to the server's keyring.
const ProtectionE2EE = docfile.ProtectionE2EE

// ErrProtection is returned for a protection level that is neither "server" nor ProtectionE2EE.
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

// ConfidentialKeyring is a Keyring that chooses the keys by the document's protection level:
//
//   - a confidential (end to end) document is wrapped ONLY for people's keys: the owner's
//     (People.Owners) and the ones it is shared with in its signed list. No server-held key
//     is ever a recipient: AtRest is not consulted, and any of ServerRecipients that is also
//     among People.Owners (a deployment that put the server key in the owners' list for the
//     at-rest level) is removed from the list, whatever its type (age or SSH). If nothing is
//     left, nothing is saved.
//   - any other document, and every item that is not a document's, is wrapped by AtRest
//     (the server keyring, level C.1 of docs/encryption-and-passkeys.md).
//
// Which documents are confidential is the "protection" field of the document's signed
// keys:<ID> record (Sharing.SetProtection writes it), never a marker in a name or a file:
// the record is verified against People.OwnerSigners, and a record that does not verify is an
// error (fail closed), not a document that falls back to the server level.
//
// Identities are the people's private keys and AtRest's, for every document: opening is not
// where the level is enforced (age uses whichever key matches a file's wrapping), and a
// re-key between levels has to open files wrapped under the old one.
//
// What is and is not defended (C.5). A store writer cannot forge a record, so cannot make a
// record say another level or other recipients. A store writer CAN still, because a record
// is only signed, not bound to a point in time:
//
//   - delete the record, which would make the document's later saves server level, and
//   - put back an older validly signed record (a replay), for example one from before the
//     document was made confidential, which would do the same.
//
// ConfidentialKeyring is a Binder, so the Adapter writes the level and the record's seq into
// the frame inside each file's encryption (only holders of a recipient key can write that),
// and cross-checks the files it reads, replaces or re-keys with the record it is about to use:
// a record that is missing, or older than a file says it was sealed under, is ErrDowngrade,
// with nothing written and no data returned, on a client that has never seen the record
// before. Files still in the older v1 frame carry no level and cannot prove it; the first
// save or re-key after upgrading writes v2. What remains for the other layers: the saver
// re-asserts the level its catalog says on every save of a confidential document
// (Saver.Rekey), the owner-signed index lists the end-to-end documents so a deleted record is
// found without opening the document (Sharing.VerifyIndex), and People.SeqFloor, when set,
// refuses a record older than the highest this client saw (ErrStaleRecord) and a missing one
// where one was seen. What none of it can do is tell a whole-store rollback to an older
// consistent snapshot (files, records and index together) from the truth: that is a rollback of
// data, not a downgrade of protection, and only a client-side floor that outlives it notices.
// SetProtection never deletes a record.
type ConfidentialKeyring struct {
	// People is the people's keyring: owner keys, the signed lists, the owner signers.
	People StoreKeyring
	// AtRest is the server-level keyring.
	AtRest Keyring
	// ServerRecipients are keys the server holds; they are never recipients of a
	// confidential document, even when People.Owners lists one.
	ServerRecipients []age.Recipient
}

// IsConfidential reports, from the signed record, whether a document is end to end.
func (k ConfidentialKeyring) IsConfidential(ctx context.Context, docID string) (bool, error) {
	rec, _, err := loadRecord(ctx, k.People.Store, docID, k.People.OwnerSigners, k.People.SeqFloor)
	return rec.Protection == ProtectionE2EE, err
}

// Recipients implements Keyring.
func (k ConfidentialKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	rs, _, err := k.Bind(ctx, id)
	return rs, err
}

// Binding implements Binder: the signed record's level, seq, pending flag and recipient hash.
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

// Bind implements Binder: Recipients, and the Binding of the record they were chosen by, from
// one read of it.
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
			if server[rid] {
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

func (k ConfidentialKeyring) serverIDs() (map[string]bool, error) {
	var m map[string]bool
	for _, s := range k.ServerRecipients {
		id, err := recipientID(s)
		if err != nil {
			return nil, err
		}
		if m == nil {
			m = map[string]bool{}
		}
		m[id] = true
	}
	return m, nil
}

// ErrUncomparableRecipient is returned by a ConfidentialKeyring that has ServerRecipients
// when a recipient (one of those, or one of the people's) is of a type whose key cannot be
// compared: it could be a server key in disguise, so nothing is encrypted to it.
var ErrUncomparableRecipient = errors.New("encrypt: a recipient's key cannot be compared with the server's keys")

// recipientID is a canonical string for the key of a recipient: the text of an age key, or,
// for an SSH key (whose recipient type is a pointer with no accessor), the type and key tag
// that the recipient puts in its stanza (a hash of the public key, which is what age itself
// uses to pick the identity). Any other type is ErrUncomparableRecipient.
func recipientID(r age.Recipient) (string, error) {
	if s, ok := r.(fmt.Stringer); ok {
		return "text:" + s.String(), nil
	}
	var fileKey [16]byte // any key: only the stanza's public header is read
	stanzas, err := r.Wrap(fileKey[:])
	if err == nil {
		for _, st := range stanzas {
			if (st.Type == "ssh-ed25519" || st.Type == "ssh-rsa") && len(st.Args) > 0 {
				return "ssh:" + st.Type + ":" + st.Args[0], nil
			}
		}
	}
	return "", fmt.Errorf("%w: %T", ErrUncomparableRecipient, r)
}

// Identities implements Keyring.
func (k ConfidentialKeyring) Identities(ctx context.Context, id string) ([]age.Identity, error) {
	out := append([]age.Identity(nil), k.People.Keys...)
	more, err := k.AtRest.Identities(ctx, id)
	return append(out, more...), err
}

// RekeyError is returned by Sharing.SetProtection when the re-key did not finish: Stale are the
// stored files of the document that are still encrypted to the recipients of the previous
// level. The record already says the new level (and pending), so new saves use it; calling
// SetProtection again finishes the rest.
//
// Unreadable are the files, among Stale, that could not be opened: because one file cannot be
// opened the re-key writes none (all or none), so a single lost file blocks the switch (and every
// other re-key of the document, Add, Remove, Repair; not Save or Load when it is a snapshot). It is
// never skipped silently. If it is a version snapshot the owner no longer needs, the caller
// can confirm and drop it (saver.UnreadableKeys, saver.Saver.DropVersions) and run
// SetProtection again; the current and previous copies of a document are not droppable.
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

// UnreadableFiles are the stored files that could not be opened (see Unreadable); the saver
// reads them through this method, as it does not import this package.
func (e *RekeyError) UnreadableFiles() []string { return e.Unreadable }

// SetProtection records a document's protection level in its signed list and encrypts every
// stored file of the document (current and previous copies, version snapshots) again to the
// recipients of that level (a ConfidentialKeyring decides which). It returns how many files
// were written.
//
// The signed record is written first, with pending set, so everything saved from then on
// already uses the new recipients; the files written before are then re-keyed, and pending is
// cleared only when all are. It is idempotent: at a level already settled it only reads and
// verifies the record, writes nothing and does not need the Signer; after a failure it
// finishes what is left. A failure is a *RekeyError naming the files still wrapped to the old
// set, and the ones that could not be opened. Changing the level (or clearing pending) needs
// the Signer.
//
// The frame check (ErrDowngrade) and this order. Each file is sealed with the record's seq in
// its frame. The record is written first (seq N+1, pending), so a file the re-key has not
// reached yet has seq N, which is below the record's: that is never a downgrade (a record's
// seq only rises, and clearing pending raises it again after every file was sealed under
// N+1), and Load accepts such a file because pending is set (a file wrapped below the
// record's level with no switch in progress is refused, see crossCheck). A save in the window
// seals under N+1 and replaces the old file. A failure at any file leaves pending set, so
// every state in between reads as in flight, never as a downgrade, and the same call again
// finishes. A record that is missing, or older than a file's seq, is refused before anything is
// signed or written (ErrDowngrade; Recover is the owner's way forward). The owner-signed index
// follows the record: listed (with the record's seq) for e2ee, removed for server, written
// after the record and repeated on a retry.
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
			// No record to read, so "settled at server level" is only believed if no file
			// of the document says a record once existed (it may have been deleted).
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
	// The owner-signed index follows the record (and is repeated on a retry: it is idempotent).
	if err := s.setIndex(ctx, docID, want, rec.Seq); err != nil {
		return 0, err
	}
	n, stale, unreadable, err := s.finishFiles(ctx, docID, repairOpts{})
	if err != nil && len(stale) > 0 {
		err = &RekeyError{Doc: docID, Stale: stale, Unreadable: unreadable, Err: err}
	}
	return n, err
}

// RekeyHook returns the function to set as saver.Saver.Rekey: it switches a document's
// stored files to the recipients of a protection level (Sharing.SetProtection).
func RekeyHook(sharing *Sharing) func(ctx context.Context, target, id, protection string) error {
	return func(ctx context.Context, _, id, protection string) error {
		_, err := sharing.SetProtection(ctx, id, protection)
		return err
	}
}
