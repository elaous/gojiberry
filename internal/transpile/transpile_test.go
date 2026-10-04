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

func transpileExpr(t *testing.T, xango string) string {
	t.Helper()
	got, err := (&parser{file: "t.xg"}).transpileChunk([]rune(xango))
	if err != nil {
		t.Fatalf("transpile error: %v\ninput: %s", err, xango)
	}
	return got
}

func TestSimpleElement(t *testing.T) {
	got := transpileExpr(t, `x := <div class="greeting">Hello</div>`)
	want := `x := xango.El("div", []xango.Attr{xango.A("class", "greeting")}, xango.Text("Hello"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestSelfClosingVoidElement(t *testing.T) {
	got := transpileExpr(t, `return <br/>`)
	want := `return xango.El("br", nil)`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestTextInterpolation(t *testing.T) {
	got := transpileExpr(t, `return <p>Hello, {name}!</p>`)
	want := `return xango.El("p", nil, xango.Text("Hello, "), xango.Child(name), xango.Text("!"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestNestedElements(t *testing.T) {
	got := transpileExpr(t, `return <div><span>a</span><span>b</span></div>`)
	want := `return xango.El("div", nil, xango.El("span", nil, xango.Text("a")), xango.El("span", nil, xango.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestFragment(t *testing.T) {
	got := transpileExpr(t, `return <><li>a</li><li>b</li></>`)
	want := `return xango.Frag(xango.El("li", nil, xango.Text("a")), xango.El("li", nil, xango.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestKeyedFragmentWithLiteralKey(t *testing.T) {
	got := transpileExpr(t, `return <Fragment key="row-1"><td>a</td><td>b</td></Fragment>`)
	want := `return xango.Frag(xango.El("td", nil, xango.Text("a")), xango.El("td", nil, xango.Text("b"))).WithKey("row-1")`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestKeyedFragmentWithExprKey(t *testing.T) {
	got := transpileExpr(t, `return <Fragment key={item.ID}><td>a</td></Fragment>`)
	want := `return xango.Frag(xango.El("td", nil, xango.Text("a"))).WithKey(xango.AttrVal(item.ID))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestKeyedFragmentSelfClosing(t *testing.T) {
	got := transpileExpr(t, `return <Fragment key="empty"/>`)
	want := `return xango.Frag().WithKey("empty")`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestKeyedFragmentRequiresExactlyOneKeyAttr(t *testing.T) {
	if _, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <Fragment>hi</Fragment>`)); err == nil {
		t.Fatal("expected an error for <Fragment> with no key attribute at all")
	}
	if _, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <Fragment key="a" other="b">hi</Fragment>`)); err == nil {
		t.Fatal("expected an error for <Fragment> with an extra attribute besides key")
	}
	if _, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <Fragment key>hi</Fragment>`)); err == nil {
		t.Fatal("expected an error for a bare key attribute with no value")
	}
}

func TestKeyedFragmentMismatchedCloseTagErrors(t *testing.T) {
	_, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <Fragment key="a">hi</>`))
	if err == nil {
		t.Fatal("expected an error: <Fragment key=...> must close with </Fragment>, not the bare </> shorthand")
	}
}

func TestEventAttr(t *testing.T) {
	got := transpileExpr(t, `return <button onClick={handleClick}>Go</button>`)
	want := `return xango.El("button", []xango.Attr{xango.On("click", handleClick)}, xango.Text("Go"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestDynamicAttrValue(t *testing.T) {
	got := transpileExpr(t, `return <div class={cls}></div>`)
	want := `return xango.El("div", []xango.Attr{xango.A("class", xango.AttrVal(cls))})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestBareAttr(t *testing.T) {
	got := transpileExpr(t, `return <input disabled/>`)
	want := `return xango.El("input", []xango.Attr{xango.A("disabled", "disabled")})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestComponentWithChildren(t *testing.T) {
	got := transpileExpr(t, `return <Button label="Click" onClick={handleClick}>Text</Button>`)
	want := `return Button(ButtonProps{Label: "Click", OnClick: handleClick, Children: []xango.Node{xango.Text("Text")}})`
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
	src := `return <ul>{ func() []xango.Node {
		var items []xango.Node
		for _, s := range list {
			items = append(items, <li>{s}</li>)
		}
		return items
	}() }</ul>`
	got := transpileExpr(t, src)
	if !strings.Contains(got, `xango.El("li", nil, xango.Child(s))`) {
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
	want := "foo()\nx := xango.El(\"div\", nil)"
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
	want := `return xango.El("div", nil, xango.El("span", nil, xango.Text("a")), xango.El("span", nil, xango.Text("b")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

// TestApostropheInChildTextDoesNotSwallowFollowingTag guards against a
// real bug parseChildren once had: it called skipStringLiteral (meant
// for scanning actual Go expressions) on plain JSX text content, so an
// apostrophe in ordinary prose ("document's") was treated as opening a
// string/rune literal and the scanner skipped everything -- including
// real tags -- up to the next quote character (e.g. the apostrophe in a
// following paragraph's "Couldn't"), silently merging both paragraphs
// and the tag between them into one xango.Text call.
func TestApostropheInChildTextDoesNotSwallowFollowingTag(t *testing.T) {
	src := `return <div><p>Only this document's own.</p><p>Couldn't load.</p></div>`
	got := transpileExpr(t, src)
	want := `return xango.El("div", nil, xango.El("p", nil, xango.Text("Only this document's own.")), xango.El("p", nil, xango.Text("Couldn't load.")))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestMismatchedCloseTagErrors(t *testing.T) {
	_, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <div>hi</span>`))
	if err == nil {
		t.Fatal("expected an error for mismatched closing tag")
	}
}

// TestMultiWordEventAttrUsesRealDOMEventName guards against a real bug
// eventNameFor once had: lowercasing only the first letter of a
// multi-word event attribute (onKeyDown -> "keyDown") produces a name
// no real DOM event ever fires under -- addEventListener("keyDown",
// ...) silently never calls the handler, since the real event name is
// "keydown", fully lowercase. onClick/onInput never caught this since
// their event names have no second capital letter to get wrong.
func TestMultiWordEventAttrUsesRealDOMEventName(t *testing.T) {
	got := transpileExpr(t, `return <div onKeyDown={handleKey}>x</div>`)
	want := `return xango.El("div", []xango.Attr{xango.On("keydown", handleKey)}, xango.Text("x"))`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestUnknownTagErrors(t *testing.T) {
	_, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <dvi>hi</dvi>`))
	if err == nil {
		t.Fatal("expected an error for a typo'd tag name (<dvi> for <div>)")
	}
	if !strings.Contains(err.Error(), "dvi") {
		t.Errorf("expected error to mention the bad tag name, got: %v", err)
	}
}

func TestCustomElementWithHyphenIsAlwaysAllowed(t *testing.T) {
	got := transpileExpr(t, `return <my-widget foo="bar"></my-widget>`)
	want := `return xango.El("my-widget", []xango.Attr{xango.A("foo", "bar")})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestUnknownAttrErrors(t *testing.T) {
	_, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <div calss="x">hi</div>`))
	if err == nil {
		t.Fatal("expected an error for a typo'd attribute name (calss for class)")
	}
	if !strings.Contains(err.Error(), "calss") {
		t.Errorf("expected error to mention the bad attribute name, got: %v", err)
	}
}

func TestDataAndAriaAttrsAreAlwaysAllowed(t *testing.T) {
	got := transpileExpr(t, `return <div data-testid="x" aria-label="y"></div>`)
	want := `return xango.El("div", []xango.Attr{xango.A("data-testid", "x"), xango.A("aria-label", "y")})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

// TestComponentAttrsAreNotValidatedAsHTML guards against validating a
// Component's attributes (its Props struct's own field names, which
// can be anything the component declares) as if they were HTML
// attributes -- "label" isn't a real HTML attribute, but <Button
// label="Save"/> is completely ordinary, valid xango.
func TestComponentAttrsAreNotValidatedAsHTML(t *testing.T) {
	got := transpileExpr(t, `return <Button label="Save" onClick={handleSave}/>`)
	want := `return Button(ButtonProps{Label: "Save", OnClick: handleSave})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestFunctionPropWithNestedMarkup(t *testing.T) {
	got := transpileExpr(t, `return <MyApp items={items} render={func(item Item) xango.Node {
	return <li class="x">{item.Name}</li>
}} />`)
	want := `return MyApp(MyAppProps{Items: items, Render: func(item Item) xango.Node {
	return xango.El("li", []xango.Attr{xango.A("class", "x")}, xango.Child(item.Name))
}})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestFunctionPropAlongsideChildren(t *testing.T) {
	got := transpileExpr(t, `return <MyApp render={func(i Item) xango.Node { return <b>{i.Name}</b> }}><i>child</i></MyApp>`)
	want := `return MyApp(MyAppProps{Render: func(i Item) xango.Node { return xango.El("b", nil, xango.Child(i.Name)) }, Children: []xango.Node{xango.El("i", nil, xango.Text("child"))}})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestGenericComponent(t *testing.T) {
	got := transpileExpr(t, `return <List[Item] items={items} render={func(it Item) xango.Node { return <li>{it.Name}</li> }} />`)
	want := `return List[Item](ListProps[Item]{Items: items, Render: func(it Item) xango.Node { return xango.El("li", nil, xango.Child(it.Name)) }})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestGenericComponentWithChildrenAndNestedTypeArgs(t *testing.T) {
	got := transpileExpr(t, `return <Table[string, map[string][]int]>text</Table>`)
	want := `return Table[string, map[string][]int](TableProps[string, map[string][]int]{Children: []xango.Node{xango.Text("text")}})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	mustFormat(t, got)
}

func TestGenericDottedComponent(t *testing.T) {
	got := transpileExpr(t, `return <components.List[Item] items={xs} />`)
	want := `return components.List[Item](components.ListProps[Item]{Items: xs})`
	if strings.TrimSpace(got) != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestGenericTypeArgsErrors(t *testing.T) {
	for _, src := range []string{`return <List[Item items={x} />`, `return <List[<b>] />`} {
		if _, err := (&parser{file: "t.xg"}).transpileChunk([]rune(src)); err == nil {
			t.Errorf("%q: expected an error", src)
		}
	}
}

func TestIndexingAfterAComponentNameStillWorks(t *testing.T) {
	// An HTML element followed by '[' is not generic syntax (only
	// Components take type arguments).
	if _, err := (&parser{file: "t.xg"}).transpileChunk([]rune(`return <div[0]></div>`)); err == nil {
		t.Error("<div[0]> should not parse as a generic tag")
	}
}
