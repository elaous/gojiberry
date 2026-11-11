//go:build !unix

package docstore

func (a *LocalFSAdapter) lockDir() (unlock func(), err error) { return func() {}, nil }

func syncDir(string) error { return nil }
