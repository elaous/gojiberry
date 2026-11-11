//go:build !linux

package docstore

import "errors"

func diskUsage(dir string) (free, total int64, err error) {
	return 0, 0, errors.New("disk usage reporting is only implemented on linux")
}
