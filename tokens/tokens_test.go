package tokens

import (
	"strings"
	"testing"
)

func fields(ps []Problem) string {
	var f []string
	for _, p := range ps {
		f = append(f, p.Field)
	}
	return strings.Join(f, ",")
}

func TestDefaultSetIsValidAndWritesCSS(t *testing.T) {
	d := Default()
	if ps := d.Validate(); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
	css, err := d.CSS()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{":root{", "--gj-color-text:#1a1a1a;", "--gj-color-accent-text:#ffffff;", "--gj-font-1:16px;", "--gj-space-6:32px;"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in %s", want, css)
		}
	}
	again, _ := d.CSS()
	if css != again {
		t.Fatal("the output is deterministic")
	}
}

func TestContrastIsChecked(t *testing.T) {
	s := Default()
	s.Colors[Muted] = "#bbbbbb" // light grey on white
	s.Colors[Link] = "#7aa7d9"
	s.Colors[AccentText] = "#8fb3dd"
	got := fields(s.Validate())
	if got != "colors.muted-text,colors.link,colors.accent-text" {
		t.Fatalf("%q", got)
	}
	msg := s.Validate()[0].Message
	if !strings.Contains(msg, "muted-text on background") || !strings.Contains(msg, "4.5:1 is needed") {
		t.Fatalf("%q", msg)
	}
	// exactly at the line passes: #767676 on white is 4.54:1
	ok := Default()
	ok.Colors[Muted] = "#767676"
	if ps := ok.Validate(); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
}

func TestMissingAndUnreadableColours(t *testing.T) {
	s := Default()
	delete(s.Colors, Danger)
	s.Colors[Text] = "var(--x)"
	s.Colors["brand"] = "not a colour"
	s.Colors["extra"] = "#123456"
	got := fields(s.Validate())
	if got != "colors.text,colors.danger,colors.brand" {
		t.Fatalf("%q", got)
	}
	if _, err := s.CSS(); err == nil {
		t.Fatal("a set with problems writes no CSS")
	}
	if len((Set{}).Validate()) < 9 {
		t.Fatalf("an empty set is missing everything: %d", len((Set{}).Validate()))
	}
}

func TestScalesAreChecked(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Set)
		want string
	}{
		"small body":     {func(s *Set) { s.TypeScale = []float64{12, 14, 18} }, "typeScale"},
		"not ascending":  {func(s *Set) { s.TypeScale = []float64{16, 16, 20} }, "typeScale"},
		"empty type":     {func(s *Set) { s.TypeScale = nil }, "typeScale"},
		"empty spacing":  {func(s *Set) { s.Spacing = nil }, "spacing"},
		"zero spacing":   {func(s *Set) { s.Spacing = []float64{0, 4} }, "spacing"},
		"spacing shrink": {func(s *Set) { s.Spacing = []float64{8, 4} }, "spacing"},
	}
	for name, c := range cases {
		s := Default()
		c.mut(&s)
		if got := fields(s.Validate()); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestNamesAndValuesCannotBreakOutOfTheDeclaration(t *testing.T) {
	s := Default()
	s.Colors["x;}body{display:none"] = "#123456"
	css, err := s.CSS()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(css, "display:none") && !strings.Contains(css, "--gj-color-x--body-display-none:#123456;") {
		t.Fatalf("name not neutralised: %s", css)
	}
	bad := Default()
	bad.Colors[Text] = "red;} body{display:none"
	if len(bad.Validate()) == 0 {
		t.Fatal("a value with CSS in it is not a colour")
	}
	if _, err := bad.CSS(); err == nil {
		t.Fatal("and writes no CSS")
	}
}

func TestFromCSSReadsWhatCSSWrote(t *testing.T) {
	want := Default()
	css, err := want.CSS()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := FromCSS(css)
	if !ok {
		t.Fatal("not read")
	}
	if len(got.Validate()) != 0 {
		t.Errorf("a set that was valid is not after the round trip: %v", got.Validate())
	}
	if again, _ := got.CSS(); again != css {
		t.Errorf("round trip changed it:\n%s\n%s", css, again)
	}
	for _, bad := range []string{"", "body{}", ":root{color:red;}", ":root{"} {
		if _, ok := FromCSS(bad); ok {
			t.Errorf("%q should not read as a theme", bad)
		}
	}
}
