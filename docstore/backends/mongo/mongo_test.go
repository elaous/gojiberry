//go:build !js

package mongo

import (
	"context"
	"github.com/elaous/gojiberry/docstore"
	"os"
	"regexp"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// mongoURI is the server the live tests run against. Without TEST_MONGO_URI they skip, and a plain go test
// still prints ok, so the message says loudly that nothing was tested (a skip once hid a stale test elsewhere
// in this repo). docs/testing-workflow.md has a no-Docker recipe; CI runs these against a service container
// and fails on a skip.
func mongoURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("SKIPPED, NOT TESTED: set TEST_MONGO_URI (for example mongodb://127.0.0.1:27017) to a MongoDB server to run the MongoDB backend tests; see docs/testing-workflow.md")
	}
	return uri
}

// TestMongoAdapterConformance only runs against a real MongoDB
// instance, since the driver has no small operation-level interface
// to fake the way s3API does for S3 -- FindOne/Find/ReplaceOne return
// concrete *mongo.SingleResult/*mongo.Cursor types that construct
// their own internal state from a live connection. Set
// TEST_MONGO_URI (and optionally TEST_MONGO_DB) to run this in an
// environment with MongoDB reachable. It has run green against MongoDB 7.0.14 under -race.
func TestMongoAdapterConformance(t *testing.T) {
	uri := mongoURI(t)
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
		// Every subtest starts from an empty store: CheckAdapter calls the factory once per subtest and a
		// collection left over from the one before would break "empty store lists as empty".
		if err := a.coll.Drop(ctx); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		return a
	})
}

// The conditional save needs a real MongoDB as well (see above).
func TestMongoAdapterSaveIf(t *testing.T) {
	uri := mongoURI(t)
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

// The filter is what the adapter adds over List: an anchored regular expression with the
// prefix quoted, so a prefix with regular-expression characters matches only itself.
func TestMongoPrefixFilterQuotesThePrefix(t *testing.T) {
	var _ docstore.PrefixLister = (*Adapter)(nil)
	f := prefixFilter(`dir.ectory/a+b(1)/`)
	re, _ := f["_id"].(bson.M)["$regex"].(string)
	want := `^dir\.ectory/a\+b\(1\)/`
	if re != want {
		t.Fatalf("regex = %q, want %q", re, want)
	}
	rx := regexp.MustCompile(re)
	if !rx.MatchString("dir.ectory/a+b(1)/x") || rx.MatchString("dirXectory/a+b(1)/x") || rx.MatchString("z/dir.ectory/a+b(1)/x") {
		t.Error("the anchored, quoted pattern matches only ids that start with the prefix")
	}
	if prefixFilter("")["_id"].(bson.M)["$regex"] != "^" {
		t.Error("an empty prefix matches everything")
	}
}

// ListPrefix against a real MongoDB (see above).
func TestMongoAdapterListPrefix(t *testing.T) {
	uri := mongoURI(t)
	ctx := context.Background()
	docstore.CheckPrefixLister(t, func() docstore.Adapter {
		a, err := New(ctx, uri, "docstore_test_prefix")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = a.coll.Drop(ctx)
		return a
	})
}

// The conditional delete needs a real MongoDB too (see above).
func TestMongoAdapterDeleteIf(t *testing.T) {
	uri := mongoURI(t)
	ctx := context.Background()
	a, err := New(ctx, uri, "docstore_test_conditional")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.coll.Drop(ctx)
	var _ docstore.ConditionalDeleter = a
	if err := a.Save(ctx, "x", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("other"))); err != docstore.ErrConflict {
		t.Errorf("a stale digest: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err != nil {
		t.Errorf("the document that was read: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err != docstore.ErrNotFound {
		t.Errorf("a document that is gone: %v", err)
	}
}

// MongoDB has no cheap way to read a prefix of a binary field, so it does not claim to be a
// HeadReader: callers fall back to Load rather than getting a slow "head read".
func TestMongoAdapterIsNotAHeadReader(t *testing.T) {
	var a docstore.Adapter = &Adapter{}
	if _, ok := a.(docstore.HeadReader); ok {
		t.Fatal("mongo must not implement HeadReader")
	}
}
