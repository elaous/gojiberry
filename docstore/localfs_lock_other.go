//go:build !unix

package docstore

// lockDir is a no-op where there is no flock: writes are still one at a time within
// the process (LocalFSAdapter.mu), but two processes sharing a directory can lose
// an update to a catalog; use the SQLite or HTTP store there.
func (a *LocalFSAdapter) lockDir() (unlock func(), err error) { return func() {}, nil }
