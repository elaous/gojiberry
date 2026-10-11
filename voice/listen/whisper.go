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
