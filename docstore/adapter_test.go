package docstore

import (
	"testing"
)

func testAdapterConformance(t *testing.T, newAdapter func() Adapter) {
	t.Helper()
	CheckAdapter(t, newAdapter)
}

func TestMemoryAdapterConformance(t *testing.T) {
	testAdapterConformance(t, func() Adapter { return NewMemoryAdapter() })
}
