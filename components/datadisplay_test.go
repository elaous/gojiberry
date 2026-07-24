package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func dtCols() []DataTableColumn {
	return []DataTableColumn{
		{Key: "name", Header: "Name", Sortable: true},
		{Key: "email", Header: "Email"},
	}
}

func dtRows() []DataTableRow {
	return []DataTableRow{
		{ID: "1", Cells: []gox.Node{gox.Text("Ada"), gox.Text("ada@example.com")}},
		{ID: "2", Cells: []gox.Node{gox.Text("Bo"), gox.Text("bo@example.com")}},
	}
}

func TestDataTable(t *testing.T) {
	t.Run("renders header/rows and a non-sortable column plainly", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows()}))
		mustContain(t, html, "Name", "Ada", "ada@example.com", "Bo", "bo@example.com", "<th>Email</th>")
	})

	t.Run("Sortable column gets aria-sort and a click handler firing OnSort with its Key", func(t *testing.T) {
		var got string
		n := DataTable(DataTableProps{
			Columns: dtCols(), Rows: dtRows(),
			SortKey: "name", SortDesc: false,
			OnSort: func(key string) { got = key },
		})
		html := render(t, n)
		mustContain(t, html, `aria-sort="ascending"`)
		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 sort click handler (only Name is Sortable), got %d", len(handlers))
		}
		handlers[0](gox.NewEvent("click", nil, false, nil, nil))
		if got != "name" {
			t.Errorf("OnSort got %q, want \"name\"", got)
		}
	})

	t.Run("SortDesc flips the aria-sort direction", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), SortKey: "name", SortDesc: true}))
		mustContain(t, html, `aria-sort="descending"`)
	})

	t.Run("Selectable renders a checkbox per row plus a select-all header checkbox", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), Selectable: true}))
		if count := strings.Count(html, `type="checkbox"`); count != 3 {
			t.Errorf("expected 3 checkboxes (1 select-all + 2 rows), found %d in: %s", count, html)
		}
	})

	t.Run("Selected marks exactly the matching row checkboxes checked, not select-all", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), Selectable: true, Selected: []string{"2"}}))
		if count := strings.Count(html, `checked="checked"`); count != 1 {
			t.Errorf("expected exactly 1 checked checkbox, found %d in: %s", count, html)
		}
		mustContain(t, html, "gj-datatable-row-selected")
	})

	t.Run("every row selected also checks select-all", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), Selectable: true, Selected: []string{"1", "2"}}))
		if count := strings.Count(html, `checked="checked"`); count != 3 {
			t.Errorf("expected all 3 checkboxes checked, found %d in: %s", count, html)
		}
	})

	t.Run("OnSelectChange fires with the row's own ID and new state", func(t *testing.T) {
		var gotID string
		var gotChecked bool
		n := DataTable(DataTableProps{
			Columns: dtCols(), Rows: dtRows(), Selectable: true,
			OnSelectChange: func(id string, checked bool) { gotID, gotChecked = id, checked },
		})
		handlers := findHandlers(n, "change")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 row change handlers, got %d", len(handlers))
		}
		handlers[1](gox.NewEvent("change", nil, true, nil, nil))
		if gotID != "2" || !gotChecked {
			t.Errorf("OnSelectChange got (%q, %v), want (\"2\", true)", gotID, gotChecked)
		}
	})

	t.Run("OnSelectAll fires from the header checkbox", func(t *testing.T) {
		var got bool
		n := DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), Selectable: true, OnSelectAll: func(checked bool) { got = checked }})
		// Only OnSelectAll is set here (no OnSelectChange), so only the
		// header checkbox gets a change handler -- per-row ones are only
		// attached when OnSelectChange itself is non-nil.
		handlers := findHandlers(n, "change")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 change handler (select-all only), got %d", len(handlers))
		}
		handlers[0](gox.NewEvent("change", nil, true, nil, nil))
		if !got {
			t.Error("OnSelectAll did not receive true")
		}
	})

	t.Run("RowActions adds a trailing column with that row's own buttons", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{
			Columns: dtCols(), Rows: dtRows(),
			RowActions: func(id string) []gox.Node { return []gox.Node{gox.Text("edit-" + id)} },
		}))
		mustContain(t, html, "Actions", "edit-1", "edit-2")
	})

	t.Run("no Rows renders the default empty message spanning every column", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols()}))
		mustContain(t, html, "No data", `colspan="2"`)
	})

	t.Run("Empty overrides the default message", func(t *testing.T) {
		custom := gox.Text("Nothing here yet")
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Empty: &custom}))
		mustContain(t, html, "Nothing here yet")
		mustNotContain(t, html, "No data")
	})

	t.Run("colspan accounts for the selection and actions columns too", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{
			Columns: dtCols(), Selectable: true,
			RowActions: func(string) []gox.Node { return nil },
		}))
		mustContain(t, html, `colspan="4"`)
	})

	t.Run("Search renders a Search bar and Pagination renders Pagination", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{
			Columns: dtCols(), Rows: dtRows(),
			Search:     &SearchProps{Placeholder: "Filter…"},
			Pagination: &PaginationProps{Page: 1, TotalPages: 3},
		}))
		mustContain(t, html, "gj-search", "Filter…", "gj-pagination")
	})

	t.Run("rows are keyed by their own ID", func(t *testing.T) {
		n := DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows()})
		body := n.Children[0].Children[1] // div > table > tbody (no Search/Pagination, so table is child 0)
		if body.Children[0].Key != "1" || body.Children[1].Key != "2" {
			t.Errorf("row keys = %q, %q, want \"1\", \"2\"", body.Children[0].Key, body.Children[1].Key)
		}
	})
}

func grItems() []GridItem {
	return []GridItem{
		{ID: "a", Content: gox.Text("Item A")},
		{ID: "b", Content: gox.Text("Item B")},
	}
}

func TestGrid(t *testing.T) {
	t.Run("renders every item's own Content", func(t *testing.T) {
		html := render(t, Grid(GridProps{Items: grItems()}))
		mustContain(t, html, "Item A", "Item B")
	})

	t.Run("Selectable renders one checkbox per item plus a select-all", func(t *testing.T) {
		html := render(t, Grid(GridProps{Items: grItems(), Selectable: true}))
		if count := strings.Count(html, `type="checkbox"`); count != 3 {
			t.Errorf("expected 3 checkboxes (1 select-all + 2 items), found %d in: %s", count, html)
		}
	})

	t.Run("Selected marks the matching item checked and adds the selected class", func(t *testing.T) {
		html := render(t, Grid(GridProps{Items: grItems(), Selectable: true, Selected: []string{"b"}}))
		if count := strings.Count(html, `checked="checked"`); count != 1 {
			t.Errorf("expected exactly 1 checked item, found %d in: %s", count, html)
		}
		mustContain(t, html, "gj-grid-item-selected")
	})

	t.Run("OnSelectChange fires with the item's own ID", func(t *testing.T) {
		var gotID string
		n := Grid(GridProps{Items: grItems(), Selectable: true, OnSelectChange: func(id string, checked bool) { gotID = id }})
		handlers := findHandlers(n, "change")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 item change handlers, got %d", len(handlers))
		}
		handlers[1](gox.NewEvent("change", nil, true, nil, nil))
		if gotID != "b" {
			t.Errorf("OnSelectChange got %q, want \"b\"", gotID)
		}
	})

	t.Run("ItemActions renders that item's own buttons", func(t *testing.T) {
		html := render(t, Grid(GridProps{
			Items:       grItems(),
			ItemActions: func(id string) []gox.Node { return []gox.Node{gox.Text("del-" + id)} },
		}))
		mustContain(t, html, "del-a", "del-b")
	})

	t.Run("no Items renders the default empty message", func(t *testing.T) {
		html := render(t, Grid(GridProps{}))
		mustContain(t, html, "No data")
	})

	t.Run("Empty overrides the default message", func(t *testing.T) {
		custom := gox.Text("Nothing to show")
		html := render(t, Grid(GridProps{Empty: &custom}))
		mustContain(t, html, "Nothing to show")
	})

	t.Run("Search and Pagination render their own components", func(t *testing.T) {
		html := render(t, Grid(GridProps{
			Items:      grItems(),
			Search:     &SearchProps{Placeholder: "Filter…"},
			Pagination: &PaginationProps{Page: 1, TotalPages: 2},
		}))
		mustContain(t, html, "gj-search", "gj-pagination")
	})

	t.Run("items are keyed by their own ID", func(t *testing.T) {
		n := Grid(GridProps{Items: grItems()})
		grid := n.Children[0] // div > div.gj-grid
		if grid.Children[0].Key != "a" || grid.Children[1].Key != "b" {
			t.Errorf("item keys = %q, %q, want \"a\", \"b\"", grid.Children[0].Key, grid.Children[1].Key)
		}
	})
}
