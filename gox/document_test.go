package gox

import "testing"

func TestDocumentRender(t *testing.T) {
	doc := Document{
		Title: "gox-canvas",
		Head:  []Node{Style("body{color:red}")},
		Body:  El("h1", nil, Text("Hello")),
	}
	got, err := RenderString(doc.Render())
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><title>gox-canvas</title><style>body{color:red}</style></head><body><h1>Hello</h1></body></html>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestDocumentRenderWithoutTitle(t *testing.T) {
	doc := Document{Body: Text("hi")}
	got, err := RenderString(doc.Render())
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head></head><body>hi</body></html>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
