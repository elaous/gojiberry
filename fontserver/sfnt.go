package fontserver

import (
	"encoding/binary"
	"fmt"
)

// nameFamily/nameSubfamily are the legacy (nameID 1/2) name-table
// entries every font must carry; namePreferredFamily/
// namePreferredSubfamily (16/17) are the modern "typographic name"
// entries some fonts add instead of ever growing a legacy family name
// past what old Mac/Windows font menus could group by weight (e.g. a
// "Roboto Condensed Bold" family with no separate weight axis in its
// legacy name). We prefer 16/17 when present, matching what a real
// font menu (including Google Fonts' own) shows.
const (
	nameFamily             = 1
	nameSubfamily          = 2
	namePreferredFamily    = 16
	namePreferredSubfamily = 17
)

// platformWindows/platformMac/platformUnicode are the sfnt 'name'
// table's platform IDs we know how to decode. Symbol and other exotic
// platforms are skipped -- a font that only carries those has no name
// we can safely read as text.
const (
	platformUnicode    = 0
	platformMac        = 1
	platformWindows    = 3
	encodingWindowsBMP = 1
	langWindowsEnUS    = 0x0409
)

// sfntNames extracts the sfnt 'name' table's family/subfamily strings
// from a raw TrueType/OpenType font file (.ttf/.otf; not woff/woff2,
// which wrap this same table format in a compressed container we
// don't unpack). It reads only the table directory and the 'name'
// table -- never glyph outlines -- so it works the same whether the
// font is TrueType- or CFF-flavored (sfntVersion 0x00010000 vs
// 'OTTO'), and does not validate table checksums, which matter for
// rendering correctness but not for reading a handful of strings.
func sfntNames(data []byte) (family, subfamily string, err error) {
	if len(data) < 12 {
		return "", "", fmt.Errorf("fontserver: file too short to be an sfnt font")
	}
	numTables := binary.BigEndian.Uint16(data[4:6])
	const dirEntrySize = 16
	dirEnd := 12 + int(numTables)*dirEntrySize
	if dirEnd > len(data) {
		return "", "", fmt.Errorf("fontserver: truncated table directory")
	}
	var nameOff, nameLen uint32
	found := false
	for i := 0; i < int(numTables); i++ {
		rec := data[12+i*dirEntrySize : 12+(i+1)*dirEntrySize]
		tag := string(rec[0:4])
		if tag == "name" {
			nameOff = binary.BigEndian.Uint32(rec[8:12])
			nameLen = binary.BigEndian.Uint32(rec[12:16])
			found = true
			break
		}
	}
	if !found {
		return "", "", fmt.Errorf("fontserver: no 'name' table")
	}
	if uint64(nameOff)+uint64(nameLen) > uint64(len(data)) || nameLen < 6 {
		return "", "", fmt.Errorf("fontserver: 'name' table out of bounds")
	}
	table := data[nameOff : nameOff+nameLen]

	names, err := parseNameTable(table)
	if err != nil {
		return "", "", err
	}
	family = names[namePreferredFamily]
	if family == "" {
		family = names[nameFamily]
	}
	subfamily = names[namePreferredSubfamily]
	if subfamily == "" {
		subfamily = names[nameSubfamily]
	}
	if family == "" {
		return "", "", fmt.Errorf("fontserver: no family name in 'name' table")
	}
	return family, subfamily, nil
}

// parseNameTable reads every name record in an sfnt 'name' table
// (format 0 or 1 -- the two share the same record layout, format 1
// just adds language-tag records we don't need between the name
// records and the string storage, which the stored stringOffset
// already accounts for) and picks, for each nameID, the best
// available string: a Windows/Unicode-BMP/US-English record if one
// exists, else the first decodable record of any platform.
func parseNameTable(table []byte) (map[uint16]string, error) {
	if len(table) < 6 {
		return nil, fmt.Errorf("fontserver: 'name' table too short")
	}
	count := binary.BigEndian.Uint16(table[2:4])
	stringOffset := binary.BigEndian.Uint16(table[4:6])
	const recSize = 12
	recsEnd := 6 + int(count)*recSize
	if recsEnd > len(table) || int(stringOffset) > len(table) {
		return nil, fmt.Errorf("fontserver: truncated 'name' records")
	}
	storage := table[stringOffset:]

	type candidate struct {
		s         string
		preferred bool
	}
	best := make(map[uint16]candidate)
	for i := 0; i < int(count); i++ {
		rec := table[6+i*recSize : 6+(i+1)*recSize]
		platformID := binary.BigEndian.Uint16(rec[0:2])
		encodingID := binary.BigEndian.Uint16(rec[2:4])
		languageID := binary.BigEndian.Uint16(rec[4:6])
		nameID := binary.BigEndian.Uint16(rec[6:8])
		length := binary.BigEndian.Uint16(rec[8:10])
		offset := binary.BigEndian.Uint16(rec[10:12])
		if int(offset)+int(length) > len(storage) {
			continue
		}
		raw := storage[offset : offset+length]
		s := decodeNameString(platformID, encodingID, raw)
		if s == "" {
			continue
		}
		preferred := platformID == platformWindows && encodingID == encodingWindowsBMP && languageID == langWindowsEnUS
		if cur, ok := best[nameID]; !ok || (preferred && !cur.preferred) {
			best[nameID] = candidate{s: s, preferred: preferred}
		}
	}
	out := make(map[uint16]string, len(best))
	for id, c := range best {
		out[id] = c.s
	}
	return out, nil
}

// decodeNameString decodes one name record's raw bytes. Windows and
// Unicode-platform records are UTF-16BE; Macintosh records are
// treated as MacRoman restricted to its ASCII-identical range, which
// covers every family/style name we expect to see (both are Latin
// script by convention). Anything else is reported as empty rather
// than guessed at.
func decodeNameString(platformID, encodingID uint16, raw []byte) string {
	switch platformID {
	case platformWindows, platformUnicode:
		if len(raw)%2 != 0 {
			return ""
		}
		runes := make([]uint16, len(raw)/2)
		for i := range runes {
			runes[i] = binary.BigEndian.Uint16(raw[i*2 : i*2+2])
		}
		return decodeUTF16(runes)
	case platformMac:
		b := make([]byte, 0, len(raw))
		for _, c := range raw {
			if c < 0x80 {
				b = append(b, c)
			}
		}
		return string(b)
	default:
		return ""
	}
}

func decodeUTF16(runes []uint16) string {
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r >= 0xD800 && r <= 0xDBFF && i+1 < len(runes) {
			r2 := runes[i+1]
			if r2 >= 0xDC00 && r2 <= 0xDFFF {
				out = append(out, ((rune(r)-0xD800)<<10|(rune(r2)-0xDC00))+0x10000)
				i++
				continue
			}
		}
		out = append(out, rune(r))
	}
	return string(out)
}
