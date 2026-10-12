package userconfig

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

// a few bytes that start like each font file; the store checks the signature, not the glyphs
func fakeFont(magic string, size int) []byte {
	b := make([]byte, size)
	copy(b, magic)
	return b
}

const licence = "Atkinson Hyperlegible, Braille Institute, SIL Open Font License 1.1"

func TestFontsNeedALicenceAndARealFontFile(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	for magic, format := range map[string]string{"\x00\x01\x00\x00": "ttf", "true": "ttf", "OTTO": "otf", "wOFF": "woff", "wOF2": "woff2"} {
		if _, err := s.PutFont(ctx, "f-"+format, fakeFont(magic, 64), licence, 0); err != nil && !errors.Is(err, ErrConflict) {
			t.Fatalf("%q: %v", magic, err)
		}
	}
	f, it, err := s.GetFont(ctx, "f-woff2")
	if err != nil || f.Format != "woff2" || f.License != licence || len(f.Data) != 64 || it.Name != "font.f-woff2" || it.Rev != 1 {
		t.Fatalf("%+v %+v %v", f, it, err)
	}
	if _, err := s.PutFont(ctx, "nolicence", fakeFont("OTTO", 64), "   ", 0); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("no licence: %v", err)
	}
	if _, err := s.PutFont(ctx, "long", fakeFont("OTTO", 64), strings.Repeat("x", 3000), 0); err == nil {
		t.Fatal("a licence note over 2 KiB is refused")
	}
	if _, err := s.PutFont(ctx, "image", []byte("\x89PNG\r\n\x1a\n and more bytes"), licence, 0); !errors.Is(err, ErrNotAFont) {
		t.Fatalf("not a font: %v", err)
	}
	if _, err := s.PutFont(ctx, "tiny", []byte("OTTO"), licence, 0); !errors.Is(err, ErrNotAFont) {
		t.Fatalf("too short to be one: %v", err)
	}
	// the generic Put cannot sneak an unchecked blob in under a font name
	for _, blob := range [][]byte{[]byte("plain bytes"), append([]byte("GJF1\x00\x00"), fakeFont("OTTO", 64)...), []byte("GJF1\x00\x03abc" + "not a font file at all")} {
		if _, err := s.Put(ctx, "font.sneaky", blob, 0); err == nil {
			t.Errorf("%q: stored", blob)
		}
	}
	if _, err := DecodeFont(nil); !errors.Is(err, ErrNotAFont) {
		t.Fatalf("%v", err)
	}
	if _, err := DecodeFont([]byte("GJF1\x00\x05ab")); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("a licence length past the end: %v", err)
	}
	if _, err := DecodeFont([]byte("GJF1\x00\x02  " + "OTTO0000000000")); !errors.Is(err, ErrNoLicense) {
		t.Fatalf("a blank licence: %v", err)
	}
}

func TestFontsHaveTheirOwnLimits(t *testing.T) {
	ctx := context.Background()
	m := docstore.NewMemoryAdapter()
	s := &Store{A: m, User: "u", MaxItem: 100, MaxTotal: 250, MaxFont: 1000, MaxFonts: 1500}
	// a font far over the settings limit fits its own
	if _, err := s.PutFont(ctx, "a", fakeFont("OTTO", 800), licence, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutFont(ctx, "big", fakeFont("OTTO", 1200), licence, 0); !errors.Is(err, ErrQuota) {
		t.Fatalf("over one font: %v", err)
	}
	if _, err := s.PutFont(ctx, "b", fakeFont("OTTO", 800), licence, 0); !errors.Is(err, ErrQuota) {
		t.Fatalf("over all fonts: %v", err)
	}
	// fonts take no room from the settings, nor settings from the fonts
	if _, err := s.Put(ctx, "prefs", make([]byte, 60), 0); err != nil {
		t.Fatalf("settings still fit: %v", err)
	}
	if _, err := s.Put(ctx, "prefs2", make([]byte, 60), 0); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := s.Put(ctx, "prefs3", make([]byte, 60), 0); !errors.Is(err, ErrQuota) {
		t.Fatalf("the settings total is still enforced: %v", err)
	}
	// defaults
	d := &Store{A: m, User: "u2"}
	if item, total := d.limitsFor("font.x"); item != DefaultMaxFont || total != DefaultMaxFonts {
		t.Fatalf("%d %d", item, total)
	}
	if item, total := d.limitsFor("prefs"); item != DefaultMaxItem || total != DefaultMaxTotal {
		t.Fatalf("%d %d", item, total)
	}
}

func TestFontsOverHTTP(t *testing.T) {
	h, _ := svc()
	h.MaxFont = 300
	blob, err := EncodeFont(fakeFont("wOFF", 100), licence)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "PUT", "/me/config/font.atkinson", "ana", "0", string(blob)); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := do(h, "GET", "/me/config/font.atkinson", "ana", "", "")
	f, err := DecodeFont(got.Body.Bytes())
	if got.Code != 200 || err != nil || f.Format != "woff" || f.License != licence {
		t.Fatalf("%d %+v %v", got.Code, f, err)
	}
	if rec := do(h, "PUT", "/me/config/font.bad", "ana", "0", "not a font container"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d", rec.Code)
	}
	nolic := append([]byte("GJF1\x00\x00"), fakeFont("OTTO", 64)...)
	if rec := do(h, "PUT", "/me/config/font.nolic", "ana", "0", string(nolic)); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "licence") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	huge, _ := EncodeFont(fakeFont("OTTO", 400), licence)
	if rec := do(h, "PUT", "/me/config/font.huge", "ana", "0", string(huge)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d", rec.Code)
	}
	if !bytes.Contains(blob, []byte(licence)) {
		t.Fatal("the licence travels with the font")
	}
}
