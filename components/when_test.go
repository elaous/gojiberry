package components

import (
	"testing"
	"time"
)

func TestFormatTimeIsTheAppsToSet(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if got := FormatTime(at, false); got != "2026-03-04 05:06" {
		t.Errorf("default = %q", got)
	}
	if got := FormatTime(at, true); got != "2026-03-04 05:06:07" {
		t.Errorf("default with seconds = %q", got)
	}
	old := FormatTime
	defer func() { FormatTime = old }()
	FormatTime = func(t time.Time, _ bool) string { return "at " + t.Format("15h") }
	if got := timeLabel(at); got != "at 05h" {
		t.Errorf("a comment's time ignores the app's format: %q", got)
	}
}
