package encrypt

import (
	"bytes"
	"context"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"sort"
	"strings"
)

// decrypt opens the age layer of a stored file; ok is false for a file that is not encrypted
// and AllowPlaintext lets it through (stored is then the plaintext).
func (a *Adapter) decrypt(ctx context.Context, id string, stored []byte) (r io.Reader, ok bool, err error) {
	if !IsEncrypted(stored) {
		if !a.AllowPlaintext {
			return nil, false, ErrNotEncrypted
		}
		return nil, false, nil
	}
	ids, err := a.Ring.Identities(ctx, id)
	if err != nil {
		return nil, true, keyringNotFound(err)
	}
	if len(ids) == 0 {
		return nil, true, ErrNoKey // a keyring with no private key opens nothing
	}
	r, err = age.Decrypt(bytes.NewReader(stored), ids...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, true, ErrNoKey
		}
		return nil, true, fmt.Errorf("encrypt: %w", err)
	}
	return r, true, nil
}

// open returns the plaintext of a stored file, what its frame said, and whether it was
// encrypted. It does not cross-check the frame with the record: the callers do, each with
// the rule that suits it (crossCheck).
func (a *Adapter) open(ctx context.Context, id string, stored []byte) (plain []byte, fi frameInfo, encrypted bool, err error) {
	return a.openWith(ctx, id, stored, false)
}

// openWith is open, with acceptUnsigned as verifyFrame has it.
func (a *Adapter) openWith(ctx context.Context, id string, stored []byte, acceptUnsigned bool) (plain []byte, fi frameInfo, encrypted bool, err error) {
	r, enc, err := a.decrypt(ctx, id, stored)
	if err != nil || !enc {
		if err == nil {
			return stored, frameInfo{}, false, nil
		}
		return nil, frameInfo{}, enc, err
	}
	framed, err := io.ReadAll(r)
	if err != nil {
		return nil, frameInfo{}, true, fmt.Errorf("encrypt: %w", err)
	}
	plain, fi, err = unframe(id, framed)
	if err == nil {
		err = a.verifyFrame(id, fi, plain, true, acceptUnsigned)
	}
	if err != nil {
		return nil, frameInfo{}, true, err
	}
	return plain, fi, true, nil
}

// mainPrefix is the prefix of the stored files that carry a document's current content: the
// generations "<ID>~<tag>.<ext>" the saver writes, or a confidential folder's one record.
func mainPrefix(id string) string {
	if strings.HasPrefix(id, docstore.FolderSecretPrefix) {
		return id
	}
	return DocID(id)
}

// checkDocument cross-checks the record binding r of the document id belongs to with what the
// document's stored files say (the rules of crossCheck without the strict one: this is the write
// side). It reads nothing for an id that is not a document's or for a keyring that is not a
// Binder. See checkFiles for which files count as evidence and which stop the operation.
func (a *Adapter) checkDocument(ctx context.Context, id string, r Binding) error {
	return a.checkFiles(ctx, DocID(id), []string{mainPrefix(id)}, r)
}

// checkDocumentByID is checkDocument for a caller that has the document's (or folder's) ID
// and not the id of one of its files: it looks at the document's generations and at a
// confidential folder's record.
func (a *Adapter) checkDocumentByID(ctx context.Context, docID string, r Binding) error {
	return a.checkFiles(ctx, docID, []string{docID, docstore.FolderSecretPrefix + docID}, r)
}

// checkFiles cross-checks r with EVERY stored file of the document, whatever order the store
// lists them in or times it reports (both are the store's to choose, so neither is evidence):
//
//   - a file that opens here and verifies (a v3 frame signed by an owner signer, or an older
//     unsigned one, v1 carrying no level) is evidence, and each is checked with crossCheck;
//   - a file that is provably someone else's (a well-formed age file none of our keys matches:
//     ErrNoKey) is skipped, and so is one that is gone (ErrNotFound);
//   - a file that is forged (ErrForgedFrame) fails the check with that error;
//   - any other file that cannot be opened (the age header but malformed or damaged, opening with
//     a key of ours but saying something else, not an age file at all unless AllowPlaintext) is
//     ErrUnreadable: it could be hiding what the record should say, so the operation stops. It
//     is never counted as "no evidence".
//
// A document none of whose files opens (all provably foreign, or none stored) has no evidence
// and passes: what the record says is then all there is.
func (a *Adapter) checkFiles(ctx context.Context, docID string, prefixes []string, r Binding) error {
	if docID == "" || !a.binds(docID) {
		return nil
	}
	seen := map[string]bool{}
	var files []string
	for _, prefix := range prefixes {
		metas, err := docstore.ListPrefix(ctx, a.Inner, prefix)
		if err != nil {
			return err
		}
		for _, m := range metas {
			if !seen[m.ID] && !a.skip(m.ID) && DocID(m.ID) == docID {
				seen[m.ID] = true
				files = append(files, m.ID)
			}
		}
	}
	sort.Strings(files)
	type evidence struct {
		id string
		fi frameInfo
	}
	var found []evidence
	for _, id := range files {
		d, err := a.Inner.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		fi, err := a.peekFrame(ctx, id, d.Data)
		switch {
		case err == nil:
			found = append(found, evidence{id, fi})
		case errors.Is(err, ErrNoKey):
			// Provably someone else's: no evidence from this one.
		case errors.Is(err, ErrNotEncrypted) && a.AllowPlaintext:
			// A file the migration has not encrypted yet.
		case errors.Is(err, ErrForgedFrame):
			return err
		default:
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, id, err)
		}
	}
	// The highest seq first: it is the one that names the record most precisely.
	sort.SliceStable(found, func(i, j int) bool { return found[i].fi.b.Seq > found[j].fi.b.Seq })
	for _, e := range found {
		if err := crossCheck(e.id, e.fi, r, false); err != nil {
			return err
		}
	}
	return nil
}

// bindChecked reads the record (the recipients and their Binding) and checks the document's
// files against it. A downgrade finding is made again once with the record read again: a client
// that finished a switch between the two reads leaves files sealed under a record newer than
// the one this read, which is not a downgrade; a record that is really missing or replaced is
// the same the second time.
func (a *Adapter) bindChecked(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	for attempt := 0; ; attempt++ {
		rs, b, err := a.bind(ctx, id)
		if err != nil {
			return nil, Binding{}, err
		}
		if a.afterBind != nil {
			a.afterBind()
		}
		err = a.checkDocument(ctx, id, b)
		if err == nil {
			return rs, b, nil
		}
		if attempt == 0 && errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrForgedFrame) {
			continue
		}
		return nil, Binding{}, err
	}
}

// Save implements docstore.Adapter: the data is encrypted to the keyring's recipients.
//
// For a document with a record (a Binder keyring) the file written carries the record's level,
// seq and recipient hash, signed by Signer, and before it is written the document's stored
// files are cross-checked with the record the recipients came from: a record that is missing or
// older than a file says it was sealed under is ErrDowngrade, a file that cannot be opened and
// is not provably someone else's is ErrUnreadable, and nothing is written. A confidential
// document without a Signer is ErrNoFrameSigner.
func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	if a.skip(id) {
		return a.Inner.Save(ctx, id, data)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs, b, err := a.bindChecked(ctx, id)
	if err != nil {
		return err
	}
	ct, err := a.sealTo(id, rs, b, data)
	if err != nil {
		return err
	}
	if err := a.Inner.Save(ctx, id, ct); err != nil {
		return err
	}
	return a.afterUnsignedWrite(ctx, id, b)
}

// afterUnsignedWrite closes the loop on an unsigned frame (an Adapter with no Signer) of a
// document with a record that did not yet say Signed when the recipients were read: the record is
// read again, and if it says Signed now, the owner flipped it between that read and this write, so
// the file just written is one the record calls forged. The writer is told (ErrNoFrameSigner),
// instead of finding out on the next Load. (The owner's re-key looks again after it flips, so a
// save that this read misses is found there: see finishFiles.)
func (a *Adapter) afterUnsignedWrite(ctx context.Context, id string, before Binding) error {
	if a.Signer != nil || before.Signed || !a.binds(id) {
		return nil
	}
	_, now, err := a.bind(ctx, id)
	if err != nil {
		return err
	}
	if now.Signed {
		return fmt.Errorf("%w: %s: the document became signed while this client saved it, so the file it wrote is unsigned and refused as forged; the owner's Sharing.Repair with AcceptUnsigned keeps it, or a client with the Signer saves it again", ErrNoFrameSigner, id)
	}
	return nil
}

// Load implements docstore.Adapter: the document is decrypted with the keyring's identities,
// and, for a Binder keyring, the file's frame is cross-checked with the keys record: a file
// that proves the record was deleted or replaced by an older one, or that is wrapped below
// the document's level with no switch in progress, is ErrDowngrade and returns no data. A v1
// file (written before the frame had a level) is read without that check.
func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	for attempt := 0; ; attempt++ {
		d, err := a.Inner.Load(ctx, id)
		if err != nil || a.skip(id) {
			return d, err
		}
		plain, fi, _, err := a.open(ctx, id, d.Data)
		if err != nil {
			return docstore.Document{}, err
		}
		if a.binds(id) {
			if a.beforeBinding != nil {
				a.beforeBinding()
			}
			r, err := a.Ring.(Binder).Binding(ctx, id)
			if err != nil {
				return docstore.Document{}, keyringNotFound(err)
			}
			if err := crossCheck(id, fi, r, true); err != nil {
				// A switch completed between the read of the file and the record leaves the
				// file as it was and the record newer: read both again once. A real finding is
				// the same the second time.
				if attempt == 0 && errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrForgedFrame) {
					continue
				}
				return docstore.Document{}, err
			}
		}
		d.Data = plain
		return d, nil
	}
}

// List implements docstore.Adapter. Sizes are of what is stored (the ciphertext).
func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) { return a.Inner.List(ctx) }

// ListPrefix implements docstore.PrefixLister over Inner (ids are stored as they are).
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return docstore.ListPrefix(ctx, a.Inner, prefix)
}

// Delete implements docstore.Adapter.
func (a *Adapter) Delete(ctx context.Context, id string) error { return a.Inner.Delete(ctx, id) }

// DeleteIf implements docstore.ConditionalDeleter for the ids this adapter leaves in the clear
// (see DefaultSkip); an encrypted id is docstore.ErrConditionalUnsupported.
func (a *Adapter) DeleteIf(ctx context.Context, id string, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalDeleter)
	if !ok || !a.skip(id) {
		return docstore.ErrConditionalUnsupported
	}
	return c.DeleteIf(ctx, id, prevDigest)
}

// SaveIf implements docstore.ConditionalAdapter. prevDigest is the Digest of what the caller
// read through this adapter: for an encrypted id, the plaintext (what Load returned), and ""
// for "there is no such document". It is compared with the decrypted stored file, and the
// write is then conditional on the stored bytes being the ones that were just read, so a
// concurrent change between the check and the write is an ErrConflict (read again and
// retry). A store that cannot write conditionally says so (ErrConditionalUnsupported).
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalAdapter)
	if !ok {
		return docstore.ErrConditionalUnsupported
	}
	if a.skip(id) {
		return c.SaveIf(ctx, id, data, prevDigest)
	}
	if err := a.checkID(id); err != nil {
		return err
	}
	rawPrev := ""
	d, err := a.Inner.Load(ctx, id)
	switch {
	case errors.Is(err, docstore.ErrNotFound):
		if prevDigest != "" {
			return docstore.ErrConflict
		}
	case err != nil:
		return err
	default:
		plain, _, _, err := a.open(ctx, id, d.Data)
		if err != nil {
			return err
		}
		if prevDigest == "" || docstore.Digest(plain) != prevDigest {
			return docstore.ErrConflict
		}
		rawPrev = docstore.Digest(d.Data)
	}
	rs, b, err := a.bindChecked(ctx, id)
	if err != nil {
		return err
	}
	ct, err := a.sealTo(id, rs, b, data)
	if err != nil {
		return err
	}
	if err := c.SaveIf(ctx, id, ct, rawPrev); err != nil {
		return err
	}
	return a.afterUnsignedWrite(ctx, id, b)
}

// Lock implements docstore.Locker by the inner store's, if it has one.
func (a *Adapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := a.Inner.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}
