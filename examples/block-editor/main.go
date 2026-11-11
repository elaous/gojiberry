package main

import (
	"fmt"
	"io"
	"os"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/xango"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	phone := crdt.NewDoc("ana-phone")
	desktop := crdt.NewDoc("ana-desktop")

	phone.Put(NewHeading("h1", "Fatura - Aulas de Inglês", 0))
	phone.Put(NewLineItem("l1", "4 aulas particulares", "R$ 320,00", 1))

	desktop.Put(NewNote("n1", "Cliente pediu 10% de desconto", 2))

	phoneOps := phone.Drain()
	desktopOps := desktop.Drain()
	phone.Apply(desktopOps...)
	desktop.Apply(phoneOps...)

	for _, doc := range []*crdt.Doc{phone, desktop} {
		if err := render(out, doc); err != nil {
			return err
		}
	}
	return nil
}

func render(out io.Writer, doc *crdt.Doc) error {
	blocks := doc.Blocks()

	editorHTML, err := xango.RenderString(Editor(EditorProps{Blocks: blocks}))
	if err != nil {
		return fmt.Errorf("editor render error: %w", err)
	}
	outputHTML, err := xango.RenderString(Output(OutputProps{Blocks: blocks}))
	if err != nil {
		return fmt.Errorf("output render error: %w", err)
	}

	_, err = fmt.Fprintln(out, "--- editor (thumb-friendly data entry) ---")
	if err == nil {
		_, err = fmt.Fprintln(out, editorHTML)
	}
	if err == nil {
		_, err = fmt.Fprintln(out, "--- output (clean final document) ---")
	}
	if err == nil {
		_, err = fmt.Fprintln(out, outputHTML)
	}
	return err
}
