package databases

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The document browser (docs/DATABASES.md#the-key-and-document-browsers):
// a MongoDB database's collections and documents, as its own user through
// the instance's socket. Documents travel as relaxed Extended JSON, the
// mongosh style ({"$oid": …} for IDs, {"$date": …} for dates), so nothing
// is lost on the way back.

// Bounds on the document browser.
const (
	maxDocsPage = 100     // documents in one page
	maxDocBytes = 2 << 20 // of documents in one page, then it's cut
	maxFilter   = 64 << 10
	mongoTime   = 30 * time.Second
)

// Collection is one of a database's collections.
type Collection struct {
	Name    string       `json:"name"`
	Count   int64        `json:"count"` // the engine's estimate
	Indexes []MongoIndex `json:"indexes"`
}

// MongoIndex is one of a collection's indexes.
type MongoIndex struct {
	Name   string `json:"name"`
	Keys   string `json:"keys"` // Extended JSON, like {"name": 1}
	Unique bool   `json:"unique,omitempty"`
}

// DocumentsQuery asks for a page of a collection's documents. Filter,
// Sort, and Projection are Extended JSON objects; "" is none.
type DocumentsQuery struct {
	Collection string `json:"collection"`
	Filter     string `json:"filter,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Projection string `json:"projection,omitempty"`
	Skip       int64  `json:"skip,omitempty"`
	Limit      int64  `json:"limit,omitempty"`
}

// DocumentsResult is a page of documents.
type DocumentsResult struct {
	Documents []string `json:"documents"` // relaxed Extended JSON
	Count     int64    `json:"count"`     // matching the filter, -1 if counting ran out of time
	More      bool     `json:"more,omitempty"`
	Cut       bool     `json:"cut,omitempty"`
	Millis    int64    `json:"millis"`
}

// mongoAs connects to a database's MongoDB instance as its user.
func (s *Service) mongoAs(ctx context.Context, serverID, id string) (*mongo.Client, string, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return nil, "", err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return nil, "", err
	}
	if in.Engine != MongoDB {
		return nil, "", invalid{"that isn't a MongoDB database"}
	}
	if s.state(r.InstanceID) != StateRunning {
		return nil, "", notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return nil, "", err
	}
	c, err := mongoConnectAs(ctx, s.dir(r.InstanceID), r.Username, pass, r.DbName)
	return c, r.DbName, err
}

// ejson reads an Extended JSON object; "" is an empty one.
func ejson(s, what string) (bson.D, error) {
	if strings.TrimSpace(s) == "" {
		return bson.D{}, nil
	}
	if len(s) > maxFilter {
		return nil, invalid{fmt.Sprintf("the %s is at most 64 KB", what)}
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(s), false, &d); err != nil {
		return nil, invalid{fmt.Sprintf("the %s isn't a JSON object: %v", what, err)}
	}
	return d, nil
}

func toEJSON(v any) (string, error) {
	b, err := bson.MarshalExtJSON(v, false, false)
	return string(b), err
}

// collection checks a collection exists and returns its name.
func collection(ctx context.Context, db *mongo.Database, name string) (string, error) {
	names, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return "", err
	}
	if !slices.Contains(names, name) {
		return "", invalid{fmt.Sprintf("there's no collection %q", name)}
	}
	return name, nil
}

// Collections lists a MongoDB database's collections, with their
// estimated counts and indexes.
func (s *Service) Collections(ctx context.Context, serverID, id string) ([]Collection, error) {
	c, name, err := s.mongoAs(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	db := c.Database(name)
	names, err := db.ListCollectionNames(ctx, bson.D{{Key: "type", Value: "collection"}})
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	out := make([]Collection, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, "system.") {
			continue
		}
		coll := db.Collection(n)
		col := Collection{Name: n, Indexes: []MongoIndex{}}
		col.Count, _ = coll.EstimatedDocumentCount(ctx)
		cur, err := coll.Indexes().List(ctx)
		if err == nil {
			var specs []struct {
				Name   string `bson:"name"`
				Key    bson.D `bson:"key"`
				Unique bool   `bson:"unique"`
			}
			if cur.All(ctx, &specs) == nil {
				for _, sp := range specs {
					keys, _ := toEJSON(sp.Key)
					col.Indexes = append(col.Indexes, MongoIndex{Name: sp.Name, Keys: keys, Unique: sp.Unique})
				}
			}
		}
		out = append(out, col)
	}
	return out, nil
}

// Documents returns a page of a collection's documents matching a filter.
func (s *Service) Documents(ctx context.Context, serverID, id string, q DocumentsQuery) (DocumentsResult, error) {
	filter, err := ejson(q.Filter, "filter")
	if err != nil {
		return DocumentsResult{}, err
	}
	sort, err := ejson(q.Sort, "sort")
	if err != nil {
		return DocumentsResult{}, err
	}
	proj, err := ejson(q.Projection, "projection")
	if err != nil {
		return DocumentsResult{}, err
	}
	limit := q.Limit
	if limit <= 0 || limit > maxDocsPage {
		limit = 50
	}
	c, name, err := s.mongoAs(ctx, serverID, id)
	if err != nil {
		return DocumentsResult{}, err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	db := c.Database(name)
	collName, err := collection(ctx, db, q.Collection)
	if err != nil {
		return DocumentsResult{}, err
	}
	coll := db.Collection(collName)
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, mongoTime) // the server stops too: the driver sends it as maxTimeMS
	defer cancel()
	o := options.Find().SetSkip(max(q.Skip, 0)).SetLimit(limit + 1)
	if len(sort) > 0 {
		o.SetSort(sort)
	}
	if len(proj) > 0 {
		o.SetProjection(proj)
	}
	cur, err := coll.Find(ctx, filter, o)
	if err != nil {
		return DocumentsResult{}, mongoErr(err)
	}
	defer func() { _ = cur.Close(context.WithoutCancel(ctx)) }()
	res := DocumentsResult{Documents: []string{}}
	size := 0
	for cur.Next(ctx) {
		if int64(len(res.Documents)) == limit {
			res.More = true
			break
		}
		var doc bson.D
		if err := cur.Decode(&doc); err != nil {
			return res, err
		}
		js, err := toEJSON(doc)
		if err != nil {
			return res, err
		}
		size += len(js)
		if size > maxDocBytes {
			res.Cut, res.More = true, true
			break
		}
		res.Documents = append(res.Documents, js)
	}
	if err := cur.Err(); err != nil {
		return res, mongoErr(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if res.Count, err = coll.CountDocuments(cctx, filter); err != nil {
		res.Count = -1
	}
	res.Millis = time.Since(start).Milliseconds()
	return res, nil
}

// mongoErr makes MongoDB's errors short sentences.
func mongoErr(err error) error {
	var ce mongo.CommandError
	if errors.As(err, &ce) {
		if ce.Code == 50 || errors.Is(err, context.DeadlineExceeded) { // MaxTimeMSExpired
			return invalid{"it ran out of time (30 seconds): narrow the filter, or add an index"}
		}
		return errors.New(ce.Message)
	}
	return err
}

// DocumentEdit is a change to a collection's documents: insert (Document),
// replace (the one with ID, by Document), or delete (those with IDs).
// IDs and documents are Extended JSON.
type DocumentEdit struct {
	Collection string `json:"collection"`
	Op         string `json:"op"`
	// ID is doc_id: a command's id is the database's.
	ID       string   `json:"doc_id,omitempty"`
	IDs      []string `json:"doc_ids,omitempty"`
	Document string   `json:"document,omitempty"`
}

// EditDocuments changes documents and returns how many it changed.
func (s *Service) EditDocuments(ctx context.Context, serverID, id string, e DocumentEdit) (int64, error) {
	c, name, err := s.mongoAs(ctx, serverID, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	db := c.Database(name)
	collName, err := collection(ctx, db, e.Collection)
	if err != nil {
		return 0, err
	}
	coll := db.Collection(collName)
	switch e.Op {
	case "insert":
		doc, err := document(e.Document)
		if err != nil {
			return 0, err
		}
		if _, err := coll.InsertOne(ctx, doc); err != nil {
			return 0, mongoErr(err)
		}
		return 1, nil
	case "replace":
		doc, err := document(e.Document)
		if err != nil {
			return 0, err
		}
		key, err := docID(e.ID)
		if err != nil {
			return 0, err
		}
		// The _id can't change; one in the document must be the same.
		doc = slices.DeleteFunc(doc, func(el bson.E) bool { return el.Key == "_id" })
		r, err := coll.ReplaceOne(ctx, bson.D{{Key: "_id", Value: key}}, doc)
		if err != nil {
			return 0, mongoErr(err)
		}
		if r.MatchedCount == 0 {
			return 0, invalid{"that document isn't there any more"}
		}
		return r.ModifiedCount, nil
	case "delete":
		if len(e.IDs) == 0 || len(e.IDs) > maxDocsPage {
			return 0, invalid{fmt.Sprintf("delete 1 to %d documents at a time", maxDocsPage)}
		}
		keys := bson.A{}
		for _, raw := range e.IDs {
			k, err := docID(raw)
			if err != nil {
				return 0, err
			}
			keys = append(keys, k)
		}
		r, err := coll.DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: keys}}}})
		if err != nil {
			return 0, mongoErr(err)
		}
		return r.DeletedCount, nil
	}
	return 0, invalid{fmt.Sprintf("unknown change %q", e.Op)}
}

func document(s string) (bson.D, error) {
	if len(s) > maxEditValue {
		return nil, invalid{"a document written from the browser is at most 1 MiB"}
	}
	d, err := ejson(s, "document")
	if err != nil {
		return nil, err
	}
	return d, nil
}

// docID reads an _id: an Extended JSON value ({"$oid": …}, a number, a
// string in quotes).
func docID(s string) (any, error) {
	var wrapper struct {
		ID bson.RawValue `bson:"_id"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"_id":`+s+`}`), false, &wrapper); err != nil {
		return nil, invalid{fmt.Sprintf("%s isn't a document's _id", s)}
	}
	return wrapper.ID, nil
}

// collNameRe is a collection the browser makes.
var collNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,119}$`)

// EditCollection makes (create) or drops (drop) a collection.
func (s *Service) EditCollection(ctx context.Context, serverID, id, op, coll string) error {
	c, name, err := s.mongoAs(ctx, serverID, id)
	if err != nil {
		return err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	db := c.Database(name)
	switch op {
	case "create":
		if !collNameRe.MatchString(coll) || strings.HasPrefix(coll, "system.") {
			return invalid{"a collection's name is letters, digits, and _ . -, up to 120"}
		}
		return mongoErr(db.CreateCollection(ctx, coll))
	case "drop":
		n, err := collection(ctx, db, coll)
		if err != nil {
			return err
		}
		return mongoErr(db.Collection(n).Drop(ctx))
	}
	return invalid{fmt.Sprintf("unknown change %q", op)}
}
