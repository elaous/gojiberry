package docstore

import (
	"context"
	"runtime"
	"testing"
)

func TestLocalFSAdapterConformance(t *testing.T) {
	testAdapterConformance(t, func() Adapter {
		a, err := NewLocalFSAdapter(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalFSAdapter: %v", err)
		}
		return a
	})
}

func TestLocalFSAdapterCreatesMissingDir(t *testing.T) {
	dir := t.TempDir() + "/nested/does/not/exist/yet"
	a, err := NewLocalFSAdapter(dir)
	if err != nil {
		t.Fatalf("NewLocalFSAdapter: %v", err)
	}
	if err := a.Save(context.Background(), "doc1", []byte("x")); err != nil {
		t.Fatalf("Save into freshly created dir: %v", err)
	}
}

func TestLocalFSAdapterUsageReportsRealDiskSpace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("disk usage reporting is only implemented on linux -- see diskusage_other.go")
	}
	a, err := NewLocalFSAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalFSAdapter: %v", err)
	}
	// Compile-time assertion that LocalFSAdapter really does satisfy
	// QuotaReporter, the same way handleQuota's own type assertion
	// will check at runtime.
	var _ QuotaReporter = a

	q, err := a.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if q.Total <= 0 {
		t.Fatalf("Total = %d, want a real positive disk size", q.Total)
	}
	if q.Used < 0 || q.Used > q.Total {
		t.Fatalf("Used = %d, want 0 <= Used <= Total (%d)", q.Used, q.Total)
	}
}
