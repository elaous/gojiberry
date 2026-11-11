package encstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"rsc.io/qr"
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

func gridOf(t *testing.T, svg string, size int) [][]bool {
	t.Helper()
	i := strings.Index(svg, `<path d="`)
	if i < 0 {
		t.Fatal("no path")
	}
	d := svg[i+len(`<path d="`):]
	d = d[:strings.Index(d, `"`)]
	grid := make([][]bool, size)
	for y := range grid {
		grid[y] = make([]bool, size)
	}
	for _, seg := range strings.Split(d, "z") {
		if seg == "" {
			continue
		}
		var x, y, w, w2 int
		if n, err := fmt.Sscanf(seg, "M%d %dh%dv1h-%d", &x, &y, &w, &w2); n != 4 || err != nil || w != w2 {
			t.Fatalf("segment %q: %v", seg, err)
		}
		for k := range w {
			grid[y-4][x-4+k] = true
		}
	}
	return grid
}

func TestRecoveryQRDrawsTheCodeOfTheKeyAndNothingElse(t *testing.T) {
	_, secret, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := RecoveryQR(secret, `QR <code> of "the" key`)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := qr.Encode(secret, qr.M)
	grid := gridOf(t, svg, code.Size)
	for y := range code.Size {
		for x := range code.Size {
			if grid[y][x] != code.Black(x, y) {
				t.Fatalf("module (%d,%d) differs from the code", x, y)
			}
		}
	}
	n := code.Size + 8 // four modules of quiet zone on every side
	for _, want := range []string{fmt.Sprintf(`viewBox="0 0 %d %d"`, n, n), `role="img"`, `aria-label="QR &lt;code&gt; of &#34;the&#34; key"`, `<title>QR &lt;code&gt; of &#34;the&#34; key</title>`, `fill="#fff"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("lacks %q in %.200s", want, svg)
		}
	}
	for _, bad := range []string{"http://", "https://", "href", "<script", "<image", "url("} {
		if strings.Contains(svg, bad) {
			t.Errorf("refers outside itself: %q", bad)
		}
	}
	if _, err := RecoveryQR("  ", "x"); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("%v", err)
	}
	if _, err := RecoveryQR(strings.Repeat("x", 5000), "x"); err == nil {
		t.Fatal("a key too long for a QR code is an error, not a broken picture")
	}
}

func TestRecoverySheetHasTheQRCodeOnlyWhenAsked(t *testing.T) {
	_, secret, _ := NewRecoveryKey("Recovery key")
	fp, _ := RecoveryFingerprint(secret)
	text := SheetText{Title: "T", Intro: "i", KeyLabel: "Key", Finger: "F", Keep: "k", Lost: "l", Print: "p"}
	without := string(RecoverySheet(text, "", secret, fp))
	if strings.Contains(without, "<svg") {
		t.Fatal("no QR text, no code")
	}
	text.QRAlt = "QR code of the key"
	with := string(RecoverySheet(text, "", secret, fp))
	if !strings.Contains(with, "<svg") || !strings.Contains(with, secret) {
		t.Fatal("the code is added and the key stays as text")
	}
	for _, bad := range []string{"http://", "https://", "src=", "href=", "@import", "url("} {
		if strings.Contains(with, bad) {
			t.Errorf("the sheet refers outside itself: %q", bad)
		}
	}
}
