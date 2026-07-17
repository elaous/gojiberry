package fontserver

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// runeRange is one contiguous, inclusive range of Unicode codepoints a
// font has a real (non-.notdef) glyph for -- sfntCmapRanges' own
// return type, and what the /symbols HTTP endpoint (server.go)
// serializes to JSON. Deliberately a range list rather than a flat
// rune slice: the 'cmap' table itself is already range-encoded
// (format 4/12's own segments), so this is both the natural parse
// output and a compact cache/wire representation -- even a huge CJK
// font's cmap comes out to at most a few thousand ranges, not tens of
// thousands of individual runes.
type runeRange struct {
	Lo rune `json:"lo"`
	Hi rune `json:"hi"`
}

// encodingWindowsSymbol/encodingWindowsUCS4/encodingUnicodeFull are
// 'cmap' subtable encoding IDs cmapPreferredSubtable's own priority
// order cares about, alongside platformWindows/platformMac/
// platformUnicode and encodingWindowsBMP already declared in sfnt.go.
const (
	encodingWindowsSymbol = 0
	encodingWindowsUCS4   = 10
	encodingUnicodeFull   = 4 // 6 also means "full repertoire" on platform 0; both score the same
)

// cmapSubtableRecord is one entry in the 'cmap' table's own subtable
// directory: which (platform, encoding) it claims to serve, and its
// own byte offset within the 'cmap' table.
type cmapSubtableRecord struct {
	platformID, encodingID uint16
	offset                 uint32
}

// sfntCmapRanges extracts the set of Unicode codepoints data's 'cmap'
// table maps to a real glyph, as a sorted, non-overlapping list of
// inclusive ranges -- the font's own actual character repertoire,
// distinct from sfntNames' family/subfamily strings. Picks the "best"
// subtable via cmapPreferredSubtable, and understands subtable formats
// 4 (segment mapping, BMP-only, what most Windows/Unicode subtables
// use), 12 (segmented coverage, full Unicode range), and 0 (byte
// encoding table, a legacy 256-glyph Mac table) -- together the
// formats every font actually likely to be installed locally uses. A
// subtable in a format we don't understand is skipped in favor of the
// next-best one (cmapPreferredSubtable's own doc comment); if none at
// all are usable, that's a reported error rather than a silently
// empty result, so a caller can tell "this font genuinely has no
// symbols" apart from "we couldn't read this font's cmap at all."
func sfntCmapRanges(data []byte) ([]runeRange, error) {
	off, length, err := findSfntTable(data, "cmap")
	if err != nil {
		return nil, err
	}
	table := data[off : off+length]
	if len(table) < 4 {
		return nil, fmt.Errorf("fontserver: 'cmap' table too short")
	}
	numTables := binary.BigEndian.Uint16(table[2:4])
	const recSize = 8
	recsEnd := 4 + int(numTables)*recSize
	if recsEnd > len(table) {
		return nil, fmt.Errorf("fontserver: truncated 'cmap' subtable directory")
	}
	recs := make([]cmapSubtableRecord, numTables)
	for i := range recs {
		rec := table[4+i*recSize : 4+(i+1)*recSize]
		recs[i] = cmapSubtableRecord{
			platformID: binary.BigEndian.Uint16(rec[0:2]),
			encodingID: binary.BigEndian.Uint16(rec[2:4]),
			offset:     binary.BigEndian.Uint32(rec[4:8]),
		}
	}
	best, ok := cmapPreferredSubtable(recs, table)
	if !ok {
		return nil, fmt.Errorf("fontserver: no usable 'cmap' subtable")
	}
	ranges, err := parseCmapSubtable(table[best.offset:])
	if err != nil {
		return nil, err
	}
	return mergeRuneRanges(ranges), nil
}

// cmapPreferredSubtable scores each subtable record by platform/
// encoding -- a full-Unicode Windows or Unicode-platform subtable
// first, then a BMP-only Windows/Unicode one, then any other Unicode-
// platform subtable, then a Windows Symbol-encoded one (used by
// dingbat fonts like Wingdings/Webdings, whose own codepoints already
// sit in the cmap at their real 0xF0xx Private Use Area positions --
// no extra offsetting needed here), then finally the legacy Macintosh
// table -- and returns the highest-scoring record whose own format
// (the uint16 at its offset) is one parseCmapSubtable actually
// understands, skipping a record we can't decode even if it would
// otherwise have won on platform/encoding alone.
func cmapPreferredSubtable(recs []cmapSubtableRecord, table []byte) (cmapSubtableRecord, bool) {
	score := func(r cmapSubtableRecord) int {
		switch {
		case r.platformID == platformWindows && r.encodingID == encodingWindowsUCS4:
			return 5
		case r.platformID == platformUnicode && (r.encodingID == encodingUnicodeFull || r.encodingID == 6):
			return 5
		case r.platformID == platformWindows && r.encodingID == encodingWindowsBMP:
			return 4
		case r.platformID == platformUnicode:
			return 3
		case r.platformID == platformWindows && r.encodingID == encodingWindowsSymbol:
			return 2
		case r.platformID == platformMac:
			return 1
		default:
			return 0
		}
	}
	var best cmapSubtableRecord
	bestScore := -1
	found := false
	for _, r := range recs {
		if uint64(r.offset)+2 > uint64(len(table)) {
			continue
		}
		format := binary.BigEndian.Uint16(table[r.offset : r.offset+2])
		if format != 0 && format != 4 && format != 12 {
			continue
		}
		if s := score(r); s > bestScore {
			best, bestScore, found = r, s, true
		}
	}
	return best, found
}

// parseCmapSubtable dispatches on sub's own format field to the
// matching parser.
func parseCmapSubtable(sub []byte) ([]runeRange, error) {
	if len(sub) < 2 {
		return nil, fmt.Errorf("fontserver: 'cmap' subtable too short")
	}
	switch binary.BigEndian.Uint16(sub[0:2]) {
	case 0:
		return parseCmapFormat0(sub)
	case 4:
		return parseCmapFormat4(sub)
	case 12:
		return parseCmapFormat12(sub)
	default:
		return nil, fmt.Errorf("fontserver: unsupported 'cmap' subtable format")
	}
}

// parseCmapFormat0 reads a byte encoding table: format(2) length(2)
// language(2) glyphIdArray[256]byte -- codepoint i (0-255) has a real
// glyph iff glyphIdArray[i] != 0. A legacy, Mac-only table; rare in
// modern fonts, but trivial to support alongside 4/12.
func parseCmapFormat0(sub []byte) ([]runeRange, error) {
	if len(sub) < 6+256 {
		return nil, fmt.Errorf("fontserver: truncated format-0 'cmap' subtable")
	}
	glyphIDs := sub[6 : 6+256]
	var ranges []runeRange
	for i, id := range glyphIDs {
		if id != 0 {
			ranges = append(ranges, runeRange{Lo: rune(i), Hi: rune(i)})
		}
	}
	return ranges, nil
}

// parseCmapFormat4 reads a segment mapping table (BMP-only, what most
// Windows/Unicode subtables actually use): a list of [startCode,
// endCode] segments, each either directly covered (idRangeOffset == 0
// -- every codepoint in the segment has a glyph, glyph ID computed as
// (code + idDelta) mod 65536) or indirectly covered through
// glyphIdArray (idRangeOffset != 0 -- an individual codepoint within
// the segment can still be a "hole", glyphIdArray entry == 0, which
// this walks and splits into sub-ranges around rather than assuming
// the whole declared segment is populated). The required terminal
// sentinel segment (startCode == endCode == 0xFFFF, present in every
// format-4 subtable) is skipped outright -- it exists only to
// terminate binary search in a real rasterizer, never a real glyph.
func parseCmapFormat4(sub []byte) ([]runeRange, error) {
	if len(sub) < 14 {
		return nil, fmt.Errorf("fontserver: truncated format-4 'cmap' subtable")
	}
	segCount := int(binary.BigEndian.Uint16(sub[6:8]) / 2)
	endCodeOff := 14
	startCodeOff := endCodeOff + segCount*2 + 2 // +2 skips reservedPad
	idDeltaOff := startCodeOff + segCount*2
	idRangeOff := idDeltaOff + segCount*2
	glyphArrayEnd := idRangeOff + segCount*2
	if segCount < 0 || glyphArrayEnd > len(sub) {
		return nil, fmt.Errorf("fontserver: truncated format-4 'cmap' segment arrays")
	}
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(sub[off : off+2]) }

	var ranges []runeRange
	for i := 0; i < segCount; i++ {
		start := u16(startCodeOff + i*2)
		end := u16(endCodeOff + i*2)
		if start == 0xFFFF || start > end {
			continue
		}
		rangeOffset := u16(idRangeOff + i*2)
		if rangeOffset == 0 {
			// idDelta doesn't affect whether a glyph exists here (only
			// its ID), so it's irrelevant to a pure coverage check.
			ranges = append(ranges, runeRange{Lo: rune(start), Hi: rune(end)})
			continue
		}
		// Indirect lookup through glyphIdArray: each covered
		// codepoint's own entry sits at idRangeOffset[i]'s own byte
		// position, plus rangeOffset (already a byte count), plus
		// twice the codepoint's distance from start -- OpenType's own
		// "pointer arithmetic from the idRangeOffset slot itself"
		// formula. A run of consecutive non-zero entries becomes one
		// range; a zero entry (a genuine hole) breaks the run.
		runStart := rune(start)
		haveRun := false
		for c := uint32(start); c <= uint32(end); c++ {
			glyphIdxOff := idRangeOff + i*2 + int(rangeOffset) + int(c-uint32(start))*2
			if glyphIdxOff+2 > len(sub) {
				break
			}
			if u16(glyphIdxOff) == 0 {
				if haveRun {
					ranges = append(ranges, runeRange{Lo: runStart, Hi: rune(c) - 1})
					haveRun = false
				}
				runStart = rune(c) + 1
				continue
			}
			haveRun = true
		}
		if haveRun {
			ranges = append(ranges, runeRange{Lo: runStart, Hi: rune(end)})
		}
	}
	return ranges, nil
}

// parseCmapFormat12 reads a segmented coverage table (the full-
// Unicode-range counterpart to format 4): format(2) reserved(2)
// length(4) language(4) numGroups(4), then numGroups groups of
// startCharCode(4) endCharCode(4) startGlyphID(4). Unlike format 4,
// every codepoint within a declared group is covered by definition --
// there's no indirect-lookup "hole" concept here, so each group maps
// directly to one runeRange.
func parseCmapFormat12(sub []byte) ([]runeRange, error) {
	if len(sub) < 16 {
		return nil, fmt.Errorf("fontserver: truncated format-12 'cmap' subtable")
	}
	numGroups := binary.BigEndian.Uint32(sub[12:16])
	const groupSize = 12
	groupsEnd := 16 + int(numGroups)*groupSize
	if groupsEnd > len(sub) {
		return nil, fmt.Errorf("fontserver: truncated format-12 'cmap' groups")
	}
	ranges := make([]runeRange, 0, numGroups)
	for i := 0; i < int(numGroups); i++ {
		g := sub[16+i*groupSize : 16+(i+1)*groupSize]
		start := binary.BigEndian.Uint32(g[0:4])
		end := binary.BigEndian.Uint32(g[4:8])
		if start > end || start > 0x10FFFF {
			continue
		}
		if end > 0x10FFFF {
			end = 0x10FFFF
		}
		ranges = append(ranges, runeRange{Lo: rune(start), Hi: rune(end)})
	}
	return ranges, nil
}

// mergeRuneRanges sorts ranges by Lo and coalesces overlapping or
// adjacent ones (Hi+1 == next Lo) into the minimal equivalent set --
// format 4/12 subtables routinely declare runs of adjacent segments/
// groups that are really one contiguous span, and collapsing them
// keeps both the in-memory cache and the JSON response compact.
func mergeRuneRanges(ranges []runeRange) []runeRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := append([]runeRange(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Lo < sorted[j].Lo })
	out := []runeRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r.Lo <= last.Hi+1 {
			if r.Hi > last.Hi {
				last.Hi = r.Hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
