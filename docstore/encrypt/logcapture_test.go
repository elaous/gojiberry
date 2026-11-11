package encrypt

import (
	"io"
	"log"
	"strings"
	"testing"
)

func logCapture(t *testing.T, w *strings.Builder) {
	t.Helper()
	log.SetOutput(w)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
}
