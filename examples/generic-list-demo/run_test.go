package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestRunPrintsThePageOnOneLine(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	want, err := xango.RenderString(Page([]Person{{"Ada", "Engineer"}, {"Grace", "Admiral"}}))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != want+"\n" {
		t.Errorf("got %q\nwant %q", out.String(), want+"\n")
	}
}

type brokenWriter struct{}

var errBroken = errors.New("pipe closed")

func (brokenWriter) Write([]byte) (int, error) { return 0, errBroken }

func TestRunReportsAWriteFailure(t *testing.T) {
	if err := run(brokenWriter{}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

func TestItemsAreKeyedByNameAndSeparatedFromTheEmptyState(t *testing.T) {
	html, err := xango.RenderString(Page([]Person{{"Ada", "Engineer"}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(html, `data-key="`) != 1 || !strings.Contains(html, `data-key="Ada"`) || strings.Contains(html, "Nobody here yet.") {
		t.Errorf("one person: %s", html)
	}
	html, err = xango.RenderString(Page([]Person{{"<i>x</i>", "r&d"}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<i>") || !strings.Contains(html, "&lt;i&gt;x&lt;/i&gt;") || !strings.Contains(html, "r&amp;d") {
		t.Errorf("not escaped: %s", html)
	}
}
