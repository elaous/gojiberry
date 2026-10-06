//go:build !js

package mongo

import (
	"context"
	"github.com/elaous/gojiberry/docstore"
	"os"
	"testing"
)

// TestMongoAdapterConformance only runs against a real MongoDB
// instance, since the driver has no small operation-level interface
// to fake the way s3API does for S3 -- FindOne/Find/ReplaceOne return
// concrete *mongo.SingleResult/*mongo.Cursor types that construct
// their own internal state from a live connection. Set
// TEST_MONGO_URI (and optionally TEST_MONGO_DB) to run this in an
// environment with MongoDB reachable; this sandbox has no Docker
// daemon available to start one, so it is skipped here. The adapter
// is still compile-checked by every `go build`/`go vet` in this
// package.
func TestMongoAdapterConformance(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set; skipping MongoDB integration test (no live MongoDB in this environment)")
	}
	db := os.Getenv("TEST_MONGO_DB")
	if db == "" {
		db = "docstore_test"
	}
	ctx := context.Background()
	docstore.CheckAdapter(t, func() docstore.Adapter {
		a, err := New(ctx, uri, db)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return a
	})
}

// The conditional save needs a real MongoDB as well (see above).
func TestMongoAdapterSaveIf(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set; skipping MongoDB integration test (no live MongoDB in this environment)")
	}
	ctx := context.Background()
	docstore.CheckConditional(t, func() docstore.ConditionalAdapter {
		a, err := New(ctx, uri, "docstore_test_conditional")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = a.coll.Drop(ctx)
		return a
	})
}
