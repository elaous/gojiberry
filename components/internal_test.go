package components

import "testing"

func TestBoolAttr(t *testing.T) {
	if got := boolAttr(true); got != "true" {
		t.Errorf("boolAttr(true) = %q, want %q", got, "true")
	}
	if got := boolAttr(false); got != "false" {
		t.Errorf("boolAttr(false) = %q, want %q", got, "false")
	}
}
