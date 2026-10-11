package docstore

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
)

func TestDirectoryLookupPolicies(t *testing.T) {
	ana := auth.User{Email: "Ana@Corp.example", VerifiedEmail: true}
	unverified := auth.User{Email: "ana@corp.example"}
	if !AllowAllLookups.CanLookup(unverified, "x@y.example") {
		t.Error("the default allows every lookup")
	}
	same := SameDomain()
	for _, tc := range []struct {
		u     auth.User
		email string
		want  bool
	}{
		{ana, "bo@corp.example", true}, {ana, "BO@CORP.EXAMPLE", true}, {ana, "bo@other.example", false},
		{unverified, "bo@corp.example", false}, {auth.User{VerifiedEmail: true}, "bo@corp.example", false}, {ana, "no-at-sign", false},
	} {
		if got := same.CanLookup(tc.u, tc.email); got != tc.want {
			t.Errorf("SameDomain %+v %q: %v", tc.u, tc.email, got)
		}
	}
	contacts := Contacts(func(auth.User) []string { return []string{"Cy@Home.example"} })
	if !contacts.CanLookup(ana, "cy@home.example") || contacts.CanLookup(ana, "di@home.example") {
		t.Error("Contacts compares the normalised address")
	}
	if AnyOf().CanLookup(ana, "bo@corp.example") {
		t.Error("no policies allow nothing")
	}
	either := AnyOf(contacts, same)
	if !either.CanLookup(ana, "cy@home.example") || !either.CanLookup(ana, "bo@corp.example") || either.CanLookup(ana, "z@elsewhere.example") {
		t.Error("AnyOf allows what any allows")
	}
}

func TestParseDirectoryLookupMode(t *testing.T) {
	ana := auth.User{Email: "ana@corp.example", VerifiedEmail: true}
	for _, raw := range []string{"", "any", " ANY "} {
		p, err := ParseDirectoryLookupMode(raw)
		if err != nil || !p.CanLookup(ana, "x@other.example") {
			t.Errorf("%q: %v", raw, err)
		}
	}
	p, err := ParseDirectoryLookupMode("Domain")
	if err != nil || p.CanLookup(ana, "x@other.example") || !p.CanLookup(ana, "x@corp.example") {
		t.Errorf("domain: %v", err)
	}
	if _, err := ParseDirectoryLookupMode("friends"); err == nil || !strings.Contains(err.Error(), "friends") {
		t.Errorf("an unknown mode is refused and named: %v", err)
	}
}

func TestValidAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"ana@corp.example": true, "  Ana@Corp.Example ": true, "a@b": true,
		"": false, "no-at": false, "@corp.example": false, "ana@": false, "a@b@c": false,
		"a b@corp.example": false, "ana@corp\x00.example": false, "ana@corp\x7f.example": false,
		strings.Repeat("a", 320) + "@x": false,
	} {
		if got := ValidAddress(addr); got != want {
			t.Errorf("%q: %v, want %v", addr, got, want)
		}
	}
}

func TestDirectoryFingerprintSegment(t *testing.T) {
	if got := DirectoryFingerprintSegment("SHA256:ab+/c"); got != "SHA256-ab-_c" {
		t.Errorf("got %q", got)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	if got := clientIP(r); got != "203.0.113.7" {
		t.Errorf("got %q", got)
	}
	r.RemoteAddr = "not-an-address"
	if got := clientIP(r); got != "not-an-address" {
		t.Errorf("without a port: %q", got)
	}
}
