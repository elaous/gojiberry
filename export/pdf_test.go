package export

import "testing"

func TestSplitOnSpaceRuns(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"hello", []string{"hello"}},
		{"a bb  c", []string{"a", " ", "bb", "  ", "c"}},
		{" leading", []string{" ", "leading"}},
		{"trailing ", []string{"trailing", " "}},
	}
	for _, tt := range tests {
		got := splitOnSpaceRuns(tt.in)
		if len(got) != len(tt.want) {
			t.Fatalf("splitOnSpaceRuns(%q) = %q, want %q", tt.in, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("splitOnSpaceRuns(%q) = %q, want %q", tt.in, got, tt.want)
			}
		}
	}
}
