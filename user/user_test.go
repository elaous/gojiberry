package user

import "testing"

func TestNormalizeAndSame(t *testing.T) {
	if Normalize(" Ana@Gojiberry.io ") != "ana@gojiberry.io" || !Same("ANA@gojiberry.io", "ana@gojiberry.io ") || Same("", "") || Same("a", "b") {
		t.Fatal("addresses are compared trimmed and case-folded; empty is nobody")
	}
	if (Person{ID: "a@gojiberry.io"}).DisplayName() != "a@gojiberry.io" || (Person{ID: "a@gojiberry.io", Name: "Ana"}).DisplayName() != "Ana" {
		t.Fatal("DisplayName")
	}
}
