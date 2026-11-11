package fontserver

import (
	"encoding/binary"
	"fmt"
	"sort"
)

type runeRange struct {
	Lo rune `json:"lo"`
	Hi rune `json:"hi"`
}

const (
	encodingWindowsSymbol = 0
	encodingWindowsUCS4   = 10
	encodingUnicodeFull   = 4
)

type cmapSubtableRecord struct {
	platformID, encodingID uint16
	offset                 uint32
}

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
			ranges = append(ranges, runeRange{Lo: rune(start), Hi: rune(end)})
			continue
		}
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
