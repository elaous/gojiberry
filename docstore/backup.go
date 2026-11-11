package docstore

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

const manifestName = "manifest.json"

// BackupEntry is one document in a backup manifest.
type BackupEntry struct {
	ID     string `json:"id"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupManifest lists what a backup holds.
type BackupManifest struct {
	Version int           `json:"version"`
	Made    time.Time     `json:"made"`
	Entries []BackupEntry `json:"entries"`
}

// Backup writes every document of a store to w.
func Backup(ctx context.Context, a Adapter, w io.Writer) (BackupManifest, error) {
	man := BackupManifest{Version: 1, Made: time.Now().UTC().Truncate(time.Second)}
	metas, err := a.List(ctx)
	if err != nil {
		return man, err
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID < metas[j].ID })
	tw := tar.NewWriter(w)
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return man, err
		}
		d, err := a.Load(ctx, m.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return man, fmt.Errorf("docstore: backing up a document: %w", err)
		}
		sum := sha256.Sum256(d.Data)
		man.Entries = append(man.Entries, BackupEntry{ID: m.ID, Size: len(d.Data), SHA256: hex.EncodeToString(sum[:])})
		if err := writeEntry(tw, hex.EncodeToString([]byte(m.ID)), d.Data, d.UpdatedAt); err != nil {
			return man, err
		}
	}
	mb, err := json.Marshal(man)
	if err != nil {
		return man, err
	}
	if err := writeEntry(tw, manifestName, mb, man.Made); err != nil {
		return man, err
	}
	return man, tw.Close()
}

func writeEntry(tw *tar.Writer, name string, data []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// ErrBadBackup is returned by Restore for an invalid archive.
var ErrBadBackup = errors.New("docstore: the backup is damaged or incomplete")

// bounded entry read: a hostile archive must not exhaust memory
const maxBackupEntry = 1 << 30

// Restore writes the documents of a backup into a store.
func Restore(ctx context.Context, a Adapter, r io.ReadSeeker) (int, error) {
	man, err := verifyBackup(r)
	if err != nil {
		return 0, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	byName := make(map[string]string, len(man.Entries))
	for _, e := range man.Entries {
		byName[hex.EncodeToString([]byte(e.ID))] = e.ID
	}
	tr := tar.NewReader(r)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, ErrBadBackup
		}
		id, ok := byName[h.Name]
		if !ok {
			continue // the manifest
		}
		if err := ctx.Err(); err != nil {
			return n, err
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxBackupEntry))
		if err != nil {
			return n, ErrBadBackup
		}
		if err := a.Save(ctx, id, data); err != nil {
			return n, err
		}
		n++
	}
}

func verifyBackup(r io.Reader) (BackupManifest, error) {
	var man BackupManifest
	tr := tar.NewReader(r)
	seen := map[string]BackupEntry{} // by entry name
	haveMan := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return man, ErrBadBackup
		}
		hash := sha256.New()
		isMan := h.Name == manifestName
		var dst io.Writer = hash
		var mb limitedBuf
		if isMan {
			dst = io.MultiWriter(hash, &mb)
		}
		size, err := io.Copy(dst, io.LimitReader(tr, maxBackupEntry))
		if err != nil {
			return man, ErrBadBackup
		}
		if isMan {
			if haveMan || json.Unmarshal(mb.b, &man) != nil || man.Version != 1 {
				return man, ErrBadBackup
			}
			haveMan = true
			continue
		}
		if _, dup := seen[h.Name]; dup {
			return man, ErrBadBackup
		}
		seen[h.Name] = BackupEntry{Size: int(size), SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if !haveMan || len(seen) != len(man.Entries) {
		return man, ErrBadBackup
	}
	for _, e := range man.Entries {
		got, ok := seen[hex.EncodeToString([]byte(e.ID))]
		if !ok || got.Size != e.Size || got.SHA256 != e.SHA256 {
			return man, ErrBadBackup
		}
	}
	return man, nil
}

type limitedBuf struct{ b []byte }

func (l *limitedBuf) Write(p []byte) (int, error) {
	if len(l.b)+len(p) <= 64<<20 {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}
