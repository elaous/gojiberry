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
