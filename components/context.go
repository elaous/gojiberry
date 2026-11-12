package components

import (
	"context"

	"github.com/elaous/xango"
)

type ContextKey struct{ name string }

// NewContextKey returns a fresh key for use with ContextProvider/ ContextValue.
func NewContextKey(name string) *ContextKey {
	return &ContextKey{name: name}
}

func ContextProvider(parent context.Context, key *ContextKey, value any, render func(ctx context.Context) xango.Node) xango.Node {
	return render(context.WithValue(parent, key, value))
}

func ContextValue[T any](ctx context.Context, key *ContextKey) (T, bool) {
	v, ok := ctx.Value(key).(T)
	return v, ok
}
