// Package speak turns text into speech with a local Piper install: it pipes the text into piper's own
// stdin and reads the WAV it writes to a temp --output_file, the well-established Piper CLI invocation
// shape (unchanged across the rhasspy/piper -> OHF-Voice/piper1-gpl rename) --
// `echo "text" | piper --model voice.onnx --output_file out.wav`.
package speak

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Timeout is how long one synthesis may run.
const Timeout = time.Minute

// Piper synthesises text with the piper binary and the voice model (model may be empty when the binary
// has a default), and returns the WAV.
func Piper(binary, model, text string) ([]byte, error) {
	out, err := os.CreateTemp("", "voiceserver-speak-*.wav")
	if err != nil {
		return nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	args := []string{"--output_file", outPath}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(text)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("piper: %w: %s", err, combined)
	}

	return os.ReadFile(outPath)
}
