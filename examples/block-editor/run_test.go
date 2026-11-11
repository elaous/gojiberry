package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/xango"
)

type failAfter struct{ n int }

var errWrite = errors.New("disk full")

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errWrite
	}
	f.n--
	return len(p), nil
}

func TestRunPrintsBothReplicasWithTheSameViews(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(out.String(), "--- ")
	if len(parts) != 5 || parts[0] != "" {
		t.Fatalf("want 4 sections, got %d: %q", len(parts)-1, out.String())
	}
	editorHdr, outputHdr := "editor (thumb-friendly data entry) ---\n", "output (clean final document) ---\n"
	for i, hdr := range []string{editorHdr, outputHdr, editorHdr, outputHdr} {
		if !strings.HasPrefix(parts[i+1], hdr) {
			t.Errorf("section %d starts %q, want %q", i, parts[i+1], hdr)
		}
	}
	if parts[1] != parts[3] || parts[2] != parts[4] {
		t.Errorf("the replicas print different views:\n%s", out.String())
	}
	for _, want := range []string{"Fatura - Aulas de Inglês", "4 aulas particulares", "R$ 320,00", "Cliente pediu 10% de desconto"} {
		if !strings.Contains(parts[1], want) || !strings.Contains(parts[2], want) {
			t.Errorf("a view lacks %q", want)
		}
	}
	if h, l, n := strings.Index(parts[2], "<h1>"), strings.Index(parts[2], `class="line-item"`), strings.Index(parts[2], `class="note"`); h < 0 || h >= l || l >= n {
		t.Errorf("output order: heading %d, line item %d, note %d", h, l, n)
	}
}

func TestRunReportsAWriteFailure(t *testing.T) {
	for ok := 0; ok < 4; ok++ {
		w := &failAfter{n: ok}
		if err := run(w); !errors.Is(err, errWrite) {
			t.Errorf("failing after %d writes: err = %v", ok, err)
		}
	}
	if err := run(&failAfter{n: 5}); !errors.Is(err, errWrite) {
		t.Errorf("failing in the second replica: err = %v", err)
	}
}

func TestBlockRowByKind(t *testing.T) {
	cases := []struct {
		name  string
		block crdt.Block
		want  []string
		not   []string
	}{
		{"heading", NewHeading("a", `Title "q"`, 0),
			[]string{`class="block block-heading"`, `class="block-input block-input-heading"`, `value="Title &#34;q&#34;"`}, []string{"<textarea"}},
		{"line item", NewLineItem("b", "Work", "R$ 1,00", 1),
			[]string{`class="block block-line-item"`, `block-input-desc" value="Work"`, `block-input-amount" value="R$ 1,00"`}, nil},
		{"note", NewNote("c", "call <back>", 2),
			[]string{`class="block block-note"`, "<textarea", "call &lt;back&gt;</textarea>"}, []string{"<input"}},
		{"unknown kind", crdt.Block{ID: "d", Kind: "photo", Content: "pic.jpg"},
			[]string{`<div class="block">pic.jpg</div>`}, []string{"<input", "<textarea"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := xango.RenderString(BlockRow(BlockRowProps{Block: c.block}))
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("lacks %q in %s", w, got)
				}
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("has %q in %s", n, got)
				}
			}
		})
	}
}

func TestOutputSkipsUnknownKindsAndEscapes(t *testing.T) {
	got, err := xango.RenderString(Output(OutputProps{Blocks: []crdt.Block{
		NewHeading("a", "<b>x</b>", 0),
		{ID: "p", Kind: "photo", Content: "pic.jpg", Order: 1},
		NewNote("n", "a & b", 2),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	want := `<div class="output"><h1>&lt;b&gt;x&lt;/b&gt;</h1><p class="note">a &amp; b</p></div>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestParseLineItemWithoutASeparator(t *testing.T) {
	d, a := ParseLineItem(crdt.Block{Content: "only a description"})
	if d != "only a description" || a != "" {
		t.Errorf("description %q, amount %q", d, a)
	}
}
