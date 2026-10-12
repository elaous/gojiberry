// Package listen turns speech into text with a local whisper.cpp install.
package listen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Timeout is how long one transcription may run.
const Timeout = 5 * time.Minute

// Whisper transcribes a WAV with a local whisper.cpp install: writes the given
// WAV to a temp file and runs it through whisper.cpp's own well-
// established CLI shape (`whisper-cli -m model.bin -f in.wav -otxt -of
// outbase -nt` -- plain text output, no timestamps, predates and
// underlies go-whisper's own CLI too), then reads back the .txt file
// it writes.
func Whisper(binary, model string, wav []byte) (string, error) {
	return WhisperWith(binary, model, wav, Options{})
}

// MaxPrompt is the longest initial prompt passed on, in bytes (whisper.cpp keeps about 224 tokens of
// it; more only costs time).
const MaxPrompt = 800

// Options tunes one transcription without training anything.
type Options struct {
	// Prompt is the initial prompt: words and phrases the speaker is likely to say (names, terms, the
	// person's own vocabulary). Whisper is biased toward spelling them as given. Control characters are
	// dropped and it is cut to MaxPrompt bytes at a character boundary.
	Prompt string
	// Language is a language code such as "de" ("" lets the model detect it). Only letters and "-".
	Language string
}

// CleanPrompt is a prompt as it is passed on: no control characters, spaces collapsed, at most MaxPrompt bytes.
func CleanPrompt(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if b.Len()+utf8.RuneLen(r)+1 > MaxPrompt {
			break
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CleanLanguage is a language code if s looks like one (2 to 8 letters, with "-"), else "".
func CleanLanguage(s string) string {
	if len(s) < 2 || len(s) > 8 {
		return ""
	}
	for _, r := range s {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && r != '-' {
			return ""
		}
	}
	return s
}

// WhisperWith is Whisper with a bias: the prompt and language of o are passed to whisper.cpp
// (`--prompt` and `-l`). With the zero Options it is exactly Whisper.
func WhisperWith(binary, model string, wav []byte, o Options) (string, error) {
	in, err := os.CreateTemp("", "voiceserver-transcribe-*.wav")
	if err != nil {
		return "", err
	}
	inPath := in.Name()
	defer func() { _ = os.Remove(inPath) }()
	if _, err := in.Write(wav); err != nil {
		_ = in.Close()
		return "", err
	}
	_ = in.Close()

	outBase := inPath[:len(inPath)-len(filepath.Ext(inPath))]
	outPath := outBase + ".txt"
	defer func() { _ = os.Remove(outPath) }()

	args := []string{"-f", inPath, "-otxt", "-of", outBase, "-nt"}
	if model != "" {
		args = append(args, "-m", model)
	}
	if p := CleanPrompt(o.Prompt); p != "" {
		args = append(args, "--prompt", p)
	}
	if l := CleanLanguage(o.Language); l != "" {
		args = append(args, "-l", l)
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("whisper: %w: %s", err, combined)
	}

	text, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(text)), nil
}
