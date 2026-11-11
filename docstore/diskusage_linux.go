//go:build linux

package docstore

import "syscall"

func diskUsage(dir string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, 0, err
	}
	free = int64(stat.Bavail) * int64(stat.Bsize)
	total = int64(stat.Blocks) * int64(stat.Bsize)
	return free, total, nil
}
