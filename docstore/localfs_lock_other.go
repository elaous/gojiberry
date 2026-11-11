//go:build !unix

package docstore

// no flock here: two processes sharing a directory can lose updates
func (a *LocalFSAdapter) lockDir() (unlock func(), err error) { return func() {}, nil }

func syncDir(string) error { return nil }
