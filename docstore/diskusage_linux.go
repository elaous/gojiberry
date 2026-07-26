//go:build linux

package docstore

import "syscall"

// diskUsage reports free/total bytes on the filesystem containing
// dir via statfs(2) -- Linux-only (Statfs_t's own field types aren't
// portable across GOOS, and every real deployment of this package is
// Docker/Linux-based, see docker-compose.yml), see
// diskusage_other.go's own stub for every other platform.
func diskUsage(dir string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, 0, err
	}
	free = int64(stat.Bavail) * int64(stat.Bsize)
	total = int64(stat.Blocks) * int64(stat.Bsize)
	return free, total, nil
}
