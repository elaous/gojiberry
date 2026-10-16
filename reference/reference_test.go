package reference

import "testing"

var (
	book = Source{ID: "b1", Kind: Book, Authors: []string{"Silva, Ana Maria", "Costa, Bruno"}, Title: "Writing for Readers", Publisher: "Harbour Press", Place: "Lisbon", Year: "2020", Edition: "2nd"}
	art  = Source{ID: "a1", Kind: Article, Authors: []string{"Costa, Bruno"}, Title: "On clear prose", Container: "Journal of Style", Volume: "12", Issue: "3", Pages: "45-67", Year: "2019", DOI: "10.1000/xyz"}
	web  = Source{ID: "w1", Kind: Web, Authors: []string{"World Health Organization"}, Title: "Healthy diet", Container: "WHO", Year: "2020", URL: "https://who.int/diet", Accessed: "2024-05-01"}
)

func TestEntries(t *testing.T) {
	for _, c := range []struct {
		st   Style
		s    Source
		n    int
		want string
	}{
		{APA, book, 1, "Silva, A. M., & Costa, B. (2020). Writing for Readers (2nd ed.). Harbour Press."},
		{APA, art, 1, "Costa, B. (2019). On clear prose. Journal of Style, 12(3), 45\u201367. https://doi.org/10.1000/xyz"},
		{APA, web, 1, "World Health Organization. (2020). Healthy diet. WHO. https://who.int/diet"},
		{MLA, book, 1, "Silva, Ana Maria, and Bruno Costa. Writing for Readers. 2nd ed., Harbour Press, 2020."},
		{MLA, art, 1, "Costa, Bruno. \u201cOn clear prose.\u201d Journal of Style, vol. 12, no. 3, 2019, pp. 45\u201367. doi.org/10.1000/xyz."},
		{Chicago, book, 1, "Silva, Ana Maria, and Bruno Costa. 2020. Writing for Readers. Lisbon: Harbour Press."},
		{Chicago, art, 1, "Costa, Bruno. 2019. \u201cOn clear prose.\u201d Journal of Style 12 (3): 45\u201367. https://doi.org/10.1000/xyz."},
		{IEEE, book, 3, "[3] A. M. Silva and B. Costa, Writing for Readers, 2nd ed. Lisbon: Harbour Press, 2020."},
		{IEEE, art, 2, "[2] B. Costa, \u201cOn clear prose,\u201d Journal of Style, vol. 12, no. 3, pp. 45\u201367, 2019. [Online]. Available: https://doi.org/10.1000/xyz"},
		{Harvard, book, 1, "Silva, A.M. and Costa, B. (2020) Writing for Readers. 2nd edn. Lisbon: Harbour Press."},
		{Harvard, art, 1, "Costa, B. (2019) \u2018On clear prose\u2019, Journal of Style, 12(3), pp. 45\u201367. Available at: https://doi.org/10.1000/xyz."},
		{Vancouver, book, 1, "1. Silva AM, Costa B. Writing for Readers. 2nd ed. Lisbon: Harbour Press; 2020."},
		{Vancouver, art, 4, "4. Costa B. On clear prose. Journal of Style. 2019;12(3):45-67. Available from: https://doi.org/10.1000/xyz"},
		{ABNT, book, 1, "SILVA, Ana Maria; COSTA, Bruno. Writing for Readers. 2nd ed. Lisbon: Harbour Press, 2020."},
		{ABNT, art, 1, "COSTA, Bruno. On clear prose. Journal of Style, v. 12, n. 3, p. 45-67, 2019. Disponível em: https://doi.org/10.1000/xyz."},
	} {
		if got := Text(Entry(c.st, c.s, c.n)); got != c.want {
			t.Errorf("%s %s:\n got  %q\n want %q", c.st, c.s.Kind, got, c.want)
		}
	}
}

func TestCitations(t *testing.T) {
	three := book
	three.Authors = []string{"A, X", "B, Y", "C, Z"}
	for _, c := range []struct {
		st   Style
		s    Source
		n    int
		page string
		want string
	}{
		{APA, book, 1, "", "(Silva & Costa, 2020)"},
		{APA, three, 1, "12", "(A et al., 2020, p. 12)"},
		{MLA, book, 1, "45", "(Silva and Costa 45)"},
		{Chicago, art, 1, "", "(Costa 2019)"},
		{IEEE, art, 7, "", "[7]"},
		{Vancouver, art, 2, "9", "(2, p. 9)"},
		{Harvard, book, 1, "", "(Silva and Costa, 2020)"},
		{ABNT, book, 1, "", "(SILVA; COSTA, 2020)"},
		{APA, Source{Title: "No author at all here ever", Year: ""}, 1, "", "(\u201cNo author at all\u201d, n.d.)"},
	} {
		if got := Citation(c.st, c.s, c.n, c.page); got != c.want {
			t.Errorf("%s: got %q want %q", c.st, got, c.want)
		}
	}
}

func TestBibliographyOrder(t *testing.T) {
	list := []Source{book, art}
	apa := Bibliography(APA, list) // alphabetical: Costa before Silva
	if Text(apa[0])[:5] != "Costa" {
		t.Fatalf("%q", Text(apa[0]))
	}
	ieee := Bibliography(IEEE, list) // as given
	if Text(ieee[0])[:7] != "[1] A. " {
		t.Fatalf("%q", Text(ieee[0]))
	}
	if o := Order(APA, list); o["a1"] != 1 || o["b1"] != 2 {
		t.Fatalf("%v", o)
	}
}

func TestItalicsAreMarked(t *testing.T) {
	var ital string
	for _, r := range Entry(APA, book, 1) {
		if r.Italic {
			ital += r.Text
		}
	}
	if ital != "Writing for Readers" {
		t.Fatalf("%q", ital)
	}
}
