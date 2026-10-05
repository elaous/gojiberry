//go:build !js

package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoConfig is the mongo backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "mongo".
type MongoConfig struct {
	URI string `json:"uri"`
	// DB is the database name backing the documents collection.
	// Defaults to "docstore" if empty.
	DB string `json:"db"`
}

func init() {
	RegisterAdapter("mongo", func(ctx context.Context, raw json.RawMessage) (Adapter, error) {
		cfg := MongoConfig{DB: "docstore"}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.URI == "" {
			return nil, errors.New(`docstore: mongo backend requires a "uri"`)
		}
		return NewMongoAdapter(ctx, cfg.URI, cfg.DB)
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

// MongoAdapter stores each document as one document in a single
// MongoDB collection.
type MongoAdapter struct {
	coll *mongo.Collection
}

// NewMongoAdapter connects to uri and uses db.documents as the
// backing collection.
func NewMongoAdapter(ctx context.Context, uri, db string) (*MongoAdapter, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}
	return &MongoAdapter{coll: client.Database(db).Collection("documents")}, nil
}

func (a *MongoAdapter) Save(ctx context.Context, id string, data []byte) error {
	doc := mongoDoc{ID: id, Data: data, UpdatedAt: time.Now()}
	_, err := a.coll.ReplaceOne(ctx, bson.M{"_id": id}, doc, options.Replace().SetUpsert(true))
	return err
}

func (a *MongoAdapter) Load(ctx context.Context, id string) (Document, error) {
	var doc mongoDoc
	err := a.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	return Document(doc), nil
}

func (a *MongoAdapter) List(ctx context.Context) ([]Meta, error) {
	cur, err := a.coll.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()
	var metas []Meta
	for cur.Next(ctx) {
		var doc mongoDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		metas = append(metas, Meta{ID: doc.ID, UpdatedAt: doc.UpdatedAt, Size: len(doc.Data)})
	}
	return metas, cur.Err()
}

func (a *MongoAdapter) Delete(ctx context.Context, id string) error {
	res, err := a.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
