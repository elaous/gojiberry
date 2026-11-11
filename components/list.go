package components

import "github.com/elaous/gojiberry/xango"

// ListProps configures List.
type ListProps[T any] struct {
	Items []T
	Item  func(item T) xango.Node
	Key   func(item T) string
	Empty *xango.Node
	// Ordered renders an <ol> instead of a <ul>.
	Ordered bool
}

func List[T any](props ListProps[T]) xango.Node {
	if len(props.Items) == 0 && props.Empty != nil {
		return *props.Empty
	}
	tag := "ul"
	if props.Ordered {
		tag = "ol"
	}
	items := make([]xango.Node, 0, len(props.Items))
	for _, it := range props.Items {
		var content []xango.Node
		if props.Item != nil {
			content = append(content, props.Item(it))
		}
		attrs := []xango.Attr{xango.A("class", "gj-list-item")}
		if props.Key != nil {
			attrs = append(attrs, xango.A("data-key", props.Key(it)))
		}
		items = append(items, xango.El("li", attrs, content...))
	}
	return xango.El(tag, []xango.Attr{xango.A("class", "gj-list")}, items...)
}
