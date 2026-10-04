package main

import (
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestPageRendersExpectedHTML(t *testing.T) {
	page := Page(PageProps{Name: "Gopher", Items: []string{"alpha", "beta", "gamma"}})
	got, err := xango.RenderString(page)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div id="app"><p class="greeting">Hello, Gopher!</p><ul class="items"><li>alpha</li><li>beta</li><li>gamma</li></ul></div>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestGreetingFallsBackForEmptyName(t *testing.T) {
	got, err := xango.RenderString(Greeting(GreetingProps{}))
	if err != nil {
		t.Fatal(err)
	}
	want := `<p class="greeting">Hello, stranger!</p>`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
