package components

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/xango"
)

type shown struct{ msg string }

func (s shown) Error() string       { return "raw " + s.msg }
func (s shown) UserMessage() string { return s.msg }

func TestExplainErrorGroupsTheBuiltInSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"downgrade", fmt.Errorf("x: %w", docstore.ErrDowngrade), ErrKindRolledBack},
		{"locked", docstore.ErrLocked, ErrKindLocked},
		{"conflict", fmt.Errorf("save: %w", docstore.ErrConflict), ErrKindConflict},
		{"storage full", fmt.Errorf("%w: quota", docstore.ErrStorageFull), ErrKindStorageFull},
		{"deadline", context.DeadlineExceeded, ErrKindOffline},
		{"browser fetch failure", errors.New("TypeError: Failed to fetch"), ErrKindOffline},
		{"host words", shown{"in words"}, ErrKindApp},
		{"unknown", errors.New("boom"), ErrKindUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := ExplainError(c.err)
			if info.Kind != c.want {
				t.Fatalf("kind %q, want %q", info.Kind, c.want)
			}
			if info.Message == "" || info.Details == "" {
				t.Fatalf("message %q details %q", info.Message, info.Details)
			}
			if info.Kind != ErrKindApp && info.Message == info.Details {
				t.Error("the message is the raw text")
			}
			if strings.Contains(info.Message, "docstore") || strings.Contains(info.Message, "Err") {
				t.Errorf("Go text in the message: %s", info.Message)
			}
		})
	}
	if (ExplainError(nil) != ErrorInfo{}) {
		t.Error("nil")
	}
}

func TestRegisteredGroupsComeFirst(t *testing.T) {
	old := errorGroups
	defer func() { errorGroups = old }()
	special := errors.New("special")
	RegisterErrorGroups(
		ErrorGroup{Kind: "special", ID: "err.special", Default: "Special words.", Match: func(err error) bool { return errors.Is(err, special) }},
		ErrorGroup{Kind: "borrowed", As: docstore.ErrConflict, Match: func(err error) bool { return err.Error() == "borrow" }},
	)
	if got := ExplainError(fmt.Errorf("w: %w", special)); got.Kind != "special" || got.Message != "Special words." {
		t.Errorf("%+v", got)
	}
	if got := ExplainError(errors.New("borrow")); got.Kind != ErrKindConflict {
		t.Errorf("%+v", got)
	}
}

func TestExplainErrorKeepsHostWordsAndRawDetails(t *testing.T) {
	inner := errors.New("encrypt: raw inner")
	info := ExplainError(&hostErr{"Reload the page.", inner})
	if info.Message != "Reload the page." || info.Details != "encrypt: raw inner" {
		t.Errorf("%+v", info)
	}
}

type hostErr struct {
	msg string
	err error
}

func (h *hostErr) Error() string       { return h.msg }
func (h *hostErr) Unwrap() error       { return h.err }
func (h *hostErr) UserMessage() string { return h.msg }

func TestErrorViewIsAlertWithNativeDetails(t *testing.T) {
	CopyText = func(string) {}
	defer func() { CopyText = nil }()
	html, err := xango.RenderString(ErrorView("my-error", fmt.Errorf("%w: doc1", docstore.ErrConflict)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="my-error"`, `role="alert"`, "<details", "<summary>Details</summary>", "<pre", "docstore: the document changed", "Copy details"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	i := strings.Index(html, "</p>")
	if i < 0 || strings.Contains(html[:i], "docstore:") {
		t.Errorf("the alert carries the raw text: %s", html)
	}
}
