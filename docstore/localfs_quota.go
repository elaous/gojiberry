package docstore

import "context"

// Usage implements QuotaReporter for LocalFSAdapter -- the one
// backend here with a real, checkable "how much space is left"
// answer: the underlying filesystem's own free space at a's own dir.
// diskUsage is platform-specific (see diskusage_linux.go/
// diskusage_other.go) since there's no portable stdlib call for it;
// on a platform without an implementation, this simply returns an
// error, the same as any other real Usage failure -- server.go's own
// handleQuota reports "not available" either way, whether that's
// because this platform isn't supported or the syscall itself failed.
func (a *LocalFSAdapter) Usage(_ context.Context) (Quota, error) {
	free, total, err := diskUsage(a.dir)
	if err != nil {
		return Quota{}, err
	}
	return Quota{Used: total - free, Total: total}, nil
}
