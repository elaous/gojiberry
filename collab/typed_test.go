package collab

import (
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab/hub"
)

// Other people's typing shows in their colour for a while.
func TestTextOthersTypedIsInTheirColourForAMoment(t *testing.T) {
	r := newRig(t, hub.Config{})
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "hello world"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	b := r.connect("b", "", newFlow())
	waitFor(t, "b has the text", func() bool { return text(b.Bridge.Flow) == "hello world" }, a, b)
	if len(b.Bridge.Flow.TypedRanges) != 0 {
		t.Fatal("the document that arrived is not somebody typing")
	}
	clock := time.Now()
	b.now = func() time.Time { return clock }
	a.Name, a.Color = "Ana", "#f00"
	a.PublishCursor()
	waitFor(t, "b to know Ana", func() bool { return len(b.Peers) == 1 }, a, b)

	click(a.Bridge.Flow, 5)
	a.Bridge.Flow.InsertText(", dear")
	waitFor(t, "b to see the text", func() bool { return text(b.Bridge.Flow) == "hello, dear world" }, a, b)
	b.RefreshPeers()
	got := b.Bridge.Flow.TypedRanges
	if len(got) != 1 || got[0].From != 5 || got[0].To != 11 || got[0].Name != "Ana" || got[0].Color != "#f00" {
		t.Fatalf("the typed ranges: %+v", got)
	}
	click(b.Bridge.Flow, 0)
	b.Bridge.Flow.InsertText(">>")
	b.RefreshPeers()
	if got := b.Bridge.Flow.TypedRanges; len(got) != 1 || got[0].From != 7 || got[0].To != 13 {
		t.Fatalf("after b typed in front: %+v", got)
	}
	// Later, it goes back to normal.
	clock = clock.Add(DefaultTypedFor + time.Second)
	b.RefreshPeers()
	if len(b.Bridge.Flow.TypedRanges) != 0 {
		t.Fatalf("still tinted: %+v", b.Bridge.Flow.TypedRanges)
	}
	if len(a.Bridge.Flow.TypedRanges) != 0 {
		t.Fatal("a's own text")
	}
	// Turned off, nothing is noted.
	b.TypedFor = -1
	a.Bridge.Flow.InsertText("!")
	waitFor(t, "the !", func() bool { return len(text(b.Bridge.Flow)) > 17 }, a, b)
	b.RefreshPeers()
	if len(b.Bridge.Flow.TypedRanges) != 0 {
		t.Fatal("colours are off")
	}
}

// An object changed by someone else is marked.
func TestAnObjectOthersChangedIsInTheirColourForAMoment(t *testing.T) {
	r := newRig(t, hub.Config{})
	img := canvas.NewImage(urlSource{"https://img.example/cat.png"}, 0, 0)
	img.Placement, img.W, img.H = canvas.PlaceInline, 80, 60
	a := r.connect("a", "", newFlow(canvas.Chars{Text: "see "}, canvas.Inline{Object: img}, canvas.Chars{Text: " here"}))
	waitFor(t, "a ready", func() bool { return a.Bridge.Ready() }, a)
	b := r.connect("b", "", newFlow())
	waitFor(t, "b has the document", func() bool { return text(b.Bridge.Flow) == "see # here" }, a, b)
	clock := time.Now()
	b.now = func() time.Time { return clock }
	a.Name, a.Color = "Ana", "#f00"
	a.PublishCursor()
	waitFor(t, "b to know Ana", func() bool { return len(b.Peers) == 1 }, a, b)
	if len(b.Bridge.Flow.TypedRanges) != 0 {
		t.Fatalf("the document that arrived: %+v", b.Bridge.Flow.TypedRanges)
	}
	img.W = 120 // Ana changes the picture
	a.Bridge.Flow.Touch()
	waitFor(t, "the change to reach b", func() bool {
		b.RefreshPeers()
		return len(b.Bridge.Flow.TypedRanges) > 0
	}, a, b)
	got := b.Bridge.Flow.TypedRanges
	if len(got) != 1 || got[0].From != 4 || got[0].To != 5 || got[0].Name != "Ana" || got[0].Color != "#f00" {
		t.Fatalf("the picture is character 4: %+v", got)
	}
	clock = clock.Add(DefaultTypedFor + time.Second)
	b.RefreshPeers()
	if len(b.Bridge.Flow.TypedRanges) != 0 {
		t.Errorf("still outlined: %+v", b.Bridge.Flow.TypedRanges)
	}
}
