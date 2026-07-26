//go:build !linux

package docstore

import "errors"

// diskUsage has no implementation outside Linux -- see
// diskusage_linux.go's own doc comment for why. LocalFSAdapter.Usage
// simply returns this error on any other platform, the same
// "QuotaReporter, if implemented, can still fail" contract as a real
// syscall failure would produce.
func diskUsage(dir string) (free, total int64, err error) {
	return 0, 0, errors.New("disk usage reporting is only implemented on linux")
}
