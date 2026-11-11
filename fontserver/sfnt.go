package fontserver

import (
	"encoding/binary"
	"fmt"
)

const (
	nameFamily             = 1
	nameSubfamily          = 2
	namePreferredFamily    = 16
	namePreferredSubfamily = 17
)

const (
	platformUnicode    = 0
	platformMac        = 1
	platformWindows    = 3
	encodingWindowsBMP = 1
	langWindowsEnUS    = 0x0409
)

func findSfntTable(data []byte, tag string) (offset, length uint32, err error) {
	if len(data) < 12 {
		return 0, 0, fmt.Errorf("fontserver: file too short to be an sfnt font")
	}
	numTables := binary.BigEndian.Uint16(data[4:6])
	const dirEntrySize = 16
	dirEnd := 12 + int(numTables)*dirEntrySize
	if dirEnd > len(data) {
		return 0, 0, fmt.Errorf("fontserver: truncated table directory")
	}
	for i := 0; i < int(numTables); i++ {
		rec := data[12+i*dirEntrySize : 12+(i+1)*dirEntrySize]
		if string(rec[0:4]) != tag {
			continue
		}
		offset = binary.BigEndian.Uint32(rec[8:12])
		length = binary.BigEndian.Uint32(rec[12:16])
		if uint64(offset)+uint64(length) > uint64(len(data)) {
			return 0, 0, fmt.Errorf("fontserver: '%s' table out of bounds", tag)
		}
		return offset, length, nil
	}
	return 0, 0, fmt.Errorf("fontserver: no '%s' table", tag)
}

func sfntNames(data []byte) (family, subfamily string, err error) {
	nameOff, nameLen, err := findSfntTable(data, "name")
	if err != nil {
		return "", "", err
	}
	if nameLen < 6 {
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

func parseNameTable(table []byte) (map[uint16]string, error) {
	if len(table) < 6 {
		return nil, fmt.Errorf("fontserver: 'name' table too short")
	}
	count := binary.BigEndian.Uint16(table[2:4])
	// format 1 adds langtag records; stringOffset already skips them
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
		raw := storage[int(offset) : int(offset)+int(length)] // int arithmetic: uint16 would wrap
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
