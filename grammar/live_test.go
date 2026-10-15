package grammar

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestOllamaLive asks a real Ollama server one grammar question. It runs only when GOJIBERRY_OLLAMA_URL names a
// server (make ollama-check starts it from the Makefile settings); a model's answer is not fixed, so it asks only
// that the answer is parsed, every issue it returns lies inside the text, and the obvious mistake is found.
func TestOllamaLive(t *testing.T) {
	url := os.Getenv("GOJIBERRY_OLLAMA_URL")
	if url == "" {
		t.Skip("GOJIBERRY_OLLAMA_URL is not set (make ollama-check sets it)")
	}
	c := Client{BaseURL: url, Model: os.Getenv("GOJIBERRY_OLLAMA_MODEL"), HTTP: &http.Client{Timeout: 3 * time.Minute}}
	text := "He go to school every days, and they was late."
	issues, err := c.Check(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	n := len([]rune(text))
	for _, is := range issues {
		if is.From < 0 || is.To > n || is.From >= is.To {
			t.Errorf("an issue outside the text: %+v", is)
		}
	}
	if len(issues) == 0 {
		t.Errorf("the model found nothing wrong with %q", text)
	}
	t.Logf("%d issues: %+v", len(issues), issues)
}
