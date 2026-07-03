package gox

import "testing"

func TestRenderBasic(t *testing.T) {
	n := El("div", []Attr{A("class", "greeting")},
		El("p", nil, Text("Hello, <world> & \"friends\"!")),
	)
	got, err := RenderString(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div class="greeting"><p>Hello, &lt;world&gt; &amp; &#34;friends&#34;!</p></div>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRenderVoidElement(t *testing.T) {
	n := El("img", []Attr{A("src", "cat.png")}, Text("should be ignored"))
	got, err := RenderString(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `<img src="cat.png">`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRenderFragmentAndEventsAreInert(t *testing.T) {
	n := Frag(
		El("button", []Attr{On("click", func(Event) {})}, Text("Click")),
		Text("!"),
	)
	got, err := RenderString(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `<button>Click</button>!`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRenderRaw(t *testing.T) {
	n := El("div", nil, Raw("<b>bold</b>"))
	got, err := RenderString(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div><b>bold</b></div>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
