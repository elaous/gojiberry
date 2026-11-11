package sessionstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func fakeClock(start time.Time) (func() time.Time, func(time.Duration)) {
	now := start
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

func TestUnknownTokensAreReadOncePerSecond(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	a, _ := New(mem)
	b, _ := New(mem)
	now, advance := fakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	a.Now, b.Now = now, now
	ctx := context.Background()

	tok, _ := a.PutSession(ctx, ana, time.Hour)
	for range 50 {
		if _, ok, _ := b.GetSession(ctx, "A"+tok[1:]); ok { // not a session
			t.Fatal("an unknown token was accepted")
		}
	}
	if got := b.Stats(); got.Reads != 1 || got.MissHits != 49 {
		t.Errorf("50 requests with one unknown token: %+v, want 1 read and 49 remembered misses", got)
	}
	suffix := tok[strings.LastIndexByte(tok, '.'):]
	for i := range 3 * maxCached {
		_, _, _ = b.GetSession(ctx, fmt.Sprintf("guess%d%s", i, suffix))
	}
	if n := len(b.misses); n > maxCached {
		t.Errorf("the miss map holds %d entries, bound %d", n, maxCached)
	}

	late, _ := New(mem)
	late.Now = now
	tok2, _ := a.PutSession(ctx, ana, time.Hour)
	if _, ok, _ := late.GetSession(ctx, tok2); !ok {
		t.Fatal("a fresh token is read from the store")
	}
	id := sessionKind.id(tok2)
	b.remember(id, nil, now().Add(DefaultMissFor))
	if _, ok, _ := b.GetSession(ctx, tok2); ok {
		t.Error("inside MissFor the remembered miss stands")
	}
	advance(DefaultMissFor + time.Millisecond)
	if _, ok, _ := b.GetSession(ctx, tok2); !ok {
		t.Error("after MissFor the token is read again and found")
	}
	b.remember(sessionKind.id("x"), nil, now().Add(time.Hour))
	tok3, _ := b.PutSession(ctx, ana, time.Hour)
	b.remember(sessionKind.id(tok3), nil, now().Add(time.Hour)) // would hide it...
	b.forget(sessionKind.id(tok3))                              // ...but PutSession forgets first; mimic the order
	if _, ok, _ := b.GetSession(ctx, tok3); !ok {
		t.Error("a session made here is not seen")
	}
}

func TestMissCacheCanBeSwitchedOff(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	s, _ := New(mem)
	s.MissFor = -1
	ctx := context.Background()
	tok, _ := s.PutSession(ctx, ana, time.Hour)
	_ = s.DeleteSession(ctx, tok)
	for range 5 {
		_, _, _ = s.GetSession(ctx, tok)
	}
	if got := s.Stats(); got.Reads != 5 || got.MissHits != 0 {
		t.Errorf("stats %+v, want 5 reads", got)
	}
}

func TestPositiveCacheHasABoundedStaleness(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	a, _ := New(mem)
	b, _ := New(mem)
	now, advance := fakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	a.Now, b.Now = now, now
	b.CacheFor = 5 * time.Second
	ctx := context.Background()
	tok, _ := a.PutSession(ctx, ana, time.Hour)

	for range 10 {
		if _, ok, _ := b.GetSession(ctx, tok); !ok {
			t.Fatal("session not found")
		}
	}
	if got := b.Stats(); got.Reads != 1 || got.CacheHits != 9 {
		t.Errorf("stats %+v, want 1 read and 9 cache hits", got)
	}
	_ = a.DeleteSession(ctx, tok)
	advance(4 * time.Second)
	if _, ok, _ := b.GetSession(ctx, tok); !ok {
		t.Error("inside CacheFor the other replica may still accept the token (the documented staleness)")
	}
	advance(2 * time.Second)
	if _, ok, _ := b.GetSession(ctx, tok); ok {
		t.Error("after CacheFor the logout is seen")
	}
	tok2, _ := a.PutSession(ctx, ana, time.Hour)
	_, _, _ = b.GetSession(ctx, tok2)
	_ = b.DeleteSession(ctx, tok2)
	if _, ok, _ := b.GetSession(ctx, tok2); ok {
		t.Error("a logout is not seen at once by the replica that handled it")
	}
	short, _ := a.PutSession(ctx, ana, 2*time.Second)
	_, _, _ = b.GetSession(ctx, short)
	advance(3 * time.Second)
	if _, ok, _ := b.GetSession(ctx, short); ok {
		t.Error("an expired session was served from the cache")
	}
}
