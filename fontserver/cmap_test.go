package fontserver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --- format-0/4/12 subtable builders -- mirror buildTestSFNT/
// buildNameTable's own "construct exactly the bytes the parser reads,
// nothing more" style. ---

func buildCmapFormat0Subtable(present []int) []byte {
	glyphIDs := make([]byte, 256)
	for _, cp := range present {
		glyphIDs[cp] = 1 // any nonzero glyph ID marks "has a glyph"
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))     // format
	_ = binary.Write(&buf, binary.BigEndian, uint16(6+256)) // length
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))     // language
	buf.Write(glyphIDs)
	return buf.Bytes()
}

// cmapFmt4Seg is one format-4 segment: either direct (glyphIDs == nil,
// every codepoint in [start,end] covered, delta unused by our own
// coverage-only parser) or indirect (glyphIDs supplied, one entry per
// codepoint from start to end inclusive, 0 meaning "no glyph here").
type cmapFmt4Seg struct {
	start, end uint16
	glyphIDs   []uint16
}

// buildCmapFormat4Subtable assembles a complete format-4 subtable from
// segs, automatically appending the format's own required terminal
// sentinel segment (startCode == endCode == 0xFFFF) -- callers supply
// only the "real" segments.
func buildCmapFormat4Subtable(segs []cmapFmt4Seg) []byte {
	segs = append(append([]cmapFmt4Seg{}, segs...), cmapFmt4Seg{start: 0xFFFF, end: 0xFFFF})
	segCount := len(segs)

	endCodes := make([]uint16, segCount)
	startCodes := make([]uint16, segCount)
	idRangeOffsets := make([]uint16, segCount)
	var glyphArray []uint16
	for i, s := range segs {
		endCodes[i] = s.end
		startCodes[i] = s.start
		if s.glyphIDs == nil {
			idRangeOffsets[i] = 0
			continue
		}
		// Byte offset from this segment's own idRangeOffset slot to
		// where its glyph IDs start in glyphArray -- see
		// parseCmapFormat4's own doc comment for the formula this
		// mirrors.
		idRangeOffsets[i] = uint16((segCount-i)*2 + len(glyphArray)*2)
		glyphArray = append(glyphArray, s.glyphIDs...)
	}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // format
	totalLen := 14 + segCount*2*4 + len(glyphArray)*2
	_ = binary.Write(&buf, binary.BigEndian, uint16(totalLen))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // language
	_ = binary.Write(&buf, binary.BigEndian, uint16(segCount*2))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // searchRange
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // entrySelector
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // rangeShift
	for _, v := range endCodes {
		_ = binary.Write(&buf, binary.BigEndian, v)
	}
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // reservedPad
	for _, v := range startCodes {
		_ = binary.Write(&buf, binary.BigEndian, v)
	}
	for range segs {
		_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // idDelta, unused for coverage
	}
	for _, v := range idRangeOffsets {
		_ = binary.Write(&buf, binary.BigEndian, v)
	}
	for _, v := range glyphArray {
		_ = binary.Write(&buf, binary.BigEndian, v)
	}
	return buf.Bytes()
}

func buildCmapFormat12Subtable(groups []runeRange) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(12)) // format
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))  // reserved
	_ = binary.Write(&buf, binary.BigEndian, uint32(16+len(groups)*12))
	_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // language
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(groups)))
	for _, g := range groups {
		_ = binary.Write(&buf, binary.BigEndian, uint32(g.Lo))
		_ = binary.Write(&buf, binary.BigEndian, uint32(g.Hi))
		_ = binary.Write(&buf, binary.BigEndian, uint32(1)) // startGlyphID, unused by the parser
	}
	return buf.Bytes()
}

type cmapSubtableSpec struct {
	platformID, encodingID uint16
	subtable               []byte
}

func buildCmapTable(specs []cmapSubtableSpec) []byte {
	headerLen := 4 + len(specs)*8
	off := uint32(headerLen)
	offsets := make([]uint32, len(specs))
	for i, s := range specs {
		offsets[i] = off
		off += uint32(len(s.subtable))
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // version
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(specs)))
	for i, s := range specs {
		_ = binary.Write(&buf, binary.BigEndian, s.platformID)
		_ = binary.Write(&buf, binary.BigEndian, s.encodingID)
		_ = binary.Write(&buf, binary.BigEndian, offsets[i])
	}
	for _, s := range specs {
		buf.Write(s.subtable)
	}
	return buf.Bytes()
}

// buildTestSFNTWithCmap is buildTestSFNT's multi-table counterpart:
// an sfnt file carrying both a 'name' table (for sfntNames/Repo.Scan)
// and a 'cmap' table (for sfntCmapRanges), matching what a real font
// -- and the /local/symbols endpoint's own end-to-end path -- actually
// needs.
func buildTestSFNTWithCmap(nameRecords []nameRecordSpec, cmapTable []byte) []byte {
	nameTable := buildNameTable(nameRecords)
	type entry struct {
		tag  string
		data []byte
	}
	tables := []entry{{"name", nameTable}, {"cmap", cmapTable}}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(0x00010000))
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(tables)))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))

	off := uint32(12 + len(tables)*16)
	offsets := make([]uint32, len(tables))
	for i, tb := range tables {
		offsets[i] = off
		off += uint32(len(tb.data))
	}
	for i, tb := range tables {
		buf.WriteString(tb.tag)
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // checksum, unchecked
		_ = binary.Write(&buf, binary.BigEndian, offsets[i])
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(tb.data)))
	}
	for _, tb := range tables {
		buf.Write(tb.data)
	}
	return buf.Bytes()
}

// --- parseCmapFormat0/4/12 ---

func TestParseCmapFormat0(t *testing.T) {
	sub := buildCmapFormat0Subtable([]int{65, 66, 67, 200})
	got, err := parseCmapSubtable(sub)
	if err != nil {
		t.Fatalf("parseCmapSubtable(format 0): %v", err)
	}
	want := []runeRange{{65, 67}, {200, 200}}
	if !rangesEqual(mergeRuneRanges(got), want) {
		t.Fatalf("format 0 ranges = %v, want %v", got, want)
	}
}

func TestParseCmapFormat4DirectSegments(t *testing.T) {
	sub := buildCmapFormat4Subtable([]cmapFmt4Seg{
		{start: 0x41, end: 0x5A}, // A-Z
		{start: 0x2014, end: 0x2014},
	})
	got, err := parseCmapSubtable(sub)
	if err != nil {
		t.Fatalf("parseCmapSubtable(format 4): %v", err)
	}
	want := []runeRange{{0x41, 0x5A}, {0x2014, 0x2014}}
	if !rangesEqual(got, want) {
		t.Fatalf("format 4 direct ranges = %v, want %v", got, want)
	}
}

func TestParseCmapFormat4IndirectSegmentWithHole(t *testing.T) {
	// codepoints 0x100-0x104: glyphs at 0x100,0x101, a hole at 0x102,
	// then glyphs again at 0x103,0x104 -- should split into two
	// ranges around the hole, not one range covering the whole span.
	sub := buildCmapFormat4Subtable([]cmapFmt4Seg{
		{start: 0x100, end: 0x104, glyphIDs: []uint16{10, 11, 0, 12, 13}},
	})
	got, err := parseCmapSubtable(sub)
	if err != nil {
		t.Fatalf("parseCmapSubtable(format 4 indirect): %v", err)
	}
	want := []runeRange{{0x100, 0x101}, {0x103, 0x104}}
	if !rangesEqual(got, want) {
		t.Fatalf("format 4 indirect ranges = %v, want %v (split around the hole at 0x102)", got, want)
	}
}

func TestParseCmapFormat4SkipsTerminalSentinel(t *testing.T) {
	// No real segments at all -- only the auto-appended terminal
	// sentinel -- should yield zero ranges, not one spurious {0xFFFF,0xFFFF}.
	sub := buildCmapFormat4Subtable(nil)
	got, err := parseCmapSubtable(sub)
	if err != nil {
		t.Fatalf("parseCmapSubtable(format 4, no real segments): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("format 4 with only the terminal sentinel = %v, want empty", got)
	}
}

func TestParseCmapFormat12(t *testing.T) {
	sub := buildCmapFormat12Subtable([]runeRange{
		{Lo: 0x1F600, Hi: 0x1F64F}, // emoticons block
		{Lo: 0x4E00, Hi: 0x9FFF},   // CJK unified ideographs
	})
	got, err := parseCmapSubtable(sub)
	if err != nil {
		t.Fatalf("parseCmapSubtable(format 12): %v", err)
	}
	want := []runeRange{{0x1F600, 0x1F64F}, {0x4E00, 0x9FFF}}
	if !rangesEqual(got, want) {
		t.Fatalf("format 12 ranges = %v, want %v", got, want)
	}
}

func TestParseCmapSubtableUnsupportedFormat(t *testing.T) {
	sub := make([]byte, 4)
	binary.BigEndian.PutUint16(sub, 6) // format 6, not one we support
	if _, err := parseCmapSubtable(sub); err == nil {
		t.Fatal("parseCmapSubtable(unsupported format): expected an error")
	}
}

func TestParseCmapSubtableTooShort(t *testing.T) {
	if _, err := parseCmapSubtable([]byte{0}); err == nil {
		t.Fatal("parseCmapSubtable(1 byte): expected an error")
	}
}

// --- mergeRuneRanges ---

func TestMergeRuneRangesCoalescesAdjacentAndOverlapping(t *testing.T) {
	in := []runeRange{{10, 20}, {21, 25}, {1, 5}, {23, 30}}
	got := mergeRuneRanges(in)
	want := []runeRange{{1, 5}, {10, 30}}
	if !rangesEqual(got, want) {
		t.Fatalf("mergeRuneRanges(%v) = %v, want %v", in, got, want)
	}
}

func TestMergeRuneRangesEmptyInput(t *testing.T) {
	if got := mergeRuneRanges(nil); got != nil {
		t.Fatalf("mergeRuneRanges(nil) = %v, want nil", got)
	}
}

// --- cmapPreferredSubtable ---

func TestCmapPreferredSubtablePrefersFullUnicodeOverBMP(t *testing.T) {
	bmp := buildCmapFormat4Subtable([]cmapFmt4Seg{{start: 0x41, end: 0x5A}})
	full := buildCmapFormat12Subtable([]runeRange{{Lo: 0x1F600, Hi: 0x1F64F}})
	table := buildCmapTable([]cmapSubtableSpec{
		{platformID: platformWindows, encodingID: encodingWindowsBMP, subtable: bmp},
		{platformID: platformWindows, encodingID: encodingWindowsUCS4, subtable: full},
	})
	var recs []cmapSubtableRecord
	numTables := binary.BigEndian.Uint16(table[2:4])
	for i := 0; i < int(numTables); i++ {
		rec := table[4+i*8 : 4+(i+1)*8]
		recs = append(recs, cmapSubtableRecord{
			platformID: binary.BigEndian.Uint16(rec[0:2]),
			encodingID: binary.BigEndian.Uint16(rec[2:4]),
			offset:     binary.BigEndian.Uint32(rec[4:8]),
		})
	}
	best, ok := cmapPreferredSubtable(recs, table)
	if !ok {
		t.Fatal("cmapPreferredSubtable: expected a match")
	}
	if best.encodingID != encodingWindowsUCS4 {
		t.Fatalf("cmapPreferredSubtable picked encoding %d, want the full-Unicode UCS4 one (%d)", best.encodingID, encodingWindowsUCS4)
	}
}

func TestCmapPreferredSubtableSkipsUnsupportedFormat(t *testing.T) {
	unsupported := make([]byte, 4)
	binary.BigEndian.PutUint16(unsupported, 6)
	bmp := buildCmapFormat4Subtable([]cmapFmt4Seg{{start: 0x41, end: 0x5A}})
	table := buildCmapTable([]cmapSubtableSpec{
		// This would win on platform/encoding alone (it's the
		// "best" score), but format 6 isn't decodable -- the BMP
		// format-4 one should be picked instead.
		{platformID: platformWindows, encodingID: encodingWindowsUCS4, subtable: unsupported},
		{platformID: platformWindows, encodingID: encodingWindowsBMP, subtable: bmp},
	})
	var recs []cmapSubtableRecord
	numTables := binary.BigEndian.Uint16(table[2:4])
	for i := 0; i < int(numTables); i++ {
		rec := table[4+i*8 : 4+(i+1)*8]
		recs = append(recs, cmapSubtableRecord{
			platformID: binary.BigEndian.Uint16(rec[0:2]),
			encodingID: binary.BigEndian.Uint16(rec[2:4]),
			offset:     binary.BigEndian.Uint32(rec[4:8]),
		})
	}
	best, ok := cmapPreferredSubtable(recs, table)
	if !ok {
		t.Fatal("cmapPreferredSubtable: expected a match")
	}
	if best.encodingID != encodingWindowsBMP {
		t.Fatalf("cmapPreferredSubtable picked encoding %d, want it to skip the undecodable format-6 record and fall back to BMP (%d)", best.encodingID, encodingWindowsBMP)
	}
}

// --- sfntCmapRanges (end to end through a full sfnt file) ---

func TestSfntCmapRangesEndToEnd(t *testing.T) {
	cmapTable := buildCmapTable([]cmapSubtableSpec{
		{platformID: platformWindows, encodingID: encodingWindowsBMP, subtable: buildCmapFormat4Subtable([]cmapFmt4Seg{
			{start: 0x20, end: 0x7E}, // ASCII printable
			{start: 0x2014, end: 0x2014},
		})},
	})
	data := buildTestSFNTWithCmap([]nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "Test Sans"},
	}, cmapTable)

	got, err := sfntCmapRanges(data)
	if err != nil {
		t.Fatalf("sfntCmapRanges: %v", err)
	}
	want := []runeRange{{0x20, 0x7E}, {0x2014, 0x2014}}
	if !rangesEqual(got, want) {
		t.Fatalf("sfntCmapRanges = %v, want %v", got, want)
	}
}

func TestSfntCmapRangesNoCmapTableIsError(t *testing.T) {
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, "No Cmap"},
	})
	if _, err := sfntCmapRanges(data); err == nil {
		t.Fatal("sfntCmapRanges(no cmap table): expected an error")
	}
}

// --- /local/symbols HTTP endpoint ---

func writeTestFontWithCmap(t *testing.T, dir, relPath, family, subfamily string, cmapTable []byte) {
	t.Helper()
	data := buildTestSFNTWithCmap([]nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, family},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, subfamily},
	}, cmapTable)
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSymbolsEndpoint(t *testing.T) {
	dir := t.TempDir()
	cmapTable := buildCmapTable([]cmapSubtableSpec{
		{platformID: platformWindows, encodingID: encodingWindowsBMP, subtable: buildCmapFormat4Subtable([]cmapFmt4Seg{
			{start: 0x41, end: 0x5A},
		})},
	})
	writeTestFontWithCmap(t, dir, "a.ttf", "Symbol Test", "Regular", cmapTable)
	srv := newTestServer(t, Config{FontsDir: dir})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/symbols?family=Symbol+Test", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /local/symbols = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Ranges []runeRange `json:"ranges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []runeRange{{0x41, 0x5A}}
	if !rangesEqual(body.Ranges, want) {
		t.Fatalf("ranges = %v, want %v", body.Ranges, want)
	}
}

func TestLocalSymbolsEndpointUnknownFamilyIs404(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/symbols?family=Nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /local/symbols?family=Nonexistent = %d, want 404", rec.Code)
	}
}

func TestLocalSymbolsEndpointMissingFamilyIsBadRequest(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/symbols", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /local/symbols (no family) = %d, want 400", rec.Code)
	}
}

func rangesEqual(a, b []runeRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
