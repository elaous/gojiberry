package xango

import (
	"strings"
	"testing"
)

func TestDocumentRender(t *testing.T) {
	doc := Document{
		Title: "xango-canvas",
		Head:  []Node{Style("body{color:red}")},
		Body:  El("h1", nil, Text("Hello")),
	}
	got, err := RenderString(doc.Render())
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><title>xango-canvas</title><style>body{color:red}</style></head><body><h1>Hello</h1></body></html>`
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

func TestStyleCannotEndItsOwnElement(t *testing.T) {
	out, err := RenderString(Style("a{b:c} /* </style><script>alert(1)</script> */"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.ToLower(out), "</style") != 1 {
		t.Fatalf("a </style> inside the CSS ended the element: %s", out)
	}
}
