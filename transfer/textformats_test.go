//go:build experimental || exp_textformats

package transfer

import (
	"strings"
	"testing"
)

func TestTheTextFormatsAreOpenedByTheirExtension(t *testing.T) {
	r := Default()
	for _, name := range []string{"a.csv", "a.TSV", "a.tab", "a.yml", "a.yaml", "a.toml", "a.ini", "a.cfg", "a.conf", "a.properties", "a.log", "a.jsonl", "a.ndjson", "a.json5"} {
		if _, err := r.ImporterFor(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	d, err := r.Import("", "t.csv", []byte("a,b\n1,2\n"))
	if err != nil || len(d.Content) == 0 || d.Content[0].Table == nil {
		t.Fatalf("csv: %+v %v", d, err)
	}
}

func TestADotJSONFileIsADocumentOrData(t *testing.T) {
	r := Default()
	doc, err := r.Import("", "d.json", []byte(`{"title":"Report","content":[{"chars":{"text":"hello\n"}}]}`))
	if err != nil || doc.Title != "Report" {
		t.Fatalf("a document: %+v %v", doc, err)
	}
	data, err := r.Import("", "data.json", []byte(`{"users":[{"id":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, it := range data.Content {
		if it.Chars != nil {
			text.WriteString(it.Chars.Text)
		}
	}
	if data.Title != "" || !strings.Contains(text.String(), "\"users\": [") {
		t.Fatalf("data: %+v %q", data, text.String())
	}
}
