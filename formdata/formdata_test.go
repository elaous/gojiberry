package formdata

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
)

func TestKeysAreNamesLabelsOrNumbersAndNeverRepeat(t *testing.T) {
	got := Keys([]Field{{Name: "mail"}, {Label: "Your e-mail"}, {Label: "  "}, {Name: "mail"}, {Label: "Your E-Mail!"}, {Label: "Größe"}})
	want := []string{"mail", "your_e_mail", "field3", "mail_2", "your_e_mail_2", "größe"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("keys %v, want %v", got, want)
	}
}

func TestJSONKeepsOrderAndTypes(t *testing.T) {
	b, err := JSON([]Field{
		{Kind: "text", Name: "name", Value: `Ana "A" <b>`},
		{Kind: "number", Name: "age", Value: " 42 "},
		{Kind: "number", Name: "bad", Value: "+5"},
		{Kind: "checkbox", Name: "agree", Value: "true"},
		{Kind: "checkbox", Name: "news", Value: ""},
		{Kind: "select", Name: "size", Value: "M"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	if back["name"] != `Ana "A" <b>` || back["age"] != float64(42) || back["bad"] != "+5" || back["agree"] != true || back["news"] != false || back["size"] != "M" {
		t.Errorf("%v", back)
	}
	s := string(b)
	if strings.Index(s, `"name"`) >= strings.Index(s, `"age"`) || strings.Index(s, `"age"`) >= strings.Index(s, `"size"`) {
		t.Errorf("the order of the fields is kept:\n%s", s)
	}
	if empty, _ := JSON(nil); string(empty) != "{}\n" {
		t.Errorf("no fields: %q", empty)
	}
}

func TestCSVHasAHeaderAndARowAndStopsFormulas(t *testing.T) {
	b, err := CSV([]Field{
		{Kind: "text", Name: "a", Value: "=HYPERLINK(\"http://evil\")"},
		{Kind: "text", Name: "b", Value: "x, y"},
		{Kind: "number", Name: "c", Value: "-3.5"},
		{Kind: "text", Name: "d", Value: "@cmd"},
		{Kind: "checkbox", Name: "e", Value: "true"},
		{Kind: "text", Name: "f", Value: "plain\nline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v %v\n%s", rows, err, b)
	}
	if strings.Join(rows[0], ",") != "a,b,c,d,e,f" {
		t.Errorf("header %v", rows[0])
	}
	want := []string{"'=HYPERLINK(\"http://evil\")", "x, y", "-3.5", "'@cmd", "true", "plain\nline"}
	for i, w := range want {
		if rows[1][i] != w {
			t.Errorf("cell %d = %q, want %q", i, rows[1][i], w)
		}
	}
}
