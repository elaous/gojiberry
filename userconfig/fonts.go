package userconfig

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
)

// Fonts are stored as items whose names start with "font." (for example "font.atkinson-regular").
// An item like that is a container: the font file and the licence note that goes with it. A font
// without a licence note is refused (a person who brings a font should be able to say under what
// terms they may use and share it), the file must be a TrueType, OpenType or WOFF file, and fonts
// have their own larger limits that do not eat into the room for settings.

// Limits for fonts, applied when the Store sets none of its own.
const (
	DefaultMaxFont  = 4 << 20  // one font file
	DefaultMaxFonts = 16 << 20 // all of the user's fonts
	maxLicense      = 2 << 10  // the licence note
)

var (
	// ErrNoLicense means a font was offered without a licence note.
	ErrNoLicense = errors.New("userconfig: a font needs a licence note (who made it and under what terms it may be used)")
	// ErrNotAFont means the bytes are not a TrueType, OpenType or WOFF font file.
	ErrNotAFont = errors.New("userconfig: the file is not a TrueType, OpenType or WOFF font")
)

func isFont(name string) bool { return strings.HasPrefix(name, "font.") }

// Font is a decoded font item.
type Font struct {
	// Format is "ttf", "otf", "woff" or "woff2", from the file's own first bytes.
	Format  string
	License string
	Data    []byte
}

var fontMagic = []byte("GJF1")

// EncodeFont packs a font file and its licence note into the form a "font." item holds. It checks
// the same things the store does.
func EncodeFont(data []byte, license string) ([]byte, error) {
	license = strings.TrimSpace(license)
	if license == "" {
		return nil, ErrNoLicense
	}
	if len(license) > maxLicense {
		return nil, errors.New("userconfig: the licence note is over 2 KiB")
	}
	if _, ok := fontFormat(data); !ok {
		return nil, ErrNotAFont
	}
	out := append([]byte(nil), fontMagic...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(license)))
	out = append(out, license...)
	return append(out, data...), nil
}

// DecodeFont reads what EncodeFont wrote, checking it again.
func DecodeFont(b []byte) (Font, error) {
	if len(b) < len(fontMagic)+2 || string(b[:len(fontMagic)]) != string(fontMagic) {
		return Font{}, ErrNotAFont
	}
	n := int(binary.BigEndian.Uint16(b[len(fontMagic):]))
	rest := b[len(fontMagic)+2:]
	if n == 0 || n > maxLicense || n > len(rest) {
		return Font{}, ErrNoLicense
	}
	license, data := strings.TrimSpace(string(rest[:n])), rest[n:]
	if license == "" {
		return Font{}, ErrNoLicense
	}
	format, ok := fontFormat(data)
	if !ok {
		return Font{}, ErrNotAFont
	}
	return Font{Format: format, License: license, Data: append([]byte(nil), data...)}, nil
}

// fontFormat names a font file by its signature.
func fontFormat(b []byte) (string, bool) {
	if len(b) < 12 {
		return "", false
	}
	switch string(b[:4]) {
	case "\x00\x01\x00\x00", "true":
		return "ttf", true
	case "OTTO":
		return "otf", true
	case "wOFF":
		return "woff", true
	case "wOF2":
		return "woff2", true
	}
	return "", false
}

// PutFont stores a font under "font."+name with its licence note (base is the revision read, 0 for new).
func (s *Store) PutFont(ctx context.Context, name string, data []byte, license string, base int) (Item, error) {
	blob, err := EncodeFont(data, license)
	if err != nil {
		return Item{}, err
	}
	return s.Put(ctx, "font."+name, blob, base)
}

// GetFont returns a font stored with PutFont.
func (s *Store) GetFont(ctx context.Context, name string) (Font, Item, error) {
	it, err := s.Get(ctx, "font."+name)
	if err != nil {
		return Font{}, Item{}, err
	}
	f, err := DecodeFont(it.Data)
	return f, it, err
}
