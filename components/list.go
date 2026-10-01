package components

import "github.com/elaous/gojiberry/gox"

// ListProps configures List. T is the item type; write it as a type
// argument in .gox: <List[Person] items={people} item={func(p Person) gox.Node {...}} />.
type ListProps[T any] struct {
	Items []T
	// Item renders one item (a "render prop": a function whose body may
	// itself contain markup).
	Item func(item T) gox.Node
	// Key, if set, gives each item's <li> a reconciliation key so
	// re-renders keep DOM identity as items move.
	Key func(item T) string
	// Empty is shown instead of the list when Items is empty.
	Empty *gox.Node
	// Ordered renders an <ol> instead of a <ul>.
	Ordered bool
}

// List renders a ul/ol with one li per item, each produced by the Item
// render prop. It is the smallest generic component: it shows how a
// component can take typed data and a function that turns an item into
// markup.
func List[T any](props ListProps[T]) gox.Node {
	if len(props.Items) == 0 && props.Empty != nil {
		return *props.Empty
	}
	tag := "ul"
	if props.Ordered {
		tag = "ol"
	}
	items := make([]gox.Node, 0, len(props.Items))
	for _, it := range props.Items {
		var content []gox.Node
		if props.Item != nil {
			content = append(content, props.Item(it))
		}
		attrs := []gox.Attr{gox.A("class", "gj-list-item")}
		if props.Key != nil {
			attrs = append(attrs, gox.A("data-key", props.Key(it)))
		}
		items = append(items, gox.El("li", attrs, content...))
	}
	return gox.El(tag, []gox.Attr{gox.A("class", "gj-list")}, items...)
}
