package saver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Protection levels (docs/encryption-and-passkeys.md, C.1 and C.2).
const (
	ProtectionServer = "server"
	ProtectionE2EE   = docfile.ProtectionE2EE
)

const ConfidentialName = "Confidential document"

var ErrProtection = errors.New("saver: the protection level is \"server\" or \"e2ee\"")

func level(p string) string {
	if p == ProtectionE2EE {
		return ProtectionE2EE
	}
	return ProtectionServer
}

func (e catalogEntry) confidential() bool { return e.Protection == ProtectionE2EE }

func (e *catalogEntry) conceal() {
	e.Protection, e.Name, e.Tags = ProtectionE2EE, ConfidentialName, nil
}

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

// Reveal returns the real name and tags of a document.
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

func (s *Saver) rekey(ctx context.Context, target, id, to string) error {
	if s.Rekey == nil {
		return nil
	}
	if err := s.Rekey(ctx, target, id, to); err != nil {
		return fmt.Errorf("saver: protection %q: files not all re-keyed (run it again to finish): %w", to, err)
	}
	return nil
}

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

func lockedEntry(m docstore.Meta, known map[string]catalogEntry) catalogEntry {
	if k, ok := known[m.ID]; ok {
		k.Key, k.Size, k.Sum, k.Prev, k.PrevSum = m.ID, m.Size, "", "", ""
		return k
	}
	e := catalogEntry{Key: m.ID, Size: m.Size, Updated: m.UpdatedAt.UnixNano()}
	e.conceal()
	return e
}

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
