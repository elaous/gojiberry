package speak

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Timeout is how long one synthesis may run.
const Timeout = time.Minute

func Piper(binary, model, text string) ([]byte, error) { return PiperAt(binary, model, text, 1) }

func PiperAt(binary, model, text string, rate float64) ([]byte, error) {
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
	if rate >= 0.5 && rate <= 2 && rate != 1 {
		args = append(args, "--length_scale", strconv.FormatFloat(1/rate, 'f', 3, 64))
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
