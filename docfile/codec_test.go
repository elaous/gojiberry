package docfile

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func TestDetectBinaryWhoseTitleLooksLikeText(t *testing.T) {
	for _, title := range []string{"my letter", "x", strings.Repeat("a", 100), strings.Repeat("a", 123)} {
		data, err := ProtoBinary.Marshal(Document{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		d, err := Detect(data).Unmarshal(data)
		if err != nil || d.Title != title {
			t.Errorf("title %q: detected %s: %v", title[:min(len(title), 10)], Detect(data).Name(), err)
		}
	}
}

func TestRaggedTableIsMadeRectangular(t *testing.T) {
	items := ToItems([]Item{{Table: &TableItem{Rows: []TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c"}}}}}})
	obj := items[0].(canvas.Inline).Object.(*canvas.TableObject)
	if obj.View.Table.Cols() != 2 || len(obj.View.Table.Cells[1]) != 2 {
		t.Fatalf("%v", obj.View.Table.Cells)
	}
}

type fakeSource struct{ url string }

func (fakeSource) Size() (w, h float64) { return 40, 30 }
func (f fakeSource) URL() string        { return f.url }

func TestInlineImageSurvivesSaveAndOpen(t *testing.T) {
	img := canvas.NewImage(fakeSource{"data:image/png;base64,AAAA"}, 0, 0)
	img.Placement, img.W, img.H, img.Alpha = canvas.PlaceInline, 80, 60, 0.5
	img.Clip = canvas.Insets{Left: 2, Top: 3, Right: 4, Bottom: 5}
	items := []canvas.Item{canvas.Chars{Text: "a"}, canvas.Inline{Object: img}, canvas.Chars{Text: "b"}}
	for _, c := range []Codec{JSON, ProtoText, ProtoBinary} {
		data, err := c.Marshal(Document{Content: FromItems(items)})
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		back := ToItems(d.Content)
		again := FromItems(back)
		if len(again) != 3 || again[1].Image == nil || again[1].Image.URL != "data:image/png;base64,AAAA" || again[1].Image.Width != 80 || again[1].Image.Alpha != 0.5 || again[1].Image.ClipBottom != 5 {
			t.Fatalf("%s: %+v", c.Name(), again)
		}
	}
	ImageLoader = func(url string) canvas.ImageSource { return fakeSource{url} }
	defer func() { ImageLoader = nil }()
	got := ToItems(FromItems(items))[1].(canvas.Inline).Object
	if loaded, ok := got.(*canvas.Image); !ok || loaded.W != 80 || loaded.Placement != canvas.PlaceInline {
		t.Fatalf("with a loader the real image returns: %T", got)
	}
}

func TestOpenIsProtobufUnderItsOwnName(t *testing.T) {
	d := Document{Title: "Plan", Content: []Item{{Chars: &Chars{Text: "hello"}}}}
	a, err := Open.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ProtoBinary.Marshal(d)
	if string(a) != string(b) {
		t.Error(".open is the protobuf wire format, byte for byte")
	}
	got, err := Open.Unmarshal(a)
	if err != nil || got.Title != "Plan" || len(got.Content) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if Open.Ext() != ".open" || Open.Name() != "open" {
		t.Error(Open.Ext(), Open.Name())
	}
	if c, err := ByName("open"); err != nil || c != Open {
		t.Error("registered", err)
	}
}
