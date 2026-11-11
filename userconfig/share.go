package userconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/tokens"
)

const (
	KindTemplate = "template"
	KindStyle    = "style"
	KindTokens   = "tokens"
)

const (
	bundleTag     = "gojiberry-share"
	bundleVersion = 1
	maxBundle     = 1 << 20 // the largest bundle read (data is base64 inside it)
	maxNote       = 500
)

var (
	ErrNotShareable = errors.New("userconfig: only templates, styles and themes can be shared (names template.*, style.* and tokens.*)")
	ErrBadBundle    = errors.New("userconfig: the shared file cannot be used")
)

type bundle struct {
	Tag     string `json:"tag"`
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Note    string `json:"note,omitempty"`
	SHA256  string `json:"sha256"`
	Data    []byte `json:"data"`
}

type Info struct {
	Kind, Name, Note string
	Size             int
}

func kindOf(name string) (string, bool) {
	k, rest, found := strings.Cut(name, ".")
	if !found || rest == "" {
		return "", false
	}
	switch k {
	case KindTemplate, KindStyle, KindTokens:
		return k, true
	}
	return "", false
}

func (s *Store) Export(ctx context.Context, name, note string) ([]byte, error) {
	_, ok := kindOf(name)
	if !ok {
		return nil, ErrNotShareable
	}
	it, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return Bundle(name, it.Data, note)
}

func Bundle(name string, data []byte, note string) ([]byte, error) {
	kind, ok := kindOf(name)
	if !ok {
		return nil, ErrNotShareable
	}
	if len([]rune(note)) > maxNote {
		note = string([]rune(note)[:maxNote])
	}
	sum := sha256.Sum256(data)
	return json.Marshal(bundle{Tag: bundleTag, Version: bundleVersion, Kind: kind, Name: name, Note: note, SHA256: hex.EncodeToString(sum[:]), Data: data})
}

func OpenBundle(b []byte) (Info, []byte, error) {
	bu, info, err := parseBundle(b)
	if err != nil {
		return Info{}, nil, err
	}
	return info, bu.Data, nil
}

func Inspect(b []byte) (Info, error) {
	_, info, err := parseBundle(b)
	return info, err
}

func parseBundle(b []byte) (bundle, Info, error) {
	bad := func(why string) (bundle, Info, error) {
		return bundle{}, Info{}, fmt.Errorf("%w: %s", ErrBadBundle, why)
	}
	if len(b) > maxBundle {
		return bad("it is over 1 MiB")
	}
	var bu bundle
	if err := json.Unmarshal(b, &bu); err != nil || bu.Tag != bundleTag {
		return bad("it is not a share file")
	}
	if bu.Version != bundleVersion {
		return bad("it was made by a newer version")
	}
	kind, ok := kindOf(bu.Name)
	if !ok || kind != bu.Kind || !nameRe.MatchString(bu.Name) {
		return bad("its kind or name is not valid")
	}
	if sum := sha256.Sum256(bu.Data); hex.EncodeToString(sum[:]) != bu.SHA256 {
		return bad("it was changed after it was made")
	}
	if len(bu.Data) > DefaultMaxItem {
		return bad("it is too large")
	}
	switch kind {
	case KindTokens:
		var set tokens.Set
		if json.Unmarshal(bu.Data, &set) != nil {
			return bad("the theme cannot be read")
		}
		if ps := set.Validate(); len(ps) > 0 {
			return bad("the theme is not readable enough to use: " + ps[0].Message)
		}
	default:
		if !json.Valid(bu.Data) {
			return bad("its content cannot be read")
		}
	}
	if len([]rune(bu.Note)) > maxNote {
		bu.Note = string([]rune(bu.Note)[:maxNote])
	}
	return bu, Info{Kind: kind, Name: bu.Name, Note: bu.Note, Size: len(bu.Data)}, nil
}

// Import writes a bundle into the user's own store.
func (s *Store) Import(ctx context.Context, b []byte, as string) (Item, error) {
	bu, _, err := parseBundle(b)
	if err != nil {
		return Item{}, err
	}
	name := bu.Name
	if as != "" {
		if k, ok := kindOf(as); !ok || k != bu.Kind || !nameRe.MatchString(as) {
			return Item{}, ErrBadName
		}
		name = as
	}
	for i := 1; i <= 20; i++ {
		try := name
		if i > 1 {
			try = name + "-" + strconv.Itoa(i)
			if len(try) > 64 {
				break
			}
		}
		it, err := s.Put(ctx, try, bu.Data, 0)
		if errors.Is(err, ErrConflict) { // taken: base 0 means "expect none"
			continue
		}
		return it, err
	}
	return Item{}, errors.New("userconfig: too many items with that name already")
}
