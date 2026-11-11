package user

import "testing"

func TestNormalizeAndSame(t *testing.T) {
	if Normalize(" Ana@Gojiberry.io ") != "ana@gojiberry.io" || !Same("ANA@gojiberry.io", "ana@gojiberry.io ") || Same("", "") || Same("a", "b") {
		t.Fatal("addresses are compared trimmed and case-folded; empty is nobody")
	}
	if Same("google:AbC", "google:abc") || !Same(" google:AbC ", "google:AbC") {
		t.Fatal("subjects are compared exactly")
	}
}

func TestPersonIdentifiersAndDisplay(t *testing.T) {
	p := Person{ID: "google:Sub1", Email: "Ana@Gojiberry.io", Username: "ana", Name: "Ana Souza"}
	if p.DisplayName() != "Ana Souza" || (Person{ID: "x", Username: "ana"}).DisplayName() != "ana" ||
		(Person{ID: "x", Email: "a@gojiberry.io"}).DisplayName() != "a@gojiberry.io" || (Person{ID: "x"}).DisplayName() != "x" {
		t.Fatal("DisplayName falls back name, username, email, id")
	}
	for _, id := range []string{"google:Sub1", "ANA@gojiberry.io", "ana", " ana "} {
		if !p.Is(id) {
			t.Errorf("%q should name the person", id)
		}
	}
	if p.Is("google:sub1") || p.Is("") || p.Is("bo") {
		t.Error("a subject differing in case, a blank and a stranger are not the person")
	}
}
