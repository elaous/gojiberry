package user

import "testing"

func TestNormalizeAndSame(t *testing.T) {
	if Normalize(" Ana@X.test ") != "ana@x.test" || !Same("ANA@x.test", "ana@x.test ") || Same("", "") || Same("a", "b") {
		t.Fatal("addresses are compared trimmed and case-folded; empty is nobody")
	}
	if (Person{ID: "a@x.test"}).DisplayName() != "a@x.test" || (Person{ID: "a@x.test", Name: "Ana"}).DisplayName() != "Ana" {
		t.Fatal("DisplayName")
	}
}
