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

func mongoURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("SKIPPED, NOT TESTED: set TEST_MONGO_URI (for example mongodb://127.0.0.1:27017) to a MongoDB server to run the MongoDB backend tests; see docs/testing-workflow.md")
	}
	return uri
}

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
		if err := a.coll.Drop(ctx); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		return a
	})
}

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

func TestMongoAdapterIsNotAHeadReader(t *testing.T) {
	var a docstore.Adapter = &Adapter{}
	if _, ok := a.(docstore.HeadReader); ok {
		t.Fatal("mongo must not implement HeadReader")
	}
}
