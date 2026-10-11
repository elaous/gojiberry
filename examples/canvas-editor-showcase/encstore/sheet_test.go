package encstore

import (
	"strings"
	"testing"
	"time"
)

func TestRecoverySheetHoldsTheKeyAsPlainEscapedText(t *testing.T) {
	_, secret, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := RecoveryFingerprint(secret)
	if err != nil || fp == "" {
		t.Fatalf("fingerprint %q %v", fp, err)
	}
	page := string(RecoverySheet(SheetText{Title: "Recovery <key> sheet", Intro: "i", KeyLabel: "Key", Finger: "Fingerprint", Keep: "Keep it.", Lost: "Lost is lost.", Print: "Print"}, `A "label"`, secret, fp))
	for _, want := range []string{secret, fp, "Recovery &lt;key&gt; sheet", "Key (A &#34;label&#34;)", "Lost is lost.", "window.print()"} {
		if !strings.Contains(page, want) {
			t.Errorf("sheet lacks %q", want)
		}
	}
	// nothing is fetched from anywhere
	for _, bad := range []string{"http://", "https://", "src=", "href=", "@import", "url("} {
		if strings.Contains(page, bad) {
			t.Errorf("the sheet refers outside itself: %q", bad)
		}
	}
	if _, err := RecoveryFingerprint("nope"); err == nil {
		t.Error("a bad secret has a fingerprint")
	}
}

func TestCheckDue(t *testing.T) {
	now := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, c := range []struct {
		name         string
		checked, snz time.Time
		due          bool
	}{
		{"never checked", time.Time{}, time.Time{}, true},
		{"checked yesterday", now.Add(-day), time.Time{}, false},
		{"checked 89 days ago", now.Add(-89 * day), time.Time{}, false},
		{"checked 90 days ago", now.Add(-90 * day), time.Time{}, true},
		{"overdue but snoozed", now.Add(-200 * day), now.Add(day), false},
		{"snooze over", now.Add(-200 * day), now.Add(-time.Second), true},
	} {
		if got := CheckDue(now, c.checked, c.snz, ReminderEvery); got != c.due {
			t.Errorf("%s: %v, want %v", c.name, got, c.due)
		}
	}
}
