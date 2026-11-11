package main

import (
	"encoding/json"

	"github.com/elaous/gojiberry/canvas"
)

const (
	maxKeymapEntries = 500
	maxChordLen      = 48
	maxCommandLen    = 64
)

func encodeKeymap(k canvas.Keymap) string {
	if len(k) == 0 {
		return ""
	}
	m := make(map[string]string, len(k))
	for chord, cmd := range k {
		m[chord] = string(cmd)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func decodeKeymap(s string) canvas.Keymap {
	if s == "" || len(s) > 64<<10 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil || len(m) > maxKeymapEntries {
		return nil
	}
	out := canvas.Keymap{}
	for chord, cmd := range m {
		if chord == "" || len(chord) > maxChordLen || len(cmd) > maxCommandLen {
			continue
		}
		out[chord] = canvas.Command(cmd)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
