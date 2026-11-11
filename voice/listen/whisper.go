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

func Whisper(binary, model string, wav []byte) (string, error) {
	return WhisperWith(binary, model, wav, Options{})
}

const MaxPrompt = 800

// Options tunes one transcription without training anything.
type Options struct {
	Prompt   string
	Language string
}

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
