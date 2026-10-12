package listen

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWhisperWithPassesPromptAndLanguage(t *testing.T) {
	// the text file is named by -of (the 4th flag value); echo every argument into it
	bin := fakeTool(t, `base=$5; printf '%s|' "$@" > "$base.txt"`)
	got, err := WhisperWith(bin, "m.bin", []byte("x"), Options{Prompt: "Gojiberry\n  Samir\x00 Aouad", Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "|--prompt|Gojiberry Samir Aouad|") || !strings.Contains(got, "|-l|de|") {
		t.Fatalf("args %q", got)
	}
	// nothing usable asked, nothing passed: the same arguments as Whisper
	got, err = WhisperWith(bin, "", []byte("x"), Options{Prompt: "  \n", Language: "de; rm -rf"})
	if err != nil || strings.Contains(got, "--prompt") || strings.Contains(got, "|-l|") {
		t.Fatalf("empty prompt and bad language are dropped: %q, %v", got, err)
	}
}

func TestCleanPromptCutsAtACharacterBoundary(t *testing.T) {
	got := CleanPrompt(strings.Repeat("é", MaxPrompt)) // two bytes each
	if len(got) > MaxPrompt || !utf8.ValidString(got) || len(got) < MaxPrompt-2 {
		t.Fatalf("len %d valid %v", len(got), utf8.ValidString(got))
	}
	if CleanPrompt("  a \t b\n") != "a b" {
		t.Error("spaces collapse")
	}
	if CleanLanguage("de") != "de" || CleanLanguage("pt-BR") != "pt-BR" || CleanLanguage("d") != "" || CleanLanguage("de-1") != "" {
		t.Error("language codes")
	}
}
