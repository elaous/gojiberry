//go:build unix

package docstore

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockDir takes an exclusive advisory lock on the store's directory (flock on a file
// in it) and returns the function that releases it. The kernel drops the lock if the
// process dies, so there is nothing stale to take over. Every write takes it, so a
// conditional save is one step with respect to the other processes writing here.
func (a *LocalFSAdapter) lockDir() (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(a.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// syncDir flushes a directory's entries to the disk, so a rename in it survives a power cut.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
