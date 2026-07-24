package components

// DataViewQuery is one request DataView's own Load makes through
// Fetch -- deliberately abstract over which real pagination/filter/
// sort protocol a backend actually speaks (JSON:API, OData, a
// gojiberry-defined convention, GraphQL, a plain bespoke REST
// endpoint, ...): docs/ROADMAP.md's own task #129 entry left that
// choice open as a real design question, and different apps
// reasonably want different ones, so a Fetch adapter translates this
// generic shape into whatever real request its own backend needs
// rather than DataView committing to one wire format.
type DataViewQuery struct {
	Page     int // 1-indexed
	PageSize int
	Search   string
	SortKey  string
	SortDesc bool
}

// DataViewResult is what a Fetch adapter hands back: Items already
// paginated/filtered/sorted server-side (DataView never repeats that
// work client-side), plus how many exist in total at the current
// PageSize/Search/Sort combination -- Total is a plain item count so a
// caller showing "N results" doesn't need to multiply TotalPages back
// out by PageSize to get it.
type DataViewResult[T any] struct {
	Items      []T
	Total      int
	TotalPages int
}

// DataViewProps configures DataView. Fetch is the only required
// field: the actual network/query logic a real backend needs; DataView
// itself owns only the *state* (current page/search/sort) and when to
// call it, never the wire format.
type DataViewProps[T any] struct {
	Fetch    func(DataViewQuery) (DataViewResult[T], error)
	PageSize int // defaults to 20 if <= 0
}

// DataView owns the paginated/filtered/sorted query state for a
// dataset fetched from some backend, and hands DataTable/Grid
// already-loaded data as plain props -- the same "component is a pure
// function of props, networking is the caller's job" split Collab's
// own doc comment already describes for that component, just for a
// data-*source* rather than a permissions gate. It renders nothing of
// its own -- docs/ROADMAP.md's own task #129 entry left "does DataView
// end up a real third visual component, or just a documented pattern"
// as part of the design work; a plain state-owning generic type turned
// out to need no rendering of its own at all, since DataTable/Grid
// already take already-rendered Rows/Items as plain props.
//
// Typical use: construct with NewDataView, call Load once, then wire
// DataTableProps (or GridProps)'s own Search/Pagination fields to
// SearchProps()/PaginationProps(), and Rows/Items to a mapping over
// Items built with each T's own real fields -- SetPage/SetSearch/
// SetSort (called by those wired callbacks) each reload and update
// Items/Total/TotalPages/Err in place, ready for the next re-render.
type DataView[T any] struct {
	props DataViewProps[T]

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

// NewDataView constructs a DataView starting on page 1 with no
// search/sort -- call Load once to populate Items.
func NewDataView[T any](props DataViewProps[T]) *DataView[T] {
	if props.PageSize <= 0 {
		props.PageSize = 20
	}
	return &DataView[T]{props: props, Page: 1}
}

// Load calls Fetch with the current Page/Search/SortKey/SortDesc and
// updates Items/Total/TotalPages/Err. Loading is only ever true for
// the duration of this call itself, from this type's own point of
// view -- running it off the main goroutine and re-rendering once it
// returns (so Loading ever has a chance to actually paint) is the
// caller's job, the same async-caller-drives-it split FileUploader/
// Search already take for their own async work.
func (v *DataView[T]) Load() error {
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
	return nil
}

// SetPage sets Page (clamped to at least 1) and reloads.
func (v *DataView[T]) SetPage(page int) error {
	if page < 1 {
		page = 1
	}
	v.Page = page
	return v.Load()
}

// SetSearch sets Search, resets Page to 1 (a new search invalidates
// whatever page the old result set was on), and reloads.
func (v *DataView[T]) SetSearch(search string) error {
	v.Search = search
	v.Page = 1
	return v.Load()
}

// SetSort sets SortKey/SortDesc and reloads. DataTableProps.OnSort's
// own callback receives exactly the clicked column's Key; a caller
// wiring this up decides whether that means flipping SortDesc (same
// key clicked again) or starting fresh ascending (a different key),
// the same "caller decides asc/desc toggling" split DataTableProps.
// OnSort's own doc comment already describes.
func (v *DataView[T]) SetSort(key string, desc bool) error {
	v.SortKey, v.SortDesc = key, desc
	return v.Load()
}

// PaginationProps returns a ready-to-use Pagination configuration
// bound to this DataView's own current Page/TotalPages, calling
// SetPage on change.
func (v *DataView[T]) PaginationProps() PaginationProps {
	return PaginationProps{
		Page:       v.Page,
		TotalPages: v.TotalPages,
		OnChange:   func(page int) { _ = v.SetPage(page) },
	}
}

// SearchProps returns a ready-to-use Search configuration bound to
// this DataView's own current Search, calling SetSearch on change.
// Suggestions/Open are left zero: DataView's own Search is a plain
// server-side filter, not Search's own autocomplete-suggestions half.
func (v *DataView[T]) SearchProps() SearchProps {
	return SearchProps{
		Value:    v.Search,
		OnChange: func(value string) { _ = v.SetSearch(value) },
	}
}
