package textimport

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func plain(d docfile.Document) string {
	var sb strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	return sb.String()
}

func codeLang(d docfile.Document) string {
	for _, it := range d.Content {
		if it.Para != nil && it.Para.Style == "Code" {
			return it.Para.CodeLang
		}
	}
	return "?"
}

func TestJSONIsIndentedWhenValidAndLeftAloneWhenNot(t *testing.T) {
	d, err := Import(JSON, []byte(`{"a":[1,2],"b":{"c":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain(d); !strings.Contains(got, "{\n  \"a\": [") || codeLang(d) != "json" {
		t.Fatalf("%q lang %q", got, codeLang(d))
	}
	d, _ = Import(JSON, []byte(`{"a": 1,}`)) // not valid: as written
	if !strings.Contains(plain(d), `{"a": 1,}`) {
		t.Fatalf("%q", plain(d))
	}
}

func TestOtherTextFormatsAreCodeBlocksAsWritten(t *testing.T) {
	for kind, lang := range map[string]string{JSONL: "json", JSON5: "json5", YAML: "yaml", TOML: "toml", INI: "ini", Log: ""} {
		src := "key: value # note\n```fence```\n"
		d, err := Import(kind, []byte(src))
		if err != nil {
			t.Fatal(kind, err)
		}
		if got := plain(d); !strings.Contains(got, "key: value # note") || !strings.Contains(got, "```fence```") {
			t.Fatalf("%s: %q", kind, got)
		}
		if codeLang(d) != lang {
			t.Fatalf("%s: lang %q want %q", kind, codeLang(d), lang)
		}
	}
}

func TestCSVAndTSVAreTables(t *testing.T) {
	d, err := Import(CSV, []byte("name,qty\n\"Pens, blue\",=1+1\nink,\"two\nlines\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var tb *docfile.TableItem
	for _, it := range d.Content {
		if it.Table != nil {
			tb = it.Table
		}
	}
	if tb == nil || len(tb.Rows) != 3 {
		t.Fatalf("%+v", d.Content)
	}
	if tb.Rows[1].Cells[0] != "Pens, blue" || tb.Rows[1].Cells[1] != `\=1+1` || tb.Rows[2].Cells[1] != "two lines" {
		t.Fatalf("cells %q", tb.Rows)
	}
	d, _ = Import(TSV, []byte("a\tb\n1\t2\n"))
	if d.Content[0].Table == nil || d.Content[0].Table.Rows[1].Cells[1] != "2" {
		t.Fatalf("%+v", d.Content)
	}
}

func TestBigTablesAreCutAndSaid(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < MaxRows+10; i++ {
		sb.WriteString("a,b\n")
	}
	d, _ := Import(CSV, []byte(sb.String()))
	if len(d.Content[0].Table.Rows) != MaxRows || !strings.Contains(plain(d), "Showing the first 5000 of 5010 rows.") {
		t.Fatalf("rows %d, text %q", len(d.Content[0].Table.Rows), plain(d))
	}
}

func TestWhatIsNotTextIsRefused(t *testing.T) {
	if _, err := Import(Log, []byte("a\x00b")); err == nil {
		t.Fatal("NUL bytes")
	}
	if _, err := Import(Log, []byte("a\x01b")); err == nil {
		t.Fatal("control characters")
	}
	if _, err := Import(Log, make([]byte, MaxBytes+1)); err == nil {
		t.Fatal("too large")
	}
	if _, err := Import("nope", []byte("x")); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestEncodings(t *testing.T) {
	d, _ := Import(Log, []byte("\xef\xbb\xbfhello é"))
	if !strings.Contains(plain(d), "hello é") || strings.Contains(plain(d), "\xef\xbb\xbf") {
		t.Fatalf("utf-8 with a mark: %q", plain(d))
	}
	d, err := Import(Log, []byte{0xff, 0xfe, 'h', 0, 'i', 0, 0xe9, 0}) // UTF-16 LE
	if err != nil || !strings.Contains(plain(d), "hié") {
		t.Fatalf("utf-16: %q %v", plain(d), err)
	}
	d, _ = Import(Log, []byte("caf\xe9")) // Latin-1
	if !strings.Contains(plain(d), "café") {
		t.Fatalf("latin-1: %q", plain(d))
	}
	d, _ = Import(Log, []byte("a\r\nb\rc"))
	if !strings.Contains(plain(d), "a\nb\nc") {
		t.Fatalf("line ends: %q", plain(d))
	}
}
