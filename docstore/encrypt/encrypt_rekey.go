package encrypt

import (
	"context"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
)

const rekeyAttempts = 5

type planned struct {
	plain     []byte
	rawDigest string
	rs        []age.Recipient
	b         Binding
}

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
			cb.Signed = false
		}
		if err := crossCheck(id, fi, cb, false); err != nil {
			return planned{}, err
		}
	}
	return planned{plain, docstore.Digest(d.Data), rs, b}, nil
}

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

func (a *Adapter) rekeyGroup(ctx context.Context, ids []string) (int, error) {
	done, _, err := a.rekeyGroupFiles(ctx, ids, rekeyOpts{})
	return len(done), err
}

func (a *Adapter) rekeyGroupFiles(ctx context.Context, ids []string, rk rekeyOpts) (written, unreadable []string, err error) {
	var errs []error
	var present []string
	for _, id := range ids {
		_, err := a.plan(ctx, id, rk)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			if !errors.Is(err, ErrDowngrade) {
				unreadable = append(unreadable, id)
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

func (a *Adapter) exclusive(ctx context.Context) (func(), error) {
	unlock, err := a.Lock(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	return func() { a.mu.Unlock(); unlock() }, nil
}

func groupOf(id string) string {
	if d := DocID(id); d != "" {
		return d
	}
	return id
}

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

// Rekey saves a document again to the keyring's recipients.
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

// RekeyDocument encrypts every stored file of one document.
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

// RekeyDocumentFiles is RekeyDocument that reports files.
func (a *Adapter) RekeyDocumentFiles(ctx context.Context, docID string) (done, stale []string, err error) {
	done, stale, _, err = a.RekeyDocumentReport(ctx, docID)
	return done, stale, err
}

// RekeyDocumentReport is RekeyDocumentFiles with a report.
func (a *Adapter) RekeyDocumentReport(ctx context.Context, docID string) (done, stale, unreadable []string, err error) {
	return a.rekeyDocumentReport(ctx, docID, rekeyOpts{})
}

type rekeyOpts struct {
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

// RekeyIDs re-keys the given files as one unit.
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

// RekeyAll re-keys every document that is encrypted.
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
			errs = append(errs, err)
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
