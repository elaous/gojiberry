package fontserver

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// buildTestSFNT constructs a minimal, otherwise-empty sfnt file
// carrying only an offset table, a one-entry table directory pointing
// at a 'name' table, and that 'name' table itself -- exactly what
// sfntNames reads, and nothing a real font also carries (glyphs,
// cmap, etc.) that would only add noise to this test.
func buildTestSFNT(t *testing.T, records []nameRecordSpec) []byte {
	t.Helper()

	nameTable := buildNameTable(records)

	var buf bytes.Buffer
	// Offset table: sfntVersion, numTables=1, searchRange/entrySelector/rangeShift (unused by our parser).
	binary.Write(&buf, binary.BigEndian, uint32(0x00010000))
	binary.Write(&buf, binary.BigEndian, uint16(1))
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint16(0))

	nameOffset := uint32(12 + 16) // offset table + one directory entry
	buf.WriteString("name")
	binary.Write(&buf, binary.BigEndian, uint32(0)) // checksum, unchecked
	binary.Write(&buf, binary.BigEndian, nameOffset)
	binary.Write(&buf, binary.BigEndian, uint32(len(nameTable)))

	buf.Write(nameTable)
	return buf.Bytes()
}

type nameRecordSpec struct {
	platformID, encodingID, languageID, nameID uint16
	value                                      string
}

func buildNameTable(records []nameRecordSpec) []byte {
	var storage bytes.Buffer
	type resolved struct {
		nameRecordSpec
		offset, length uint16
	}
	resolvedRecs := make([]resolved, len(records))
	for i, rec := range records {
		off := uint16(storage.Len())
		var encoded []byte
		if rec.platformID == platformMac {
			encoded = []byte(rec.value)
		} else {
			for _, r := range utf16.Encode([]rune(rec.value)) {
				encoded = append(encoded, byte(r>>8), byte(r))
			}
		}
		storage.Write(encoded)
		resolvedRecs[i] = resolved{rec, off, uint16(len(encoded))}
	}

	headerLen := 6 + len(records)*12
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint16(0)) // format
	binary.Write(&buf, binary.BigEndian, uint16(len(records)))
	binary.Write(&buf, binary.BigEndian, uint16(headerLen)) // stringOffset
	for _, r := range resolvedRecs {
		binary.Write(&buf, binary.BigEndian, r.platformID)
		binary.Write(&buf, binary.BigEndian, r.encodingID)
		binary.Write(&buf, binary.BigEndian, r.languageID)
		binary.Write(&buf, binary.BigEndian, r.nameID)
		binary.Write(&buf, binary.BigEndian, r.length)
		binary.Write(&buf, binary.BigEndian, r.offset)
	}
	buf.Write(storage.Bytes())
	return buf.Bytes()
}

func TestSfntNamesWindowsPlatform(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Test Sans"},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Bold Italic"},
	})
	family, subfamily, err := sfntNames(data)
	if err != nil {
		t.Fatalf("sfntNames: %v", err)
	}
	if family != "Test Sans" {
		t.Errorf("family = %q, want %q", family, "Test Sans")
	}
	if subfamily != "Bold Italic" {
		t.Errorf("subfamily = %q, want %q", subfamily, "Bold Italic")
	}
}

func TestSfntNamesPrefersPreferredFamily(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Test Sans Bold"},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Bold"},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, namePreferredFamily, "Test Sans"},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, namePreferredSubfamily, "Bold"},
	})
	family, subfamily, err := sfntNames(data)
	if err != nil {
		t.Fatalf("sfntNames: %v", err)
	}
	if family != "Test Sans" {
		t.Errorf("family = %q, want the preferred-family entry %q", family, "Test Sans")
	}
	if subfamily != "Bold" {
		t.Errorf("subfamily = %q, want %q", subfamily, "Bold")
	}
}

func TestSfntNamesMacPlatformFallback(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformMac, 0, 0, nameFamily, "Test Serif"},
		{platformMac, 0, 0, nameSubfamily, "Regular"},
	})
	family, subfamily, err := sfntNames(data)
	if err != nil {
		t.Fatalf("sfntNames: %v", err)
	}
	if family != "Test Serif" || subfamily != "Regular" {
		t.Errorf("got family=%q subfamily=%q, want %q/%q", family, subfamily, "Test Serif", "Regular")
	}
}

func TestSfntNamesPrefersWindowsOverMac(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformMac, 0, 0, nameFamily, "Mac Name"},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Windows Name"},
	})
	family, _, err := sfntNames(data)
	if err != nil {
		t.Fatalf("sfntNames: %v", err)
	}
	if family != "Windows Name" {
		t.Errorf("family = %q, want the Windows/en-US record %q", family, "Windows Name")
	}
}

func TestSfntNamesNoFamilyIsError(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, "Bold"},
	})
	if _, _, err := sfntNames(data); err == nil {
		t.Fatal("sfntNames: expected an error with no family name present")
	}
}

func TestSfntNamesRejectsTruncatedInput(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0, 1, 2},
		buildTestSFNT(t, nil)[:14], // cuts off mid table-directory
	} {
		if _, _, err := sfntNames(data); err == nil {
			t.Errorf("sfntNames(%v): expected an error on truncated input", data)
		}
	}
}

func TestSfntNamesRejectsNonSfnt(t *testing.T) {
	if _, _, err := sfntNames([]byte("this is not a font file, just twelve-plus bytes of text")); err == nil {
		t.Fatal("sfntNames: expected an error for non-sfnt data")
	}
}

func TestDecodeUTF16SurrogatePair(t *testing.T) {
	// U+1F600 GRINNING FACE, encoded as a surrogate pair -- a family
	// name would never realistically contain this, but the decoder
	// should still assemble it correctly rather than emit two bogus
	// runes.
	pair := utf16.Encode([]rune{0x1F600})
	if len(pair) != 2 {
		t.Fatalf("test setup: expected a surrogate pair, got %d units", len(pair))
	}
	got := decodeUTF16(pair)
	if got != string(rune(0x1F600)) {
		t.Errorf("decodeUTF16(surrogate pair) = %q, want %q", got, string(rune(0x1F600)))
	}
}

func TestDecodeNameStringUnknownPlatform(t *testing.T) {
	if got := decodeNameString(2 /* ISO, unsupported */, 0, []byte("ignored")); got != "" {
		t.Errorf("decodeNameString(unsupported platform) = %q, want empty", got)
	}
}

func TestDecodeNameStringOddLengthWindowsRecordIsRejected(t *testing.T) {
	if got := decodeNameString(platformWindows, encodingWindowsBMP, []byte{0x00}); got != "" {
		t.Errorf("decodeNameString(odd-length UTF-16) = %q, want empty", got)
	}
}
