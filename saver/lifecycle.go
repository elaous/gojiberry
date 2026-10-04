package saver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// The life of a document beyond saving it: versions (snapshots to go back to),
// the trash (deleted documents that can be restored), copies and tags. They live
// in the same target as the documents, under keys of their own:
//
//	versions/<name>/<time><ext>   a snapshot of <name>, <time> in nanoseconds
//	trash/<key>                   the document <key> after it was trashed
//
// List does not show these keys, and Load, Save and Delete refuse them: use the
// functions here.
const (
	VersionPrefix = "versions/"
	TrashPrefix   = "trash/"
)

// DefaultMaxVersions is how many snapshots of one document are kept when
// Saver.MaxVersions is 0; the oldest go first.
const DefaultMaxVersions = 50

// ErrNotFound is returned for a version or trashed document that is not there.
var ErrNotFound = docstore.ErrNotFound

// Version is a snapshot of a document.
type Version struct {
	Key  string // what LoadVersion and DeleteVersion take
	Name string // the document's name
	Time time.Time
	Size int
}

func (s *Saver) maxVersions() int {
	if s.MaxVersions > 0 {
		return s.MaxVersions
	}
	return DefaultMaxVersions
}

func versionKey(name string, c docfile.Codec, t time.Time) string {
	return fmt.Sprintf("%s%s/%020d%s", VersionPrefix, name, t.UnixNano(), c.Ext())
}

// SaveVersion stores a snapshot of doc as a version of the document called name
// (in the codec), and drops the oldest snapshots beyond Saver.MaxVersions.
func (s *Saver) SaveVersion(ctx context.Context, target, name string, c docfile.Codec, doc docfile.Document) (Version, error) {
	t, err := s.Target(target)
	if err != nil {
		return Version{}, err
	}
	if !validName(name) {
		return Version{}, ErrName
	}
	now := time.Now()
	doc.Updated = now.UnixNano()
	data, err := c.Marshal(doc)
	if err != nil {
		return Version{}, err
	}
	key := versionKey(name, c, now)
	// Two snapshots in one nanosecond cannot happen on a real clock, but keys
	// must not collide on a coarse one.
	for i := 1; ; i++ {
		if _, err := t.Adapter.Load(ctx, key); errors.Is(err, docstore.ErrNotFound) {
			break
		} else if err != nil {
			return Version{}, err
		}
		key = versionKey(name, c, now.Add(time.Duration(i)))
	}
	if err := t.Adapter.Save(ctx, key, data); err != nil {
		return Version{}, err
	}
	if vs, err := s.Versions(ctx, target, name); err == nil && len(vs) > s.maxVersions() {
		for _, old := range vs[s.maxVersions():] {
			_ = t.Adapter.Delete(ctx, old.Key)
		}
	}
	return Version{Key: key, Name: name, Time: now, Size: len(data)}, nil
}

// Versions lists the snapshots of the document called name, newest first.
func (s *Saver) Versions(ctx context.Context, target, name string) ([]Version, error) {
	t, err := s.Target(target)
	if err != nil {
		return nil, err
	}
	if !validName(name) {
		return nil, ErrName
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	prefix := VersionPrefix + name + "/"
	var out []Version
	for _, m := range metas {
		rest, ok := strings.CutPrefix(m.ID, prefix)
		if !ok || strings.Contains(rest, "/") {
			continue
		}
		c, stamp := s.codecOf(rest)
		if c == nil {
			continue
		}
		ns, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, Version{Key: m.ID, Name: name, Time: time.Unix(0, ns), Size: m.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	return out, nil
}

func checkPrefix(key, prefix string) error {
	if !strings.HasPrefix(key, prefix) || strings.Contains(key, "..") {
		return ErrName
	}
	return nil
}

// LoadVersion reads a snapshot back.
func (s *Saver) LoadVersion(ctx context.Context, target, key string) (docfile.Document, error) {
	return s.loadRaw(ctx, target, key, VersionPrefix)
}

func (s *Saver) loadRaw(ctx context.Context, target, key, prefix string) (docfile.Document, error) {
	if err := checkPrefix(key, prefix); err != nil {
		return docfile.Document{}, err
	}
	t, err := s.Target(target)
	if err != nil {
		return docfile.Document{}, err
	}
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return docfile.Document{}, err
	}
	c, _ := s.codecOf(key)
	if c == nil {
		c = docfile.Detect(stored.Data)
	}
	return c.Unmarshal(stored.Data)
}

// DeleteVersion removes one snapshot.
func (s *Saver) DeleteVersion(ctx context.Context, target, key string) error {
	if err := checkPrefix(key, VersionPrefix); err != nil {
		return err
	}
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	return t.Adapter.Delete(ctx, key)
}

// Trash moves the saved document key to the trash. Its versions stay, so a
// restored document keeps its history.
func (s *Saver) Trash(ctx context.Context, target, key string) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	if reserved(key) || strings.Contains(key, "/") {
		return ErrName
	}
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return err
	}
	// An earlier trashed copy by the same name is kept, not overwritten.
	c, name := s.codecOf(key)
	dest := TrashPrefix + key
	if c != nil {
		for i := 2; ; i++ {
			if _, err := t.Adapter.Load(ctx, dest); errors.Is(err, docstore.ErrNotFound) {
				break
			} else if err != nil {
				return err
			}
			dest = fmt.Sprintf("%s%s (%d)%s", TrashPrefix, name, i, c.Ext())
		}
	}
	if err := t.Adapter.Save(ctx, dest, stored.Data); err != nil {
		return err
	}
	return t.Adapter.Delete(ctx, key)
}

// Trashed lists the documents in the trash, the most recently trashed first. An
// Entry's Key is the trash key (give it to Restore and Purge), Name the
// document's, and Updated when it was trashed.
func (s *Saver) Trashed(ctx context.Context, target string) ([]Entry, error) {
	t, err := s.Target(target)
	if err != nil {
		return nil, err
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, m := range metas {
		rest, ok := strings.CutPrefix(m.ID, TrashPrefix)
		if !ok {
			continue
		}
		if c, name := s.codecOf(rest); c != nil && validName(name) {
			out = append(out, Entry{Key: m.ID, Name: name, Format: c, Size: m.Size, Updated: m.UpdatedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// uniqueKey returns key, or name with " (n)" added to it until nothing is saved
// under it.
func (s *Saver) uniqueKey(ctx context.Context, t Target, name, ext string) (string, error) {
	for i := 0; ; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)", name, i+1)
		}
		key := n + ext
		if _, err := t.Adapter.Load(ctx, key); errors.Is(err, docstore.ErrNotFound) {
			return key, nil
		} else if err != nil {
			return "", err
		}
	}
}

// Restore puts a trashed document back and returns its key. If a document by
// that name has been saved since, the restored one is named "<name> (restored)".
func (s *Saver) Restore(ctx context.Context, target, trashKey string) (string, error) {
	if err := checkPrefix(trashKey, TrashPrefix); err != nil {
		return "", err
	}
	t, err := s.Target(target)
	if err != nil {
		return "", err
	}
	stored, err := t.Adapter.Load(ctx, trashKey)
	if err != nil {
		return "", err
	}
	orig := strings.TrimPrefix(trashKey, TrashPrefix)
	c, name := s.codecOf(orig)
	if c == nil || !validName(name) || reserved(orig) || strings.Contains(orig, "/") {
		return "", ErrName
	}
	key := orig
	if _, err := t.Adapter.Load(ctx, key); err == nil {
		if key, err = s.uniqueKey(ctx, t, name+" (restored)", c.Ext()); err != nil {
			return "", err
		}
	} else if !errors.Is(err, docstore.ErrNotFound) {
		return "", err
	}
	if err := t.Adapter.Save(ctx, key, stored.Data); err != nil {
		return "", err
	}
	return key, t.Adapter.Delete(ctx, trashKey)
}

// Purge deletes a trashed document for good.
func (s *Saver) Purge(ctx context.Context, target, trashKey string) error {
	if err := checkPrefix(trashKey, TrashPrefix); err != nil {
		return err
	}
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	return t.Adapter.Delete(ctx, trashKey)
}

// EmptyTrash deletes everything in the trash and returns how many it removed.
func (s *Saver) EmptyTrash(ctx context.Context, target string) (int, error) {
	list, err := s.Trashed(ctx, target)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		if err := s.Purge(ctx, target, e.Key); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Duplicate saves a copy of the document key under newName (in the same format)
// and returns the new key. With newName "" the copy is called "Copy of <name>"; a
// name already taken gets " (2)", " (3)", and so on.
func (s *Saver) Duplicate(ctx context.Context, target, key, newName string) (string, error) {
	t, err := s.Target(target)
	if err != nil {
		return "", err
	}
	if reserved(key) || strings.Contains(key, "/") {
		return "", ErrName
	}
	c, name := s.codecOf(key)
	if c == nil {
		return "", ErrName
	}
	if newName == "" {
		newName = "Copy of " + name
	}
	if !validName(newName) {
		return "", ErrName
	}
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return "", err
	}
	doc, err := c.Unmarshal(stored.Data)
	if err != nil {
		return "", err
	}
	doc.Title = newName
	data, err := c.Marshal(doc)
	if err != nil {
		return "", err
	}
	newKey, err := s.uniqueKey(ctx, t, newName, c.Ext())
	if err != nil {
		return "", err
	}
	return newKey, t.Adapter.Save(ctx, newKey, data)
}

// SetTags replaces the tags of the saved document key.
func (s *Saver) SetTags(ctx context.Context, target, key string, tags []string) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	if reserved(key) || strings.Contains(key, "/") {
		return ErrName
	}
	c, _ := s.codecOf(key)
	doc, err := s.Load(ctx, target, key)
	if err != nil {
		return err
	}
	if c == nil {
		c = docfile.JSON
	}
	doc.SetTags(tags)
	data, err := c.Marshal(doc)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, key, data)
}

// ListTagged is List with each entry's Tags filled in. It reads every
// document, so it is for stores of a few hundred documents, not thousands.
func (s *Saver) ListTagged(ctx context.Context, target string) ([]Entry, error) {
	list, err := s.List(ctx, target)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if doc, err := s.Load(ctx, target, list[i].Key); err == nil {
			list[i].Tags = doc.Tags()
		}
	}
	return list, nil
}
