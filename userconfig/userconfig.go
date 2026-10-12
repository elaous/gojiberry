// Package userconfig keeps a person's settings (preferences, styles, templates) in a docstore, so
// they follow the person across devices. They can reveal a disability, so the rules are strict:
// the keys hold a hash of the user, never the address; the package writes through whatever adapter it
// is given, which for a real deployment is the encrypting adapter (docstore.ConfigPrefix is not in
// its skip list, so the owner's keys protect every item); nothing here logs a name or a value;
// and each item has a revision, so two devices that change the same item meet a conflict instead of
// one silently overwriting the other.
package userconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// Limits, applied when the Store sets none of its own.
const (
	DefaultMaxItem  = 256 << 10 // one item
	DefaultMaxTotal = 2 << 20   // everything of one user
)

var (
	// ErrConflict means the item changed since it was read; Put returns it with the current item.
	ErrConflict = errors.New("userconfig: the item changed since it was read")
	// ErrQuota means the change would take the user over a limit.
	ErrQuota = errors.New("userconfig: over the storage limit")
	// ErrBadName means a name outside lower-case letters, digits, '.', '_' and '-' (1 to 64 long).
	ErrBadName = errors.New("userconfig: a name is 1 to 64 of a-z, 0-9, '.', '_' and '-'")
	// ErrNotFound is returned by Get and Delete for an item that is not there.
	ErrNotFound = errors.New("userconfig: no such item")
)

var nameRe = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// Item is one stored setting.
type Item struct {
	Name string
	// Rev counts the writes of the item, from 1.
	Rev     int
	Data    []byte
	Updated time.Time
}

// Store is one user's configuration in an adapter.
type Store struct {
	A    docstore.Adapter
	User string // any stable id of the user; only its hash reaches the keys
	// MaxItem and MaxTotal are the size limits in bytes of one item and of all of the user's items;
	// zero means the defaults. Fonts (names starting "font.", see fonts.go) have their own: MaxFont
	// for one and MaxFonts for all of them, and do not count against the other two.
	MaxItem, MaxTotal int
	MaxFont, MaxFonts int
}

// envelope is what is stored: the revision travels inside the (encrypted) bytes.
type envelope struct {
	Rev     int    `json:"rev"`
	Data    []byte `json:"data"`
	Updated int64  `json:"updated"`
}

func (s *Store) prefix() string {
	sum := sha256.Sum256([]byte(s.User))
	return docstore.ConfigPrefix + hex.EncodeToString(sum[:16]) + "/"
}

func (s *Store) key(name string) (string, error) {
	if !nameRe.MatchString(name) || s.User == "" {
		return "", ErrBadName
	}
	return s.prefix() + name, nil
}

func (s *Store) limits() (item, total int) {
	item, total = s.MaxItem, s.MaxTotal
	if item <= 0 {
		item = DefaultMaxItem
	}
	if total <= 0 {
		total = DefaultMaxTotal
	}
	return item, total
}

// limitsFor are the limits that apply to the item called name: the font ones or the ordinary ones.
func (s *Store) limitsFor(name string) (item, total int) {
	if !isFont(name) {
		return s.limits()
	}
	item, total = s.MaxFont, s.MaxFonts
	if item <= 0 {
		item = DefaultMaxFont
	}
	if total <= 0 {
		total = DefaultMaxFonts
	}
	return item, total
}

// Get returns an item.
func (s *Store) Get(ctx context.Context, name string) (Item, error) {
	it, _, err := s.read(ctx, name)
	return it, err
}

// read returns the item and the raw stored bytes it came from (to condition a write on them).
func (s *Store) read(ctx context.Context, name string) (Item, []byte, error) {
	k, err := s.key(name)
	if err != nil {
		return Item{}, nil, err
	}
	d, err := s.A.Load(ctx, k)
	if errors.Is(err, docstore.ErrNotFound) {
		return Item{}, nil, ErrNotFound
	}
	if err != nil {
		return Item{}, nil, err
	}
	var e envelope
	if err := json.Unmarshal(d.Data, &e); err != nil {
		return Item{}, nil, fmt.Errorf("userconfig: the stored item cannot be read")
	}
	return Item{Name: name, Rev: e.Rev, Data: e.Data, Updated: time.Unix(0, e.Updated)}, d.Data, nil
}

// Put stores data as the next revision of the item. base is the revision the caller read (0 for an
// item it expects not to exist); if the stored revision differs, nothing is written and the current
// item comes back with ErrConflict so the caller can merge and try again (see Update).
func (s *Store) Put(ctx context.Context, name string, data []byte, base int) (Item, error) {
	k, err := s.key(name)
	if err != nil {
		return Item{}, err
	}
	maxItem, maxTotal := s.limitsFor(name)
	if len(data) > maxItem {
		return Item{}, ErrQuota
	}
	if isFont(name) {
		if _, err := DecodeFont(data); err != nil {
			return Item{}, err
		}
	}
	cur, raw, err := s.read(ctx, name)
	switch {
	case errors.Is(err, ErrNotFound):
		cur, raw = Item{}, nil
	case err != nil:
		return Item{}, err
	}
	if cur.Rev != base {
		return cur, ErrConflict
	}
	used, err := s.used(ctx, k, isFont(name))
	if err != nil {
		return Item{}, err
	}
	if used+len(data) > maxTotal {
		return Item{}, ErrQuota
	}
	next := Item{Name: name, Rev: cur.Rev + 1, Data: append([]byte(nil), data...), Updated: time.Now().Truncate(time.Nanosecond)}
	b, err := json.Marshal(envelope{Rev: next.Rev, Data: next.Data, Updated: next.Updated.UnixNano()})
	if err != nil {
		return Item{}, err
	}
	if c, ok := s.A.(docstore.ConditionalAdapter); ok {
		prev := ""
		if raw != nil {
			prev = docstore.Digest(raw)
		}
		if err = c.SaveIf(ctx, k, b, prev); err == nil {
			return next, nil
		}
		if errors.Is(err, docstore.ErrConflict) {
			now, _, rerr := s.read(ctx, name)
			if rerr != nil {
				return Item{}, ErrConflict
			}
			return now, ErrConflict
		}
		if !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return Item{}, err
		}
	}
	if err := s.A.Save(ctx, k, b); err != nil {
		return Item{}, err
	}
	return next, nil
}

// used is the bytes the user's other items of the same class (fonts or not) take; the one being
// written is replaced, not added.
func (s *Store) used(ctx context.Context, except string, fonts bool) (int, error) {
	metas, err := docstore.ListPrefix(ctx, s.A, s.prefix())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if m.ID != except && isFont(strings.TrimPrefix(m.ID, s.prefix())) == fonts {
			n += m.Size
		}
	}
	return n, nil
}

// Update reads the item, lets merge produce the new data from it (nil Item.Rev 0 when it does not
// exist yet), and stores that, repeating when another device wrote in between (up to five times).
func (s *Store) Update(ctx context.Context, name string, merge func(cur Item) ([]byte, error)) (Item, error) {
	var last error
	for range 5 {
		cur, err := s.Get(ctx, name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Item{}, err
		}
		data, err := merge(cur)
		if err != nil {
			return Item{}, err
		}
		it, err := s.Put(ctx, name, data, cur.Rev)
		if !errors.Is(err, ErrConflict) {
			return it, err
		}
		last = err
	}
	return Item{}, last
}

// List returns the names of the user's items, sorted.
func (s *Store) List(ctx context.Context) ([]string, error) {
	if s.User == "" {
		return nil, ErrBadName
	}
	metas, err := docstore.ListPrefix(ctx, s.A, s.prefix())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range metas {
		names = append(names, strings.TrimPrefix(m.ID, s.prefix()))
	}
	sort.Strings(names)
	return names, nil
}

// Delete removes an item.
func (s *Store) Delete(ctx context.Context, name string) error {
	k, err := s.key(name)
	if err != nil {
		return err
	}
	err = s.A.Delete(ctx, k)
	if errors.Is(err, docstore.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// DeleteAll removes everything of the user (the right to be forgotten) and returns how many items.
func (s *Store) DeleteAll(ctx context.Context) (int, error) {
	names, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, name := range names {
		if err := s.Delete(ctx, name); err != nil && !errors.Is(err, ErrNotFound) {
			return n, err
		}
		n++
	}
	return n, nil
}
