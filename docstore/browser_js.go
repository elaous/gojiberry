//go:build js && wasm

package docstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"
)

// BrowserAdapter keeps documents in the browser.
type BrowserAdapter struct{ prefix string }

// NewBrowserAdapter returns an adapter using browser storage.
func NewBrowserAdapter(prefix string) *BrowserAdapter {
	if prefix == "" {
		prefix = "gojiberry:"
	}
	return &BrowserAdapter{prefix: prefix}
}

type browserRecord struct {
	Data    string `json:"d"` // base64
	Updated int64  `json:"t"` // unix nanoseconds
}

func (a *BrowserAdapter) storage() (js.Value, error) {
	s := js.Global().Get("localStorage")
	if !s.Truthy() {
		return js.Value{}, errors.New("docstore: this browser has no localStorage")
	}
	return s, nil
}

// Save implements Adapter.
func (a *BrowserAdapter) Save(_ context.Context, id string, data []byte) (err error) {
	s, err := a.storage()
	if err != nil {
		return err
	}
	rec, _ := json.Marshal(browserRecord{Data: base64.StdEncoding.EncodeToString(data), Updated: time.Now().UnixNano()})
	defer func() { // setItem throws when the quota is exceeded
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: the browser refused to store the document (quota)", ErrStorageFull)
		}
	}()
	s.Call("setItem", a.prefix+id, string(rec))
	return nil
}

// SaveIf implements ConditionalAdapter.
func (a *BrowserAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) (err error) {
	release, err := a.lock(ctx, "gojiberry-docstore:"+a.prefix+"#cas")
	if err != nil {
		return err
	}
	defer release()
	defer recoverJS(&err)
	s, err := a.storage()
	if err != nil {
		return err
	}
	key := a.prefix + id
	cur, ok := a.read(s, key)
	if prev == "" && ok {
		return ErrConflict
	}
	if prev != "" {
		if !ok {
			return ErrConflict
		}
		have, derr := base64.StdEncoding.DecodeString(cur.Data)
		if derr != nil || Digest(have) != prev {
			return ErrConflict
		}
	}
	if err := a.Save(ctx, id, data); err != nil {
		return err
	}
	if back, ok := a.read(s, key); !ok || back.Data != base64.StdEncoding.EncodeToString(data) {
		return ErrConflict
	}
	return nil
}

func (a *BrowserAdapter) read(s js.Value, key string) (browserRecord, bool) {
	v := s.Call("getItem", key)
	if v.IsNull() {
		return browserRecord{}, false
	}
	var rec browserRecord
	if json.Unmarshal([]byte(v.String()), &rec) != nil {
		return browserRecord{}, false
	}
	return rec, true
}

// Load implements Adapter.
func (a *BrowserAdapter) Load(_ context.Context, id string) (doc Document, err error) {
	defer recoverJS(&err)
	s, err := a.storage()
	if err != nil {
		return Document{}, err
	}
	rec, ok := a.read(s, a.prefix+id)
	if !ok {
		return Document{}, ErrNotFound
	}
	data, err := base64.StdEncoding.DecodeString(rec.Data)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: id, Data: data, UpdatedAt: time.Unix(0, rec.Updated)}, nil
}

// List implements Adapter.
func (a *BrowserAdapter) List(context.Context) (metas []Meta, err error) {
	defer recoverJS(&err)
	s, err := a.storage()
	if err != nil {
		return nil, err
	}
	for i := range s.Get("length").Int() {
		key := s.Call("key", i).String()
		id, ok := strings.CutPrefix(key, a.prefix)
		if !ok {
			continue
		}
		if rec, ok := a.read(s, key); ok {
			metas = append(metas, Meta{ID: id, UpdatedAt: time.Unix(0, rec.Updated), Size: base64.StdEncoding.DecodedLen(len(rec.Data))})
		}
	}
	return metas, nil
}

// Delete implements Adapter.
func (a *BrowserAdapter) Delete(_ context.Context, id string) (err error) {
	defer recoverJS(&err)
	s, err := a.storage()
	if err != nil {
		return err
	}
	if _, ok := a.read(s, a.prefix+id); !ok {
		return ErrNotFound
	}
	s.Call("removeItem", a.prefix+id)
	return nil
}

// recoverJS turns a JS exception into a Go error.
func recoverJS(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("docstore: browser storage: %v", r)
	}
}
