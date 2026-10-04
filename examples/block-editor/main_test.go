package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/xango"
)

// buildDiverged reproduces main's scenario: two replicas edit the
// same invoice fully offline before ever syncing.
func buildDiverged() (phone, desktop *crdt.Doc) {
	phone = crdt.NewDoc("ana-phone")
	desktop = crdt.NewDoc("ana-desktop")

	phone.Put(NewHeading("h1", "Fatura - Aulas de Inglês", 0))
	phone.Put(NewLineItem("l1", "4 aulas particulares", "R$ 320,00", 1))
	desktop.Put(NewNote("n1", "Cliente pediu 10% de desconto", 2))
	return phone, desktop
}

func TestReplicasConvergeAfterOfflineEdits(t *testing.T) {
	phone, desktop := buildDiverged()

	phoneOps := phone.Drain()
	desktopOps := desktop.Drain()
	phone.Apply(desktopOps...)
	desktop.Apply(phoneOps...)

	phoneBlocks := phone.Blocks()
	desktopBlocks := desktop.Blocks()
	if len(phoneBlocks) != 3 {
		t.Fatalf("expected 3 blocks after sync, got %d", len(phoneBlocks))
	}
	for i := range phoneBlocks {
		if phoneBlocks[i] != desktopBlocks[i] {
			t.Fatalf("replicas diverged at position %d: phone=%+v desktop=%+v", i, phoneBlocks[i], desktopBlocks[i])
		}
	}
}

func TestRenderedOutputIdenticalRegardlessOfSyncOrder(t *testing.T) {
	// Sync phone-first-then-desktop in one run, desktop-first-then-phone
	// in another; a correct CRDT merge renders byte-identical HTML
	// either way.
	renderBoth := func(applyOrder func(a, b *crdt.Doc)) (editorHTML, outputHTML string) {
		phone, desktop := buildDiverged()
		applyOrder(phone, desktop)
		blocks := phone.Blocks()

		e, err := xango.RenderString(Editor(EditorProps{Blocks: blocks}))
		if err != nil {
			t.Fatalf("editor render: %v", err)
		}
		o, err := xango.RenderString(Output(OutputProps{Blocks: blocks}))
		if err != nil {
			t.Fatalf("output render: %v", err)
		}
		return e, o
	}

	e1, o1 := renderBoth(func(a, b *crdt.Doc) {
		aOps, bOps := a.Drain(), b.Drain()
		a.Apply(bOps...)
		b.Apply(aOps...)
	})
	e2, o2 := renderBoth(func(a, b *crdt.Doc) {
		aOps, bOps := a.Drain(), b.Drain()
		b.Apply(aOps...)
		a.Apply(bOps...)
	})

	if e1 != e2 {
		t.Fatalf("editor HTML depends on sync order:\n%s\n---\n%s", e1, e2)
	}
	if o1 != o2 {
		t.Fatalf("output HTML depends on sync order:\n%s\n---\n%s", o1, o2)
	}
}

func TestOutputDecoupledFromEditorMarkup(t *testing.T) {
	blocks := []crdt.Block{
		NewHeading("h1", "Fatura", 0),
		NewLineItem("l1", "Consultoria", "R$ 100,00", 1),
	}

	editorHTML, err := xango.RenderString(Editor(EditorProps{Blocks: blocks}))
	if err != nil {
		t.Fatalf("editor render: %v", err)
	}
	outputHTML, err := xango.RenderString(Output(OutputProps{Blocks: blocks}))
	if err != nil {
		t.Fatalf("output render: %v", err)
	}

	// The editor is made of inputs (touch-editable); the output has no
	// inputs at all, just the clean rendered document -- same source
	// data, two independent renderings.
	if !strings.Contains(editorHTML, "<input") {
		t.Fatalf("expected editor view to contain editable inputs, got: %s", editorHTML)
	}
	if strings.Contains(outputHTML, "<input") {
		t.Fatalf("expected output view to contain no inputs, got: %s", outputHTML)
	}
	if !strings.Contains(outputHTML, "<h1>Fatura</h1>") {
		t.Fatalf("expected output view to render the heading as <h1>, got: %s", outputHTML)
	}
}
