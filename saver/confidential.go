package saver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Protection levels (docs/encryption-and-passkeys.md, C.1 and C.2). A document is
// protected by the server (ProtectionServer, the default: the catalog lists its real name
// and tags, searches and server features work) or end to end (ProtectionE2EE: the real
// name and tags are inside the document, and the catalog lists ConfidentialName and no
// tags). Sizes, times, the folder and the trash flag stay in the catalog either way.
//
// The level is a field of the catalog entry and of the document (docfile.ProtectionKey), so a
// catalog rebuilt from the documents keeps it, and nothing infers it from a name. Which keys
// the document's files are encrypted to is the store's (docstore/encrypt keyring), not
// something the saver decides: the saver keeps the catalog from betraying what the
// encryption hides.
const (
	ProtectionServer = "server"
	ProtectionE2EE   = docfile.ProtectionE2EE
)

// ConfidentialName is what the catalog lists as the name of an end-to-end document.
const ConfidentialName = "Confidential document"

// ErrProtection is returned for a protection level that is neither ProtectionServer nor
// ProtectionE2EE.
var ErrProtection = errors.New("saver: the protection level is \"server\" or \"e2ee\"")

// level normalizes a stored protection ("" is the default).
func level(p string) string {
	if p == ProtectionE2EE {
		return ProtectionE2EE
	}
	return ProtectionServer
}

func (e catalogEntry) confidential() bool { return e.Protection == ProtectionE2EE }

// conceal makes the entry list the placeholder: the real name and tags are in the document.
func (e *catalogEntry) conceal() {
	e.Protection, e.Name, e.Tags = ProtectionE2EE, ConfidentialName, nil
}

// withCatalog gives a document read from its file what only the catalog knows. For a
// server document that is the name and tags (the catalog is where they are edited). For an
// end-to-end document the file is the authority for both, since the catalog holds none, and
// the file is marked so that it says what it is when read without the catalog.
func (e catalogEntry) withCatalog(doc *docfile.Document) {
	doc.SetFolder(e.Folder)
	if e.confidential() {
		doc.SetProtection(ProtectionE2EE)
		if doc.Name() == "" {
			doc.SetName(ConfidentialName)
		}
		return
	}
	doc.SetProtection("")
	doc.SetName(e.Name)
	doc.SetTags(e.Tags)
}

// Reveal returns the real name and tags of a document. For a server document they are the
// catalog's; for an end-to-end one they are read out of the document, which therefore has to
// be openable here (an error wrapping docstore.ErrLocked when no key opens it): until then a
// client shows ConfidentialName.
func (s *Saver) Reveal(ctx context.Context, target, id string) (name string, tags []string, err error) {
	e, err := s.Get(ctx, target, id)
	if err != nil {
		return "", nil, err
	}
	if e.Protection != ProtectionE2EE {
		return e.Name, e.Tags, nil
	}
	doc, _, err := s.LoadChecked(ctx, target, id)
	if err != nil {
		return "", nil, err
	}
	return doc.Name(), doc.Tags(), nil
}

// SetProtection switches a document to a protection level and saves it again with its
// name and tags where that level keeps them: to ProtectionE2EE they move into the document
// and the catalog lists ConfidentialName and no tags; back to ProtectionServer they move out
// into the catalog again. The document has to be openable here. Before enabling end to end
// protection the ConfidentialCheck hook, if there is one, must agree (a refusal is returned,
// wrapped, and nothing changes).
//
// Keys. With Saver.Rekey set (encrypt.RekeyHook), every stored file of the document (the
// current copy, the previous copy, each version snapshot) is encrypted again to the new level's
// recipients, in an order that never leaves a confidential document wrapped for a server key
// once SetProtection has returned nil:
//
//   - to ProtectionE2EE the re-key runs first (the signed record flips, then every file is
//     rewritten for people's keys only), and only then do the real name and tags move into
//     the document, so no file that holds them is ever wrapped for the server. If the re-key
//     fails, the error names the files still wrapped to the old set (encrypt.RekeyError),
//     the catalog still says server, nothing was moved; later saves of the document already
//     use the new keys.
//   - to ProtectionServer the catalog and the document change first, still wrapped for
//     people, and the re-key to the server's keys runs last. If it fails, the document is
//     server level in the catalog but still wrapped for people only: nothing is exposed.
//
// A document whose protection was lowered behind the owner's back (its signed recipient record
// deleted or an older one put back, which the encrypting adapter proves from inside the files:
// docstore.ErrDowngrade) is neither switched nor saved: SetProtection and Save return that error,
// wrapped, with nothing written, in either direction. The switch's own order below never
// looks like one to the adapter: the record is written first and left pending until every file
// was re-keyed, so a retry after a failure is always safe.
//
// SetProtection is idempotent and the way to retry: called again with the same level it runs
// the hook again even if the catalog already says so (the hook does nothing when the level is
// settled), which finishes a re-key that was cut short. Without Rekey, the store's keyring
// alone decides who the files are encrypted to and the copies and versions written before
// keep the wrapping they had until they are re-keyed (encrypt.Sharing.Repair).
//
// WARNING (no Rekey hook only): the previous generation of the document and its version
// snapshots still hold the pre-switch plaintext, real name and tags included, until the
// store's keyring re-keys them.
func (s *Saver) SetProtection(ctx context.Context, target, id, to string) error {
	if to != ProtectionServer && to != ProtectionE2EE {
		return ErrProtection
	}
	to = level(to)
	cur, err := s.Get(ctx, target, id)
	if err != nil {
		return err
	}
	if cur.Protection == to {
		// Already so in the catalog: no check, nothing to move. The files may still be
		// half way (an earlier re-key failed): finishing is the hook's idempotent job.
		return s.rekey(ctx, target, id, to)
	}
	if to == ProtectionE2EE {
		if s.ConfidentialCheck != nil {
			if err := s.ConfidentialCheck(ctx, target, id); err != nil {
				return fmt.Errorf("saver: not made confidential: %w", err)
			}
		}
		if err := s.rekey(ctx, target, id, to); err != nil {
			return err
		}
	}
	if err := s.setLevel(ctx, target, id, to); err != nil {
		return err
	}
	if to == ProtectionServer {
		return s.rekey(ctx, target, id, to)
	}
	return nil
}

// rekey runs the Rekey hook, if there is one.
func (s *Saver) rekey(ctx context.Context, target, id, to string) error {
	if s.Rekey == nil {
		return nil
	}
	if err := s.Rekey(ctx, target, id, to); err != nil {
		return fmt.Errorf("saver: protection %q: files not all re-keyed (run it again to finish): %w", to, err)
	}
	return nil
}

// setLevel moves the name and tags and the level in the catalog and the document.
func (s *Saver) setLevel(ctx context.Context, target, id, to string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		e, ok := cat.Docs[id]
		if !ok {
			return ErrNotFound
		}
		if level(e.Protection) == to {
			return nil
		}
		realName, realTags := e.Name, e.Tags
		e.Protection = "" // prepareTouch marks the file with the level the entry has
		if to == ProtectionE2EE {
			e.Protection = ProtectionE2EE
		}
		ran := false
		err := s.touch(ctx, t, cat, id, &e, later, func(d *docfile.Document) {
			ran = true
			if to == ProtectionE2EE {
				d.SetName(realName)
				d.SetTags(realTags)
				return
			}
			realName, realTags = d.Name(), d.Tags() // the file is the authority for an end-to-end document
		})
		if err != nil {
			return err
		}
		if !ran {
			return errors.New("saver: unknown format")
		}
		if to == ProtectionE2EE {
			e.conceal()
		} else {
			e.Protection, e.Name, e.Tags = "", realName, realTags
			if e.Name == "" {
				e.Name = id
			}
		}
		cat.Docs[id] = e
		return nil
	})
}

// lockedEntry is the catalog entry for a file that cannot be opened here (a rebuild
// without the key): all that is known is the file, so it is listed as confidential, under
// the placeholder, unless the catalog being replaced already lists it.
func lockedEntry(m docstore.Meta, known map[string]catalogEntry) catalogEntry {
	if k, ok := known[m.ID]; ok {
		k.Key, k.Size, k.Sum, k.Prev, k.PrevSum = m.ID, m.Size, "", "", ""
		return k
	}
	e := catalogEntry{Key: m.ID, Size: m.Size, Updated: m.UpdatedAt.UnixNano()}
	e.conceal()
	return e
}

// UnreadableKeys returns, from the error of SetProtection (or of the Rekey hook), the version
// snapshots of the document that could not be opened and so blocked the re-key (it is all or
// none: one lost file stops the switch). It is empty when the failure was something else, or
// when the files that could not be opened are not snapshots (the current and previous copies
// of a document are never offered for dropping). Nothing is dropped by finding them: show
// them to the person, and if they confirm, pass them to Saver.DropVersions and run
// SetProtection again.
func UnreadableKeys(err error) []string {
	var u interface{ UnreadableFiles() []string }
	if !errors.As(err, &u) {
		return nil
	}
	var out []string
	for _, k := range u.UnreadableFiles() {
		if checkVersionKey(k) == nil {
			out = append(out, k)
		}
	}
	return out
}

// DropVersions deletes the given version snapshots of the document, which the person has
// confirmed they give up (see UnreadableKeys): the way past a snapshot that cannot be opened
// and so blocks the re-keys (SetProtection, and the Add, Remove, Repair and Recover of
// encrypt.Sharing, which end by re-keying every file). A snapshot that cannot be opened does not
// block Save or Load of the document (they do not scan version snapshots); a generation or
// previous copy that cannot be opened blocks those too and is not droppable here. Every key must be a snapshot of this document, else nothing is
// deleted and ErrName is returned. A key that is already gone is not an error.
func (s *Saver) DropVersions(ctx context.Context, target, id string, keys []string) error {
	if !docfile.ValidID(id) {
		return ErrID
	}
	for _, k := range keys {
		if checkVersionKey(k) != nil || !strings.HasPrefix(k, VersionPrefix+id+"/") {
			return fmt.Errorf("%w: %s", ErrName, k)
		}
	}
	for _, k := range keys {
		if err := s.DeleteVersion(ctx, target, k); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}
