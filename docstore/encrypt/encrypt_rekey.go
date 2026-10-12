package encrypt

import (
	"context"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
)

// rekeyAttempts is how many times a re-key re-reads a file that was saved under it.
const rekeyAttempts = 5

// planned is a stored file opened for re-keying: its plaintext, the digest the write is
// conditional on, and the recipients and Binding it will be sealed with.
type planned struct {
	plain     []byte
	rawDigest string
	rs        []age.Recipient
	b         Binding
}

// plan opens one stored file for re-keying and checks it can be sealed again, without
// writing anything. The file's frame is cross-checked with the record the new recipients
// come from, so a re-key never seals a file under a record older than the one it was sealed
// under (ErrDowngrade). A file below the record's level is exactly what a re-key repairs, so
// that part of the rule is not applied.
func (a *Adapter) plan(ctx context.Context, id string, rk rekeyOpts) (planned, error) {
	d, err := a.Inner.Load(ctx, id)
	if err != nil {
		return planned{}, err
	}
	plain, fi, _, err := a.openWith(ctx, id, d.Data, rk.acceptUnsigned)
	if err != nil {
		return planned{}, err
	}
	rs, b, err := a.bind(ctx, id)
	if err != nil {
		return planned{}, err
	}
	if len(rs) == 0 {
		return planned{}, ErrNoRecipients
	}
	if a.binds(id) {
		cb := b
		if rk.acceptUnsigned && fi.version < 3 {
			// The owner said to seal unsigned files again: the "every file is signed" claim of
			// the record is what such a file contradicts, and sealing it again is what makes it
			// true. Level, seq and recipients are still checked; a v3 frame is never excused.
			cb.Signed = false
		}
		if err := crossCheck(id, fi, cb, false); err != nil {
			return planned{}, err
		}
	}
	return planned{plain, docstore.Digest(d.Data), rs, b}, nil
}

// rekeyOne re-keys one file: read, open, seal to the current recipients, and write only if
// the file is still the one that was read (a Save in between is not lost: the file is read
// again). Without a conditional store the caller's lock is what serializes it.
func (a *Adapter) rekeyOne(ctx context.Context, id string, rk rekeyOpts) error {
	for range rekeyAttempts {
		p, err := a.plan(ctx, id, rk)
		if err != nil {
			return err
		}
		ct, err := a.sealTo(id, p.rs, p.b, p.plain)
		if err != nil {
			return err
		}
		c, ok := a.Inner.(docstore.ConditionalAdapter)
		if !ok {
			return a.Inner.Save(ctx, id, ct)
		}
		if err := c.SaveIf(ctx, id, ct, p.rawDigest); !errors.Is(err, docstore.ErrConflict) {
			return err
		}
	}
	return docstore.ErrConflict
}

// rekeyGroup re-keys the files of one document: all of them are opened and checked first,
// and if any cannot be, none is written (a document is not left half re-keyed because one of
// its files cannot be opened). Write failures after that are collected, not stopping the
// others. The caller holds the lock.
func (a *Adapter) rekeyGroup(ctx context.Context, ids []string) (int, error) {
	done, _, err := a.rekeyGroupFiles(ctx, ids, rekeyOpts{})
	return len(done), err
}

// rekeyGroupFiles is rekeyGroup that says which files it wrote and, when it wrote none because
// some could not be opened, which those were (unreadable).
func (a *Adapter) rekeyGroupFiles(ctx context.Context, ids []string, rk rekeyOpts) (written, unreadable []string, err error) {
	var errs []error
	var present []string
	for _, id := range ids {
		_, err := a.plan(ctx, id, rk)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
			// Deleted since it was listed: nothing to re-key.
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			if !errors.Is(err, ErrDowngrade) {
				unreadable = append(unreadable, id) // a downgrade is a finding, not a lost file
			}
		default:
			present = append(present, id)
		}
	}
	if len(errs) > 0 {
		return nil, unreadable, errors.Join(errs...)
	}
	var done []string
	for _, id := range present {
		if err := a.rekeyOne(ctx, id, rk); err != nil {
			if errors.Is(err, docstore.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		done = append(done, id)
	}
	return done, nil, errors.Join(errs...)
}

// unsignedFiles lists the stored files of the document whose frame is not signed (v1, v2) or
// that are stored in the clear (AllowPlaintext): the ones a re-key by a signing adapter has still
// to seal. A file that cannot be opened (other than a foreign one, or one that is gone) is an
// error: nothing can be said about it. A v3 file with a bad signature is ErrForgedFrame.
func (a *Adapter) unsignedFiles(ctx context.Context, docID string) ([]string, error) {
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		d, err := a.Inner.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		fi, err := a.peekFrameWith(ctx, id, d.Data, true)
		switch {
		case err == nil:
			if fi.version < 3 {
				out = append(out, id)
			}
		case errors.Is(err, ErrNoKey):
		case errors.Is(err, ErrNotEncrypted) && a.AllowPlaintext:
			out = append(out, id)
		case errors.Is(err, ErrForgedFrame):
			return nil, err
		default:
			return nil, fmt.Errorf("%w: %s: %w", ErrUnreadable, id, err)
		}
	}
	return out, nil
}

// exclusive takes the store's Lock and then the adapter's own, which a Save of an encrypted
// id takes: what a re-key holds from its read to its write.
func (a *Adapter) exclusive(ctx context.Context) (func(), error) {
	unlock, err := a.Lock(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	return func() { a.mu.Unlock(); unlock() }, nil
}

// groupOf names the group of files that are re-keyed as one unit: the document that DocID
// finds, or, for a stored item that is not a document's, the item alone. Rekey, RekeyAll and
// RekeyDocument all group this way.
func groupOf(id string) string {
	if d := DocID(id); d != "" {
		return d
	}
	return id
}

// filesOf lists the stored files, not skipped, whose group is g. The caller holds the lock,
// so what is listed is what is then read.
func (a *Adapter) filesOf(ctx context.Context, g string) ([]string, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range metas {
		if !a.skip(m.ID) && groupOf(m.ID) == g {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// Rekey saves a document again to the keyring's current recipients, with a new data key:
// after a recipient is taken out of the keyring, this is what stops them reading what is
// saved from now on (they keep whatever they decrypted before, and a copy of the old
// ciphertext they may hold). It re-keys every file of the document id belongs to (as
// RekeyDocument does), all or none. It holds the store lock (so a caller must not already
// hold it) and the adapter's, so a Save through this adapter is not overwritten by it, on a
// store that writes conditionally or not. A file that is stored in the clear
// (AllowPlaintext) is encrypted by it.
func (a *Adapter) Rekey(ctx context.Context, id string) error {
	if a.skip(id) {
		return nil
	}
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, groupOf(id))
	if err != nil {
		return err
	}
	found := false
	for _, x := range ids {
		found = found || x == id
	}
	if !found {
		return docstore.ErrNotFound
	}
	_, err = a.rekeyGroup(ctx, ids)
	return err
}

// RekeyDocument encrypts every stored file of one document again, to the keyring's current
// recipients for it (a new data key each): its current and previous copies and its versions.
// The files are listed under the lock and all opened first, and none is written if one
// cannot be (see RekeyIDs).
func (a *Adapter) RekeyDocument(ctx context.Context, docID string) (int, error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return 0, err
	}
	return a.rekeyGroup(ctx, ids)
}

// RekeyDocumentFiles is RekeyDocument that says which files it wrote (done) and which of the
// document's files it listed and did not write (stale: they are still encrypted to whatever
// recipients they had), so a caller can report what a failed re-key left behind.
func (a *Adapter) RekeyDocumentFiles(ctx context.Context, docID string) (done, stale []string, err error) {
	done, stale, _, err = a.RekeyDocumentReport(ctx, docID)
	return done, stale, err
}

// RekeyDocumentReport is RekeyDocumentFiles that also says which of the files could not be
// opened (unreadable, a subset of stale): the reason none was written. Nothing is dropped:
// what to do with a file that cannot be opened is the caller's decision.
func (a *Adapter) RekeyDocumentReport(ctx context.Context, docID string) (done, stale, unreadable []string, err error) {
	return a.rekeyDocumentReport(ctx, docID, rekeyOpts{})
}

// rekeyOpts changes what a re-key accepts.
type rekeyOpts struct {
	// acceptUnsigned: an unsigned (v1, v2) file is opened and sealed again with a signed frame
	// even where the record says every file is signed or the Adapter has RequireSigned (the
	// owner's AcceptUnsigned). A v3 file with a bad signature is still refused.
	acceptUnsigned bool
}

func (a *Adapter) rekeyDocumentReport(ctx context.Context, docID string, rk rekeyOpts) (done, stale, unreadable []string, err error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer unlock()
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return nil, ids, nil, err
	}
	done, unreadable, err = a.rekeyGroupFiles(ctx, ids, rk)
	if err != nil {
		in := map[string]bool{}
		for _, d := range done {
			in[d] = true
		}
		for _, id := range ids {
			if !in[id] {
				stale = append(stale, id)
			}
		}
	}
	return done, stale, unreadable, err
}

// RekeyIDs re-keys the given files as one unit (the files of one document): all are opened
// first and none is written if any cannot be opened. It returns how many were written and
// all the errors.
func (a *Adapter) RekeyIDs(ctx context.Context, ids []string) (int, error) {
	var todo []string
	for _, id := range ids {
		if !a.skip(id) {
			todo = append(todo, id)
		}
	}
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return a.rekeyGroup(ctx, todo)
}

// RekeyAll re-keys every document that is encrypted (not the ones Skip leaves readable),
// and returns how many files it wrote. A document with a file it cannot open is left as it
// was (none of its files written) and the others go on; every error is returned, joined.
// The files are listed under the lock; one that is gone by the time it is read is skipped.
func (a *Adapter) RekeyAll(ctx context.Context) (int, error) {
	unlock, err := a.exclusive(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return 0, err
	}
	groups := map[string][]string{}
	var order []string
	for _, m := range metas {
		if a.skip(m.ID) {
			continue
		}
		g := groupOf(m.ID)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], m.ID)
	}
	n := 0
	var errs []error
	for i, g := range order {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err) // stopped between two documents: none is left half done
			break
		}
		k, err := a.rekeyGroup(ctx, groups[g])
		n += k
		if err != nil {
			errs = append(errs, err)
		}
		if a.RekeyProgress != nil {
			a.RekeyProgress(i+1, len(order))
		}
	}
	return n, errors.Join(errs...)
}
