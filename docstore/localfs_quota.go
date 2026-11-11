package docstore

import "context"

func (a *LocalFSAdapter) Usage(_ context.Context) (Quota, error) {
	free, total, err := diskUsage(a.dir)
	if err != nil {
		return Quota{}, err
	}
	return Quota{Used: total - free, Total: total}, nil
}
