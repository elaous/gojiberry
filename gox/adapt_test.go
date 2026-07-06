package gox

import "testing"

type stringerID int

func (s stringerID) String() string { return "id-42" }

func TestChild(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want Node
	}{
		{"a Node is used as-is", Text("already a node"), Text("already a node")},
		{"a []Node becomes a Fragment", []Node{Text("a"), Text("b")}, Frag(Text("a"), Text("b"))},
		{"nil renders nothing", nil, Frag()},
		{"a string becomes Text", "hello", Text("hello")},
		{"a fmt.Stringer is stringified via String()", stringerID(42), Text("id-42")},
		{"anything else is stringified via fmt.Sprint", 7, Text("7")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Child(c.in)
			gotHTML, err := RenderString(got)
			if err != nil {
				t.Fatal(err)
			}
			wantHTML, err := RenderString(c.want)
			if err != nil {
				t.Fatal(err)
			}
			if gotHTML != wantHTML {
				t.Errorf("Child(%#v) rendered %q, want %q", c.in, gotHTML, wantHTML)
			}
		})
	}
}

func TestAttrVal(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"a string is used as-is", "some-class", "some-class"},
		{"a fmt.Stringer is stringified via String()", stringerID(42), "id-42"},
		{"an int is stringified via fmt.Sprint", 7, "7"},
		{"a bool is stringified via fmt.Sprint", true, "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AttrVal(c.in); got != c.want {
				t.Errorf("AttrVal(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
