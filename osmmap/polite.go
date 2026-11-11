package osmmap

import (
	"container/list"
	"context"
	"fmt"
	"image"
	"strings"
	"sync"
	"time"
)

type gate struct {
	mu   sync.Mutex
	gap  time.Duration
	next time.Time
	now  func() time.Time
	nap  func(context.Context, time.Duration) error
}

func (g *gate) wait(ctx context.Context) error {
	if g.gap <= 0 {
		return nil
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	g.mu.Lock()
	at := g.next
	if t := now(); at.Before(t) {
		at = t
	}
	g.next = at.Add(g.gap)
	g.mu.Unlock()
	d := at.Sub(now())
	if d <= 0 {
		return nil
	}
	if g.nap != nil {
		return g.nap(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type lru[V any] struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

type lruItem[V any] struct {
	key string
	val V
}

func newLRU[V any](max int) *lru[V] {
	return &lru[V]{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (c *lru[V]) get(k string) (v V, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, found := c.items[k]; found {
		c.order.MoveToFront(e)
		return e.Value.(lruItem[V]).val, true
	}
	return v, false
}

func (c *lru[V]) put(k string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, found := c.items[k]; found {
		e.Value = lruItem[V]{k, v}
		c.order.MoveToFront(e)
		return
	}
	c.items[k] = c.order.PushFront(lruItem[V]{k, v})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(lruItem[V]).key)
	}
}

type PoliteTiles struct {
	Source   TileSource
	MaxTiles int
	Gap      time.Duration

	once  sync.Once
	cache *lru[image.Image]
	gate  gate
}

func (p *PoliteTiles) init() {
	p.once.Do(func() {
		n := p.MaxTiles
		if n <= 0 {
			n = 128
		}
		p.cache, p.gate.gap = newLRU[image.Image](n), p.Gap
	})
}

// Tile implements TileSource.
func (p *PoliteTiles) Tile(ctx context.Context, z, x, y int) (image.Image, error) {
	p.init()
	key := fmt.Sprintf("%d/%d/%d", z, x, y)
	if img, ok := p.cache.get(key); ok {
		return img, nil
	}
	if err := p.gate.wait(ctx); err != nil {
		return nil, err
	}
	img, err := p.Source.Tile(ctx, z, x, y)
	if err == nil {
		p.cache.put(key, img)
	}
	return img, err
}

type PoliteGeocoder struct {
	Source     Geocoder
	MaxQueries int
	Gap        time.Duration

	once  sync.Once
	cache *lru[[]Place]
	gate  gate
}

// Search implements Geocoder.
func (p *PoliteGeocoder) Search(ctx context.Context, query string) ([]Place, error) {
	p.once.Do(func() {
		n := p.MaxQueries
		if n <= 0 {
			n = 64
		}
		p.cache, p.gate.gap = newLRU[[]Place](n), p.Gap
	})
	key := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if places, ok := p.cache.get(key); ok {
		return append([]Place(nil), places...), nil
	}
	if err := p.gate.wait(ctx); err != nil {
		return nil, err
	}
	places, err := p.Source.Search(ctx, query)
	if err == nil {
		p.cache.put(key, append([]Place(nil), places...))
	}
	return places, err
}
