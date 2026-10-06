package saver

import (
	"context"
	"errors"
	"fmt"

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
// This changes what the catalog and the document say, not who the files are encrypted to:
// afterwards the store's keyring (docstore/encrypt) is what decides that, and the copies
// and versions written before keep the wrapping they had until they are re-keyed.
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
		return nil // already so: nothing to check or change
	}
	if to == ProtectionE2EE && s.ConfidentialCheck != nil {
		if err := s.ConfidentialCheck(ctx, target, id); err != nil {
			return fmt.Errorf("saver: not made confidential: %w", err)
		}
	}
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
