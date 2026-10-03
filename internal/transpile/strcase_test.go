package transpile

import "testing"

func TestCapWordsKeepsUnicodeAndMakesIdentifiers(t *testing.T) {
	for in, want := range map[string]string{"created_at": "CreatedAt", "élan_vital": "ÉlanVital", "1st_col": "X1stCol", "user-id": "UserId", "": "X"} {
		if got := CapWords(in); got != want {
			t.Errorf("CapWords(%q) = %q, want %q", in, got, want)
		}
	}
}
