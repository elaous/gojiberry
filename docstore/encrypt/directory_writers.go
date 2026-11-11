package encrypt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

const instancePrefix = docstore.DirectoryLogPrefix + "instance/"

const (
	writerBeat  = 30 * time.Second
	writerFresh = 3 * writerBeat
	writerStale = time.Hour // registrations older than this are deleted
)

type instanceRecord struct {
	At int64 `json:"at"`
}

// instanceID is this process's registration name.
func (d *AdapterDirectory) instanceID() string {
	d.idMu.Lock()
	defer d.idMu.Unlock()
	if d.id == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		d.id = hex.EncodeToString(b[:])
	}
	return d.id
}

// Writers refreshes this process's registration.
func (d *AdapterDirectory) Writers(ctx context.Context) (int, error) {
	if d.Signer == nil {
		return 0, ErrDirectoryOff
	}
	now := d.now().Unix()
	data, _ := json.Marshal(instanceRecord{At: now})
	own := instancePrefix + d.instanceID()
	if err := d.Store.Save(ctx, own, data); err != nil {
		return 0, err
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, instancePrefix)
	if err != nil {
		return 0, err
	}
	others := 0
	for _, m := range metas {
		if m.ID == own {
			continue
		}
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return others, err
		}
		var r instanceRecord
		if json.Unmarshal(doc.Data, &r) != nil {
			continue
		}
		switch age := now - r.At; {
		case age < int64(writerFresh/time.Second):
			others++
		case age > int64(writerStale/time.Second):
			_ = d.Store.Delete(ctx, m.ID)
		}
	}
	return others, nil
}

// RunWriterWatch keeps the registration fresh while it runs.
func (d *AdapterDirectory) RunWriterWatch(ctx context.Context, replicas int) {
	t := time.NewTicker(writerBeat)
	defer t.Stop()
	var warned time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := d.Writers(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("encrypt: refreshing the key directory's writer registration: %v", err)
			}
			continue
		}
		if n > 0 && replicas <= 1 && d.now().Sub(warned) > 10*time.Minute {
			warned = d.now()
			log.Printf("docstore: ERROR: %s other server process(es) are writing this key directory, and GOJIBERRY_DOCSTORE_REPLICAS says %d: set it to the real number (and use a store with conditional writes) or stop the others", strconv.Itoa(n), replicas)
		}
	}
}
