package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func dtCols() []DataTableColumn {
	return []DataTableColumn{
		{Key: "name", Header: "Name", Sortable: true},
		{Key: "email", Header: "Email"},
	}
}

func dtRows() []DataTableRow {
	return []DataTableRow{
		{ID: "1", Cells: []xango.Node{xango.Text("Ada"), xango.Text("ada@example.com")}},
		{ID: "2", Cells: []xango.Node{xango.Text("Bo"), xango.Text("bo@example.com")}},
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
		handlers[0](xango.NewEvent("click", nil, false, nil, nil))
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
		handlers[1](xango.NewEvent("change", nil, true, nil, nil))
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
		handlers[0](xango.NewEvent("change", nil, true, nil, nil))
		if !got {
			t.Error("OnSelectAll did not receive true")
		}
	})

	t.Run("RowActions adds a trailing column with that row's own buttons", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{
			Columns: dtCols(), Rows: dtRows(),
			RowActions: func(id string) []xango.Node { return []xango.Node{xango.Text("edit-" + id)} },
		}))
		mustContain(t, html, "Actions", "edit-1", "edit-2")
	})

	t.Run("no Rows renders the default empty message spanning every column", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{Columns: dtCols()}))
		mustContain(t, html, "No data", `colspan="2"`)
	})

	t.Run("Empty overrides the default message", func(t *testing.T) {
		custom := xango.Text("Nothing here yet")
		html := render(t, DataTable(DataTableProps{Columns: dtCols(), Empty: &custom}))
		mustContain(t, html, "Nothing here yet")
		mustNotContain(t, html, "No data")
	})

	t.Run("colspan accounts for the selection and actions columns too", func(t *testing.T) {
		html := render(t, DataTable(DataTableProps{
			Columns: dtCols(), Selectable: true,
			RowActions: func(string) []xango.Node { return nil },
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

func TestDataTableSelectAllChecksTheRowsOnShowNotTheCount(t *testing.T) {
	// Two selected ids from another page happen to equal the row count.
	html := render(t, DataTable(DataTableProps{Columns: dtCols(), Rows: dtRows(), Selectable: true, Selected: []string{"9", "8"}}))
	if count := strings.Count(html, `checked="checked"`); count != 0 {
		t.Errorf("stale selections checked %d boxes: %s", count, html)
	}
}
