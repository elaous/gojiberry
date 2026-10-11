package collab

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docfile"
)

// painter measures text as 8px per rune and draws nothing.
type painter struct{}

func (painter) SetGlobalAlpha(float64)      {}
func (painter) Save()                       {}
func (painter) Restore()                    {}
func (painter) Translate(x, y float64)      {}
func (painter) Rotate(float64)              {}
func (painter) ClipRect(x, y, w, h float64) {}
func (painter) DrawImage(canvas.ImageSource, float64, float64, float64, float64, float64, float64, float64, float64) {
}

func (painter) SetFillStyle(string)               {}
func (painter) SetStrokeStyle(string)             {}
func (painter) SetLineWidth(float64)              {}
func (painter) SetFont(string)                    {}
func (painter) SetTextAlign(string)               {}
func (painter) SetTextBaseline(string)            {}
func (painter) FillRect(x, y, w, h float64)       {}
func (painter) StrokeRect(x, y, w, h float64)     {}
func (painter) ClearRect(x, y, w, h float64)      {}
func (painter) BeginPath()                        {}
func (painter) MoveTo(x, y float64)               {}
func (painter) LineTo(x, y float64)               {}
func (painter) Stroke()                           {}
func (painter) FillText(string, float64, float64) {}
func (painter) MeasureTextWidth(t string) float64 { return 8 * float64(utf8.RuneCountInString(t)) }

// newFlow is an editable flow with the caret in its text.
func newFlow(items ...canvas.Item) *canvas.TextFlow {
	f := canvas.NewTextFlow("", 10, 30, 160, 200)
	f.Items = items
	f.SetPainter(painter{})
	f.SetMode(canvas.ModeEdit)
	f.MouseDown(10, 40)
	f.MouseUp(0, 0)
	return f
}

// normalized renders a flow's content so that equal documents compare equal
// whatever the fragmentation into runs.
func normalized(f *canvas.TextFlow) string {
	var items []canvas.Item
	for _, it := range f.ContentItems() {
		if ch, ok := it.(canvas.Chars); ok && len(items) > 0 {
			if last, ok := items[len(items)-1].(canvas.Chars); ok && last.Format == ch.Format {
				last.Text += ch.Text
				items[len(items)-1] = last
				continue
			}
		}
		items = append(items, it)
	}
	d := docfile.FromFlow("", &canvas.TextFlow{Items: items})
	d.Comments, d.Footnotes = docfile.FromComments(f.Comments), docfile.FromFootnotes(f.Footnotes)
	b, _ := json.Marshal(d)
	return string(b)
}

func text(f *canvas.TextFlow) string { return strings.ReplaceAll(f.EditText(), "￼", "#") }

func exchange(a, b *Bridge) {
	ao, bo := a.Drain(), b.Drain()
	a.Apply(bo...)
	b.Apply(ao...)
}

// pair returns two bridged flows holding the same document.
func pair(t *testing.T, items ...canvas.Item) (*Bridge, *Bridge) {
	t.Helper()
	a, err := New("a", newFlow(items...))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Join("b", newFlow(canvas.Chars{Text: "local junk that the snapshot replaces"}))
	if err != nil {
		t.Fatal(err)
	}
	if b.Ready() {
		t.Fatal("a joined bridge waits for the document")
	}
	b.Apply(a.Snapshot()...)
	if !b.Ready() {
		t.Fatal("ready after the first operations")
	}
	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatalf("the joiner differs:\n a %s\n b %s", normalized(a.Flow), normalized(b.Flow))
	}
	return a, b
}

func click(f *canvas.TextFlow, offset int) {
	f.MouseDown(10+8*float64(offset), 40)
	f.MouseUp(0, 0)
}

func TestJoinReceivesFormattedContent(t *testing.T) {
	a, b := pair(t,
		canvas.ParaFormat{Align: canvas.AlignCenter, List: canvas.ListStyle{Kind: canvas.ListBullet}},
		canvas.Chars{Text: "bold ", Format: canvas.Format{FontWeight: 700, Color: "#123"}},
		canvas.Chars{Text: "link", Format: canvas.Format{Link: "https://gojiberry.io"}},
		canvas.Chars{Text: "\nsecond"},
		canvas.Break{Kind: canvas.BreakPage},
		canvas.ColumnConfig{Count: 2, Gap: 8},
		canvas.Inline{Object: &canvas.Rule{Color: "#abc"}, Block: true}, canvas.Chars{Text: "\n"},
		canvas.Inline{Object: &canvas.FootnoteRef{ID: "n1"}},
	)
	a.Flow.Footnotes = canvas.NewFootnoteSet(canvas.Footnote{ID: "n1", Text: "note"})
	a.Sync()
	b.Apply(a.Drain()...)
	got := b.Flow.ContentItems()
	if p, ok := got[0].(canvas.ParaFormat); !ok || p.Align != canvas.AlignCenter || p.List.Kind != canvas.ListBullet {
		t.Fatalf("paragraph format: %#v", got[0])
	}
	if ch := got[1].(canvas.Chars); ch.Text != "bold " || ch.Format.FontWeight != 700 || ch.Format.Color != "#123" {
		t.Fatalf("%#v", ch)
	}
	if n, ok := b.Flow.Footnotes.Get("n1"); !ok || n.Text != "note" {
		t.Fatal("footnote text travels")
	}
	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatal("differ")
	}
}

func TestTypingAndFormattingSync(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "hello world"})
	click(a.Flow, 5)
	a.Flow.InsertText(",")
	b.Apply(a.Drain()...)
	if text(b.Flow) != "hello, world" {
		t.Fatalf("typing syncs: %q", text(b.Flow))
	}
	// B selects "world" and makes it bold; A sees it.
	b.Flow.SetCursors([]canvas.Cursor{{Anchor: 7, Caret: 12}})
	b.Flow.ApplyFormat(func(f *canvas.Format) { f.FontWeight = 700 })
	a.Apply(b.Drain()...)
	found := false
	for _, it := range a.Flow.ContentItems() {
		if ch, ok := it.(canvas.Chars); ok && ch.Text == "world" && ch.Format.FontWeight == 700 {
			found = true
		}
	}
	if !found || normalized(a.Flow) != normalized(b.Flow) {
		t.Fatalf("formatting syncs: %s", normalized(a.Flow))
	}
	// And backspacing.
	click(a.Flow, 6)
	a.Flow.HandleKey("Backspace", false)
	b.Apply(a.Drain()...)
	if text(b.Flow) != "hello world" || text(a.Flow) != "hello world" {
		t.Fatalf("deleting syncs: %q %q", text(a.Flow), text(b.Flow))
	}
}

func TestConcurrentEditsConverge(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "one two three"})
	click(a.Flow, 0)
	a.Flow.InsertText(">")
	click(b.Flow, 13)
	b.Flow.InsertText("!")
	b.Flow.SetCursors([]canvas.Cursor{{Anchor: 4, Caret: 7}}) // "two"
	b.Flow.ApplyFormat(func(f *canvas.Format) { f.Underline = true })
	exchange(a, b)
	if normalized(a.Flow) != normalized(b.Flow) || text(a.Flow) != ">one two three!" {
		t.Fatalf("a=%q b=%q", text(a.Flow), text(b.Flow))
	}
}

func TestCursorsStayBesideTheirCharacters(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "hello world"})
	click(b.Flow, 8) // between "wo" and "rld"
	b.Flow.SetCursors([]canvas.Cursor{{Anchor: 6, Caret: 11}, canvas.At(2)})
	_ = b.Drain()
	click(a.Flow, 0)
	a.Flow.InsertText(">>>")
	b.Apply(a.Drain()...)
	cs := b.Flow.Cursors()
	if len(cs) != 2 || cs[0] != (canvas.Cursor{Anchor: 9, Caret: 14}) || cs[1] != canvas.At(5) {
		t.Fatalf("both cursors moved with their text: %v", cs)
	}
	// A deleted character's neighbour keeps the caret near where it was.
	click(a.Flow, 8)
	a.Flow.HandleKey("Backspace", false)
	b.Apply(a.Drain()...)
	if got := b.Flow.Cursors()[0]; got.Anchor != 8 || got.Caret != 13 {
		t.Fatalf("%v", got)
	}
	if !b.Flow.Editing() {
		t.Fatal("still editing after a remote change")
	}
}

func TestCommentsRepliesAndStylesTravel(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "commented text"})
	a.Flow.Comments = canvas.NewCommentSet()
	a.Flow.Author = "ana"
	a.Flow.SetCursors([]canvas.Cursor{{Anchor: 0, Caret: 9}})
	c := a.Flow.Comments.Add(canvas.Comment{Author: "ana", Text: "**why**?"})
	a.Flow.ApplyFormat(func(f *canvas.Format) { f.Comment = c.ID })
	a.Flow.Styles = canvas.DefaultStyles()[:2]
	a.Sync()
	b.Apply(a.Drain()...)
	got, ok := b.Flow.Comments.Get(c.ID)
	if !ok || got.Text != "**why**?" || got.Author != "ana" {
		t.Fatalf("%+v", got)
	}
	if len(b.Flow.Styles) != 2 {
		t.Fatalf("styles: %d", len(b.Flow.Styles))
	}
	// Two replies written at the same time both survive.
	a.Flow.Comments.Update(c.ID, func(c *canvas.Comment) {
		c.Replies = append(c.Replies, canvas.Reply{Author: "ana", Text: "one", Created: time0(1)})
	})
	b.Flow.Comments.Update(c.ID, func(c *canvas.Comment) {
		c.Replies = append(c.Replies, canvas.Reply{Author: "bob", Text: "two", Created: time0(2)})
	})
	a.Sync()
	b.Sync()
	exchange(a, b)
	for _, f := range []*canvas.TextFlow{a.Flow, b.Flow} {
		cm, _ := f.Comments.Get(c.ID)
		if len(cm.Replies) != 2 || cm.Replies[0].Text != "one" || cm.Replies[1].Text != "two" {
			t.Fatalf("both replies survive, in time order: %+v", cm.Replies)
		}
	}
	// Resolving and deleting travel; the set stays the same object.
	set := b.Flow.Comments
	a.Flow.Comments.Update(c.ID, func(c *canvas.Comment) { c.Resolved = true })
	a.Sync()
	b.Apply(a.Drain()...)
	if cm, _ := b.Flow.Comments.Get(c.ID); !cm.Resolved || b.Flow.Comments != set {
		t.Fatal("resolved, in place")
	}
	a.Flow.Comments.Delete(c.ID)
	a.Sync()
	b.Apply(a.Drain()...)
	if b.Flow.Comments.Len() != 0 {
		t.Fatal("deleted")
	}
}

func TestUnsupportedContentIsRefused(t *testing.T) {
	odd := newFlow(canvas.Chars{Text: "x"})
	odd.Items = append(odd.Items, canvas.Inline{Object: oddObject{}})
	if _, err := New("a", odd); err == nil || !strings.Contains(err.Error(), "cannot share") {
		t.Fatalf("%v", err)
	}
	if _, err := Join("a", odd); err == nil {
		t.Fatal("join checks too")
	}
	reserved := newFlow(canvas.Chars{Text: "bad  char"})
	if _, err := New("a", reserved); err == nil {
		t.Fatal("reserved characters")
	}
	// A later unshareable edit is reported and does not break sharing.
	a, _ := pair(t, canvas.Chars{Text: "fine"})
	a.Flow.Items = append(a.Flow.ContentItems(), canvas.Inline{Object: oddObject{}})
	a.Flow.Touch()
	if a.Err() == nil {
		t.Fatal("Err reports it")
	}
	a.Flow.Items = a.Flow.ContentItems()[:1]
	a.Flow.Touch()
	if a.Err() != nil {
		t.Fatalf("cleared: %v", a.Err())
	}
}

type oddObject struct{}

func (oddObject) Measure(canvas.Painter, float64, float64) (float64, float64) { return 1, 1 }
func (oddObject) DrawAt(canvas.Painter, float64, float64)                     {}

func TestOnChangeIsChained(t *testing.T) {
	f := newFlow(canvas.Chars{Text: "x"})
	n := 0
	f.OnChange = func(*canvas.TextFlow) { n++ }
	a, _ := New("a", f)
	click(f, 1)
	f.InsertText("y")
	if n != 1 || len(a.Drain()) == 0 {
		t.Fatalf("the earlier OnChange still runs and the edit is shared: %d", n)
	}
}

func TestDiffRunes(t *testing.T) {
	apply := func(old string, es []edit) string {
		rs := []rune(old)
		for i := len(es) - 1; i >= 0; i-- {
			e := es[i]
			rs = append(rs[:e.at], append(append([]rune{}, e.ins...), rs[e.at+e.del:]...)...)
		}
		return string(rs)
	}
	rng := rand.New(rand.NewSource(1))
	letters := []rune("abc ")
	rand10 := func(n int) string {
		r := make([]rune, rng.Intn(n))
		for i := range r {
			r[i] = letters[rng.Intn(len(letters))]
		}
		return string(r)
	}
	for i := 0; i < 500; i++ {
		old, cur := rand10(30), rand10(30)
		if got := apply(old, diffRunes([]rune(old), []rune(cur))); got != cur {
			t.Fatalf("%q -> %q gave %q", old, cur, got)
		}
	}
	if diffRunes([]rune("same"), []rune("same")) != nil {
		t.Fatal("no edits for equal text")
	}
	// Two far-apart edits stay two edits.
	es := diffRunes([]rune("aXbbbbbbbbbbbbbbbbbbbbYc"), []rune("abbbbbbbbbbbbbbbbbbbbc"))
	if len(es) != 2 {
		t.Fatalf("%+v", es)
	}
	// Huge middles fall back to one replacement.
	big1, big2 := []rune(strings.Repeat("a", 3000)), []rune(strings.Repeat("b", 3000))
	if es := diffRunes(big1, big2); len(es) != 1 || es[0].del != 3000 {
		t.Fatalf("%d edits", len(es))
	}
}

func TestAtomsRoundTripFormats(t *testing.T) {
	f := canvas.Format{FontFamily: "Lato", FontSize: 12.5, Scale: 1.6, FontWeight: 700, FontStyle: "italic", Color: "#fff",
		Underline: true, Strike: true, Superscript: true, Background: "yellow", Case: 2, Style: "Emphasis", Link: "u", Comment: "c1", Bookmark: "b", Index: "t"}
	if got := attrsFormat(formatAttrs(f)); got != f {
		t.Fatalf("%+v\n%+v", got, f)
	}
	if len(formatAttrs(canvas.Format{})) != 0 {
		t.Fatal("a plain format has no attributes")
	}
	for k := range formatAttrs(f) {
		_ = growsAtEnd(k)
	}
	if growsAtEnd("link") || growsAtEnd("comment") || !growsAtEnd("fontWeight") {
		t.Fatal("grow rules")
	}
	// A control atom without a payload is dropped; a plain object rune is text.
	items := itemsFromAtoms([]atom{{r: ctlPara, attrs: map[string]string{}}, {r: objRune, attrs: map[string]string{}}, {r: 'x', attrs: map[string]string{}}})
	if len(items) != 1 || items[0].(canvas.Chars).Text != "￼x" {
		t.Fatalf("%#v", items)
	}
	bad := itemsFromAtoms([]atom{{r: ctlBreak, attrs: map[string]string{ctlKey: "{not json"}}})
	if len(bad) != 0 {
		t.Fatal("unreadable payloads are skipped")
	}
	if streamCount([]atom{{r: ctlPara}, {r: 'a'}, {r: objRune}, {r: ctlCols}}) != 2 || atomPos([]atom{{r: ctlPara}, {r: 'a'}, {r: ctlCols}, {r: 'b'}}, 1) != 2 || atomPos(nil, 3) != 0 {
		t.Fatal("stream positions")
	}
}

// Two flows edited at random, synchronised at random moments, end up with
// the same document -- and the replica always agrees with its own flow.
func TestRandomEditingConverges(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		a, err := New("a", newFlow(canvas.Chars{Text: "the quick brown fox jumps over the lazy dog"}))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Join("b", newFlow())
		b.Apply(a.Snapshot()...)
		bridges := []*Bridge{a, b}
		var inflight [][]crdt.Op
		var dest []int
		step := func(i int) {
			f := bridges[i].Flow
			n := len([]rune(f.EditText()))
			pos := func() int { return rng.Intn(n + 1) }
			switch rng.Intn(6) {
			case 0, 1:
				f.SetCursors([]canvas.Cursor{canvas.At(pos())})
				f.InsertText(string(rune('a' + rng.Intn(26))))
			case 2:
				if n > 0 {
					p := 1 + rng.Intn(n)
					f.SetCursors([]canvas.Cursor{canvas.At(p)})
					f.HandleKey("Backspace", false)
				}
			case 3:
				if n > 1 {
					from := rng.Intn(n - 1)
					f.SetCursors([]canvas.Cursor{{Anchor: from, Caret: from + 1 + rng.Intn(n-from-1)}})
					switch rng.Intn(3) {
					case 0:
						f.ApplyFormat(func(fm *canvas.Format) { fm.FontWeight = 700 - fm.FontWeight })
					case 1:
						f.ApplyFormat(func(fm *canvas.Format) { fm.Color = []string{"", "#111", "#222"}[rng.Intn(3)] })
					default:
						f.ApplyFormat(func(fm *canvas.Format) { fm.Link = []string{"", "u1"}[rng.Intn(2)] })
					}
				}
			case 4:
				f.SetCursors([]canvas.Cursor{canvas.At(pos())})
				f.ApplyParagraph(func(p *canvas.ParaFormat) { p.Align = canvas.Align(rng.Intn(3)) })
			default:
				f.SetCursors([]canvas.Cursor{canvas.At(pos())})
				f.InsertText("\n")
			}
			if ops := bridges[i].Drain(); len(ops) > 0 {
				inflight, dest = append(inflight, ops), append(dest, 1-i)
			}
			if bridges[i].Err() != nil {
				t.Fatalf("trial %d: %v", trial, bridges[i].Err())
			}
		}
		for s := 0; s < 60; s++ {
			if rng.Intn(3) > 0 || len(inflight) == 0 {
				step(rng.Intn(2))
				continue
			}
			k := rng.Intn(len(inflight))
			bridges[dest[k]].Apply(inflight[k]...)
			if rng.Intn(4) > 0 {
				inflight, dest = append(inflight[:k], inflight[k+1:]...), append(dest[:k], dest[k+1:]...)
			}
		}
		for k := range inflight {
			bridges[dest[k]].Apply(inflight[k]...)
		}
		if normalized(a.Flow) != normalized(b.Flow) {
			t.Fatalf("trial %d diverged:\n a %s\n b %s", trial, normalized(a.Flow), normalized(b.Flow))
		}
		// The replica agrees with its flow.
		for i, br := range bridges {
			want, _ := atomsFromItems(br.Flow.ContentItems())
			have := br.read()
			if len(want) != len(have) {
				t.Fatalf("trial %d bridge %d: %d atoms in the flow, %d in the replica", trial, i, len(want), len(have))
			}
			for k := range want {
				if want[k].r != have[k].r || !sameAttrs(want[k].attrs, have[k].attrs) {
					t.Fatalf("trial %d bridge %d: atom %d is %q %v in the flow, %q %v in the replica", trial, i, k, want[k].r, want[k].attrs, have[k].r, have[k].attrs)
				}
			}
		}
	}
}

func time0(n int64) time.Time { return time.Unix(0, n) }

func TestSlideEditFollowsTheCaret(t *testing.T) {
	old, cur := []rune("hello"), []rune("helo")
	// Deleting one of the two l's: the diff picks the second; the caret says the first.
	e := diffRunes(old, cur)[0]
	if e.at != 3 {
		t.Fatalf("the diff alone deletes the second l: %+v", e)
	}
	if got := slideEdit(old, cur, e, 2); got.at != 2 || got.del != 1 {
		t.Fatalf("%+v", got)
	}
	if got := slideEdit(old, cur, e, 0); got.at != e.at {
		t.Fatalf("a caret outside the run changes nothing: %+v", got)
	}
	// Typing the second of two equal letters.
	old2, cur2 := []rune("helo"), []rune("hello")
	e2 := diffRunes(old2, cur2)[0]
	got := slideEdit(old2, cur2, e2, 4) // caret after the typed l (offset 4 in "hell|o")
	if got.at != 3 || string(got.ins) != "l" {
		t.Fatalf("%+v", got)
	}
	if got := slideEdit(old2, cur2, e2, 99); got.at != e2.at {
		t.Fatalf("%+v", got)
	}
	// A replacement is left alone.
	rep := edit{at: 1, del: 1, ins: []rune("x")}
	if got := slideEdit(old, cur, rep, 1); got.at != 1 || string(got.ins) != "x" {
		t.Fatalf("%+v", got)
	}
}

// Two clients that open an empty document together and start from the same
// content must end with one copy of it, not two.
func TestSimultaneousSeedingMakesOneCopy(t *testing.T) {
	start := func() *canvas.TextFlow {
		return newFlow(canvas.Chars{Text: "Starting text"}, canvas.Break{}, canvas.Chars{Text: "second line"})
	}
	a, _ := Join("a", start())
	b, _ := Join("b", start())
	var fromA, fromB []crdt.Op
	a.Outgoing = func(ops []crdt.Op) { fromA = append(fromA, ops...) }
	b.Outgoing = func(ops []crdt.Op) { fromB = append(fromB, ops...) }
	a.Seed()
	b.Seed()
	a.Apply(fromB...)
	b.Apply(fromA...)
	want := text(start())
	if text(a.Flow) != want || text(b.Flow) != want {
		t.Fatalf("a=%q b=%q, want one copy %q", text(a.Flow), text(b.Flow), want)
	}
}

type urlSource struct{ url string }

func (urlSource) Size() (w, h float64) { return 40, 30 }
func (u urlSource) URL() string        { return u.url }

// An inline image is shared by where it came from; the other side shows it as
// a placeholder (or the real image if docfile.ImageLoader can supply it).
func TestInlineImageIsShared(t *testing.T) {
	img := canvas.NewImage(urlSource{"https://img.example/cat.png"}, 0, 0)
	img.Placement, img.W, img.H = canvas.PlaceInline, 80, 60
	a, b := pair(t, canvas.Chars{Text: "see "}, canvas.Inline{Object: img}, canvas.Chars{Text: " here"})
	if text(a.Flow) != "see # here" || text(b.Flow) != "see # here" {
		t.Fatalf("a=%q b=%q", text(a.Flow), text(b.Flow))
	}
	items := docfile.FromItems(b.Flow.ContentItems())
	var found *docfile.ImageItem
	for _, it := range items {
		if it.Image != nil {
			found = it.Image
		}
	}
	if found == nil || found.URL != "https://img.example/cat.png" || found.Width != 80 {
		t.Fatalf("the image did not arrive: %+v", items)
	}
	// An image with no URL cannot be shared: refuse it, as before.
	bare := canvas.NewImage(fakeSize{}, 0, 0)
	bare.Placement = canvas.PlaceInline
	if _, err := New("c", newFlow(canvas.Inline{Object: bare})); err == nil {
		t.Fatal("an image that cannot be saved cannot be shared")
	}
}

type fakeSize struct{}

func (fakeSize) Size() (w, h float64) { return 10, 10 }

// Different starting content from two clients is kept whole, not interleaved.
func TestSimultaneousSeedingOfDifferentContentKeepsBoth(t *testing.T) {
	a, _ := Join("a", newFlow(canvas.Chars{Text: "AAAAAAAAAAAA"}))
	b, _ := Join("b", newFlow(canvas.Chars{Text: "bbbb"}))
	var fromA, fromB []crdt.Op
	a.Outgoing = func(ops []crdt.Op) { fromA = append(fromA, ops...) }
	b.Outgoing = func(ops []crdt.Op) { fromB = append(fromB, ops...) }
	a.Seed()
	b.Seed()
	a.Apply(fromB...)
	b.Apply(fromA...)
	if text(a.Flow) != text(b.Flow) {
		t.Fatalf("not converged: %q vs %q", text(a.Flow), text(b.Flow))
	}
	got := text(a.Flow)
	if got != "AAAAAAAAAAAAbbbb" && got != "bbbbAAAAAAAAAAAA" {
		t.Fatalf("both contents must survive whole, got %q", got)
	}
}

// Two people suggesting changes at the same time, in different places and in the same place: the copies
// converge, every proposal is there with its own author, and accepting or rejecting one on one side
// settles it on the other.
func TestSuggestionsFromTwoPeopleConverge(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "the brown fox runs fast"})
	a.Flow.Author, b.Flow.Author = "ana", "bo"
	a.Flow.Suggesting, b.Flow.Suggesting = true, true

	// ana replaces "brown" with "red"; bo, meanwhile, strikes "fast" and, in the same place as ana, types "big "
	a.Flow.SetCursors([]canvas.Cursor{{Anchor: 4, Caret: 9}})
	a.Flow.InsertText("red")
	b.Flow.SetCursors([]canvas.Cursor{{Anchor: 19, Caret: 23}})
	b.Flow.InsertText("")
	b.Flow.HandleKey("Delete", false)
	click(b.Flow, 4)
	b.Flow.InsertText("big ")
	exchange(a, b)

	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatalf("the copies differ:\n a %s\n b %s", normalized(a.Flow), normalized(b.Flow))
	}
	byAuthor := map[string]canvas.Proposal{}
	for _, p := range a.Flow.Proposals() {
		byAuthor[p.Author+":"+p.Inserted+"/"+p.Deleted] = p
	}
	if _, ok := byAuthor["ana:red/brown"]; !ok {
		t.Fatalf("ana's replacement is one proposal: %+v", a.Flow.Proposals())
	}
	if _, ok := byAuthor["bo:big /"]; !ok {
		t.Fatalf("bo's addition: %+v", a.Flow.Proposals())
	}
	var struck canvas.Proposal
	for _, p := range b.Flow.Proposals() {
		if p.Author == "bo" && p.Deleted != "" {
			struck = p
		}
	}
	if struck.ID == "" {
		t.Fatalf("bo's removal: %+v", b.Flow.Proposals())
	}
	if len(a.Flow.Proposals()) != len(b.Flow.Proposals()) {
		t.Fatalf("both see the same number: %d vs %d", len(a.Flow.Proposals()), len(b.Flow.Proposals()))
	}

	// ana accepts bo's removal; bo rejects ana's replacement: both settle on both sides
	a.Flow.Suggesting, b.Flow.Suggesting = false, false
	if !a.Flow.AcceptProposal(struck.ID) {
		t.Fatal("accept")
	}
	ana := byAuthor["ana:red/brown"]
	if !b.Flow.RejectProposal(ana.ID) {
		t.Fatal("reject")
	}
	exchange(a, b)
	exchange(a, b)
	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatalf("after settling:\n a %s\n b %s", normalized(a.Flow), normalized(b.Flow))
	}
	got := text(a.Flow)
	if strings.Contains(got, "fast") || !strings.Contains(got, "brown") || strings.Contains(got, "red") {
		t.Fatalf("accepted removal gone, rejected replacement undone: %q", got)
	}
	ps := a.Flow.Proposals()
	if len(ps) != 1 || ps[0].Author != "bo" || ps[0].Inserted != "big " {
		t.Fatalf("only bo's addition is left: %+v", ps)
	}
}
