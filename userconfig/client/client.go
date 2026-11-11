package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Entry is one item as this device has it.
type Entry struct {
	Data    []byte `json:"data,omitempty"`
	Rev     int    `json:"rev"`
	Dirty   bool   `json:"dirty,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// Cache keeps the device's copy between runs.
type Cache interface {
	Load() map[string]Entry
	Save(map[string]Entry)
}

type MemoryCache struct {
	mu sync.Mutex
	m  map[string]Entry
}

// Load implements Cache.
func (c *MemoryCache) Load() map[string]Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Entry, len(c.m))
	for k, v := range c.m {
		out[k] = v
	}
	return out
}

// Save implements Cache.
func (c *MemoryCache) Save(m map[string]Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = make(map[string]Entry, len(m))
	for k, v := range m {
		c.m[k] = v
	}
}

var (
	ErrOffline   = errors.New("userconfig client: the server could not be reached")
	ErrSignedOut = errors.New("userconfig client: sign in to sync settings")
	ErrNotFound  = errors.New("userconfig client: no such item")
)

type Merge func(name string, local, remote []byte) []byte

// Client syncs one person's items. Use New.
type Client struct {
	// BaseURL is the server ("https://host"), Prefix the route (default "/me/config").
	BaseURL, Prefix string
	// HTTP is the client to use (nil: http.DefaultClient).
	HTTP   *http.Client
	Header func(*http.Request)
	// Merge resolves conflicts; nil keeps the local change.
	Merge Merge

	cache Cache
	mu    sync.Mutex
	items map[string]Entry
}

// New makes a client over a cache (nil: in memory).
func New(baseURL string, cache Cache) *Client {
	if cache == nil {
		cache = &MemoryCache{}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), cache: cache, items: cache.Load()}
}

func (c *Client) base() string {
	p := c.Prefix
	if p == "" {
		p = "/me/config"
	}
	return c.BaseURL + strings.TrimRight(p, "/")
}

func (c *Client) save() { c.cache.Save(c.items) }

func (c *Client) Get(name string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[name]
	if !ok || e.Deleted {
		return nil, ErrNotFound
	}
	return append([]byte(nil), e.Data...), nil
}

// Names lists the items this device has, sorted.
func (c *Client) Names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for n, e := range c.items {
		if !e.Deleted {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Pending is how many changes wait to be sent.
func (c *Client) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.items {
		if e.Dirty {
			n++
		}
	}
	return n
}

func (c *Client) Set(name string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.items[name]
	e.Data, e.Dirty, e.Deleted = append([]byte(nil), data...), true, false
	c.items[name] = e
	c.save()
}

func (c *Client) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[name]
	if !ok {
		return
	}
	if e.Rev == 0 { // the server never had it: nothing to tell
		delete(c.items, name)
	} else {
		e.Data, e.Dirty, e.Deleted = nil, true, true
		c.items[name] = e
	}
	c.save()
}

func (c *Client) Sync(ctx context.Context) error {
	if err := c.push(ctx); err != nil {
		return err
	}
	return c.pull(ctx)
}

func (c *Client) push(ctx context.Context) error {
	c.mu.Lock()
	var names []string
	for n, e := range c.items {
		if e.Dirty {
			names = append(names, n)
		}
	}
	c.mu.Unlock()
	sort.Strings(names)
	for _, n := range names {
		if err := c.pushOne(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) pushOne(ctx context.Context, name string) error {
	for range 5 {
		c.mu.Lock()
		e, ok := c.items[name]
		c.mu.Unlock()
		if !ok || !e.Dirty {
			return nil
		}
		method, body := http.MethodPut, io.Reader(bytes.NewReader(e.Data))
		if e.Deleted {
			method, body = http.MethodDelete, nil
		}
		resp, err := c.do(ctx, method, "/"+name, body, strconv.Quote(strconv.Itoa(e.Rev)))
		if err != nil {
			return err
		}
		switch {
		case resp.status/100 == 2:
			c.mu.Lock()
			if e.Deleted {
				delete(c.items, name)
			} else {
				cur := c.items[name]
				cur.Rev, cur.Dirty = revOf(resp.etag), false
				c.items[name] = cur
			}
			c.save()
			c.mu.Unlock()
			return nil
		case resp.status == http.StatusPreconditionFailed:
			if err := c.resolve(ctx, name, e); err != nil {
				return err
			}
		case resp.status == http.StatusNotFound && e.Deleted: // already gone
			c.mu.Lock()
			delete(c.items, name)
			c.save()
			c.mu.Unlock()
			return nil
		default:
			return fmt.Errorf("userconfig client: the server answered %d for %q", resp.status, name)
		}
	}
	return fmt.Errorf("userconfig client: %q keeps changing on another device", name)
}

func (c *Client) resolve(ctx context.Context, name string, local Entry) error {
	resp, err := c.do(ctx, http.MethodGet, "/"+name, nil, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.items[name]
	switch resp.status {
	case http.StatusOK:
		merged := local.Data
		if c.Merge != nil && !local.Deleted {
			merged = c.Merge(name, local.Data, resp.body)
		}
		cur.Data, cur.Rev = merged, revOf(resp.etag)
		if local.Deleted {
			cur.Data, cur.Dirty, cur.Deleted = resp.body, false, false
		}
	case http.StatusNotFound: // it was removed elsewhere: our change recreates it
		cur.Rev = 0
		if local.Deleted {
			delete(c.items, name)
			c.save()
			return nil
		}
	default:
		return fmt.Errorf("userconfig client: the server answered %d for %q", resp.status, name)
	}
	c.items[name] = cur
	c.save()
	return nil
}

func (c *Client) pull(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "", nil, "")
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("userconfig client: the server answered %d to the listing", resp.status)
	}
	var list struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(resp.body, &list); err != nil {
		return fmt.Errorf("userconfig client: the listing cannot be read")
	}
	remote := map[string]bool{}
	for _, n := range list.Items {
		remote[n] = true
		c.mu.Lock()
		e, have := c.items[n]
		c.mu.Unlock()
		if have && e.Dirty {
			continue
		}
		r, err := c.do(ctx, http.MethodGet, "/"+n, nil, "")
		if err != nil {
			return err
		}
		if r.status != http.StatusOK {
			continue // removed since the listing; the next sync settles it
		}
		c.mu.Lock()
		c.items[n] = Entry{Data: r.body, Rev: revOf(r.etag)}
		c.save()
		c.mu.Unlock()
	}
	c.mu.Lock()
	for n, e := range c.items {
		if !remote[n] && !e.Dirty {
			delete(c.items, n)
		}
	}
	c.save()
	c.mu.Unlock()
	return nil
}

type result struct {
	status int
	etag   string
	body   []byte
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, ifMatch string) (result, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return result{}, err
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if c.Header != nil {
		c.Header(req)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result{}, ctx.Err()
		}
		return result{}, ErrOffline
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return result{}, ErrOffline
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return result{}, ErrSignedOut
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return result{}, ErrOffline // the server is there but cannot serve now: try later
	}
	return result{status: resp.StatusCode, etag: resp.Header.Get("Etag"), body: b}, nil
}

func revOf(etag string) int {
	n, err := strconv.Atoi(strings.Trim(etag, `"`))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
