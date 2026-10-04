package main

import (
	"fmt"
	"os"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/xango"
)

func main() {
	phone := crdt.NewDoc("ana-phone")
	desktop := crdt.NewDoc("ana-desktop")

	// Ana is on a bus in Contagem with no signal; she drafts the
	// invoice entirely offline on her phone.
	phone.Put(NewHeading("h1", "Fatura - Aulas de Inglês", 0))
	phone.Put(NewLineItem("l1", "4 aulas particulares", "R$ 320,00", 1))

	// Before either device ever syncs, she also opens the same invoice
	// on her desktop at home and adds a note -- a real offline
	// conflict window, not a hypothetical one.
	desktop.Put(NewNote("n1", "Cliente pediu 10% de desconto", 2))

	// The bus reaches a cell tower: sync exchanges only what's pending
	// on each side -- the delta, never the whole document.
	phoneOps := phone.Drain()
	desktopOps := desktop.Drain()
	phone.Apply(desktopOps...)
	desktop.Apply(phoneOps...)

	for _, doc := range []*crdt.Doc{phone, desktop} {
		if err := render(doc); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func render(doc *crdt.Doc) error {
	blocks := doc.Blocks()

	editorHTML, err := xango.RenderString(Editor(EditorProps{Blocks: blocks}))
	if err != nil {
		return fmt.Errorf("editor render error: %w", err)
	}
	outputHTML, err := xango.RenderString(Output(OutputProps{Blocks: blocks}))
	if err != nil {
		return fmt.Errorf("output render error: %w", err)
	}

	fmt.Println("--- editor (thumb-friendly data entry) ---")
	fmt.Println(editorHTML)
	fmt.Println("--- output (clean final document) ---")
	fmt.Println(outputHTML)
	return nil
}
