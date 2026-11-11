package fontserver

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFont(t *testing.T, dir, relPath, family, subfamily string) {
	t.Helper()
	data := buildTestSFNT(t, []nameRecordSpec{
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameFamily, family},
		{platformWindows, encodingWindowsBMP, langWindowsEnUS, nameSubfamily, subfamily},
	})
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoScanGroupsByFamily(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir, "sans/Regular.ttf", "Test Sans", "Regular")
	writeTestFont(t, dir, "sans/Bold.ttf", "Test Sans", "Bold")
	writeTestFont(t, dir, "serif/Regular.otf", "Test Serif", "Regular")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.ttf"), []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := NewRepo(dir)
	if err := repo.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	families := repo.Families()
	if len(families) != 2 {
		t.Fatalf("Families() = %d families, want 2: %+v", len(families), families)
	}
	if families[0].Name != "Test Sans" || families[1].Name != "Test Serif" {
		t.Fatalf("Families() = %+v, want sorted [Test Sans, Test Serif]", families)
	}
	if len(families[0].Variants) != 2 {
		t.Fatalf("Test Sans has %d variants, want 2", len(families[0].Variants))
	}
}

func TestRepoScanMissingDirIsNotAnError(t *testing.T) {
	repo := NewRepo(filepath.Join(t.TempDir(), "does-not-exist"))
	if err := repo.Scan(); err != nil {
		t.Fatalf("Scan of a missing directory: %v, want nil", err)
	}
	if got := repo.Families(); len(got) != 0 {
		t.Fatalf("Families() = %+v, want empty", got)
	}
}

func TestRepoOpenRejectsPathsOutsideScan(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir, "a.ttf", "Test Sans", "Regular")
	repo := NewRepo(dir)
	if err := repo.Scan(); err != nil {
		t.Fatal(err)
	}

	if _, ok := repo.Open("a.ttf"); !ok {
		t.Error("Open(a.ttf) = not found, want found")
	}
	for _, bad := range []string{
		"../a.ttf",
		"../../etc/passwd",
		"b.ttf", // never scanned
		"a.ttf/../a.ttf",
	} {
		if _, ok := repo.Open(bad); ok {
			t.Errorf("Open(%q) = found, want rejected (not an exact scanned path)", bad)
		}
	}
}

func TestClassifySubfamily(t *testing.T) {
	cases := []struct {
		subfamily  string
		wantWeight int
		wantItalic bool
	}{
		{"Regular", 400, false},
		{"Bold", 700, false},
		{"Italic", 400, true},
		{"Bold Italic", 700, true},
		{"Thin", 100, false},
		{"ExtraLight", 200, false},
		{"Light", 300, false},
		{"Medium", 500, false},
		{"SemiBold", 600, false},
		{"ExtraBold", 800, false},
		{"Black", 900, false},
		{"Black Italic", 900, true},
		{"", 400, false},
	}
	for _, c := range cases {
		weight, italic := classifySubfamily(c.subfamily)
		if weight != c.wantWeight || italic != c.wantItalic {
			t.Errorf("classifySubfamily(%q) = (%d, %v), want (%d, %v)", c.subfamily, weight, italic, c.wantWeight, c.wantItalic)
		}
	}
}

func TestRepoScanIsAtomicSwap(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir, "a.ttf", "Test Sans", "Regular")
	repo := NewRepo(dir)
	if err := repo.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(repo.Families()) != 1 {
		t.Fatalf("expected 1 family after first scan")
	}

	writeTestFont(t, dir, "b.ttf", "Another Family", "Regular")
	if err := repo.Scan(); err != nil {
		t.Fatal(err)
	}
	if len(repo.Families()) != 2 {
		t.Fatalf("expected 2 families after rescan picking up the new file")
	}
}
