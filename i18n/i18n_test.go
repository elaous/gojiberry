package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestTextFallsBackFromRegionToLanguageToEnglish(t *testing.T) {
	c := New()
	c.Add("pt", map[string]string{"hello": "olá", "bye": "adeus"})
	c.Add("pt-BR", map[string]string{"hello": "oi"})
	if got := c.Text("pt-BR", "hello", "hello"); got != "oi" {
		t.Errorf("region wins: %q", got)
	}
	if got := c.Text("pt-BR", "bye", "bye"); got != "adeus" {
		t.Errorf("language next: %q", got)
	}
	if got := c.Text("pt_br", "hello", "hello"); got != "oi" {
		t.Errorf("underscores and case: %q", got)
	}
	if got := c.Text("pt", "missing", "the English"); got != "the English" {
		t.Errorf("English default: %q", got)
	}
	if got := c.Text("xx", "hello", "hello"); got != "hello" {
		t.Errorf("unknown language: %q", got)
	}
	c.Fallback = "pt"
	if got := c.Text("fr", "bye", "bye"); got != "adeus" {
		t.Errorf("Fallback: %q", got)
	}
}

func TestFormattingArguments(t *testing.T) {
	c := New()
	c.Add("de", map[string]string{"n": "%[2]d von %[1]d"})
	if got := c.Text("de", "n", "%d of %d", 5, 2); got != "2 von 5" {
		t.Errorf("a language may reorder: %q", got)
	}
	if got := c.Text("en", "n", "%d of %d", 5, 2); got != "5 of 2" {
		t.Errorf("English default formats too: %q", got)
	}
}

func TestMatchAndDirection(t *testing.T) {
	avail := []string{"en", "es", "fr", "pt-BR"}
	cases := []struct {
		pref []string
		want string
	}{
		{[]string{"fr-CA", "en"}, "fr"},
		{[]string{"de", "es-MX"}, "es"},
		{[]string{"pt-PT"}, "pt-BR"},
		{[]string{"ja", "ko"}, "en"},
		{nil, "en"},
	}
	for _, c := range cases {
		if got := Match(c.pref, avail, "en"); got != c.want {
			t.Errorf("Match(%v) = %q, want %q", c.pref, got, c.want)
		}
	}
	if Direction("ar-EG") != "rtl" || Direction("he") != "rtl" || Direction("fr") != "ltr" || Direction("") != "ltr" {
		t.Error("direction")
	}
}

// The shipped packs hold the same ids, so no language is left half done, and
// every message keeps the fmt verbs of its English original's count.
func TestShippedPacksAreComplete(t *testing.T) {
	c := Default()
	langs := c.Languages()
	if len(langs) < 3 {
		t.Fatalf("languages %v", langs)
	}
	ref := c.msgs["es"]
	for _, l := range langs {
		for id := range ref {
			if _, ok := c.msgs[l][id]; !ok {
				t.Errorf("%s lacks %q", l, id)
			}
		}
		for id := range c.msgs[l] {
			if _, ok := ref[id]; !ok {
				t.Errorf("%s has %q, which es lacks", l, id)
			}
			if strings.Count(c.msgs[l][id], "%") != strings.Count(ref[id], "%") {
				t.Errorf("%s %q has a different number of verbs: %q vs %q", l, id, c.msgs[l][id], ref[id])
			}
		}
	}
	if got := c.Text("fr", "saver.save", "Save"); got != "Enregistrer" {
		t.Errorf("fr save: %q", got)
	}
	if got := c.Text("de", "find.nOfM", "%d of %d", 1, 4); got != "1 von 4" {
		t.Errorf("de: %q", got)
	}
}

// Every message the code asks for by id, with its English text, is in the shipped packs. A new T("id", "text")
// (or tr, trf, Translate) in the sources without a translation fails here, so a language is never left behind
// by a feature. Ids built at run time ("tab."+name) are not seen by this check; add them by hand.
func TestEveryMessageInTheSourcesIsInThePacks(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ref := Default().msgs["es"]
	callers := map[string]bool{"T": true, "tr": true, "trf": true, "Translate": true}
	// plural callers take (id, one, other, n): the pack must hold id#other
	plurals := map[string]bool{"Tn": true, "trn": true, "TranslatePlural": true}
	fset := token.NewFileSet()
	missing := map[string]string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || len(c.Args) < 2 {
				return true
			}
			var name string
			switch fn := c.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			suffix := ""
			if plurals[name] {
				suffix = "#other"
			} else if !callers[name] {
				return true
			}
			a, ok1 := c.Args[0].(*ast.BasicLit)
			b, ok2 := c.Args[1].(*ast.BasicLit)
			if !ok1 || !ok2 || a.Kind != token.STRING || b.Kind != token.STRING {
				return true
			}
			id, _ := strconv.Unquote(a.Value)
			text, _ := strconv.Unquote(b.Value)
			if _, ok := ref[id+suffix]; !ok {
				rel, _ := filepath.Rel(root, p)
				missing[id+suffix] = rel + ": " + text
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id := range missing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t.Errorf("%q is asked for in the sources but is in no pack (%s): add it to i18n/packs/*.json", id, missing[id])
	}
}

// A pack message the sources no longer ask for is stale: it is translated for nothing and hides that a
// feature was removed. An id counts as used when its text is a string literal in the sources, or begins
// with one that ends in "." or "-" (an id built at run time, as "tab."+name), or is the "#form" of a plural id
// that is used.
func TestNoStaleMessagesInThePacks(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	literals := map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.STRING {
				if s, err := strconv.Unquote(b.Value); err == nil && len(s) < 80 {
					literals[s] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var prefixes []string
	for l := range literals {
		if strings.HasSuffix(l, ".") || strings.HasSuffix(l, "-") {
			prefixes = append(prefixes, l)
		}
	}
	var stale []string
	for id := range Default().msgs["es"] {
		base, _, _ := strings.Cut(id, "#")
		if literals[base] {
			continue
		}
		dynamic := false
		for _, p := range prefixes {
			if strings.HasPrefix(base, p) {
				dynamic = true
				break
			}
		}
		if !dynamic {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	for _, id := range stale {
		t.Errorf("%q is in the packs but no source asks for it: remove it from i18n/packs/*.json (or, if it is built at run time, make the prefix a literal ending in \".\")", id)
	}
}
