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

func (a *Adapter) open(ctx context.Context, id string, stored []byte) (plain []byte, fi frameInfo, encrypted bool, err error) {
	return a.openWith(ctx, id, stored, false)
}

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

func mainPrefix(id string) string {
	if strings.HasPrefix(id, docstore.FolderSecretPrefix) {
		return id
	}
	return DocID(id)
}

func (a *Adapter) checkDocument(ctx context.Context, id string, r Binding) error {
	return a.checkFiles(ctx, DocID(id), []string{mainPrefix(id)}, r)
}

func (a *Adapter) checkDocumentByID(ctx context.Context, docID string, r Binding) error {
	return a.checkFiles(ctx, docID, []string{docID, docstore.FolderSecretPrefix + docID}, r)
}

// store listing order and times are not evidence
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
		case errors.Is(err, ErrNotEncrypted) && a.AllowPlaintext:
		case errors.Is(err, ErrForgedFrame):
			return err
		default:
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, id, err)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].fi.b.Seq > found[j].fi.b.Seq })
	for _, e := range found {
		if err := crossCheck(e.id, e.fi, r, false); err != nil {
			return err
		}
	}
	return nil
}

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

// Save implements docstore.Adapter; the data is encrypted first.
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

// Load implements docstore.Adapter: the document is decrypted.
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

// List implements docstore.Adapter.
func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) { return a.Inner.List(ctx) }

// ListPrefix implements docstore.PrefixLister.
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return docstore.ListPrefix(ctx, a.Inner, prefix)
}

// Delete implements docstore.Adapter.
func (a *Adapter) Delete(ctx context.Context, id string) error { return a.Inner.Delete(ctx, id) }

func (a *Adapter) DeleteIf(ctx context.Context, id string, prevDigest string) error {
	c, ok := a.Inner.(docstore.ConditionalDeleter)
	if !ok || !a.skip(id) {
		return docstore.ErrConditionalUnsupported
	}
	return c.DeleteIf(ctx, id, prevDigest)
}

// SaveIf implements docstore.ConditionalAdapter.
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

// Lock implements docstore.Locker through the inner store.
func (a *Adapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := a.Inner.(docstore.Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}
