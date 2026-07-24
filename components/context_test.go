package components

import (
	"context"
	"testing"

	"github.com/elaous/gojiberry/gox"
)

func TestContextProvider(t *testing.T) {
	t.Run("render receives a ctx carrying the provided value", func(t *testing.T) {
		key := NewContextKey("theme")
		n := ContextProvider(context.Background(), key, "dark", func(ctx context.Context) gox.Node {
			v, ok := ContextValue[string](ctx, key)
			if !ok {
				t.Fatal("ContextValue reported ok=false for a key ContextProvider just set")
			}
			return gox.Text(v)
		})
		html := render(t, n)
		mustContain(t, html, "dark")
	})

	t.Run("nested ContextProvider calls compose, inner value winning for a shared key", func(t *testing.T) {
		key := NewContextKey("mode")
		n := ContextProvider(context.Background(), key, "outer", func(ctx context.Context) gox.Node {
			return ContextProvider(ctx, key, "inner", func(ctx context.Context) gox.Node {
				v, _ := ContextValue[string](ctx, key)
				return gox.Text(v)
			})
		})
		html := render(t, n)
		mustContain(t, html, "inner")
		mustNotContain(t, html, "outer")
	})

	t.Run("distinct keys don't collide even with the same debug name", func(t *testing.T) {
		a, b := NewContextKey("k"), NewContextKey("k")
		ctx := context.WithValue(context.Background(), a, "for-a")
		if _, ok := ContextValue[string](ctx, b); ok {
			t.Error("ContextValue found a value under key b, but only a was ever set")
		}
	})

	t.Run("ContextValue reports ok=false for a key never set", func(t *testing.T) {
		key := NewContextKey("missing")
		if _, ok := ContextValue[string](context.Background(), key); ok {
			t.Error("expected ok=false for a key that was never attached")
		}
	})

	t.Run("ContextValue reports ok=false for the wrong type", func(t *testing.T) {
		key := NewContextKey("n")
		ctx := context.WithValue(context.Background(), key, 42)
		if _, ok := ContextValue[string](ctx, key); ok {
			t.Error("expected ok=false reading an int-valued key as a string")
		}
	})

	t.Run("works from any real parent context, e.g. context.TODO", func(t *testing.T) {
		key := NewContextKey("k")
		n := ContextProvider(context.TODO(), key, "v", func(ctx context.Context) gox.Node {
			v, ok := ContextValue[string](ctx, key)
			if !ok || v != "v" {
				t.Errorf("ContextValue = (%q, %v), want (\"v\", true)", v, ok)
			}
			return gox.Text(v)
		})
		render(t, n)
	})
}
