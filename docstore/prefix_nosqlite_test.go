//go:build js

package docstore

import "testing"

func sqliteForTest(*testing.T) Adapter { return nil }
