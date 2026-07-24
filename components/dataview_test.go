package components

import (
	"errors"
	"testing"
)

type dvRow struct {
	Name string
}

func TestDataView(t *testing.T) {
	t.Run("NewDataView defaults PageSize and starts on page 1", func(t *testing.T) {
		var gotQuery DataViewQuery
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(q DataViewQuery) (DataViewResult[dvRow], error) {
				gotQuery = q
				return DataViewResult[dvRow]{}, nil
			},
		})
		if err := v.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if gotQuery.Page != 1 || gotQuery.PageSize != 20 {
			t.Errorf("first Load's query = %+v, want Page=1 PageSize=20", gotQuery)
		}
	})

	t.Run("Load populates Items/Total/TotalPages from the Fetch result", func(t *testing.T) {
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(DataViewQuery) (DataViewResult[dvRow], error) {
				return DataViewResult[dvRow]{Items: []dvRow{{Name: "Ada"}}, Total: 1, TotalPages: 1}, nil
			},
		})
		if err := v.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(v.Items) != 1 || v.Items[0].Name != "Ada" || v.Total != 1 || v.TotalPages != 1 {
			t.Errorf("state after Load = %+v", v)
		}
		if v.Loading {
			t.Error("Loading should be false once Load has returned")
		}
	})

	t.Run("a Fetch error is stored on Err and returned, leaving Items untouched", func(t *testing.T) {
		wantErr := errors.New("boom")
		first := true
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(DataViewQuery) (DataViewResult[dvRow], error) {
				if first {
					first = false
					return DataViewResult[dvRow]{Items: []dvRow{{Name: "kept"}}}, nil
				}
				return DataViewResult[dvRow]{}, wantErr
			},
		})
		if err := v.Load(); err != nil {
			t.Fatalf("first Load: %v", err)
		}
		if err := v.Load(); !errors.Is(err, wantErr) {
			t.Errorf("second Load error = %v, want %v", err, wantErr)
		}
		if v.Err != wantErr {
			t.Errorf("Err = %v, want %v", v.Err, wantErr)
		}
		if len(v.Items) != 1 || v.Items[0].Name != "kept" {
			t.Errorf("Items changed after a failed Load: %+v", v.Items)
		}
	})

	t.Run("SetPage clamps below 1 and reloads with the new page", func(t *testing.T) {
		var gotPage int
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(q DataViewQuery) (DataViewResult[dvRow], error) {
				gotPage = q.Page
				return DataViewResult[dvRow]{}, nil
			},
		})
		_ = v.SetPage(3)
		if v.Page != 3 || gotPage != 3 {
			t.Errorf("Page = %d, query.Page = %d, want 3", v.Page, gotPage)
		}
		_ = v.SetPage(-5)
		if v.Page != 1 || gotPage != 1 {
			t.Errorf("Page = %d, query.Page = %d, want 1 (clamped)", v.Page, gotPage)
		}
	})

	t.Run("SetSearch resets Page to 1 and reloads with the new Search", func(t *testing.T) {
		var gotQuery DataViewQuery
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(q DataViewQuery) (DataViewResult[dvRow], error) {
				gotQuery = q
				return DataViewResult[dvRow]{}, nil
			},
		})
		_ = v.SetPage(4)
		_ = v.SetSearch("ada")
		if v.Page != 1 || v.Search != "ada" || gotQuery.Page != 1 || gotQuery.Search != "ada" {
			t.Errorf("state after SetSearch = Page=%d Search=%q, query=%+v", v.Page, v.Search, gotQuery)
		}
	})

	t.Run("SetSort sets SortKey/SortDesc and reloads", func(t *testing.T) {
		var gotQuery DataViewQuery
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(q DataViewQuery) (DataViewResult[dvRow], error) {
				gotQuery = q
				return DataViewResult[dvRow]{}, nil
			},
		})
		_ = v.SetSort("name", true)
		if v.SortKey != "name" || !v.SortDesc || gotQuery.SortKey != "name" || !gotQuery.SortDesc {
			t.Errorf("state after SetSort = %+v, query = %+v", v, gotQuery)
		}
	})

	t.Run("PaginationProps/SearchProps reflect current state and drive it on change", func(t *testing.T) {
		v := NewDataView(DataViewProps[dvRow]{
			Fetch: func(DataViewQuery) (DataViewResult[dvRow], error) {
				return DataViewResult[dvRow]{TotalPages: 5}, nil
			},
		})
		_ = v.Load()
		pp := v.PaginationProps()
		if pp.Page != 1 || pp.TotalPages != 5 {
			t.Errorf("PaginationProps = %+v", pp)
		}
		pp.OnChange(2)
		if v.Page != 2 {
			t.Errorf("Page after PaginationProps.OnChange(2) = %d, want 2", v.Page)
		}

		sp := v.SearchProps()
		if sp.Value != "" {
			t.Errorf("SearchProps.Value = %q, want \"\"", sp.Value)
		}
		sp.OnChange("query")
		if v.Search != "query" || v.Page != 1 {
			t.Errorf("state after SearchProps.OnChange = Search=%q Page=%d, want \"query\", 1", v.Search, v.Page)
		}
	})
}
