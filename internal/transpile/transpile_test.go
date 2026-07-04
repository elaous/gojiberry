package transpile

import (
	"go/format"
	"strings"
	"testing"
)

// mustFormat runs gofmt over got and fails the test with a readable diff
// if the generated code isn't even syntactically valid Go.
func mustFormat(t *testing.T, got string) string {
	t.Helper()
	src := "package p\nfunc f() {\n" + got + "\n}\n"
	out, err := format.Source([]byte(src))
	if err != nil {
		t.Fatalf("generated code is not valid Go: %v\n---\n%s\n---", err, src)
	}
	return string(out)
}

func transpileExpr(t *testing.T, gox string) string {
	t.Helper()
	got, err := (&parser{file: "t.gox"}).transpileChunk([]rune(gox))
	if err != nil {
		t.Fatalf("transpile error: %v\ninput: %s", err, gox)
	}
	return got
}

func TestSimpleElement(t *testing.T) {
	got := transpileExpr(t, `x := <div class="greeting">Hello</div>`)
	want := `x := gox.El("div", []gox.Attr{gox.A("class", "greeting")}, gox.Text("Hello"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestSelfClosingVoidElement(t *testing.T) {
	got := transpileExpr(t, `return <br/>`)
	want := `return gox.El("br", nil)`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestTextInterpolation(t *testing.T) {
	got := transpileExpr(t, `return <p>Hello, {name}!</p>`)
	want := `return gox.El("p", nil, gox.Text("Hello, "), gox.Child(name), gox.Text("!"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestNestedElements(t *testing.T) {
	got := transpileExpr(t, `return <div><span>a</span><span>b</span></div>`)
	want := `return gox.El("div", nil, gox.El("span", nil, gox.Text("a")), gox.El("span", nil, gox.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestFragment(t *testing.T) {
	got := transpileExpr(t, `return <><li>a</li><li>b</li></>`)
	want := `return gox.Frag(gox.El("li", nil, gox.Text("a")), gox.El("li", nil, gox.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestEventAttr(t *testing.T) {
	got := transpileExpr(t, `return <button onClick={handleClick}>Go</button>`)
	want := `return gox.El("button", []gox.Attr{gox.On("click", handleClick)}, gox.Text("Go"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestDynamicAttrValue(t *testing.T) {
	got := transpileExpr(t, `return <div class={cls}></div>`)
	want := `return gox.El("div", []gox.Attr{gox.A("class", gox.AttrVal(cls))})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestBareAttr(t *testing.T) {
	got := transpileExpr(t, `return <input disabled/>`)
	want := `return gox.El("input", []gox.Attr{gox.A("disabled", "disabled")})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestComponentWithChildren(t *testing.T) {
	got := transpileExpr(t, `return <Button label="Click" onClick={handleClick}>Text</Button>`)
	want := `return Button(ButtonProps{Label: "Click", OnClick: handleClick, Children: []gox.Node{gox.Text("Text")}})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestDottedComponent(t *testing.T) {
	got := transpileExpr(t, `return <ui.Spinner size={32}/>`)
	want := `return ui.Spinner(ui.SpinnerProps{Size: 32})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestLoopViaIIFEExpression(t *testing.T) {
	src := `return <ul>{ func() []gox.Node {
		var items []gox.Node
		for _, s := range list {
			items = append(items, <li>{s}</li>)
		}
		return items
	}() }</ul>`
	got := transpileExpr(t, src)
	if !strings.Contains(got, `gox.El("li", nil, gox.Child(s))`) {
		t.Errorf("expected nested <li> inside for-loop to be expanded, got: %s", got)
	}
	mustFormat(t, got)
}

func TestLessThanIsNotATag(t *testing.T) {
	got := transpileExpr(t, `if a < b { return x }`)
	want := `if a < b { return x }`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestLessThanAfterCloseParenIsNotATag(t *testing.T) {
	got := transpileExpr(t, `if f(a) < g(b) { return x }`)
	want := `if f(a) < g(b) { return x }`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestTagAfterReturnOnNewLineAfterCall(t *testing.T) {
	// ASI: foo() ends a statement, so a tag on the next line is a fresh
	// expression-statement, not "foo() < div".
	got := transpileExpr(t, "foo()\nx := <div></div>")
	want := "foo()\nx := gox.El(\"div\", nil)"
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestStringContainingAngleBracketIsUntouched(t *testing.T) {
	got := transpileExpr(t, `s := "a < b"`)
	want := `s := "a < b"`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestWhitespaceOnlyChildTextIsDropped(t *testing.T) {
	src := "return <div>\n  <span>a</span>\n  <span>b</span>\n</div>"
	got := transpileExpr(t, src)
	want := `return gox.El("div", nil, gox.El("span", nil, gox.Text("a")), gox.El("span", nil, gox.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestMismatchedCloseTagErrors(t *testing.T) {
	_, err := (&parser{file: "t.gox"}).transpileChunk([]rune(`return <div>hi</span>`))
	if err == nil {
		t.Fatal("expected an error for mismatched closing tag")
	}
}

func TestStructComponentWithChildren(t *testing.T) {
	got := transpileExpr(t, `return <*Counter count={5}>Text</*Counter>`)
	want := `return NewCounter(CounterProps{Count: 5, Children: []gox.Node{gox.Text("Text")}}).Render()`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestStructComponentSelfClosing(t *testing.T) {
	got := transpileExpr(t, `return <*Widget/>`)
	want := `return NewWidget(WidgetProps{}).Render()`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestDottedStructComponent(t *testing.T) {
	got := transpileExpr(t, `return <*ui.Spinner size={32}/>`)
	want := `return ui.NewSpinner(ui.SpinnerProps{Size: 32}).Render()`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestStructComponentMismatchedCloseTagErrors(t *testing.T) {
	// A struct component's close tag must repeat the leading '*'; a
	// plain </Foo> must not be able to close a <*Foo>, or vice versa.
	_, err := (&parser{file: "t.gox"}).transpileChunk([]rune(`return <*Foo>hi</Foo>`))
	if err == nil {
		t.Fatal("expected an error: </Foo> shouldn't close <*Foo>")
	}
	_, err = (&parser{file: "t.gox"}).transpileChunk([]rune(`return <Foo>hi</*Foo>`))
	if err == nil {
		t.Fatal("expected an error: </*Foo> shouldn't close <Foo>")
	}
}

func TestLowercaseStructTagErrors(t *testing.T) {
	_, err := (&parser{file: "t.gox"}).transpileChunk([]rune(`return <*div>hi</*div>`))
	if err == nil {
		t.Fatal("expected an error: <*div> isn't a valid struct component name")
	}
}

func TestLessThanStarIsNotATag(t *testing.T) {
	// "< *p" (less-than, then a pointer dereference) must stay untouched
	// when '<' can't legally start a new expression there.
	got := transpileExpr(t, `if x < *p { return y }`)
	want := `if x < *p { return y }`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
