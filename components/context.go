package components

import (
	"context"

	"github.com/elaous/gojiberry/xango"
)

// ContextKey is an opaque, unforgeable key created by NewContextKey --
// see that function's own doc comment for why a plain string key
// would collide across independent packages/callers the way
// stdlib's own context.Context is documented to warn against.
type ContextKey struct{ name string }

// NewContextKey returns a fresh key for use with ContextProvider/
// ContextValue. name is only for readability in a debug print --
// identity is the pointer itself, not the string, so two keys created
// with the same name are still distinct from each other.
func NewContextKey(name string) *ContextKey {
	return &ContextKey{name: name}
}

// ContextProvider is gojiberry's answer to the open design question
// docs/ROADMAP.md (task #129) posed for a Context API: unlike a
// component library built on a stateful class-component tree (React et
// al.), every component in this package is a plain, stateless function
// of its own props (see the root README's own "A ready-made component
// library" section) -- there is no implicit, ambient scope a
// deeply-nested component could read a value out of without it having
// been passed somehow, in *either* rendering mode. A xango.Node tree is
// built eagerly, one plain Go function call at a time, entirely before
// xango.Render/domrt.Mount ever walks it -- so by the time either
// renderer sees the tree, there's no "current context" left to
// consult; whatever a descendant needed had to already be resolved
// during that eager build, the same constraint that makes keyed
// fragments need dedicated .xg syntax rather than a runtime trick
// (see xango's own Frag/WithKey doc comments).
//
// The answer here is the same one stdlib's own context.Context is
// designed for: an explicit, ordinary Go value threaded through an
// explicit call chain, never a hidden global (which also wouldn't be
// safe under concurrent SSR requests rendering different pages in
// separate goroutines at once). render receives ctx with value already
// attached under key, and returns whatever subtree that lets it build
// -- typically by reading it back with ContextValue and handing it
// down as an ordinary prop to whatever actually needs it, or by
// wrapping a further nested ContextProvider of its own. This is
// deliberately *not* fully transparent the way React's Context is: an
// intermediate component between the Provider and the real consumer
// still has to accept and forward ctx itself (or the extracted value)
// to reach a descendant several layers down. What it does buy over
// plain prop-drilling is that the intermediate layers only ever thread
// one generic context.Context parameter, rather than growing a
// bespoke, unrelated prop of their own for every distinct value a
// deeper descendant happens to need.
func ContextProvider(parent context.Context, key *ContextKey, value any, render func(ctx context.Context) xango.Node) xango.Node {
	return render(context.WithValue(parent, key, value))
}

// ContextValue reads key back out of ctx -- ok is false if key was
// never attached anywhere up ctx's own chain, or if the value stored
// under it isn't a T.
func ContextValue[T any](ctx context.Context, key *ContextKey) (T, bool) {
	v, ok := ctx.Value(key).(T)
	return v, ok
}
