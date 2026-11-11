package components

import "sync"

type DataViewQuery struct {
	Page     int // 1-indexed
	PageSize int
	Search   string
	SortKey  string
	SortDesc bool
}

type DataViewResult[T any] struct {
	Items      []T
	Total      int
	TotalPages int
}

// DataViewProps configures DataView.
type DataViewProps[T any] struct {
	Fetch    func(DataViewQuery) (DataViewResult[T], error)
	PageSize int // defaults to 20 if <= 0
}

type DataView[T any] struct {
	props DataViewProps[T]
	mu    sync.Mutex // serializes Load

	Page     int
	Search   string
	SortKey  string
	SortDesc bool

	Items      []T
	Total      int
	TotalPages int
	Loading    bool
	Err        error
}

func NewDataView[T any](props DataViewProps[T]) *DataView[T] {
	if props.PageSize <= 0 {
		props.PageSize = 20
	}
	return &DataView[T]{props: props, Page: 1}
}

// Load calls Fetch with the current Page/Search/SortKey/SortDesc and updates Items/Total/TotalPages/Err.
func (v *DataView[T]) Load() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.load(true)
}

func (v *DataView[T]) load(clamp bool) error {
	v.Loading = true
	result, err := v.props.Fetch(DataViewQuery{
		Page:     v.Page,
		PageSize: v.props.PageSize,
		Search:   v.Search,
		SortKey:  v.SortKey,
		SortDesc: v.SortDesc,
	})
	v.Loading = false
	v.Err = err
	if err != nil {
		return err
	}
	v.Items, v.Total, v.TotalPages = result.Items, result.Total, result.TotalPages
	if clamp && v.TotalPages > 0 && v.Page > v.TotalPages {
		v.Page = v.TotalPages
		return v.load(false)
	}
	return nil
}

// SetPage sets Page (clamped to at least 1) and reloads.
func (v *DataView[T]) SetPage(page int) error {
	if page < 1 {
		page = 1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	previous := v.Page
	v.Page = page
	if err := v.load(true); err != nil {
		v.Page = previous // the table still shows the previous page's rows
		return err
	}
	return nil
}

func (v *DataView[T]) SetSearch(search string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Search = search
	v.Page = 1
	return v.load(true)
}

// SetSort sets SortKey/SortDesc and reloads.
func (v *DataView[T]) SetSort(key string, desc bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.SortKey, v.SortDesc = key, desc
	v.Page = 1
	return v.load(true)
}

func (v *DataView[T]) PaginationProps() PaginationProps {
	return PaginationProps{
		Page:       v.Page,
		TotalPages: v.TotalPages,
		OnChange:   func(page int) { _ = v.SetPage(page) },
	}
}

func (v *DataView[T]) SearchProps() SearchProps {
	return SearchProps{
		Value:    v.Search,
		OnChange: func(value string) { _ = v.SetSearch(value) },
	}
}
