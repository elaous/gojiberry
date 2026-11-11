package saver

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docdiff"
	"strconv"
	"strings"
	"time"
)

// SaveVersion snapshots the document being edited.
func (s *Session) SaveVersion(ctx context.Context) error {
	if s.ID() == "" {
		return errors.New("save the document before saving a version of it")
	}
	doc, c, err := s.current()
	if err != nil {
		return err
	}
	if _, err := s.Saver.SaveVersionBy(ctx, s.targetName(), c, doc, s.Author); err != nil {
		return storageFull(err)
	}
	return s.after(ctx, msg("saver.versionSaved", "A version of %s saved", s.Name()))
}

// OpenVersion opens a snapshot in the editor.
func (s *Session) OpenVersion(ctx context.Context, key string) error {
	doc, err := s.Saver.LoadVersion(ctx, s.targetName(), key)
	if err != nil {
		return err
	}
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	s.say(msg("saver.versionOpened", "Version opened"))
	return nil
}

func (s *Session) CompareVersion(ctx context.Context, key string) error {
	older, err := s.Saver.LoadVersion(ctx, s.targetName(), key)
	if err != nil {
		return err
	}
	newer, _, err := s.current()
	if err != nil {
		return err
	}
	changes := docdiff.Compare(older, newer)
	s.set(func() { s.diff, s.diffKey = changes, key })
	s.say(msg("saver.versionCompared", "Compared with the current document"))
	return nil
}

func (s *Session) RestoreVersion(ctx context.Context, key string) error {
	if s.ID() == "" {
		return errors.New("save the document before restoring a version of it")
	}
	older, err := s.Saver.LoadVersion(ctx, s.targetName(), key)
	if err != nil {
		return err
	}
	doc, c, err := s.current()
	if err != nil {
		return err
	}
	v, err := s.Saver.SaveVersionBy(ctx, s.targetName(), c, doc, s.Author)
	if err != nil {
		return storageFull(err)
	}
	label := "Before restoring " + versionLabel(key)
	if err := s.Saver.RenameVersion(ctx, s.targetName(), v.Key, label); err != nil {
		return err
	}
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(older) })
	}
	return s.after(ctx, msg("saver.versionRestored", "Version restored; what you had is kept as a version"))
}

func versionLabel(key string) string {
	rest := key[strings.LastIndex(key, "/")+1:]
	stamp, _, _ := strings.Cut(rest, ".")
	if ns, err := strconv.ParseInt(stamp, 10, 64); err == nil {
		return time.Unix(0, ns).Format("2006-01-02 15:04:05")
	}
	return rest
}

// RenameVersion gives a snapshot a name ("" removes it).
func (s *Session) RenameVersion(ctx context.Context, key, name string) error {
	if err := s.Saver.RenameVersion(ctx, s.targetName(), key, name); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.versionRenamed", "Version renamed"))
}

func (s *Session) PinVersion(ctx context.Context, key string, pinned bool) error {
	if err := s.Saver.PinVersion(ctx, s.targetName(), key, pinned); err != nil {
		return err
	}
	if pinned {
		return s.after(ctx, msg("saver.versionPinned", "Version kept"))
	}
	return s.after(ctx, msg("saver.versionUnpinned", "Version no longer kept"))
}

// DeleteVersion removes one snapshot.
func (s *Session) DeleteVersion(ctx context.Context, key string) error {
	if err := s.Saver.DeleteVersion(ctx, s.targetName(), key); err != nil {
		return err
	}
	s.set(func() {
		if s.diffKey == key {
			s.diff, s.diffKey = nil, ""
		}
	})
	return s.after(ctx, msg("saver.versionDeleted", "Version deleted"))
}

func (s *Session) Versions() components.VersionsProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.VersionsProps{Pending: s.Pending, Save: s.SaveVersion, Open: s.OpenVersion, Delete: s.DeleteVersion, Compare: s.CompareVersion, Diff: s.diff, DiffKey: s.diffKey, Rename: s.RenameVersion, Pin: s.PinVersion, Restore: s.RestoreVersion}
	if s.id != "" {
		p.Name = s.name
	}
	for _, v := range s.versions {
		p.Versions = append(p.Versions, components.DocVersion{Key: v.Key, Time: v.Time, Size: v.Size, Name: v.Name, Pinned: v.Pinned, Author: v.Author})
	}
	return p
}
