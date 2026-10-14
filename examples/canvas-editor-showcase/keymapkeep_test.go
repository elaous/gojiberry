//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func TestKeymapRoundTripAndDefaults(t *testing.T) {
	if encodeKeymap(nil) != "" || decodeKeymap("") != nil {
		t.Error("the defaults are the empty text")
	}
	k := canvas.Keymap{"Mod+Shift+U": canvas.CmdUnderline, "Mod+U": ""} // a new chord, and an explicit blank
	got := decodeKeymap(encodeKeymap(k))
	if len(got) != 2 || got["Mod+Shift+U"] != canvas.CmdUnderline || got["Mod+U"] != "" {
		t.Errorf("round trip: %v", got)
	}
}

func TestKeymapRefusesWhatIsNotOne(t *testing.T) {
	for name, in := range map[string]string{
		"not json": "hello", "array": "[1]", "huge": `{"a":"` + strings.Repeat("x", 70<<10) + `"}`,
		"only odd": `{"` + strings.Repeat("c", 100) + `":"x"}`,
	} {
		if decodeKeymap(in) != nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
