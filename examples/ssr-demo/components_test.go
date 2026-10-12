package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestItemListEmptyRendersAnEmptyList(t *testing.T) {
	for name, items := range map[string][]string{"nil": nil, "empty": {}} {
		got, err := xango.RenderString(ItemList(ItemListProps{Items: items}))
		if err != nil {
			t.Fatal(err)
		}
		if want := `<ul class="items"></ul>`; got != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}

func TestItemListKeepsOrderAndEscapesText(t *testing.T) {
	got, err := xango.RenderString(ItemList(ItemListProps{Items: []string{"b", "<i>a</i>", "a&b"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := `<ul class="items"><li>b</li><li>&lt;i&gt;a&lt;/i&gt;</li><li>a&amp;b</li></ul>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestGreetingEscapesTheName(t *testing.T) {
	got, err := xango.RenderString(Greeting(GreetingProps{Name: `<b>"x"</b>`}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<b>") || !strings.Contains(got, "&lt;b&gt;") {
		t.Errorf("name not escaped: %s", got)
	}
}

func TestPageWithoutNameOrItems(t *testing.T) {
	got, err := xango.RenderString(Page(PageProps{}))
	if err != nil {
		t.Fatal(err)
	}
	want := `<div id="app"><p class="greeting">Hello, stranger!</p><ul class="items"></ul></div>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
