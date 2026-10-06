//go:build !js

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config is the mongo backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "mongo".
type Config struct {
	URI string `json:"uri"`
	// DB is the database name backing the documents collection.
	// Defaults to "docstore" if empty.
	DB string `json:"db"`
}

func init() {
	docstore.RegisterAdapter("mongo", func(ctx context.Context, raw json.RawMessage) (docstore.Adapter, error) {
		cfg := Config{DB: "docstore"}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.URI == "" {
			return nil, errors.New(`docstore: mongo backend requires a "uri"`)
		}
		return New(ctx, cfg.URI, cfg.DB)
	})
}

// mongoDoc is documents collection's on-disk shape. _id holds the
// document ID directly (never an ObjectID) so lookups are a plain
// equality match on the caller-supplied string.
type mongoDoc struct {
	ID        string    `bson:"_id"`
	Data      []byte    `bson:"data"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// Adapter stores each document as one document in a single
// MongoDB collection.
type Adapter struct {
	coll *mongo.Collection
}

// New connects to uri and uses db.documents as the
// backing collection.
func New(ctx context.Context, uri, db string) (*Adapter, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}
	return &Adapter{coll: client.Database(db).Collection("documents")}, nil
}

func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	doc := mongoDoc{ID: id, Data: data, UpdatedAt: time.Now()}
	_, err := a.coll.ReplaceOne(ctx, bson.M{"_id": id}, doc, options.Replace().SetUpsert(true))
	return err
}

// SaveIf implements docstore.ConditionalAdapter: a new document is inserted (a duplicate
// key is a conflict), and a changed one is replaced with a filter on the bytes that were
// read and found to be what prevDigest says, so nothing replaced it in between: a filter
// that matches nothing is a conflict.
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	doc := mongoDoc{ID: id, Data: data, UpdatedAt: time.Now()}
	if prevDigest == "" {
		_, err := a.coll.InsertOne(ctx, doc)
		if mongo.IsDuplicateKeyError(err) {
			return docstore.ErrConflict
		}
		return err
	}
	var cur mongoDoc
	err := a.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&cur)
	if err == mongo.ErrNoDocuments {
		return docstore.ErrConflict
	}
	if err != nil {
		return err
	}
	if docstore.Digest(cur.Data) != prevDigest {
		return docstore.ErrConflict
	}
	res, err := a.coll.ReplaceOne(ctx, bson.M{"_id": id, "data": cur.Data}, doc)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return docstore.ErrConflict
	}
	return nil
}

func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	var doc mongoDoc
	err := a.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return docstore.Document{}, docstore.ErrNotFound
	}
	if err != nil {
		return docstore.Document{}, err
	}
	return docstore.Document(doc), nil
}

func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) {
	return a.list(ctx, bson.M{})
}

// prefixFilter matches the documents whose _id starts with prefix: a regular expression
// anchored at the start with the prefix quoted, which MongoDB answers from the _id index as a
// range scan (case-sensitive and anchored, so no full scan).
func prefixFilter(prefix string) bson.M {
	return bson.M{"_id": bson.M{"$regex": "^" + regexp.QuoteMeta(prefix)}}
}

// ListPrefix implements docstore.PrefixLister.
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	return a.list(ctx, prefixFilter(prefix))
}

func (a *Adapter) list(ctx context.Context, filter bson.M) ([]docstore.Meta, error) {
	cur, err := a.coll.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var metas []docstore.Meta
	for cur.Next(ctx) {
		var doc mongoDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		metas = append(metas, docstore.Meta{ID: doc.ID, UpdatedAt: doc.UpdatedAt, Size: len(doc.Data)})
	}
	return metas, cur.Err()
}

func (a *Adapter) Delete(ctx context.Context, id string) error {
	res, err := a.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return docstore.ErrNotFound
	}
	return nil
}
