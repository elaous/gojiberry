package i18n

import (
	"testing"
	"time"
)

func TestNumber(t *testing.T) {
	for _, c := range []struct {
		lang string
		n    float64
		dec  int
		want string
	}{
		{"en", 1234567.891, 2, "1,234,567.89"}, {"en", 999, 0, "999"}, {"en", 1000, 0, "1,000"},
		{"de", 1234567.891, 2, "1.234.567,89"}, {"fr", 1234.5, 1, "1 234,5"}, {"es", 1234, 0, "1234"}, {"es", 12345, 0, "12.345"},
		{"en", -1234.5, 1, "-1,234.5"}, {"en", -0.001, 2, "0.00"}, {"pt-BR", 1000, 0, "1,000"}, {"de-AT", 0.5, 1, "0,5"},
	} {
		if got := Number(c.lang, c.n, c.dec); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.lang, c.n, got, c.want)
		}
	}
	if got := Percent("fr", 0.256); got != "26 %" {
		t.Errorf("percent fr %q", got)
	}
	if got := Percent("en", 0.5); got != "50%" {
		t.Errorf("percent en %q", got)
	}
}

func TestDateAndTime(t *testing.T) {
	d := time.Date(2026, time.December, 31, 15, 4, 0, 0, time.UTC)
	for _, c := range []struct {
		lang  string
		style DateStyle
		want  string
	}{
		{"en", DateShort, "12/31/2026"}, {"en-GB", DateShort, "31/12/2026"}, {"en-GB", DateMedium, "31 Dec 2026"}, {"en", DateMedium, "Dec 31, 2026"}, {"en", DateLong, "December 31, 2026"},
		{"de", DateShort, "31.12.2026"}, {"de", DateLong, "31. Dezember 2026"}, {"fr", DateLong, "31 décembre 2026"}, {"fr", DateShort, "31/12/2026"},
		{"es", DateLong, "31 de diciembre de 2026"}, {"es", DateMedium, "31 dic 2026"}, {"ja", DateShort, "12/31/2026"},
	} {
		if got := Date(c.lang, d, c.style); got != c.want {
			t.Errorf("%s %d: %q, want %q", c.lang, c.style, got, c.want)
		}
	}
	if got := Time("en", d); got != "3:04 PM" {
		t.Errorf("en time %q", got)
	}
	if got := Time("en", time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)); got != "12:05 AM" {
		t.Errorf("midnight %q", got)
	}
	if got := Time("de", d); got != "15:04" {
		t.Errorf("de time %q", got)
	}
}

func TestAgo(t *testing.T) {
	c := Default()
	for _, tc := range []struct {
		lang string
		d    time.Duration
		want string
	}{
		{"en", 10 * time.Second, "just now"}, {"en", 60 * time.Second, "1 minute ago"}, {"en", 5 * time.Minute, "5 minutes ago"},
		{"en", 59 * time.Minute, "59 minutes ago"}, {"en", 61 * time.Minute, "1 hour ago"}, {"en", 5 * time.Hour, "5 hours ago"},
		{"en", 36 * time.Hour, "2 days ago"}, {"en", 48 * time.Hour, "2 days ago"}, {"en", -time.Minute, "just now"},
		{"de", 5 * time.Minute, "vor 5 Minuten"}, {"de", 25 * time.Hour, "vor 1 Tag"}, {"fr", 2 * time.Hour, "il y a 2 heures"},
		{"fr", 100 * time.Hour, "il y a 4 jours"}, {"es", 90 * time.Second, "hace 2 minutos"}, {"es", 0, "ahora mismo"},
	} {
		if got := c.Ago(tc.lang, tc.d); got != tc.want {
			t.Errorf("%s %v: %q, want %q", tc.lang, tc.d, got, tc.want)
		}
	}
}

func TestZoneAndDateTime(t *testing.T) {
	at := time.Date(2026, 10, 14, 14, 5, 0, 0, time.UTC)
	for _, c := range []struct {
		loc  *time.Location
		lang string
		want string
	}{
		{time.UTC, "en", "Oct 14, 2026, 2:05 PM (UTC)"},
		{time.FixedZone("x", 2*3600), "de", "14. Okt. 2026, 16:05 (UTC+2)"},
		{time.FixedZone("x", -5*3600), "en-GB", "14 Oct 2026, 09:05 (UTC-5)"},
		{time.FixedZone("x", 5*3600+1800), "fr", "14 oct. 2026, 19:35 (UTC+5:30)"},
	} {
		if got := DateTime(c.lang, at.In(c.loc)); got != c.want {
			t.Errorf("DateTime(%q, %v) = %q, want %q", c.lang, c.loc, got, c.want)
		}
	}
}
