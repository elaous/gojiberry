package encrypt

import (
	"io"
	"log"
	"strings"
	"testing"
)

// logCapture sends the standard logger's output to w until the test ends.
func logCapture(t *testing.T, w *strings.Builder) {
	t.Helper()
	log.SetOutput(w)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
}
