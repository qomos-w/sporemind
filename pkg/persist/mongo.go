package persist

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// BackendMongo is the MongoDB backend type registered in the backend registry.
const BackendMongo BackendType = "mongo"

// mongoPersist maps the Persist contract onto a MongoDB database. One logical
// store per (Database, Prefix): documents live in a collection named after the
// actor-type Prefix, keyed by _id = name; append-only records live in a
// "<prefix>_append" collection. value is stored as a JSON string so the
// marshalled bytes roundtrip byte-for-byte (a BSON subdocument would mangle
// large int64 precision, key order, and duplicate keys — byte fidelity is the
// priority).
//
// Mongo standalone has no multi-document transactions, so mongoPersist does
// NOT implement Saver: callers go through SaveAll's sequential fallback. SQL
// backends implement Saver via a transaction.
type mongoPersist struct {
	client  *mongo.Client
	dbName  string
	coll    *mongo.Collection // <prefix>
	appColl *mongo.Collection // <prefix>_append
}

// newMongoPersist constructs a Mongo persist. It resolves the tunnel (decision
// point 4: tunnel => local dial, TLS off; direct => Endpoint, TLS on),
// resolves credentials at dial time, connects, pings (startup failure is an
// explicit error), and selects the per-prefix collections.
func newMongoPersist(cfg PersistConfig) (Persist, error) {
	if cfg.Prefix == "" {
		return nil, fmt.Errorf("persist: mongo backend requires a non-empty Prefix")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("persist: mongo backend requires an Endpoint (host:port)")
	}
	if err := validateCollectionName(cfg.Prefix); err != nil {
		return nil, fmt.Errorf("persist: mongo Prefix %q: %w", cfg.Prefix, err)
	}
	host, port, err := splitEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("persist: mongo Endpoint %q: %w", cfg.Endpoint, err)
	}
	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return nil, err
	}
	// decision point 4: tunnel => local dial, TLS off; direct => Endpoint,
	// TLS on unless the endpoint is loopback (the dev/test topology exemption
	// shared with the kv backends, netkv.go).
	tlsOn := !isLoopbackEndpoint(cfg.Endpoint)
	dialAddr := net.JoinHostPort(host, port)
	if cfg.TunnelRef != "" {
		local, err := ResolveTunnel(cfg.TunnelRef, cfg.Endpoint)
		if err != nil {
			return nil, err
		}
		dialAddr = local
		tlsOn = false
	}
	dh, dp, err := splitEndpoint(dialAddr)
	if err != nil {
		return nil, fmt.Errorf("persist: mongo dial addr %q: %w", dialAddr, err)
	}
	clientOpts := options.Client().
		SetHosts([]string{net.JoinHostPort(dh, dp)}).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetMaxPoolSize(16)
	if cred.Username != "" {
		clientOpts.SetAuth(options.Credential{
			Username:   cred.Username,
			Password:   cred.Password,
			AuthSource: "admin",
		})
	}
	if tlsOn {
		clientOpts.SetTLSConfig(&tls.Config{ServerName: dh, MinVersion: tls.VersionTLS12})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("persist: mongo connect: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("persist: mongo ping %s: %w", dialAddr, err)
	}
	dbName := defaultDatabase(cfg.Database)
	db := client.Database(dbName)
	return &mongoPersist{
		client:  client,
		dbName:  dbName,
		coll:    db.Collection(cfg.Prefix),
		appColl: db.Collection(cfg.Prefix + "_append"),
	}, nil
}

// Close disconnects the client. Optional capability (not part of Persist).
func (p *mongoPersist) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.client.Disconnect(ctx)
}

// Save upserts the JSON-encocoded value for name as a string field, preserving
// byte fidelity (the marshalled text is stored verbatim, not as a BSON
// subdocument).
func (p *mongoPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	ctx, cancel := p.opCtx()
	defer cancel()
	_, err = p.coll.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: name}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "value", Value: string(data)}}}},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("persist: mongo upsert %q: %w", name, err)
	}
	return nil
}

// Load decodes the stored JSON string for name into v. A missing document
// yields ErrNotExist.
func (p *mongoPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := p.opCtx()
	defer cancel()
	var doc struct {
		Value string `bson:"value"`
	}
	err := p.coll.FindOne(ctx, bson.D{{Key: "_id", Value: name}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("persist: mongo load %q: %w", name, err)
	}
	if err := decode([]byte(doc.Value), v); err != nil {
		return fmt.Errorf("persist: decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document for name and cascades into its name/ subtree
// plus the matching append records. Idempotent.
func (p *mongoPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := p.opCtx()
	defer cancel()
	// _id == name OR _id under name/ (mirror FSPersist's <name>.json + <name>/
	// reclaim and the SQL name = ? OR name LIKE prefix% cascade).
	docFilter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "_id", Value: name}},
		bson.D{{Key: "_id", Value: subtreeRegex(name)}},
	}}}
	if _, err := p.coll.DeleteMany(ctx, docFilter); err != nil {
		return fmt.Errorf("persist: mongo delete %q: %w", name, err)
	}
	appFilter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "name", Value: name}},
		bson.D{{Key: "name", Value: subtreeRegex(name)}},
	}}}
	if _, err := p.appColl.DeleteMany(ctx, appFilter); err != nil {
		return fmt.Errorf("persist: mongo delete append %q: %w", name, err)
	}
	return nil
}

// List implements Lister with the same hierarchical semantics as FSPersist:
// List("ws") and List("ws/") enumerate the ws/ subtree and never return the
// document "ws" itself; List("") enumerates everything. Implemented as an _id
// prefix regex; regex metacharacters in the prefix are escaped.
func (p *mongoPersist) List(prefix string) ([]string, error) {
	ctx, cancel := p.opCtx()
	defer cancel()
	cur, err := p.coll.Find(ctx,
		bson.D{{Key: "_id", Value: subtreeRegex(prefix)}},
		options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("persist: mongo list %q: %w", prefix, err)
	}
	defer cur.Close(ctx)
	var names []string
	for cur.Next(ctx) {
		var d struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, fmt.Errorf("persist: mongo list decode: %w", err)
		}
		names = append(names, d.ID)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("persist: mongo list cursor: %w", err)
	}
	return names, nil
}

// Append implements Appender. Each call inserts one record {name, data} into
// the <prefix>_append collection; _id is a generated ObjectID, so sorting by
// _id yields write order. A single InsertOne is atomic; concurrent Appends do
// not interleave individual records.
func (p *mongoPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := p.opCtx()
	defer cancel()
	_, err := p.appColl.InsertOne(ctx, bson.D{
		{Key: "name", Value: name},
		{Key: "data", Value: bson.Binary{Subtype: 0x00, Data: data}},
	})
	if err != nil {
		return fmt.Errorf("persist: mongo append %q: %w", name, err)
	}
	return nil
}

// appendRecords returns the ordered data records for name (test/inspection
// helper — the shared Appender contract suite verifies readback through
// BasePather for fs backends; mongo verifies readback through here).
func (p *mongoPersist) appendRecords(name string) ([][]byte, error) {
	ctx, cancel := p.opCtx()
	defer cancel()
	cur, err := p.appColl.Find(ctx,
		bson.D{{Key: "name", Value: name}},
		options.Find().SetProjection(bson.D{{Key: "data", Value: 1}}).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("persist: mongo append records %q: %w", name, err)
	}
	defer cur.Close(ctx)
	var out [][]byte
	for cur.Next(ctx) {
		var d struct {
			Data bson.Binary `bson:"data"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, fmt.Errorf("persist: mongo append decode: %w", err)
		}
		out = append(out, d.Data.Data)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("persist: mongo append cursor: %w", err)
	}
	return out, nil
}

func (p *mongoPersist) opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// subtreeRegex builds a BSON regex value matching _id in the subtree of prefix:
// prefix itself (as a directory) and everything under prefix/. List("ws") and
// List("ws/") both match "ws/..."; List("") matches everything. Regex
// metacharacters are escaped so names containing . * etc. match literally.
func subtreeRegex(prefix string) bson.M {
	scan := ""
	if prefix != "" {
		scan = strings.TrimSuffix(prefix, "/") + "/"
	}
	return bson.M{"$regex": "^" + regexp.QuoteMeta(scan)}
}

// validateCollectionName enforces MongoDB collection name rules for the prefix:
// no empty name, no NUL byte, no '$', must not start with "system.", and a
// conservative 120-byte cap (the server limit is 235/255). Prefixes are short
// actor-type segments, but the check keeps a typo'd prefix from creating an
// unusable collection.
func validateCollectionName(name string) error {
	if name == "" {
		return fmt.Errorf("collection name must not be empty")
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("collection name must not contain a NUL byte")
	}
	if strings.Contains(name, "$") {
		return fmt.Errorf("collection name must not contain '$'")
	}
	if strings.HasPrefix(name, "system.") {
		return fmt.Errorf("collection name must not start with 'system.'")
	}
	if len(name) > 120 {
		return fmt.Errorf("collection name too long (%d bytes)", len(name))
	}
	return nil
}
